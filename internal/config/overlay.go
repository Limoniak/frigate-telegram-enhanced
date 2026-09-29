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

// Overlay est l'ensemble des réglages de notification enregistrés par l'interface
// web. Il est stocké à part de config.yml — qui reste la source des secrets, des
// ${VAR} et des commentaires, et que le conteneur monte en lecture seule — et
// remplace entièrement les sections notify et cameras de celui-ci quand il existe.
type Overlay struct {
	Notify     NotifyPatch            `yaml:"notify" json:"notify"`
	Cameras    map[string]NotifyPatch `yaml:"cameras,omitempty" json:"cameras"`
	Recipients map[string]Recipient   `yaml:"recipients,omitempty" json:"recipients"`
	// ExternalURL remplace frigate.external_url (FRIGATE_EXTERNAL_URL) ; absent, la
	// valeur de la configuration s'applique ; vide, les liens utilisent frigate.url.
	ExternalURL *string `yaml:"external_url,omitempty" json:"external_url,omitempty"`
}

// settings est l'ensemble des réglages modifiables à chaud.
type settings struct {
	notify      Notify
	cameras     map[string]Notify
	recipients  map[string]Recipient
	externalURL string
}

const overlayHeader = `# Réglages de notification enregistrés par l'interface web de frigate-telegram-enhanced.
# Ce fichier remplace les sections notify et cameras de config.yml.
# Il est réécrit à chaque enregistrement : les commentaires ajoutés à la main seront perdus.
`

// LoadOverlay lit le fichier de surcharge. Un fichier absent n'est pas une erreur :
// la fonction renvoie alors (nil, nil) et config.yml fait foi.
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

// SaveOverlay écrit le fichier de surcharge de façon atomique : un remplacement
// interrompu ne peut pas laisser un fichier tronqué que le prochain démarrage
// refuserait de lire.
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
	defer os.Remove(tmp) // sans effet après un rename réussi
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

// resolve calcule les réglages effectifs d'un overlay sans rien installer.
// Un overlay nil redonne les sections notify et cameras de config.yml.
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

// ValidateOverlay indique si un overlay est applicable, sans l'installer. L'interface
// web s'en sert pour refuser un enregistrement avant d'écrire quoi que ce soit sur disque.
// Les erreurs sont rédigées dans la langue lang (celle de l'interface).
func (c *Config) ValidateOverlay(o *Overlay, lang i18n.Lang) error {
	_, err := c.resolve(o, lang)
	return err
}

// ApplyOverlay installe les réglages si, et seulement si, ils sont valides — une
// surcharge refusée laisse le service sur les réglages précédents. Un overlay nil
// rétablit les sections notify et cameras de config.yml.
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

// CurrentOverlay décrit les réglages effectifs sous la forme enregistrable par
// l'interface : le global entièrement renseigné, chaque caméra réduite à ce par
// quoi elle s'écarte du global.
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
