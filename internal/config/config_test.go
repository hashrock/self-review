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

func TestRecent(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SELF_REVIEW_CONFIG", filepath.Join(dir, "config.json"))
	a, b := t.TempDir(), t.TempDir()
	for _, p := range []string{a, b, a} {
		if err := AddRecent(p); err != nil {
			t.Fatal(err)
		}
	}
	got := RecentRepos()
	if len(got) != 2 || got[0].Path != a || got[1].Path != b {
		t.Fatalf("recent: %+v", got)
	}
	if err := RemoveRecent(a); err != nil {
		t.Fatal(err)
	}
	if got = RecentRepos(); len(got) != 1 || got[0].Path != b {
		t.Fatalf("after remove: %+v", got)
	}
	_ = AddRecent(filepath.Join(dir, "gone"))
	if got = RecentRepos(); len(got) != 1 {
		t.Fatalf("missing folders should be hidden: %+v", got)
	}
}
