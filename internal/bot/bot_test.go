package bot

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"frigate-telegram-enhanced/internal/config"
	"frigate-telegram-enhanced/internal/i18n"
	"frigate-telegram-enhanced/internal/state"
	"frigate-telegram-enhanced/internal/telegram"
)

type fakeTG struct {
	mu           sync.Mutex
	messages     []string
	photos       []string
	answers      []string
	keyboards    int
	edits        []string
	lastMarkup   *telegram.InlineKeyboardMarkup
	photoReplyTo int
}

func (f *fakeTG) GetUpdates(context.Context, int, time.Duration) ([]telegram.Update, error) {
	return nil, nil
}
func (f *fakeTG) SetMyCommands(context.Context, []telegram.BotCommand) error { return nil }
func (f *fakeTG) SendMessage(_ context.Context, _ int64, text string, o telegram.SendOptions) (telegram.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.messages = append(f.messages, text)
	if o.Markup != nil {
		f.keyboards++
		f.lastMarkup = o.Markup
	}
	return telegram.Message{MessageID: len(f.messages)}, nil
}
func (f *fakeTG) SendPhoto(_ context.Context, _ int64, _ telegram.InputFile, o telegram.SendOptions) (telegram.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.photos = append(f.photos, o.Caption)
	f.photoReplyTo = o.ReplyTo
	return telegram.Message{MessageID: 1}, nil
}
func (f *fakeTG) AnswerCallbackQuery(_ context.Context, _, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.answers = append(f.answers, text)
	return nil
}
func (f *fakeTG) EditMessageText(_ context.Context, _ int64, id int, text string, m *telegram.InlineKeyboardMarkup) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.edits = append(f.edits, text)
	f.lastMarkup = m
	return nil
}
func (f *fakeTG) last() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.messages) == 0 {
		return ""
	}
	return f.messages[len(f.messages)-1]
}

type fakeFR struct{}

func (fakeFR) Cameras(context.Context) ([]string, error)               { return []string{"garage", "jardin"}, nil }
func (fakeFR) GetBytes(context.Context, string, int64) ([]byte, error) { return []byte("jpeg"), nil }

type fakeNotif struct {
	mu    sync.Mutex
	clips []string
	lasts []string
}

func (f *fakeNotif) SendClipTo(_ context.Context, chatID int64, replyTo int, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.clips = append(f.clips, fmt.Sprintf("%d/%d/%s", chatID, replyTo, id))
	return nil
}
func (f *fakeNotif) SendLast(_ context.Context, _ int64, camera string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lasts = append(f.lasts, camera)
	return nil
}
func (f *fakeNotif) Count24h() int { return 3 }

const cfgYAML = `
timezone: Europe/Paris
frigate: {url: "http://f"}
mqtt: {broker: "tcp://m:1883"}
telegram: {token: t, admins: [1], chats: {moi: 1}}
cameras:
  salon: {enabled: false}
`

var now = time.Date(2026, 9, 28, 14, 0, 0, 0, time.UTC)

type env struct {
	b     *Bot
	tg    *fakeTG
	notif *fakeNotif
	st    *state.Store
}

func newEnv(t *testing.T) env {
	t.Helper()
	cfg, err := config.Parse([]byte(cfgYAML), func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatal(err)
	}
	st, _ := state.Load(filepath.Join(t.TempDir(), "state.json"), now)
	e := env{tg: &fakeTG{}, notif: &fakeNotif{}, st: st}
	e.b = New(Deps{Config: cfg, Telegram: e.tg, Frigate: fakeFR{}, Notifier: e.notif, State: st,
		Log: slog.New(slog.DiscardHandler), Now: func() time.Time { return now }, MQTTConnected: func() bool { return true }})
	return e
}

func (e env) cmd(text string, from int64) {
	e.b.HandleUpdate(context.Background(), telegram.Update{Message: &telegram.Message{
		MessageID: 10, From: &telegram.User{ID: from}, Chat: telegram.Chat{ID: from}, Text: text}})
	e.b.Wait()
}

