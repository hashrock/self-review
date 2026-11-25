package config

import (
	"path/filepath"
	"testing"
)

func TestLoadSaveResolve(t *testing.T) {
	t.Setenv("SELF_REVIEW_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	c, err := Load()
	if err != nil || c.Lang != Auto {
		t.Fatalf("defaults: %+v %v", c, err)
	}
	if err := Save(Config{Lang: "ja"}); err != nil {
		t.Fatal(err)
	}
	if c, _ = Load(); c.Lang != "ja" || c.ResolvedLang() != "ja" {
		t.Fatalf("saved: %+v", c)
	}
	if !ValidLang("en") || !ValidLang(Auto) || ValidLang("fr") {
		t.Fatal("ValidLang")
	}
	if got := (Config{Lang: "en"}).ResolvedLang(); got != "en" {
		t.Fatalf("en: %q", got)
	}
}
