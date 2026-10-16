package labels

import (
	"testing"

	"frigate-telegram-enhanced/internal/i18n"
)

func TestText(t *testing.T) {
	for _, c := range []struct {
		label string
		sound bool
		lang  i18n.Lang
		want  string
	}{
		{"person", false, i18n.EN, "🚶 Person"},
		{"person", false, i18n.FR, "🚶 Personne"},
		{"fire_alarm", true, i18n.FR, "🔥 Alarme incendie"},
		{"fedex", false, i18n.FR, "🚚 FedEx"},     // a brand keeps its name
		{"unicorn", false, i18n.EN, "🔔 unicorn"}, // an object not in the table
		{"tuba", true, i18n.EN, "🔊 tuba"},        // a sound not in the table
		{"", false, i18n.FR, "🔔 Détection"},
	} {
		if got := Text(c.label, c.sound, c.lang); got != c.want {
			t.Errorf("Text(%q, %v, %s) = %q, want %q", c.label, c.sound, c.lang, got, c.want)
		}
	}
}

// Every name of the table, brands aside, is translated in every catalog.
func TestNamesTranslated(t *testing.T) {
	for _, l := range i18n.Languages()[1:] {
		msgs := i18n.Catalogs()[l].Messages
		for key, x := range Known {
			if x.Emoji == "" || x.Name == "" {
				t.Errorf("%s: emoji or name missing", key)
			}
			if !x.Brand && msgs[x.Name] == "" {
				t.Errorf("%s: %q has no translation in locales/%s.json", key, x.Name, l)
			}
		}
	}
}
