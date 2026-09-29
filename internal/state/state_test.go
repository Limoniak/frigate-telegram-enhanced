package state

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

var t0 = time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)

func newStore(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sub", "state.json")
	s, err := Load(path, t0)
	if err != nil {
		t.Fatal(err)
	}
	return s, path
}

func TestPauseAndResume(t *testing.T) {
	s, _ := newStore(t)
	if s.IsPaused(t0) {
		t.Fatal("pause au démarrage")
	}
	if err := s.Pause(t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if !s.IsPaused(t0.Add(30*time.Minute)) || s.IsPaused(t0.Add(2*time.Hour)) {
		t.Error("fenêtre de pause incorrecte")
	}
	s.Mute("jardin", Forever)
	if err := s.Resume(); err != nil {
		t.Fatal(err)
	}
	if s.IsPaused(t0) || s.IsMuted("jardin", t0) {
		t.Error("Resume doit tout lever")
	}
}

func TestMuteIsPerCamera(t *testing.T) {
	s, _ := newStore(t)
	s.Mute("jardin", t0.Add(time.Hour))
	if !s.IsMuted("jardin", t0) || s.IsMuted("garage", t0) {
		t.Error("coupure mal ciblée")
	}
	s.Unmute("jardin")
	if s.IsMuted("jardin", t0) {
		t.Error("Unmute sans effet")
	}
}

func TestPersistenceRoundTrip(t *testing.T) {
	s, path := newStore(t)
	s.Pause(t0.Add(time.Hour))
	s.Mute("jardin", t0.Add(10*time.Minute))
	s.Mute("garage", t0.Add(3*time.Hour))
	s.MarkNotified("jardin/person", t0)
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}

	later := t0.Add(30 * time.Minute)
	s2, err := Load(path, later)
	if err != nil {
		t.Fatal(err)
	}
	if !s2.IsPaused(later) || !s2.IsMuted("garage", later) {
		t.Error("état non restauré")
	}
	if _, ok := s2.Status(later).Mutes["jardin"]; ok {
		t.Error("coupure expirée non purgée")
	}
	if !s2.LastNotified("jardin/person").Equal(t0) {
		t.Error("cooldown non restauré")
	}
}

func TestForeverSurvivesRoundTrip(t *testing.T) {
	s, path := newStore(t)
	s.Pause(Forever)
	s2, _ := Load(path, t0.AddDate(5, 0, 0))
	if !s2.IsPaused(t0.AddDate(5, 0, 0)) {
		t.Error("pause illimitée perdue")
	}
}

func TestCorruptFileGivesEmptyState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	os.WriteFile(path, []byte("{"), 0o600)
	s, err := Load(path, t0)
	if err == nil {
		t.Error("avertissement attendu")
	}
	if s == nil || s.IsPaused(t0) {
		t.Fatal("un état vide utilisable est attendu")
	}
}

func TestFlushIfDirty(t *testing.T) {
	s, path := newStore(t)
	if err := s.FlushIfDirty(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("aucune écriture attendue sans modification")
	}
	s.MarkNotified("jardin/person", t0)
	if err := s.FlushIfDirty(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("fichier attendu : %v", err)
	}
}

func TestStatus(t *testing.T) {
	s, _ := newStore(t)
	if st := s.Status(t0); !st.PausedUntil.IsZero() || len(st.Mutes) != 0 {
		t.Errorf("status vide attendu : %+v", st)
	}
	s.Pause(t0.Add(time.Hour))
	s.Mute("jardin", t0.Add(time.Hour))
	st := s.Status(t0)
	if !st.PausedUntil.Equal(t0.Add(time.Hour)) || len(st.Mutes) != 1 {
		t.Errorf("status = %+v", st)
	}
}

func TestMarkNotifiedForgetsOldCooldowns(t *testing.T) {
	now := time.Date(2026, 9, 28, 14, 0, 0, 0, time.UTC)
	s, _ := Load(filepath.Join(t.TempDir(), "state.json"), now)
	s.MarkNotified("garage/person", now)
	s.MarkNotified("jardin/cat", now.Add(cooldownRetention))
	if !s.LastNotified("garage/person").IsZero() {
		t.Error("un cooldown de plus de 24 h doit être oublié")
	}
	if s.LastNotified("jardin/cat").IsZero() {
		t.Error("le cooldown qui vient d'être posé doit rester")
	}
}
