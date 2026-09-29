package i18n

import "testing"

func TestParse(t *testing.T) {
	for in, want := range map[string]Lang{"": EN, "en": EN, "EN-us": EN, "fr": FR, "fr_FR": FR, " Français ": FR} {
		if got, err := Parse(in); err != nil || got != want {
			t.Errorf("Parse(%q) = %q, %v ; attendu %q", in, got, err, want)
		}
	}
	if _, err := Parse("de"); err == nil {
		t.Error("de doit être refusé")
	}
}

func TestT(t *testing.T) {
	if EN.T("a", "b") != "a" || FR.T("a", "b") != "b" || Lang("xx").T("a", "b") != "a" {
		t.Error("T choisit mal la langue")
	}
	if FR.Tf("%d", "n°%d", 3) != "n°3" {
		t.Error("Tf")
	}
}
