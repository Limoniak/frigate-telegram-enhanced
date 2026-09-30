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

// historySize borne l'historique gardé pour l'interface web.
const historySize = 50

// HistoryEntry décrit l'issue d'une détection : notifiée, ou ignorée et pourquoi.
type HistoryEntry struct {
	ID       string    `json:"id"`
	At       time.Time `json:"at"`
	Camera   string    `json:"camera"`
	Label    string    `json:"label"`
	SubLabel string    `json:"sub_label,omitempty"` // étiquette de Frigate (« clio 3 océane »…)
	Zones    []string  `json:"zones"`
	Score    float64   `json:"score,omitempty"`
	Sent     bool      `json:"sent"`
	Grouped  bool      `json:"grouped,omitempty"` // ajoutée au message d'une notification précédente
	Reason   string    `json:"reason,omitempty"`  // raison du filtrage (voir filter.Reason*)
	Thumb    string    `json:"-"`                 // chemin Frigate de la miniature, vide si aucune
}

// storedEntry est une entrée telle qu'écrite dans HistoryFile : la miniature, que
// l'interface ne voit pas, doit tout de même survivre au redémarrage.
type storedEntry struct {
	HistoryEntry
	Thumb string `json:"thumb,omitempty"`
}

// record ajoute l'issue d'un suivi à l'historique. Appelé sous n.mu.
func (n *Notifier) record(t *tracked, sent bool) {
	e := HistoryEntry{
		ID: t.id, At: t.start, Camera: t.camera, Label: t.label, SubLabel: t.subLabel,
		Zones: slices.Clone(t.zones), Sent: sent,
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

// updateHistory reporte dans l'historique l'étiquette arrivée après coup. Appelé sous n.mu.
func (n *Notifier) updateHistory(t *tracked) {
	for i := range n.history {
		if n.history[i].ID == t.id {
			n.history[i].SubLabel = t.subLabel
			n.histDirty = true
		}
	}
}

// History renvoie les dernières détections, de la plus récente à la plus ancienne.
func (n *Notifier) History() []HistoryEntry {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := slices.Clone(n.history)
	slices.Reverse(out)
	return out
}

// HistoryThumb renvoie le chemin Frigate de la miniature d'une entrée de
// l'historique. Seuls les identifiants présents dans l'historique sont acceptés :
// l'interface ne peut pas s'en servir pour lire n'importe quoi sur Frigate.
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

// loadHistory relit l'activité enregistrée par le démarrage précédent. Un fichier
// absent laisse l'historique vide ; un fichier illisible est signalé puis ignoré.
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
	for _, e := range entries {
		e.HistoryEntry.Thumb = e.Thumb
		n.history = append(n.history, e.HistoryEntry)
	}
}

// FlushHistory écrit l'activité récente dans HistoryFile si elle a changé depuis
// la dernière écriture.
func (n *Notifier) FlushHistory() error {
	n.mu.Lock()
	if n.HistoryFile == "" || !n.histDirty {
		n.mu.Unlock()
		return nil
	}
	entries := make([]storedEntry, len(n.history))
	for i, e := range n.history {
		entries[i] = storedEntry{HistoryEntry: e, Thumb: e.Thumb}
	}
	n.histDirty = false
	n.mu.Unlock()
	b, err := json.Marshal(entries)
	if err == nil {
		err = writeAtomic(n.HistoryFile, b)
	}
	if err != nil {
		n.mu.Lock()
		n.histDirty = true // nouvel essai à la prochaine écriture
		n.mu.Unlock()
		return fmt.Errorf("writing the activity: %w", err)
	}
	return nil
}

// writeAtomic écrit b dans path via un fichier temporaire : une écriture
// interrompue ne laisse jamais un fichier tronqué.
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
