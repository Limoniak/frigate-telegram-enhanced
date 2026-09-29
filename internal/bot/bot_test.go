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
	mu         sync.Mutex
	messages   []string
	photos     []string
	answers    []string
	keyboards  int
	edits      []string
	lastMarkup *telegram.InlineKeyboardMarkup
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
			t.Errorf("ParseDuration(%q) aurait dû échouer", s)
		}
	}
}

func TestPauseGlobalDefaultsToOneHour(t *testing.T) {
	e := newEnv(t)
	e.cmd("/pause", 1)
	if !e.st.IsPaused(now.Add(59*time.Minute)) || e.st.IsPaused(now.Add(61*time.Minute)) {
		t.Error("pause d'une heure attendue")
	}
	if !strings.Contains(e.tg.last(), "until Sep 28 17:00") {
		t.Errorf("réponse = %q", e.tg.last())
	}
}

func TestPauseCameraWithDuration(t *testing.T) {
	e := newEnv(t)
	e.cmd("/pause jardin 2h", 1)
	if !e.st.IsMuted("jardin", now.Add(time.Hour)) || e.st.IsPaused(now) {
		t.Error("seule la caméra jardin doit être coupée")
	}
}

func TestPauseZeroIsForever(t *testing.T) {
	e := newEnv(t)
	e.cmd("/pause 0", 1)
	if !e.st.IsPaused(now.AddDate(1, 0, 0)) || !strings.Contains(e.tg.last(), "/resume") {
		t.Errorf("pause illimitée attendue, réponse %q", e.tg.last())
	}
}

func TestPauseUnknownCamera(t *testing.T) {
	e := newEnv(t)
	e.cmd("/pause cuisine", 1)
	if e.st.IsMuted("cuisine", now) || !strings.Contains(e.tg.last(), "Unknown camera") {
		t.Errorf("réponse = %q", e.tg.last())
	}
}

func TestResume(t *testing.T) {
	e := newEnv(t)
	e.cmd("/pause", 1)
	e.cmd("/pause jardin", 1)
	e.cmd("/resume jardin", 1)
	if e.st.IsMuted("jardin", now) || !e.st.IsPaused(now) {
		t.Error("/resume jardin ne doit lever que la caméra")
	}
	e.cmd("/resume", 1)
	if e.st.IsPaused(now) {
		t.Error("/resume doit tout reprendre")
	}
}

func TestUnauthorizedUserIgnored(t *testing.T) {
	e := newEnv(t)
	e.cmd("/pause", 999)
	if e.st.IsPaused(now) || len(e.tg.messages) != 0 {
		t.Error("un non-admin ne doit rien pouvoir faire")
	}
}

func TestStatus(t *testing.T) {
	e := newEnv(t)
	e.cmd("/pause jardin 1h", 1)
	e.cmd("/status", 1)
	out := e.tg.last()
	for _, want := range []string{"MQTT: ✅", "Notifications active", "🔇 jardin", "3 notification(s)"} {
		if !strings.Contains(out, want) {
			t.Errorf("status sans %q :\n%s", want, out)
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
		t.Error("un clavier de sélection est attendu sans argument")
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
		t.Error("bouton mute sans effet")
	}
	e.callback("p:1800", 1)
	if !e.st.IsPaused(now.Add(20 * time.Minute)) {
		t.Error("bouton pause sans effet")
	}
	e.callback("c:evt1", 1)
	if len(e.notif.clips) != 1 || e.notif.clips[0] != "1/77/evt1" {
		t.Errorf("clips = %v", e.notif.clips)
	}
	e.callback("s:garage", 1)
	if len(e.tg.photos) != 1 {
		t.Error("bouton snapshot sans effet")
	}
}

func TestCallbackUnauthorized(t *testing.T) {
	e := newEnv(t)
	e.callback("p:1800", 999)
	if e.st.IsPaused(now) || len(e.tg.answers) != 1 || !strings.Contains(e.tg.answers[0], "Not allowed") {
		t.Errorf("answers = %v", e.tg.answers)
	}
}

// slowFR bloque Cameras jusqu'à la fermeture de release et compte les appels.
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

// Une commande qui attend Frigate ne doit pas bloquer la boucle de polling, et les
// réponses doivent garder l'ordre des commandes.
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
		t.Fatal("HandleUpdate attend Frigate")
	}
	close(fr.release)
	e.b.Wait()

	e.tg.mu.Lock()
	defer e.tg.mu.Unlock()
	if len(e.tg.messages) != 2 || !strings.Contains(e.tg.messages[0], "Cameras") || !strings.Contains(e.tg.messages[1], "Commands") {
		t.Errorf("réponses = %q, attendu /cameras puis /help", e.tg.messages)
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
		t.Errorf("appels à Frigate = %d, attendu 1 (cache)", calls)
	}
	clock = clock.Add(camerasTTL + time.Second)
	e.cmd("/cameras", 1)
	if calls != 2 {
		t.Errorf("appels à Frigate = %d, attendu 2 après expiration du cache", calls)
	}
}

func TestFrenchReplies(t *testing.T) {
	e := newEnv(t)
	e.b.Config.Language = i18n.FR
	e.cmd("/pause", 1)
	if !strings.Contains(e.tg.last(), "Notifications en pause jusqu'à 28/09 17:00") {
		t.Errorf("réponse = %q", e.tg.last())
	}
	e.cmd("/pause cuisine", 1)
	if !strings.Contains(e.tg.last(), "Caméra inconnue") {
		t.Errorf("réponse = %q", e.tg.last())
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

// buttonsOf aplatit un clavier en « texte → données ».
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
			t.Fatalf("bouton %q absent : %v", want, btns)
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
		t.Error("garage doit être coupée 1 h")
	}
	if len(e.tg.edits) != 1 || !strings.Contains(e.tg.edits[0], "🔇 garage") {
		t.Fatalf("le menu doit être redessiné avec garage coupée : %q", e.tg.edits)
	}
	btns = buttonsOf(e.tg.lastMarkup)
	press(btns["🔇 garage"])
	if e.st.IsMuted("garage", now) {
		t.Error("garage doit être réactivée")
	}

	press(btns["⏸ 1 h"])
	if !e.st.IsPaused(now.Add(30 * time.Minute)) {
		t.Error("pause 1 h attendue")
	}
	btns = buttonsOf(e.tg.lastMarkup)
	if btns["▶️ Resume"] == "" || btns["⏸ 30 min"] != "" {
		t.Fatalf("en pause, le menu propose de reprendre : %v", btns)
	}
	press(btns["▶️ Resume"])
	if e.st.IsPaused(now) {
		t.Error("reprise attendue")
	}
}
