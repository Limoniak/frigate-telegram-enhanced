package notifier

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"frigate-telegram-enhanced/internal/config"
	"frigate-telegram-enhanced/internal/filter"
	"frigate-telegram-enhanced/internal/frigate"
	"frigate-telegram-enhanced/internal/metrics"
	"frigate-telegram-enhanced/internal/state"
	"frigate-telegram-enhanced/internal/telegram"
)

type fakeFrigate struct {
	mu       sync.Mutex
	files    map[string][]byte
	fails    map[string]int // nombre de 404 à renvoyer avant succès
	tooLarge map[string]bool
	stalls   map[string]bool // téléchargement qui cale : files[path] n'est reçu qu'en partie
	calls    []string
	events   []frigate.APIEvent
	reviews  map[string]frigate.Review
	// rattrapage : événements par id, revues récentes, et date demandée
	byID       map[string]frigate.APIEvent
	reviewList []frigate.Review
	after      time.Time

	downloadDelay time.Duration // pause simulée dans DownloadToFile, pour tester la concurrence
	concurrent    int32         // téléchargements en cours (atomique)
	maxConcurrent int32         // pic observé (atomique)
}

func newFakeFrigate() *fakeFrigate {
	return &fakeFrigate{files: map[string][]byte{}, fails: map[string]int{}, tooLarge: map[string]bool{}, stalls: map[string]bool{}, reviews: map[string]frigate.Review{}}
}

func (f *fakeFrigate) GetBytes(_ context.Context, path string, _ int64) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, path)
	if f.tooLarge[path] {
		return nil, frigate.ErrTooLarge
	}
	if f.fails[path] > 0 {
		f.fails[path]--
		return nil, &frigate.HTTPError{Status: 404, Path: path}
	}
	b, ok := f.files[path]
	if !ok {
		return nil, &frigate.HTTPError{Status: 404, Path: path}
	}
	return b, nil
}

func (f *fakeFrigate) DownloadToFile(ctx context.Context, path string, max int64) (string, error) {
	cur := atomic.AddInt32(&f.concurrent, 1)
	defer atomic.AddInt32(&f.concurrent, -1)
	for {
		prev := atomic.LoadInt32(&f.maxConcurrent)
		if cur <= prev || atomic.CompareAndSwapInt32(&f.maxConcurrent, prev, cur) {
			break
		}
	}
	if f.downloadDelay > 0 {
		time.Sleep(f.downloadDelay)
	}
	b, err := f.GetBytes(ctx, path, max)
	if err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp("", "fake-*")
	if err != nil {
		return "", err
	}
	_, err = tmp.Write(b)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	f.mu.Lock()
	stalled := f.stalls[path]
	f.mu.Unlock()
	if err == nil && stalled {
		err = frigate.ErrIncomplete
	}
	return tmp.Name(), err
}

func (f *fakeFrigate) Review(_ context.Context, id string) (frigate.Review, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.reviews[id]
	if !ok {
		return r, &frigate.HTTPError{Status: 404, Path: "/api/review/" + id}
	}
	return r, nil
}

func (f *fakeFrigate) Events(context.Context, string, int) ([]frigate.APIEvent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.events, nil
}

func (f *fakeFrigate) Event(_ context.Context, id string) (frigate.APIEvent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ev, ok := f.byID[id]
	if !ok {
		return ev, &frigate.HTTPError{Status: 404, Path: "/api/events/" + id}
	}
	return ev, nil
}

func (f *fakeFrigate) EventsSince(_ context.Context, after time.Time, _ int) ([]frigate.APIEvent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.after = after
	var out []frigate.APIEvent
	for _, ev := range f.events {
		if !frigate.UnixTime(ev.StartTime).Before(after) {
			out = append(out, ev)
		}
	}
	return out, nil
}

func (f *fakeFrigate) ReviewsSince(_ context.Context, after time.Time, _ int) ([]frigate.Review, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.after = after
	return f.reviewList, nil
}

func (f *fakeFrigate) countCalls(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if c == path {
			n++
		}
	}
	return n
}

type tgCall struct {
	Method string
	ChatID int64
	Target int    // message visé par une édition
	Text   string // texte ou légende
	FileID string
	Data   string // contenu uploadé
	Opts   telegram.SendOptions
	Result int // message_id renvoyé
}

type fakeTelegram struct {
	mu     sync.Mutex
	calls  []tgCall
	nextID int
}

func (f *fakeTelegram) add(c tgCall) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	c.Result = f.nextID
	f.calls = append(f.calls, c)
	return f.nextID
}

func fileData(file telegram.InputFile) string {
	if file.Path != "" {
		b, _ := os.ReadFile(file.Path)
		return string(b)
	}
	return string(file.Data)
}

func (f *fakeTelegram) SendMessage(_ context.Context, chatID int64, text string, o telegram.SendOptions) (telegram.Message, error) {
	return telegram.Message{MessageID: f.add(tgCall{Method: "sendMessage", ChatID: chatID, Text: text, Opts: o})}, nil
}

