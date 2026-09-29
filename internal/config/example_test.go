package config

import (
	"os"
	"testing"
)

func TestExampleConfigIsValid(t *testing.T) {
	raw, err := os.ReadFile("../../config.example.yml")
	if err != nil {
		t.Fatal(err)
	}
	c, err := Parse(raw, env(map[string]string{"TELEGRAM_TOKEN": "123:abc"}))
	if err != nil {
		t.Fatalf("config.example.yml invalide : %v", err)
	}
	if c.ForCamera("salon").Enabled {
		t.Error("l'exemple désactive la caméra salon")
	}
	// WEB_PASSWORD n'est pas défini : l'interface doit rester servie, sans mot de passe.
	if !c.Web.Enabled || c.Web.Password != "" {
		t.Errorf("web = %+v", c.Web)
	}
}
