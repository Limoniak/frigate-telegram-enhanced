// Package bot handles the Telegram commands and buttons (long polling).
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

// camerasTTL bounds how fresh the list of cameras is: it rarely changes, and
// /cameras, /snapshot and /pause <camera> read it on every call.
const camerasTTL = 30 * time.Second

type Bot struct {
	Deps
	lastPoll atomic.Int64
	pollErr  atomic.Pointer[error] // last getUpdates error, nil after a success
	wg       sync.WaitGroup

	serialMu sync.Mutex
	tail     chan struct{} // closed when the last queued command is done

	camMu    sync.Mutex
	camList  []string
	camUntil time.Time

	refMu   sync.Mutex
	refused []Refused           // latest refused users, oldest first
	replied map[int64]time.Time // last "your ID" reply, per user
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

// LastPoll returns the time of the last successful getUpdates (used by /healthz).
func (b *Bot) LastPoll() time.Time { return time.Unix(0, b.lastPoll.Load()) }

// PollError returns the error of the last getUpdates, nil if it succeeded.
func (b *Bot) PollError() error {
	if p := b.pollErr.Load(); p != nil {
		return *p
	}
	return nil
}

// Wait waits for the actions started in the background to finish.
func (b *Bot) Wait() { b.wg.Wait() }

func (b *Bot) async(f func()) {
	b.wg.Add(1)
	go func() {
		defer b.wg.Done()
		f()
	}()
}

// serial runs f in the background, after every command queued before it: the long
// polling loop never waits for Frigate, and "/pause" then "/resume" are always
// applied in that order.
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

// cameras returns the list of Frigate's cameras, cached for camerasTTL.
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

// commands is the command menu shown by Telegram, in the language l.
func commands(l i18n.Lang) []telegram.BotCommand {
	return []telegram.BotCommand{
		{Command: "pause", Description: l.T("Pause: /pause [duration] [camera]")},
		{Command: "resume", Description: l.T("Resume: /resume [camera]")},
		{Command: "status", Description: l.T("Service status")},
		{Command: "cameras", Description: l.T("List cameras")},
		{Command: "snapshot", Description: l.T("Live image: /snapshot [camera]")},
		{Command: "last", Description: l.T("Latest event: /last [camera]")},
		{Command: "menu", Description: l.T("Control panel: pause, mute cameras")},
		{Command: "help", Description: l.T("Help")},
	}
}

// Run long-polls until ctx is canceled.
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

// HandleUpdate routes an update to the command or button it concerns.
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
