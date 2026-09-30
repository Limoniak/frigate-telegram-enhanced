// Package state keeps the pauses, camera mutes and cooldowns, persisted as JSON.
package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"
)

// Forever stands for a pause without a deadline (until /resume).
var Forever = time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC)

const cooldownRetention = 24 * time.Hour

type Store struct {
	mu        sync.Mutex
	path      string
	pause     time.Time
	mutes     map[string]time.Time
	cooldowns map[string]time.Time
	dirty     bool
	// presence: for each presence topic, is the person home? Not persisted:
	// presence topics are usually retained by the broker, which sends them again on
	// reconnection.
	presence map[string]bool
}

type fileData struct {
	GlobalPauseUntil time.Time            `json:"global_pause_until"`
	CameraMutes      map[string]time.Time `json:"camera_mutes"`
	Cooldowns        map[string]time.Time `json:"cooldowns"`
}

// Load reads the state from path. The store returned is always usable; a non-nil
// error only reports an unreadable file, ignored (to be logged as a warning).
func Load(path string, now time.Time) (*Store, error) {
	s := &Store{path: path, mutes: map[string]time.Time{}, cooldowns: map[string]time.Time{}, presence: map[string]bool{}}
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, fmt.Errorf("reading the state: %w", err)
	}
	var f fileData
	if err := json.Unmarshal(raw, &f); err != nil {
		return s, fmt.Errorf("corrupted state, ignored: %w", err)
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

// MarkNotified records a notification for the cooldown; written to disk on the next FlushIfDirty.
// Entries older than cooldownRetention are forgotten along the way.
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

// Resume lifts the global pause and every camera mute.
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

// SetPresence records whether the person tracked by topic is home.
func (s *Store) SetPresence(topic string, home bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.presence[topic] = home
}

// SomeoneHome reports whether at least one tracked person is home.
func (s *Store) SomeoneHome() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, home := range s.presence {
		if home {
			return true
		}
	}
	return false
}

type Status struct {
	PausedUntil time.Time            // zero if no pause is active
	Mutes       map[string]time.Time // coupures actives uniquement
	Home        []string             // presence topics saying "home", sorted
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
	for topic, home := range s.presence {
		if home {
			st.Home = append(st.Home, topic)
		}
	}
	slices.Sort(st.Home)
	return st
}

func (s *Store) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveLocked()
}

// FlushIfDirty writes the state only if cooldowns changed since the last write.
func (s *Store) FlushIfDirty() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.dirty {
		return nil
	}
	return s.saveLocked()
}

// saveLocked writes atomically (temporary file then rename).
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
		return fmt.Errorf("writing the state: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("writing the state: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("writing the state: %w", err)
	}
	s.dirty = false
	return nil
}
