// Package notifier turns Frigate's MQTT messages into Telegram notifications.
package notifier

import (
	"context"
	"log/slog"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"frigate-telegram-enhanced/internal/config"
	"frigate-telegram-enhanced/internal/filter"
	"frigate-telegram-enhanced/internal/frigate"
	"frigate-telegram-enhanced/internal/metrics"
	"frigate-telegram-enhanced/internal/state"
	"frigate-telegram-enhanced/internal/telegram"
	"frigate-telegram-enhanced/internal/transcode"
)

const (
	maxPhotoSize        = 10 << 20
	maxUploadSize       = 50 << 20
	maxCompressInput    = 512 << 20 // largest clip downloaded to be re-encoded (compress_clips)
	inboxSize           = 256
	endedRetention      = 10 * time.Minute // gives the GenAI descriptions time to arrive
	staleAfter          = time.Hour
	defaultMediaWorkers = 4 // pool of concurrent clip/GIF downloads (spec §2)
)

type Frigate interface {
	GetBytes(ctx context.Context, path string, max int64) ([]byte, error)
	DownloadToFile(ctx context.Context, path string, max int64) (string, error)
	Review(ctx context.Context, id string) (frigate.Review, error)
	Events(ctx context.Context, camera string, limit int) ([]frigate.APIEvent, error)
	// Catching up after an MQTT outage (see CatchUp).
	Event(ctx context.Context, id string) (frigate.APIEvent, error)
	EventsSince(ctx context.Context, after time.Time, limit int) ([]frigate.APIEvent, error)
	ReviewsSince(ctx context.Context, after time.Time, limit int) ([]frigate.Review, error)
	// Recordings lists the stored recording segments, to wait for the clip's (see waitRecorded).
	Recordings(ctx context.Context, camera string, after, before float64) ([]frigate.Recording, error)
}

type Telegram interface {
	SendMessage(ctx context.Context, chatID int64, text string, o telegram.SendOptions) (telegram.Message, error)
	SendPhoto(ctx context.Context, chatID int64, f telegram.InputFile, o telegram.SendOptions) (telegram.Message, error)
	SendVideo(ctx context.Context, chatID int64, f telegram.InputFile, o telegram.SendOptions) (telegram.Message, error)
	SendAnimation(ctx context.Context, chatID int64, f telegram.InputFile, o telegram.SendOptions) (telegram.Message, error)
	EditMessageCaption(ctx context.Context, chatID int64, messageID int, caption string, markup *telegram.InlineKeyboardMarkup) error
	EditMessageText(ctx context.Context, chatID int64, messageID int, text string, markup *telegram.InlineKeyboardMarkup) error
	EditMessageMedia(ctx context.Context, chatID int64, messageID int, kind string, f telegram.InputFile, caption string, markup *telegram.InlineKeyboardMarkup) (telegram.Message, error)
}

type Deps struct {
	// Schedule runs f after d (default: time.AfterFunc); replaceable in tests.
	Schedule        func(d time.Duration, f func())
	Config          *config.Config
	Engine          *filter.Engine
	State           *state.Store
	Frigate         Frigate
	Telegram        Telegram
	Metrics         *metrics.Metrics
	Log             *slog.Logger
	Now             func() time.Time // default: time.Now
	ClipRetryDelays []time.Duration  // default: 5 s, 10 s, 20 s
	// RecordingPoll and RecordingWait: how often, and at most how long, to check that
	// Frigate has stored the recording of an ended event before fetching its clip
	// (default: 2 s, 30 s).
	RecordingPoll, RecordingWait time.Duration
	SnapshotRetryDelay           time.Duration // default: 1 s
	MediaWorkers                 int           // default: 4 (concurrent clip/GIF downloads)
	HistoryFile                  string        // recent activity kept across restarts; empty: in memory only
	// Transcode re-encodes a clip under max bytes (default: transcode.Fit, ffmpeg).
	Transcode func(ctx context.Context, in string, max int64) (string, error)
}

type Notifier struct {
	Deps
	inbox       chan inMsg
	topics      topics
	sendCtx     context.Context
	cancelSends context.CancelFunc
	wg          sync.WaitGroup
	media       chan struct{} // semaphore: bounds the concurrent clip/GIF downloads
	transcoding chan struct{} // semaphore: one clip re-encoded at a time (CPU)
	dropped     atomic.Int64  // messages dropped, queue full, since startup
	lastDrop    atomic.Int64  // time of the last one (UnixNano), 0 if none

	mu        sync.Mutex // guards tracked, sent and the fields of the *tracked (except messages)
	tracked   map[string]*tracked
	sent      []time.Time
	history   []HistoryEntry
	histDirty bool              // history changed since the last write of HistoryFile
	groups    map[string]*group // regroupement en cours, par destinataire
}

