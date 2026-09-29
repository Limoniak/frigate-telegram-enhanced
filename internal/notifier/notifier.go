// Package notifier transforme les messages MQTT de Frigate en notifications Telegram.
package notifier

import (
	"context"
	"log/slog"
	"slices"
	"sync"
	"time"

	"frigate-telegram-enhanced/internal/config"
	"frigate-telegram-enhanced/internal/filter"
	"frigate-telegram-enhanced/internal/frigate"
	"frigate-telegram-enhanced/internal/metrics"
	"frigate-telegram-enhanced/internal/state"
	"frigate-telegram-enhanced/internal/telegram"
)

const (
	maxPhotoSize        = 10 << 20
	maxUploadSize       = 50 << 20
	inboxSize           = 256
	endedRetention      = 10 * time.Minute // laisse le temps aux descriptions GenAI d'arriver
	staleAfter          = time.Hour
	defaultMediaWorkers = 4 // pool de téléchargements clip/GIF concurrents (spec §2)
)

type Frigate interface {
	GetBytes(ctx context.Context, path string, max int64) ([]byte, error)
	DownloadToFile(ctx context.Context, path string, max int64) (string, error)
	Review(ctx context.Context, id string) (frigate.Review, error)
	Events(ctx context.Context, camera string, limit int) ([]frigate.APIEvent, error)
}

type Telegram interface {
	SendMessage(ctx context.Context, chatID int64, text string, o telegram.SendOptions) (telegram.Message, error)
	SendPhoto(ctx context.Context, chatID int64, f telegram.InputFile, o telegram.SendOptions) (telegram.Message, error)
	SendVideo(ctx context.Context, chatID int64, f telegram.InputFile, o telegram.SendOptions) (telegram.Message, error)
	SendAnimation(ctx context.Context, chatID int64, f telegram.InputFile, o telegram.SendOptions) (telegram.Message, error)
	EditMessageCaption(ctx context.Context, chatID int64, messageID int, caption string, markup *telegram.InlineKeyboardMarkup) error
	EditMessageText(ctx context.Context, chatID int64, messageID int, text string, markup *telegram.InlineKeyboardMarkup) error
}

type Deps struct {
	Config             *config.Config
	Engine             *filter.Engine
	State              *state.Store
	Frigate            Frigate
	Telegram           Telegram
	Metrics            *metrics.Metrics
	Log                *slog.Logger
	Now                func() time.Time // défaut : time.Now
	ClipRetryDelays    []time.Duration  // défaut : 5 s, 10 s, 20 s
	SnapshotRetryDelay time.Duration    // défaut : 1 s
	MediaWorkers       int              // défaut : 4 (téléchargements clip/GIF concurrents)
}

type Notifier struct {
	Deps
	inbox       chan inMsg
	topics      topics
	sendCtx     context.Context
	cancelSends context.CancelFunc
	wg          sync.WaitGroup
	media       chan struct{} // sémaphore : limite les téléchargements clip/GIF concurrents

	mu      sync.Mutex // protège tracked, sent et les champs des *tracked (sauf messages)
	tracked map[string]*tracked
	sent    []time.Time
	history []HistoryEntry
	groups  map[string]*group // regroupement en cours, par destinataire
}

type inMsg struct {
	topic   string
	payload []byte
}

type topics struct{ events, reviews, updates string }

type sentMsg struct {
	id   int
	text bool // message texte (pas de photo) : s'édite avec editMessageText
}

// tracked suit un événement (mode events) ou une review (mode reviews).
type tracked struct {
	id, camera   string // immuables après création
	label        string
	subLabel     string
	zones        []string
	score        float64
	hasScore     bool
	start        time.Time
	startTS      float64
	eventIDs     []string // ids des événements Frigate liés (descriptions GenAI)
	snapshotPath string
	clipPath     string
	gifPath      string
	link         string
	description  string
	lastReason   string

	notified, silent, ended bool
	// suppressed : refusé pour cooldown. On ne le réévalue plus, sinon un objet qui
	// reste dans le champ serait notifié à l'expiration du cooldown, en plein milieu
	// de l'événement, avec un snapshot sans rapport avec son début.
	suppressed        bool
	chats             []string
	endedAt, lastSeen time.Time

	ready    chan struct{} // fermé quand le snapshot a été envoyé à tous les chats
	msgMu    sync.Mutex
	messages map[string]sentMsg // nom du chat → message envoyé
	groups   map[string]*group  // regroupements dont t est la tête, par chat (sous n.mu)
	grouped  bool               // ajouté au message d'une autre notification
}