func (f *fakeTelegram) SendPhoto(_ context.Context, chatID int64, file telegram.InputFile, o telegram.SendOptions) (telegram.Message, error) {
	id := f.add(tgCall{Method: "sendPhoto", ChatID: chatID, Text: o.Caption, FileID: file.FileID, Data: fileData(file), Opts: o})
	return telegram.Message{MessageID: id, Photo: []telegram.PhotoSize{{FileID: "photo-file"}}}, nil
}

func (f *fakeTelegram) SendVideo(_ context.Context, chatID int64, file telegram.InputFile, o telegram.SendOptions) (telegram.Message, error) {
	id := f.add(tgCall{Method: "sendVideo", ChatID: chatID, FileID: file.FileID, Data: fileData(file), Opts: o})
	return telegram.Message{MessageID: id, Video: &telegram.Video{FileID: "video-file"}}, nil
}

func (f *fakeTelegram) SendAnimation(_ context.Context, chatID int64, file telegram.InputFile, o telegram.SendOptions) (telegram.Message, error) {
	id := f.add(tgCall{Method: "sendAnimation", ChatID: chatID, FileID: file.FileID, Data: fileData(file), Opts: o})
	return telegram.Message{MessageID: id, Animation: &telegram.Animation{FileID: "gif-file"}}, nil
}

func (f *fakeTelegram) EditMessageCaption(_ context.Context, chatID int64, messageID int, caption string, _ *telegram.InlineKeyboardMarkup) error {
	f.add(tgCall{Method: "editMessageCaption", ChatID: chatID, Target: messageID, Text: caption})
	return nil
}

func (f *fakeTelegram) EditMessageText(_ context.Context, chatID int64, messageID int, text string, _ *telegram.InlineKeyboardMarkup) error {
	f.add(tgCall{Method: "editMessageText", ChatID: chatID, Target: messageID, Text: text})
	return nil
}

func (f *fakeTelegram) EditMessageMedia(_ context.Context, chatID int64, messageID int, kind string, file telegram.InputFile, caption string, _ *telegram.InlineKeyboardMarkup) (telegram.Message, error) {
	id := f.add(tgCall{Method: "editMessageMedia:" + kind, ChatID: chatID, Target: messageID, Text: caption, FileID: file.FileID, Data: fileData(file)})
	return telegram.Message{MessageID: id, Video: &telegram.Video{FileID: "video-file"}}, nil
}

// videos renvoie les clips livrés, qu'ils remplacent l'image ou arrivent en réponse.
func (f *fakeTelegram) videos() []tgCall {
	return append(f.byMethod("editMessageMedia:video"), f.byMethod("sendVideo")...)
}

func (f *fakeTelegram) byMethod(method string) []tgCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []tgCall
	for _, c := range f.calls {
		if c.Method == method {
			out = append(out, c)
		}
	}
	return out
}

func (f *fakeTelegram) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func find(calls []tgCall, chatID int64) (tgCall, bool) {
	for _, c := range calls {
		if c.ChatID == chatID {
			return c, true
		}
	}
	return tgCall{}, false
}

func countData(calls []tgCall, data string) int {
	n := 0
	for _, c := range calls {
		if c.Data == data {
			n++
		}
	}
	return n
}

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time      { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) Add(d time.Duration) { c.mu.Lock(); defer c.mu.Unlock(); c.t = c.t.Add(d) }

const testConfig = `
timezone: Europe/Paris
mode: %s
frigate:
  url: http://frigate:5000
  external_url: https://nvr.example
mqtt:
  broker: tcp://mqtt:1883
telegram:
  token: t
  admins: [1]
  chats:
    moi: 1
    famille: -100
notify:
  labels: [person]
  cooldown: 1m
  clip_delay: 0s
  severity: [alert]
cameras:
  jardin:
    zones: [allee]
`

type harness struct {
	n     *Notifier
	fr    *fakeFrigate
	tg    *fakeTelegram
	clock *clock
	m     *metrics.Metrics
}

// newHarness construit un notifier de test. opts permet d'ajuster Deps avant New
// (ex. MediaWorkers) sans changer les nombreux appels existants sans options.
func newHarness(t *testing.T, mode string, opts ...func(*Deps)) *harness {
	t.Helper()
	cfg, err := config.Parse([]byte(fmt.Sprintf(testConfig, mode)), func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatal(err)
	}
	clk := &clock{t: time.Date(2026, 9, 28, 14, 0, 0, 0, time.UTC)}
	st, err := state.Load(filepath.Join(t.TempDir(), "state.json"), clk.Now())
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{fr: newFakeFrigate(), tg: &fakeTelegram{}, clock: clk, m: metrics.New()}
	deps := Deps{
		Config: cfg, Engine: filter.New(cfg, st), State: st,
		Frigate: h.fr, Telegram: h.tg, Metrics: h.m,
		Log: slog.New(slog.DiscardHandler), Now: clk.Now,
		ClipRetryDelays:    []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond},
		SnapshotRetryDelay: time.Millisecond,
	}
	for _, o := range opts {
		o(&deps)
	}
	h.n = New(deps)
	return h
}

// send traite un message puis attend la fin de tous les envois déclenchés.
func (h *harness) send(t *testing.T, topic string, payload []byte) {
	t.Helper()
	h.n.Process(context.Background(), topic, payload)
	if !h.n.Wait(5 * time.Second) {
		t.Fatal("envois non terminés après 5 s")
	}
}
