// Package bot traite les commandes et les boutons Telegram (long polling).
package bot

import (
	"context"
	"log/slog"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"frigate-telegram-enhanced/internal/config"
	"frigate-telegram-enhanced/internal/i18n"
	"frigate-telegram-enhanced/internal/state"
	"frigate-telegram-enhanced/internal/telegram"
)

type Telegram interface {
	GetUpdates(ctx context.Context, offset int, timeout time.Duration) ([]telegram.Update, error)
	SetMyCommands(ctx context.Context, cmds []telegram.BotCommand) error
	SendMessage(ctx context.Context, chatID int64, text string, o telegram.SendOptions) (telegram.Message, error)
	SendPhoto(ctx context.Context, chatID int64, f telegram.InputFile, o telegram.SendOptions) (telegram.Message, error)
	AnswerCallbackQuery(ctx context.Context, id, text string) error
	EditMessageText(ctx context.Context, chatID int64, messageID int, text string, markup *telegram.InlineKeyboardMarkup) error
}

type Frigate interface {
	Cameras(ctx context.Context) ([]string, error)
	GetBytes(ctx context.Context, path string, max int64) ([]byte, error)
}

type Notifier interface {
	SendClipTo(ctx context.Context, chatID int64, replyTo int, id string) error
	SendLast(ctx context.Context, chatID int64, camera string) error
	Count24h() int
}

type Deps struct {
	Config        *config.Config
	Telegram      Telegram
	Frigate       Frigate
	Notifier      Notifier
	State         *state.Store
	Log           *slog.Logger
	Now           func() time.Time
	MQTTConnected func() bool
}

// camerasTTL borne la fraîcheur de la liste des caméras : elle change rarement, et
// /cameras, /snapshot et /pause <caméra> la consultent à chaque appel.
const camerasTTL = 30 * time.Second

type Bot struct {
	Deps
	lastPoll atomic.Int64
	pollErr  atomic.Pointer[error] // dernière erreur de getUpdates, nil après un succès
	wg       sync.WaitGroup

	serialMu sync.Mutex
	tail     chan struct{} // fermé quand la dernière commande mise en file est terminée

	camMu    sync.Mutex
	camList  []string
	camUntil time.Time

	refMu   sync.Mutex
	refused []Refused           // derniers utilisateurs refusés, du plus ancien au plus récent
	replied map[int64]time.Time // dernière réponse « votre identifiant » par utilisateur
}

func New(d Deps) *Bot {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Log == nil {
		d.Log = slog.Default()
	}
	if d.MQTTConnected == nil {
		d.MQTTConnected = func() bool { return false }
	}
	b := &Bot{Deps: d}
	b.lastPoll.Store(d.Now().UnixNano())
	return b
}

// LastPoll renvoie l'heure du dernier getUpdates réussi (utilisé par /healthz).
func (b *Bot) LastPoll() time.Time { return time.Unix(0, b.lastPoll.Load()) }

// PollError renvoie l'erreur du dernier getUpdates, nil s'il a réussi.
func (b *Bot) PollError() error {
	if p := b.pollErr.Load(); p != nil {
		return *p
	}
	return nil
}

// Wait attend la fin des actions lancées en arrière-plan.
func (b *Bot) Wait() { b.wg.Wait() }

func (b *Bot) async(f func()) {
	b.wg.Add(1)
	go func() {
		defer b.wg.Done()
		f()
	}()
}

// serial exécute f en arrière-plan, après toutes les commandes mises en file avant
// elle : la boucle de long polling n'attend jamais Frigate, et « /pause » puis
// « /resume » s'appliquent toujours dans cet ordre.
func (b *Bot) serial(f func()) {
	b.serialMu.Lock()
	prev, done := b.tail, make(chan struct{})
	b.tail = done
	b.serialMu.Unlock()
	b.async(func() {
		defer close(done)
		if prev != nil {
			<-prev
		}
		f()
	})
}

// cameras renvoie la liste des caméras de Frigate, gardée en cache camerasTTL.
func (b *Bot) cameras(ctx context.Context) ([]string, error) {
	b.camMu.Lock()
	defer b.camMu.Unlock()
	now := b.Now()
	if b.camList != nil && now.Before(b.camUntil) {
		return slices.Clone(b.camList), nil
	}
	cams, err := b.Frigate.Cameras(ctx)
	if err != nil {
		return nil, err
	}
	b.camList, b.camUntil = cams, now.Add(camerasTTL)
	return slices.Clone(cams), nil
}

// commands est le menu des commandes affiché par Telegram, dans la langue l.
func commands(l i18n.Lang) []telegram.BotCommand {
	return []telegram.BotCommand{
		{Command: "pause", Description: l.T("Pause: /pause [duration] [camera]", "Mettre en pause : /pause [durée] [caméra]")},
		{Command: "resume", Description: l.T("Resume: /resume [camera]", "Reprendre : /resume [caméra]")},
		{Command: "status", Description: l.T("Service status", "État du service")},
		{Command: "cameras", Description: l.T("List cameras", "Liste des caméras")},
		{Command: "snapshot", Description: l.T("Live image: /snapshot [camera]", "Image en direct : /snapshot [caméra]")},
		{Command: "last", Description: l.T("Latest event: /last [camera]", "Dernier événement : /last [caméra]")},
		{Command: "menu", Description: l.T("Control panel: pause, mute cameras", "Tableau de contrôle : pause, couper des caméras")},
		{Command: "help", Description: l.T("Help", "Aide")},
	}
}

// Run fait du long polling jusqu'à l'annulation de ctx.
func (b *Bot) Run(ctx context.Context) {
	if err := b.Telegram.SetMyCommands(ctx, commands(b.Config.Language)); err != nil {
		b.Log.Warn("setMyCommands failed", "err", err)
	}
	offset := 0
	for ctx.Err() == nil {
		updates, err := b.Telegram.GetUpdates(ctx, offset, 50*time.Second)
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			b.Log.Warn("getUpdates failed", "err", err)
			b.pollErr.Store(&err)
			sleep(ctx, 5*time.Second)
			continue
		}
		b.pollErr.Store(nil)
		b.lastPoll.Store(b.Now().UnixNano())
		for _, u := range updates {
			offset = u.UpdateID + 1
			b.HandleUpdate(ctx, u)
		}
	}
	b.wg.Wait()
}

// HandleUpdate route une mise à jour vers la commande ou le bouton concerné.
func (b *Bot) HandleUpdate(ctx context.Context, u telegram.Update) {
	switch {
	case u.CallbackQuery != nil:
		b.handleCallback(ctx, *u.CallbackQuery)
	case u.Message != nil && len(u.Message.Text) > 0 && u.Message.Text[0] == '/':
		b.handleCommand(ctx, *u.Message)
	}
}

func sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}
