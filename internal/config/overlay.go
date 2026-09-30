package config

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"frigate-telegram-enhanced/internal/i18n"
)

// Overlay is the set of notification settings saved by the web interface. It is
// stored apart from config.yml — which stays the source of the secrets, the ${VAR}
// and the comments, and which the container mounts read-only — and entirely
// replaces its notify and cameras sections when it exists.
type Overlay struct {
	Notify     NotifyPatch            `yaml:"notify" json:"notify"`
	Cameras    map[string]NotifyPatch `yaml:"cameras,omitempty" json:"cameras"`
	Recipients map[string]Recipient   `yaml:"recipients,omitempty" json:"recipients"`
	// ExternalURL replaces frigate.external_url (FRIGATE_EXTERNAL_URL); missing, the
	// configuration's value applies; empty, the links use frigate.url.
	ExternalURL *string `yaml:"external_url,omitempty" json:"external_url,omitempty"`
}

// settings is the set of settings that can be changed live.
type settings struct {
	notify      Notify
	cameras     map[string]Notify
	recipients  map[string]Recipient
	externalURL string
}

const overlayHeader = `# Notification settings saved by the web interface of frigate-telegram-enhanced.
# This file replaces the notify and cameras sections of config.yml.
# It is rewritten on every save: comments added by hand will be lost.
`

// LoadOverlay reads the override file. A missing file is not an error: the
// function then returns (nil, nil) and config.yml applies.
func LoadOverlay(path string) (*Overlay, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	var o Overlay
	if err := yaml.Unmarshal(raw, &o); err != nil {
		return nil, fmt.Errorf("invalid %s: %w", path, err)
	}
	return &o, nil
}

// SaveOverlay writes the override file atomically: an interrupted replacement
// cannot leave a truncated file that the next startup would refuse to read.
func SaveOverlay(path string, o Overlay) error {
	body, err := yaml.Marshal(o)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".notify-*.yml")
	if err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	tmp := f.Name()
	defer os.Remove(tmp) // no effect after a successful rename
	_, err = f.WriteString(overlayHeader + string(body))
	if err == nil {
		err = f.Chmod(0o600)
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

// resolve computes the effective settings of an overlay without installing anything.
// A nil overlay gives back the notify and cameras sections of config.yml.
func (c *Config) resolve(o *Overlay, lang i18n.Lang) (settings, error) {
	s := settings{notify: c.fileNotify, cameras: maps.Clone(c.fileCameras), recipients: maps.Clone(c.fileRecipients),
		externalURL: c.fileExternalURL}
	if o != nil {
		s.notify = o.Notify.ApplyTo(defaultNotify(c.Telegram.Chats))
		s.cameras = make(map[string]Notify, len(o.Cameras))
		for name, p := range o.Cameras {
			s.cameras[name] = p.ApplyTo(s.notify)
		}
		if o.ExternalURL != nil {
			s.externalURL = strings.TrimRight(strings.TrimSpace(*o.ExternalURL), "/")
		}
		s.recipients = make(map[string]Recipient, len(o.Recipients))
		for name, r := range o.Recipients {
			if !r.IsZero() {
				s.recipients[name] = r
			}
		}
	}
	errs := c.validateRecipients(s.recipients, lang)
	if err := validateExternalURL(s.externalURL, lang); err != nil {
		errs = append(errs, err)
	}
	errs = append(errs, c.validateNotify("notify", s.notify, lang)...)
	for _, name := range slices.Sorted(maps.Keys(s.cameras)) {
		errs = append(errs, c.validateNotify("cameras."+name, s.cameras[name], lang)...)
	}
	return s, errors.Join(errs...)
}

// ValidateOverlay reports whether an overlay can be applied, without installing it.
// The web interface uses it to refuse a save before writing anything to disk.
// Errors are written in the language lang (the interface's).
func (c *Config) ValidateOverlay(o *Overlay, lang i18n.Lang) error {
	_, err := c.resolve(o, lang)
	return err
}

// ApplyOverlay installs the settings if, and only if, they are valid — a refused
// override leaves the service on the previous settings. A nil overlay restores the
// notify and cameras sections of config.yml.
func (c *Config) ApplyOverlay(o *Overlay) error {
	s, err := c.resolve(o, c.Language)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.notify, c.cameras, c.recipients, c.externalURL = s.notify, s.cameras, s.recipients, s.externalURL
	return nil
}

// CurrentOverlay describes the effective settings in the form the interface saves:
// the global settings in full, each camera reduced to how it differs from the
// global ones.
func (c *Config) CurrentOverlay() Overlay {
	c.mu.RLock()
	defer c.mu.RUnlock()
	o := Overlay{Notify: FullPatch(c.notify), Cameras: make(map[string]NotifyPatch, len(c.cameras)),
		Recipients: maps.Clone(c.recipients)}
	if o.Recipients == nil {
		o.Recipients = map[string]Recipient{}
	}
	ext := c.externalURL
	o.ExternalURL = &ext
	for name, n := range c.cameras {
		o.Cameras[name] = DiffPatch(c.notify, n)
	}
	return o
}
