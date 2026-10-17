package notifier

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"frigate-telegram-enhanced/internal/frigate"
)

// historySize bounds the history kept for the web interface.
const historySize = 50

// HistoryEntry describes the outcome of a detection: notified, or ignored and why.
type HistoryEntry struct {
	ID       string    `json:"id"`
	At       time.Time `json:"at"`
	Camera   string    `json:"camera"`
	Label    string    `json:"label"`
	Sound    bool      `json:"sound,omitempty"`     // Label is a sound of the audio detection
	SubLabel string    `json:"sub_label,omitempty"` // Frigate's label ("clio 3 océane"…)
	Zones    []string  `json:"zones"`
	Score    float64   `json:"score,omitempty"`
	Sent     bool      `json:"sent"`
	Grouped  bool      `json:"grouped,omitempty"` // added to the message of a previous notification
	Reason   string    `json:"reason,omitempty"`  // reason for the filtering (see filter.Reason*)
	Link     string    `json:"link,omitempty"`    // the notification's "Open in Frigate" link, empty without an address
	Thumb    string    `json:"-"`                 // Frigate path of the thumbnail, empty if none
	Snapshot string    `json:"-"`                 // Frigate path of the snapshot, empty if none
}

// storedEntry is an entry as written to HistoryFile: the thumbnail, which the
// interface does not see, must still survive a restart.
type storedEntry struct {
	HistoryEntry
	Thumb    string `json:"thumb,omitempty"`
	Snapshot string `json:"snapshot,omitempty"`
}

// record adds the outcome of a tracked item to the history. Called under n.mu.
func (n *Notifier) record(t *tracked, sent bool) {
	e := HistoryEntry{
		ID: t.id, At: t.start, Camera: t.camera, Label: t.label, Sound: t.sound, SubLabel: t.subLabel,
		Zones: slices.Clone(t.zones), Sent: sent, Link: t.link,
	}
	if !sent {
		e.Reason = t.lastReason
	}
	e.Grouped = sent && t.grouped
	if t.hasScore {
		e.Score = t.score
	}
	if len(t.eventIDs) > 0 {
		e.Thumb = frigate.EventThumbnailPath(t.eventIDs[0])
		e.Snapshot = frigate.EventSnapshotPath(t.eventIDs[0])
	}
	if e.At.IsZero() {
		e.At = n.Now()
	}
	n.history = append(n.history, e)
	if len(n.history) > historySize {
		n.history = slices.Delete(n.history, 0, len(n.history)-historySize)
	}
	n.histDirty = true
}

// updateHistory carries into the history the label that arrived afterwards. Called under n.mu.
func (n *Notifier) updateHistory(t *tracked) {
	for i := range n.history {
		if n.history[i].ID == t.id {
			n.history[i].SubLabel = t.subLabel
			n.histDirty = true
		}
	}
}

// History returns the latest detections, most recent first.
func (n *Notifier) History() []HistoryEntry {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := slices.Clone(n.history)
	slices.Reverse(out)
	return out
}

// HistoryThumb returns the Frigate path of the thumbnail of a history entry. Only
// the ids present in the history are accepted: the interface cannot use it to read
// anything on Frigate.
func (n *Notifier) HistoryThumb(id string) (string, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, e := range n.history {
		if e.ID == id && e.Thumb != "" {
			return e.Thumb, true
		}
	}
	return "", false
}

// HistorySnapshot returns the Frigate path of the snapshot of a history entry, with
// the same restriction as HistoryThumb.
func (n *Notifier) HistorySnapshot(id string) (string, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, e := range n.history {
		if e.ID == id && e.Snapshot != "" {
			return e.Snapshot, true
		}
	}
	return "", false
}

// loadHistory reads back the activity saved by the previous run. A missing file
// leaves the history empty; an unreadable file is reported, then ignored.
func (n *Notifier) loadHistory() {
	if n.HistoryFile == "" {
		return
	}
	raw, err := os.ReadFile(n.HistoryFile)
	if os.IsNotExist(err) {
		return
	}
	var entries []storedEntry
	if err == nil {
		err = json.Unmarshal(raw, &entries)
	}
	if err != nil {
		n.Log.Warn("previous activity ignored", "file", n.HistoryFile, "err", err)
		return
	}
	if len(entries) > historySize {
		entries = entries[len(entries)-historySize:]
	}
	var sent []time.Time
	for _, e := range entries {
		e.HistoryEntry.Thumb, e.HistoryEntry.Snapshot = e.Thumb, e.Snapshot
		n.history = append(n.history, e.HistoryEntry)
		if e.Sent {
			sent = append(sent, e.At)
		}
	}
	// The history keeps only historySize entries, so the count read back is a minimum.
	slices.SortFunc(sent, func(a, b time.Time) int { return a.Compare(b) })
	n.sent = append(n.sent, sent...)
	n.pruneSent(n.Now())
}

// FlushHistory writes the recent activity to HistoryFile if it changed since the
// last write.
func (n *Notifier) FlushHistory() error {
	n.mu.Lock()
	if n.HistoryFile == "" || !n.histDirty {
		n.mu.Unlock()
		return nil
	}
	entries := make([]storedEntry, len(n.history))
	for i, e := range n.history {
		entries[i] = storedEntry{HistoryEntry: e, Thumb: e.Thumb, Snapshot: e.Snapshot}
	}
	n.histDirty = false
	n.mu.Unlock()
	b, err := json.Marshal(entries)
	if err == nil {
		err = writeAtomic(n.HistoryFile, b)
	}
	if err != nil {
		n.mu.Lock()
		n.histDirty = true // try again on the next write
		n.mu.Unlock()
		return fmt.Errorf("writing the activity: %w", err)
	}
	return nil
}

// writeAtomic writes b to path through a temporary file: an interrupted write
// never leaves a truncated file.
func writeAtomic(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