func (e env) callback(data string, from int64) {
	e.b.HandleUpdate(context.Background(), telegram.Update{CallbackQuery: &telegram.CallbackQuery{
		ID: "q", From: telegram.User{ID: from}, Data: data,
		Message: &telegram.Message{MessageID: 77, Chat: telegram.Chat{ID: 1}}}})
	e.b.Wait()
}

func TestParseCommand(t *testing.T) {
	name, args := parseCommand("/Pause@MonBot 30m jardin")
	if name != "pause" || len(args) != 2 || args[0] != "30m" || args[1] != "jardin" {
		t.Errorf("parseCommand = %q %v", name, args)
	}
}

func TestParseDuration(t *testing.T) {
	ok := map[string]time.Duration{"30m": 30 * time.Minute, "2h": 2 * time.Hour, "1h30m": 90 * time.Minute, "1d": 24 * time.Hour, "0": 0}
	for s, want := range ok {
		if got, err := ParseDuration(s); err != nil || got != want {
			t.Errorf("ParseDuration(%q) = %v, %v", s, got, err)
		}
	}
	for _, s := range []string{"jardin", "-1h", "xd", ""} {
		if _, err := ParseDuration(s); err == nil {
			t.Errorf("ParseDuration(%q) should have failed", s)
		}
	}
}

func TestPauseGlobalDefaultsToOneHour(t *testing.T) {
	e := newEnv(t)
	e.cmd("/pause", 1)
	if !e.st.IsPaused(now.Add(59*time.Minute)) || e.st.IsPaused(now.Add(61*time.Minute)) {
		t.Error("want a one-hour pause")
	}
	if !strings.Contains(e.tg.last(), "until Sep 28 17:00") {
		t.Errorf("reply = %q", e.tg.last())
	}
}

func TestPauseCameraWithDuration(t *testing.T) {
	e := newEnv(t)
	e.cmd("/pause jardin 2h", 1)
	if !e.st.IsMuted("jardin", now.Add(time.Hour)) || e.st.IsPaused(now) {
		t.Error("only the jardin camera must be muted")
	}
}

func TestPauseZeroIsForever(t *testing.T) {
	e := newEnv(t)
	e.cmd("/pause 0", 1)
	if !e.st.IsPaused(now.AddDate(1, 0, 0)) || !strings.Contains(e.tg.last(), "/resume") {
		t.Errorf("want an unlimited pause, reply %q", e.tg.last())
	}
}

func TestPauseUnknownCamera(t *testing.T) {
	e := newEnv(t)
	e.cmd("/pause cuisine", 1)
	if e.st.IsMuted("cuisine", now) || !strings.Contains(e.tg.last(), "Unknown camera") {
		t.Errorf("reply = %q", e.tg.last())
	}
}

func TestResume(t *testing.T) {
	e := newEnv(t)
	e.cmd("/pause", 1)
	e.cmd("/pause jardin", 1)
	e.cmd("/resume jardin", 1)
	if e.st.IsMuted("jardin", now) || !e.st.IsPaused(now) {
		t.Error("/resume jardin must only unmute the camera")
	}
	e.cmd("/resume", 1)
	if e.st.IsPaused(now) {
		t.Error("/resume must resume everything")
	}
}

func TestUnauthorizedUserIgnored(t *testing.T) {
	e := newEnv(t)
	e.cmd("/pause", 999)
	if e.st.IsPaused(now) || len(e.tg.messages) != 0 {
		t.Error("a non-admin must not be able to do anything")
	}
}

func TestStatus(t *testing.T) {
	e := newEnv(t)
	e.cmd("/pause jardin 1h", 1)
	e.cmd("/status", 1)
	out := e.tg.last()
	for _, want := range []string{"MQTT: ✅", "Notifications active", "🔇 jardin", "3 notification(s)"} {
		if !strings.Contains(out, want) {
			t.Errorf("status without %q:\n%s", want, out)
		}
	}
}

