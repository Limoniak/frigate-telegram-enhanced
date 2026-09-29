// Package state conserve les pauses, coupures de caméras et cooldowns, avec persistance JSON.
package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Forever représente une pause sans échéance (jusqu'à /resume).
var Forever = time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC)

const cooldownRetention = 24 * time.Hour

type Store struct {
	mu        sync.Mutex
	path      string
	pause     time.Time
	mutes     map[string]time.Time
	cooldowns map[string]time.Time
	dirty     bool
}

type fileData struct {
	GlobalPauseUntil time.Time            `json:"global_pause_until"`
	CameraMutes      map[string]time.Time `json:"camera_mutes"`
	Cooldowns        map[string]time.Time `json:"cooldowns"`
}

// Load lit l'état depuis path. Le store renvoyé est toujours utilisable ; une erreur
// non nil signale seulement un fichier illisible, ignoré (à journaliser en avertissement).
func Load(path string, now time.Time) (*Store, error) {
	s := &Store{path: path, mutes: map[string]time.Time{}, cooldowns: map[string]time.Time{}}
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, fmt.Errorf("lecture de l'état: %w", err)
	}
	var f fileData
	if err := json.Unmarshal(raw, &f); err != nil {
		return s, fmt.Errorf("état corrompu, ignoré: %w", err)
	}
	if f.GlobalPauseUntil.After(now) {
		s.pause = f.GlobalPauseUntil
	}
	for cam, until := range f.CameraMutes {
		if until.After(now) {
			s.mutes[cam] = until
		}
	}
	for key, at := range f.Cooldowns {
		if now.Sub(at) < cooldownRetention {
			s.cooldowns[key] = at
		}
	}
	return s, nil
}

func (s *Store) IsPaused(now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return now.Before(s.pause)
}

func (s *Store) IsMuted(camera string, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	until, ok := s.mutes[camera]
	return ok && now.Before(until)
}

func (s *Store) LastNotified(key string) time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cooldowns[key]
}

// MarkNotified enregistre une notification pour le cooldown ; écrit sur disque au prochain FlushIfDirty.
// Les entrées de plus de cooldownRetention sont oubliées au passage.
func (s *Store) MarkNotified(key string, at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	maps.DeleteFunc(s.cooldowns, func(_ string, t time.Time) bool { return at.Sub(t) >= cooldownRetention })
	s.cooldowns[key] = at
	s.dirty = true
}

func (s *Store) Pause(until time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pause = until
	return s.saveLocked()
}

// Resume lève la pause globale et toutes les coupures de caméras.
func (s *Store) Resume() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pause = time.Time{}
	s.mutes = map[string]time.Time{}
	return s.saveLocked()
}

func (s *Store) Mute(camera string, until time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mutes[camera] = until
	return s.saveLocked()
}

func (s *Store) Unmute(camera string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.mutes, camera)
	return s.saveLocked()
}

type Status struct {
	PausedUntil time.Time            // zéro si pas de pause active
	Mutes       map[string]time.Time // coupures actives uniquement
}

func (s *Store) Status(now time.Time) Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := Status{Mutes: map[string]time.Time{}}
	if now.Before(s.pause) {
		st.PausedUntil = s.pause
	}
	for cam, until := range s.mutes {
		if now.Before(until) {
			st.Mutes[cam] = until
		}
	}
	return st
}

func (s *Store) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveLocked()
}

// FlushIfDirty écrit l'état seulement si des cooldowns ont changé depuis la dernière écriture.
func (s *Store) FlushIfDirty() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.dirty {
		return nil
	}
	return s.saveLocked()
}

// saveLocked écrit atomiquement (fichier temporaire puis rename).
func (s *Store) saveLocked() error {
	b, err := json.MarshalIndent(fileData{
		GlobalPauseUntil: s.pause,
		CameraMutes:      maps.Clone(s.mutes),
		Cooldowns:        maps.Clone(s.cooldowns),
	}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("écriture de l'état: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("écriture de l'état: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("écriture de l'état: %w", err)
	}
	s.dirty = false
	return nil
}