func (t *tracked) setMessage(chat string, m sentMsg) {
	t.msgMu.Lock()
	defer t.msgMu.Unlock()
	t.messages[chat] = m
}

func (t *tracked) message(chat string) sentMsg {
	t.msgMu.Lock()
	defer t.msgMu.Unlock()
	return t.messages[chat]
}

func New(d Deps) *Notifier {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.ClipRetryDelays == nil {
		d.ClipRetryDelays = []time.Duration{5 * time.Second, 10 * time.Second, 20 * time.Second}
	}
	if d.SnapshotRetryDelay == 0 {
		d.SnapshotRetryDelay = time.Second
	}
	if d.Log == nil {
		d.Log = slog.Default()
	}
	if d.MediaWorkers == 0 {
		d.MediaWorkers = defaultMediaWorkers
	}
	p := d.Config.MQTT.TopicPrefix
	ctx, cancel := context.WithCancel(context.Background())
	return &Notifier{
		Deps:        d,
		inbox:       make(chan inMsg, inboxSize),
		topics:      topics{events: p + "/events", reviews: p + "/reviews", updates: p + "/tracked_object_update"},
		sendCtx:     ctx,
		cancelSends: cancel,
		tracked:     map[string]*tracked{},
		groups:      map[string]*group{},
		media:       make(chan struct{}, d.MediaWorkers),
	}
}

// Topics renvoie les topics MQTT à écouter selon le mode.
func (n *Notifier) Topics() []string {
	main := n.topics.events
	if n.Config.Mode == config.ModeReviews {
		main = n.topics.reviews
	}
	return []string{main, n.topics.updates}
}

// Handle est appelé par le client MQTT ; il ne bloque jamais.
func (n *Notifier) Handle(topic string, payload []byte) {
	select {
	case n.inbox <- inMsg{topic: topic, payload: payload}:
	default:
		n.Metrics.EventsDropped.Inc()
		n.Log.Warn("event queue full, message dropped", "topic", topic)
	}
}

// Run consomme la file jusqu'à l'annulation de ctx. Les envois utilisent un contexte
// distinct, annulé seulement par Shutdown, pour pouvoir se terminer proprement.
func (n *Notifier) Run(ctx context.Context) {
	sweep := time.NewTicker(time.Minute)
	defer sweep.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case m := <-n.inbox:
			n.Process(n.sendCtx, m.topic, m.payload)
		case <-sweep.C:
			n.sweep()
		}
	}
}

// Process traite un message de façon synchrone ; les envois partent en arrière-plan.
func (n *Notifier) Process(ctx context.Context, topic string, payload []byte) {
	n.mu.Lock()
	defer n.mu.Unlock()
	var err error
	switch topic {
	case n.topics.events:
		var msg frigate.EventMessage
		if msg, err = frigate.ParseEventMessage(payload); err == nil {
			n.handleEvent(ctx, msg)
		}
	case n.topics.reviews:
		var msg frigate.ReviewMessage
		if msg, err = frigate.ParseReviewMessage(payload); err == nil {
			n.handleReview(ctx, msg)
		}
	case n.topics.updates:
		var u frigate.TrackedObjectUpdate
		if u, err = frigate.ParseTrackedObjectUpdate(payload); err == nil {
			n.handleUpdate(ctx, u)
		}
	}
	if err != nil {
		n.Log.Warn("MQTT message ignored", "topic", topic, "err", err)
	}
}

// Wait attend la fin des envois en cours ; false si timeout est dépassé.
func (n *Notifier) Wait(timeout time.Duration) bool {
	done := make(chan struct{})
	go func() {
		n.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-time.After(timeout):
		return false
	}
}

// Shutdown laisse timeout aux envois en cours, puis les annule.
func (n *Notifier) Shutdown(timeout time.Duration) bool {
	ok := n.Wait(timeout)
	n.cancelSends()
	if !ok {
		n.Wait(2 * time.Second)
	}
	return ok
}

// Count24h renvoie le nombre de notifications envoyées sur les dernières 24 h.
func (n *Notifier) Count24h() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.pruneSent(n.Now())
	return len(n.sent)
}

func (n *Notifier) recordSent(now time.Time) {
	n.sent = append(n.sent, now)
	n.pruneSent(now)
}

func (n *Notifier) pruneSent(now time.Time) {
	i := slices.IndexFunc(n.sent, func(t time.Time) bool { return now.Sub(t) < 24*time.Hour })
	if i < 0 {
		n.sent = n.sent[:0]
		return
	}
	n.sent = n.sent[i:]
}

