// Package i18n choisit la langue des textes présentés à l'utilisateur : messages
// Telegram, erreurs de configuration et réponses de l'interface web. L'anglais est
// la langue par défaut ; le français est l'autre langue prise en charge.
package i18n

import (
	"errors"
	"fmt"
	"strings"
)

type Lang string

const (
	EN Lang = "en"
	FR Lang = "fr"

	Default = EN
)

// Parse lit une langue ("en", "fr", "fr-FR", "English"…) ; vide donne Default.
func Parse(s string) (Lang, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	switch {
	case s == "":
		return Default, nil
	case s == "en" || strings.HasPrefix(s, "en-") || strings.HasPrefix(s, "en_") || s == "english":
		return EN, nil
	case s == "fr" || strings.HasPrefix(s, "fr-") || strings.HasPrefix(s, "fr_") || s == "french" || s == "français" || s == "francais":
		return FR, nil
	}
	return Default, fmt.Errorf("unsupported language %q (en or fr) / langue %q non prise en charge (en ou fr)", s, s)
}

// T renvoie le texte dans la langue l : en pour l'anglais (et toute langue
// inconnue), fr pour le français.
func (l Lang) T(en, fr string) string {
	if l == FR {
		return fr
	}
	return en
}

// Tf est T suivi de fmt.Sprintf.
func (l Lang) Tf(en, fr string, a ...any) string { return fmt.Sprintf(l.T(en, fr), a...) }

// Errorf est Tf renvoyé comme erreur.
func (l Lang) Errorf(en, fr string, a ...any) error { return fmt.Errorf(l.T(en, fr), a...) }

// DateTime est le format date + heure d'une notification.
func (l Lang) DateTime() string { return l.T("Jan 2 15:04:05", "02/01 15:04:05") }

// DateTimeShort est le format date + heure sans les secondes (échéances).
func (l Lang) DateTimeShort() string { return l.T("Jan 2 15:04", "02/01 15:04") }

// Error est une erreur disponible dans les deux langues. Error() renvoie l'anglais ;
// Message choisit la langue au moment de l'afficher. Elle sert quand la langue n'est
// pas connue là où l'erreur naît (décodage d'une durée, par exemple).
type Error struct{ EN, FR string }

func (e *Error) Error() string { return e.EN }

// NewError construit une Error bilingue, avec les mêmes arguments pour les deux textes.
func NewError(en, fr string, a ...any) error {
	return &Error{EN: fmt.Sprintf(en, a...), FR: fmt.Sprintf(fr, a...)}
}

// Message rend err dans la langue l si c'est une Error bilingue, tel quel sinon.
func (l Lang) Message(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return l.T(e.EN, e.FR)
	}
	return err.Error()
}
