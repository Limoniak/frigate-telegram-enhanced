// Package i18n translates the texts shown to users: Telegram messages,
// configuration errors and the web interface.
//
// Texts are written in English in the code. Every other language has a catalog,
// locales/<code>.json, mapping each English text to its translation; a text
// missing from a catalog is shown in English. Adding a language means adding a
// catalog: the service, the bot and the web interface all pick it up.
package i18n

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strings"
)

type Lang string

const (
	EN Lang = "en"
	FR Lang = "fr"

	Default = EN
)

// Catalog is the translation file of a language.
type Catalog struct {
	Name     string            `json:"name"`     // the language's own name, e.g. "Français"
	Messages map[string]string `json:"messages"` // English text → translation
}

//go:embed locales/*.json
var locales embed.FS

var catalogs = mustLoad(locales)

func mustLoad(fsys fs.FS) map[Lang]Catalog {
	out := map[Lang]Catalog{EN: {Name: "English", Messages: map[string]string{}}}
	files, err := fs.Glob(fsys, "locales/*.json")
	if err != nil {
		panic(err)
	}
	for _, f := range files {
		raw, err := fs.ReadFile(fsys, f)
		if err != nil {
			panic(err)
		}
		var c Catalog
		if err := json.Unmarshal(raw, &c); err != nil {
			panic(fmt.Sprintf("i18n: %s: %v", f, err))
		}
		code := strings.TrimSuffix(strings.TrimPrefix(f, "locales/"), ".json")
		out[Lang(code)] = c
	}
	return out
}

// Languages returns the supported languages: English, then the others by code.
func Languages() []Lang {
	var out []Lang
	for l := range catalogs {
		if l != EN {
			out = append(out, l)
		}
	}
	slices.Sort(out)
	return append([]Lang{EN}, out...)
}

// Catalogs returns every catalog, by language. The result must not be modified.
func Catalogs() map[Lang]Catalog { return catalogs }

// Name returns the language's own name ("Français"), or its code if unknown.
func (l Lang) Name() string {
	if c, ok := catalogs[l]; ok && c.Name != "" {
		return c.Name
	}
	return string(l)
}

// aliases are the language names accepted besides the codes.
var aliases = map[string]Lang{"english": EN, "french": FR, "français": FR, "francais": FR}

// Parse reads a language ("en", "fr", "fr-FR", "English"…); empty gives Default.
func Parse(s string) (Lang, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return Default, nil
	}
	if l, ok := aliases[s]; ok {
		return l, nil
	}
	code, _, _ := strings.Cut(strings.ReplaceAll(s, "_", "-"), "-")
	if _, ok := catalogs[Lang(code)]; ok {
		return Lang(code), nil
	}
	codes := make([]string, 0, len(catalogs))
	for _, l := range Languages() {
		codes = append(codes, string(l))
	}
	return Default, NewError("unsupported language %q (%s)", s, strings.Join(codes, ", "))
}

// T returns the translation of the English text s in the language l, or s itself
// when the catalog has none.
func (l Lang) T(s string) string {
	if t := catalogs[l].Messages[s]; t != "" {
		return t
	}
	return s
}

// Tf is T followed by fmt.Sprintf.
func (l Lang) Tf(format string, a ...any) string { return fmt.Sprintf(l.T(format), a...) }

// Errorf is Tf returned as an error (%w is supported).
func (l Lang) Errorf(format string, a ...any) error { return fmt.Errorf(l.T(format), a...) }

// DateTime is the date and time layout of a notification.
func (l Lang) DateTime() string { return l.T("Jan 2 15:04:05") }

// DateTimeShort is the date and time layout without seconds (deadlines).
func (l Lang) DateTimeShort() string { return l.T("Jan 2 15:04") }

// Error is an error whose language is chosen when it is shown: Error() gives the
// English text, Message the translation. It serves where the language is not
// known when the error is created (decoding a duration, for example).
type Error struct {
	format string
	args   []any
}

func (e *Error) Error() string { return fmt.Sprintf(e.format, e.args...) }

// NewError builds an Error from an English format and its arguments.
func NewError(format string, a ...any) error { return &Error{format: format, args: a} }

// Message renders err in the language l if it is an Error, as is otherwise.
func (l Lang) Message(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return fmt.Sprintf(l.T(e.format), e.args...)
	}
	return err.Error()
}
