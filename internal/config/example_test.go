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
		t.Fatalf("config.example.yml is invalid: %v", err)
	}
	if c.ForCamera("salon").Enabled {
		t.Error("the example disables the salon camera")
	}
	// WEB_PASSWORD is not set: the interface must still be served, without a password.
	if !c.Web.Enabled || c.Web.Password != "" {
		t.Errorf("web = %+v", c.Web)
	}
}