func TestCameras(t *testing.T) {
	e := newEnv(t)
	e.cmd("/cameras", 1)
	if out := e.tg.last(); !strings.Contains(out, "✅ garage") || !strings.Contains(out, "✅ jardin") {
		t.Errorf("cameras = %q", out)
	}
}

func TestSnapshotCommands(t *testing.T) {
	e := newEnv(t)
	e.cmd("/snapshot", 1)
	if e.tg.keyboards != 1 {
		t.Error("want a selection keyboard without an argument")
	}
	e.cmd("/snapshot jardin", 1)
	if len(e.tg.photos) != 1 || !strings.Contains(e.tg.photos[0], "jardin") {
		t.Errorf("photos = %v", e.tg.photos)
	}
}

func TestLast(t *testing.T) {
	e := newEnv(t)
	e.cmd("/last garage", 1)
	if len(e.notif.lasts) != 1 || e.notif.lasts[0] != "garage" {
		t.Errorf("lasts = %v", e.notif.lasts)
	}
}

func TestCallbacks(t *testing.T) {
	e := newEnv(t)
	e.callback("m:jardin:3600", 1)
	if !e.st.IsMuted("jardin", now.Add(30*time.Minute)) {
		t.Error("mute button had no effect")
	}
	e.callback("p:1800", 1)
	if !e.st.IsPaused(now.Add(20 * time.Minute)) {
		t.Error("pause button had no effect")
	}
	e.callback("c:evt1", 1)
	if len(e.notif.clips) != 1 || e.notif.clips[0] != "1/77/evt1" {
		t.Errorf("clips = %v", e.notif.clips)
	}
	e.callback("s:garage", 1)
	if len(e.tg.photos) != 1 {
		t.Error("snapshot button had no effect")
	}
}

func TestCallbackUnauthorized(t *testing.T) {
	e := newEnv(t)
	e.callback("p:1800", 999)
	if e.st.IsPaused(now) || len(e.tg.answers) != 1 || !strings.Contains(e.tg.answers[0], "Not allowed") || !strings.Contains(e.tg.answers[0], "999") {
		t.Errorf("answers = %v", e.tg.answers)
	}
}

// slowFR blocks Cameras until release is closed, and counts the calls.
type slowFR struct {
	fakeFR
	release chan struct{}
	calls   *int32
}

func (f slowFR) Cameras(ctx context.Context) ([]string, error) {
	atomic.AddInt32(f.calls, 1)
	<-f.release
	return f.fakeFR.Cameras(ctx)
}

// A command waiting for Frigate must not block the polling loop, and the replies
// must keep the order of the commands.
func TestCommandsDoNotBlockPollingAndKeepOrder(t *testing.T) {
	e := newEnv(t)
	var calls int32
	fr := slowFR{release: make(chan struct{}), calls: &calls}
	e.b.Frigate = fr
	send := func(text string) {
		e.b.HandleUpdate(context.Background(), telegram.Update{Message: &telegram.Message{
			MessageID: 10, From: &telegram.User{ID: 1}, Chat: telegram.Chat{ID: 1}, Text: text}})
	}

	returned := make(chan struct{})
	go func() {
		send("/cameras")
		send("/help")
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("HandleUpdate waits for Frigate")
	}
	close(fr.release)
	e.b.Wait()

	e.tg.mu.Lock()
	defer e.tg.mu.Unlock()
	if len(e.tg.messages) != 2 || !strings.Contains(e.tg.messages[0], "Cameras") || !strings.Contains(e.tg.messages[1], "Commands") {
		t.Errorf("replies = %q, want /cameras then /help", e.tg.messages)
	}
}

