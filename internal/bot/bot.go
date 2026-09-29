// Package bot traite les commandes et les boutons Telegram (long polling).
package bot

import (
	"context"
	"log/slog"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"frigate-telegram/internal/config"
	"frigate-telegram/internal/state"
	"frigate-telegram/internal/telegram"
)

type Telegram interface {
	GetUpdates(ctx context.Context, offset int, timeout time.Duration) ([]telegram.Update, error)
	SetMyCommands(ctx context.Context, cmds []telegram.BotCommand) error
	SendMessage(ctx context.Context, chatID int64, text string, o telegram.SendOptions) (telegram.Message, error)
	SendPhoto(ctx context.Context, chatID int64, f telegram.InputFile, o telegram.SendOptions) (telegram.Message, error)
	AnswerCallbackQuery(ctx context.Context, id, text string) error
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
	wg       sync.WaitGroup

	serialMu sync.Mutex
	tail     chan struct{} // fermé quand la dernière commande mise en file est terminée

	camMu    sync.Mutex
	camList  []string
	camUntil time.Time
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

var commands = []telegram.BotCommand{
	{Command: "pause", Description: "Mettre en pause : /pause [durée] [caméra]"},
	{Command: "resume", Description: "Reprendre : /resume [caméra]"},
	{Command: "status", Description: "État du service"},
	{Command: "cameras", Description: "Liste des caméras"},
	{Command: "snapshot", Description: "Image en direct : /snapshot [caméra]"},
	{Command: "last", Description: "Dernier événement : /last [caméra]"},
	{Command: "help", Description: "Aide"},
}

// Run fait du long polling jusqu'à l'annulation de ctx.
func (b *Bot) Run(ctx context.Context) {
	if err := b.Telegram.SetMyCommands(ctx, commands); err != nil {
		b.Log.Warn("setMyCommands échoué", "err", err)
	}
	offset := 0
	for ctx.Err() == nil {
		updates, err := b.Telegram.GetUpdates(ctx, offset, 50*time.Second)
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			b.Log.Warn("getUpdates échoué", "err", err)
			sleep(ctx, 5*time.Second)
			continue
		}
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
