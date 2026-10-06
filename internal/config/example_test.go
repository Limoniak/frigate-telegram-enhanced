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
	c, err := Parse(raw)
	if err != nil {
		t.Fatalf("config.example.yml is invalid: %v", err)
	}
	if c.ForCamera("salon").Enabled {
		t.Error("the example disables the salon camera")
	}
	// No password: the interface must still be served, without one.
	if !c.Web.Enabled || c.Web.Password != "" {
		t.Errorf("web = %+v", c.Web)
	}
}