func TestCameraListIsCached(t *testing.T) {
	e := newEnv(t)
	var calls int32
	release := make(chan struct{})
	close(release)
	e.b.Frigate = slowFR{release: release, calls: &calls}
	clock := now
	e.b.Now = func() time.Time { return clock }

	e.cmd("/cameras", 1)
	e.cmd("/pause garage", 1)
	if calls != 1 {
		t.Errorf("calls to Frigate = %d, want 1 (cache)", calls)
	}
	clock = clock.Add(camerasTTL + time.Second)
	e.cmd("/cameras", 1)
	if calls != 2 {
		t.Errorf("calls to Frigate = %d, want 2 after the cache expired", calls)
	}
}

func TestFrenchReplies(t *testing.T) {
	e := newEnv(t)
	e.b.Config.Language = i18n.FR
	e.cmd("/pause", 1)
	if !strings.Contains(e.tg.last(), "Notifications en pause jusqu'à 28/09 17:00") {
		t.Errorf("reply = %q", e.tg.last())
	}
	e.cmd("/pause cuisine", 1)
	if !strings.Contains(e.tg.last(), "Caméra inconnue") {
		t.Errorf("reply = %q", e.tg.last())
	}
	if c := commands(i18n.FR); c[0].Description != "Mettre en pause : /pause [durée] [caméra]" {
		t.Errorf("menu = %+v", c[0])
	}
}

func TestStatusShowsPresence(t *testing.T) {
	e := newEnv(t)
	e.b.Config.Presence = config.Presence{Topics: []string{"homeassistant/person/+/state"}}
	e.cmd("/status", 1)
	if !strings.Contains(e.tg.last(), "Nobody at home") {
		t.Errorf("status = %q", e.tg.last())
	}
	e.st.SetPresence("homeassistant/person/alice/state", true)
	e.cmd("/status", 1)
	if !strings.Contains(e.tg.last(), "At home: alice") {
		t.Errorf("status = %q", e.tg.last())
	}
}

// buttonsOf flattens a keyboard into "text → data".
func buttonsOf(m *telegram.InlineKeyboardMarkup) map[string]string {
	out := map[string]string{}
	for _, row := range m.InlineKeyboard {
		for _, k := range row {
			out[k.Text] = k.CallbackData
		}
	}
	return out
}

func TestMenu(t *testing.T) {
	e := newEnv(t)
	e.cmd("/menu", 1)
	if !strings.Contains(e.tg.last(), "Control") || !strings.Contains(e.tg.last(), "Notifications active") {
		t.Fatalf("menu = %q", e.tg.last())
	}
	btns := buttonsOf(e.tg.lastMarkup)
	for _, want := range []string{"⏸ 30 min", "✅ garage", "✅ jardin", "🔄 Refresh"} {
		if btns[want] == "" {
			t.Fatalf("button %q missing: %v", want, btns)
		}
	}
	press := func(data string) {
		e.b.HandleUpdate(context.Background(), telegram.Update{CallbackQuery: &telegram.CallbackQuery{
			ID: "q", From: telegram.User{ID: 1}, Data: data,
			Message: &telegram.Message{MessageID: 7, Chat: telegram.Chat{ID: 1}}}})
		e.b.Wait()
	}

	press(btns["✅ garage"])
	if !e.st.IsMuted("garage", now.Add(59*time.Minute)) {
		t.Error("garage must be muted for 1 h")
	}
	if len(e.tg.edits) != 1 || !strings.Contains(e.tg.edits[0], "🔇 garage") {
		t.Fatalf("the menu must be redrawn with garage muted: %q", e.tg.edits)
	}
	btns = buttonsOf(e.tg.lastMarkup)
	press(btns["🔇 garage"])
	if e.st.IsMuted("garage", now) {
		t.Error("garage must be back on")
	}

	press(btns["⏸ 1 h"])
	if !e.st.IsPaused(now.Add(30 * time.Minute)) {
		t.Error("want a 1 h pause")
	}
	btns = buttonsOf(e.tg.lastMarkup)
	if btns["▶️ Resume"] == "" || btns["⏸ 30 min"] != "" {
		t.Fatalf("when paused, the menu offers to resume: %v", btns)
	}
	press(btns["▶️ Resume"])
	if e.st.IsPaused(now) {
		t.Error("want a resume")
	}
}