type inMsg struct {
	topic   string
	payload []byte
}

type topics struct{ events, reviews, updates string }

type sentMsg struct {
	id   int
	text bool // text message (no photo): edited with editMessageText
}

// tracked follows an event (events mode) or a review (reviews mode).
type tracked struct {
	id, camera   string // immutable after creation
	label        string
	subLabel     string
	zones        []string
	score        float64
	hasScore     bool
	start        time.Time
	startTS      float64
	endTS        float64  // end of the event in Frigate's time; 0 until it ends
	eventIDs     []string // ids of the linked Frigate events (GenAI descriptions)
	snapshotPath string
	clipPath     string
	gifPath      string
	link         string
	description  string
	lastReason   string

	notified, silent, ended bool
	// suppressed: refused because of the cooldown. It is not evaluated again,
	// otherwise an object staying in view would be notified when the cooldown expires,
	// in the middle of the event, with a snapshot unrelated to its start.
	suppressed        bool
	chats             []string
	endedAt, lastSeen time.Time

	ready    chan struct{} // closed when the snapshot was sent to every chat
	msgMu    sync.Mutex
	messages map[string]sentMsg // chat name → message sent
	groups   map[string]*group  // groupings t is the head of, per chat (under n.mu)
	grouped  bool               // added to the message of another notification
	// pending: a detection that would be notified, waiting for its label for the
	// label filter (see decide); nil otherwise.
	pending *filter.Input
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
	if d.RecordingPoll == 0 {
		d.RecordingPoll = 2 * time.Second
	}
	if d.RecordingWait == 0 {
		d.RecordingWait = 30 * time.Second
	}
	if d.SnapshotRetryDelay == 0 {
		d.SnapshotRetryDelay = time.Second
	}
	if d.Log == nil {
		d.Log = slog.Default()
	}
	if d.Schedule == nil {
		d.Schedule = func(wait time.Duration, f func()) { time.AfterFunc(wait, f) }
	}
	if d.MediaWorkers == 0 {
		d.MediaWorkers = defaultMediaWorkers
	}
	if d.Transcode == nil {
		d.Transcode = transcode.Fit
	}
	p := d.Config.MQTT.TopicPrefix
	ctx, cancel := context.WithCancel(context.Background())
	n := &Notifier{
		Deps:        d,
		inbox:       make(chan inMsg, inboxSize),
		topics:      topics{events: p + "/events", reviews: p + "/reviews", updates: p + "/tracked_object_update"},
		sendCtx:     ctx,
		cancelSends: cancel,
		tracked:     map[string]*tracked{},
		groups:      map[string]*group{},
		media:       make(chan struct{}, d.MediaWorkers),
		transcoding: make(chan struct{}, 1),
	}
	n.loadHistory()
	return n
}

// Topics returns the MQTT topics to listen to, depending on the mode.
func (n *Notifier) Topics() []string {
	main := n.topics.events
	if n.Config.Mode == config.ModeReviews {
		main = n.topics.reviews
	}
	return []string{main, n.topics.updates}
}

// Handle is called by the MQTT client; it never blocks.
func (n *Notifier) Handle(topic string, payload []byte) {
	select {
	case n.inbox <- inMsg{topic: topic, payload: payload}:
	default:
		n.dropped.Add(1)
		n.lastDrop.Store(n.Now().UnixNano())
		n.Metrics.EventsDropped.Inc()
		n.Log.Warn("event queue full, message dropped", "topic", topic)
	}
}

// Dropped returns the number of MQTT messages dropped for lack of room in the
// queue since startup, and the time of the last one (zero if none).
func (n *Notifier) Dropped() (int, time.Time) {
	last := n.lastDrop.Load()
	if last == 0 {
		return 0, time.Time{}
	}
	return int(n.dropped.Load()), time.Unix(0, last)
}

// Run consumes the queue until ctx is canceled. Sends use a separate context,
// canceled only by Shutdown, so that they can finish cleanly.
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

// Process handles a message synchronously; the sends go out in the background.
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
	case recheckTopic:
		n.recheck(ctx, string(payload))
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

// Wait waits for the sends in progress to finish; false if timeout is exceeded.
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