func (n *Notifier) goAsync(f func()) {
	n.wg.Add(1)
	go func() {
		defer n.wg.Done()
		f()
	}()
}

func (n *Notifier) sweep() {
	n.mu.Lock()
	defer n.mu.Unlock()
	now := n.Now()
	for chat, g := range n.groups {
		if now.Sub(g.started) > staleAfter {
			delete(n.groups, chat)
		}
	}
	for id, t := range n.tracked {
		if (t.ended && now.Sub(t.endedAt) > endedRetention) || now.Sub(t.lastSeen) > staleAfter {
			// Un événement filtré dont la fin n'est jamais arrivée doit tout de même
			// compter dans les métriques de filtrage (finish ne le verra pas).
			if !t.notified && t.lastReason != "" {
				n.Metrics.EventsFiltered.WithLabelValues(t.lastReason).Inc()
				n.record(t, false)
			}
			delete(n.tracked, id)
		}
	}
}

func (n *Notifier) uiLink(path string) string {
	if n.Config.Frigate.ExternalURL == "" {
		return ""
	}
	return n.Config.Frigate.ExternalURL + path
}

func (n *Notifier) captionData(t *tracked) captionData {
	return captionData{
		Label: t.label, SubLabel: t.subLabel, Camera: t.camera, Zones: t.zones,
		Score: t.score, HasScore: t.hasScore, Start: t.start,
		Description: t.description, Link: t.link,
	}
}

func (n *Notifier) caption(t *tracked) string {
	return buildCaption(n.captionData(t), n.Config.Location, n.Config.Language)
}

// notify marque l'événement notifié et lance l'envoi du snapshot. Appelé sous n.mu.
func (n *Notifier) notify(ctx context.Context, t *tracked, d filter.Decision) {
	now := n.Now()
	fresh, grouped := n.split(t, d.Chats, now)
	// Seuls les destinataires d'un nouveau message recevront clip et GIF en réponse.
	t.notified, t.silent, t.chats, t.label = true, d.Silent, fresh, d.Label
	t.ready = make(chan struct{})
	t.messages = map[string]sentMsg{}
	t.groups = map[string]*group{}
	t.grouped = len(fresh) == 0
	n.State.MarkNotified(filter.CooldownKey(t.camera, d.Label), now)
	n.recordSent(now)
	n.record(t, true)
	if len(grouped) > 0 {
		n.Log.Info("notification grouped", "camera", t.camera, "label", d.Label, "event_id", t.id, "chats", grouped)
		n.Metrics.NotificationsSent.WithLabelValues("grouped").Inc()
		n.addToGroups(ctx, t, grouped)
	}
	if len(fresh) == 0 {
		close(t.ready)
		return
	}
	n.startGroups(t, fresh, now)

	path := ""
	if cfg := n.Config.ForCamera(t.camera); cfg.Snapshot {
		path = t.snapshotPath
		if cfg.Crop {
			path = frigate.Cropped(path)
		}
	}
	caption, silent, chats := n.caption(t), d.SilentFor, fresh
	n.Log.Info("notification", "camera", t.camera, "label", d.Label, "event_id", t.id, "silent", d.Silent, "quiet_chats", d.QuietChats, "chats", chats)
	n.goAsync(func() { n.sendSnapshot(ctx, t, path, caption, silent, chats) })
}

// finish traite la fin d'un événement : comptage du filtrage, ou envoi du clip et du GIF. Appelé sous n.mu.
func (n *Notifier) finish(ctx context.Context, t *tracked, hasClip bool) {
	if t == nil {
		return
	}
	now := n.Now()
	t.lastSeen = now
	if !t.notified {
		if t.lastReason != "" {
			n.Metrics.EventsFiltered.WithLabelValues(t.lastReason).Inc()
			n.record(t, false)
		}
		delete(n.tracked, t.id)
		return
	}
	if t.ended {
		return
	}
	t.ended, t.endedAt = true, now
	cfg := n.Config.ForCamera(t.camera)
	chats := t.chats
	if len(chats) == 0 {
		return // regroupée dans le message d'une autre notification : ni clip ni GIF
	}
	if cfg.Clip && hasClip {
		path := t.clipPath
		n.goAsync(func() { n.sendFollowUp(ctx, t, "video", path, chats, cfg.ClipDelay) })
	}
	if cfg.GIF && t.gifPath != "" {
		path := t.gifPath
		n.goAsync(func() { n.sendFollowUp(ctx, t, "animation", path, chats, 0) })
	}
}