// "📷 Now" on a notification: the live image, as a reply to it.
func TestNowButtonRepliesWithLiveImage(t *testing.T) {
	e := newEnv(t)
	e.b.HandleUpdate(context.Background(), telegram.Update{CallbackQuery: &telegram.CallbackQuery{
		ID: "q", From: telegram.User{ID: 1}, Data: "s:garage",
		Message: &telegram.Message{MessageID: 42, Chat: telegram.Chat{ID: 1}}}})
	e.b.Wait()
	e.tg.mu.Lock()
	defer e.tg.mu.Unlock()
	if len(e.tg.photos) != 1 || !strings.Contains(e.tg.photos[0], "garage") || !strings.Contains(e.tg.photos[0], "live at") {
		t.Errorf("photos = %q", e.tg.photos)
	}
	if e.tg.photoReplyTo != 42 {
		t.Errorf("reply to %d, want 42 (the notification)", e.tg.photoReplyTo)
	}
}

func (e env) cmdIn(text string, from telegram.User, chat telegram.Chat) {
	e.b.HandleUpdate(context.Background(), telegram.Update{Message: &telegram.Message{
		MessageID: 10, From: &from, Chat: chat, Text: text}})
	e.b.Wait()
}

func TestUnauthorizedPrivateUserLearnsTheirID(t *testing.T) {
	e := newEnv(t)
	alice := telegram.User{ID: 999, FirstName: "Alice", Username: "alice"}
	e.cmdIn("/start", alice, telegram.Chat{ID: 999, Type: "private"})
	if len(e.tg.messages) != 1 || !strings.Contains(e.tg.last(), "<code>999</code>") || !strings.Contains(e.tg.last(), "TELEGRAM_CHAT_ID") {
		t.Fatalf("reply = %q", e.tg.messages)
	}
	e.cmdIn("/pause", alice, telegram.Chat{ID: 999, Type: "private"})
	if e.st.IsPaused(now) {
		t.Error("a non-admin must not be able to do anything")
	}
	if len(e.tg.messages) != 1 {
		t.Errorf("%d replies, want 1: a single reply per period", len(e.tg.messages))
	}
}

func TestUnauthorizedInGroupStaysSilent(t *testing.T) {
	e := newEnv(t)
	e.cmdIn("/status", telegram.User{ID: 999}, telegram.Chat{ID: -100, Type: "group"})
	if len(e.tg.messages) != 0 {
		t.Errorf("replies = %q: no reply in a group", e.tg.messages)
	}
}

func TestRefusedUsersAreListed(t *testing.T) {
	e := newEnv(t)
	if got := e.b.Refused(); len(got) != 0 {
		t.Fatalf("Refused() = %+v", got)
	}
	e.cmdIn("/start", telegram.User{ID: 999, FirstName: "Alice", Username: "alice"}, telegram.Chat{ID: 999, Type: "private"})
	e.cmdIn("/menu", telegram.User{ID: 999, FirstName: "Alice", Username: "alice"}, telegram.Chat{ID: 999, Type: "private"})
	e.callback("p:1800", 555)
	got := e.b.Refused()
	if len(got) != 2 {
		t.Fatalf("Refused() = %+v, want 2 users", got)
	}
	if got[0].ID != 555 || got[1].ID != 999 || got[1].Name != "Alice" || got[1].Username != "alice" || !got[1].At.Equal(now) {
		t.Errorf("Refused() = %+v (most recent first)", got)
	}
	for i := range maxRefused + 5 {
		e.callback("p:1800", int64(1000+i))
	}
	if n := len(e.b.Refused()); n != maxRefused {
		t.Errorf("%d refused users kept, want %d", n, maxRefused)
	}
}