// Shutdown gives the sends in progress timeout to finish, then cancels them.
func (n *Notifier) Shutdown(timeout time.Duration) bool {
	ok := n.Wait(timeout)
	n.cancelSends()
	if !ok {
		n.Wait(2 * time.Second)
	}
	return ok
}

// Count24h returns the number of notifications sent over the last 24 h.
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
			// A filtered event whose end never arrived must still count in the
			// filtering metrics (finish will not see it).
			if !t.notified && t.lastReason != "" {
				n.Metrics.EventsFiltered.WithLabelValues(t.lastReason).Inc()
				n.record(t, false)
			}
			delete(n.tracked, id)
		}
	}
}

func (n *Notifier) uiLink(path string) string {
	base := n.Config.ExternalURL()
	if base == "" {
		return ""
	}
	return base + path
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

// notify marks the event notified and starts sending the snapshot. Called under n.mu.
func (n *Notifier) notify(ctx context.Context, t *tracked, d filter.Decision) {
	now := n.Now()
	fresh, grouped := n.split(t, d.Chats, now)
	// Only the recipients of a new message will get the clip and GIF as a reply.
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

// finish handles the end of an event: counting the filtering, or sending the clip
// and GIF. end is the end of the event in Frigate's time (0: unknown). Called under n.mu.
func (n *Notifier) finish(ctx context.Context, t *tracked, hasClip bool, end float64) {
	if t == nil {
		return
	}
	now := n.Now()
	if t.endTS == 0 {
		t.endTS = end
	}
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
		return // grouped into another notification's message: no clip or GIF
	}
	clip := cfg.Clip && hasClip
	if clip {
		path := t.clipPath
		n.goAsync(func() { n.sendFollowUp(ctx, t, "video", path, chats, cfg.ClipDelay, cfg.MediaInPlace) })
	}
	if cfg.GIF && t.gifPath != "" {
		// With a clip, the clip takes the image's place: the GIF arrives as a reply.
		path, inPlace := t.gifPath, cfg.MediaInPlace && !clip
		n.goAsync(func() { n.sendFollowUp(ctx, t, "animation", path, chats, 0, inPlace) })
	}
}

// setSubLabel records the label Frigate assigns to an object already notified
// (custom classification, face, plate) and updates the caption of the messages
// sent, without a new ring. Called under n.mu.
func (n *Notifier) setSubLabel(ctx context.Context, t *tracked, sub string) {
	if sub == "" || sub == t.subLabel {
		return
	}
	t.subLabel = sub
	n.updateHistory(t)
	for _, chat := range t.chats {
		n.goAsync(func() { n.editCaption(ctx, t, chat) })
	}
}

// recheckTopic is an internal "topic": the end of the wait for a label goes through
// the same queue as the MQTT messages, to be handled under n.mu, in order.
const recheckTopic = "\x00recheck-sub-label"

// decide evaluates a detection not notified yet and notifies it, refuses it, or
// holds it until its label arrives: with a filter on labels, a detection without a
// label waits up to SubLabelWait for Frigate to classify it. final (end of the wait
// or of the event) notifies without waiting any longer: the object is unknown.
// Called under n.mu.
func (n *Notifier) decide(ctx context.Context, t *tracked, in filter.Input, now time.Time, final bool) {
	d := n.Engine.Evaluate(in, now)
	if !d.Notify {
		t.lastReason = d.Reason
		t.suppressed = d.Reason == filter.ReasonCooldown
		t.pending = nil
		return
	}
	cfg := n.Config.ForCamera(t.camera)
	known := slices.ContainsFunc(in.SubLabels, func(s string) bool { return s != "" })
	if !final && !known && cfg.FiltersSubLabels() && cfg.SubLabelWait > 0 {
		if t.pending == nil {
			id := t.id
			n.Schedule(cfg.SubLabelWait, func() { n.Handle(recheckTopic, []byte(id)) })
		}
		t.pending = &in // the latest version of the detection will be evaluated at the end
		return
	}
	t.pending = nil
	n.notify(ctx, t, d)
}

// recheck ends the wait for a detection's label: with no label arrived in the
// meantime, the object is considered unknown and notified (if the other rules still
// allow it). Called under n.mu.
func (n *Notifier) recheck(ctx context.Context, id string) {
	t := n.tracked[id]
	if t == nil || t.pending == nil || t.notified || t.suppressed {
		return
	}
	n.decide(ctx, t, *t.pending, n.Now(), true)
}
