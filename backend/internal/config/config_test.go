package config

import (
	"strings"
	"testing"
)

func TestLoadDefaults(t *testing.T) {
	c, err := Load(nil)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Addr != ":8080" {
		t.Errorf("Addr = %q, want :8080", c.Addr)
	}
	if c.IndexURL != DefaultIndexURL {
		t.Errorf("IndexURL = %q, want the Centene default", c.IndexURL)
	}
	if !strings.HasSuffix(c.CacheDir, ".cache") {
		t.Errorf("CacheDir = %q, want it to end in .cache", c.CacheDir)
	}
	if !strings.HasPrefix(c.CacheDir, "/") {
		t.Errorf("CacheDir = %q, want an absolute path", c.CacheDir)
	}
	// The Vite dev server must be allowed out of the box, or local development
	// fails with an opaque CORS error.
	if len(c.AllowedOrigins) == 0 || !contains(c.AllowedOrigins, "http://localhost:5173") {
		t.Errorf("AllowedOrigins = %v, want the Vite dev origin", c.AllowedOrigins)
	}
	if c.AllowLocalFiles {
		t.Error("local files should be disabled for a remote index")
	}
}

func TestLoadFlagsOverrideDefaults(t *testing.T) {
	c, err := Load([]string{
		"-addr", ":9999",
		"-index-url", "https://example.test/index.json",
		"-cache-dir", "",
		"-static-dir", "../frontend/dist",
		"-allowed-origins", "https://a.test, https://b.test",
		"-max-items", "5",
		"-max-files", "1",
		"-offline",
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Addr != ":9999" || c.IndexURL != "https://example.test/index.json" {
		t.Errorf("flags not applied: %+v", c)
	}
	if c.CacheDir != "" {
		t.Errorf("CacheDir = %q, want empty (caching disabled)", c.CacheDir)
	}
	if c.MaxItemsPerFile != 5 || c.MaxFiles != 1 || !c.Offline {
		t.Errorf("limits not applied: %+v", c)
	}
	if len(c.AllowedOrigins) != 2 || c.AllowedOrigins[1] != "https://b.test" {
		t.Errorf("AllowedOrigins = %v, want trimmed and split", c.AllowedOrigins)
	}
}

func TestLocalIndexImpliesLocalFiles(t *testing.T) {
	for _, in := range []string{"./testdata/index.json", "file:///tmp/index.json"} {
		c, err := Load([]string{"-index-url", in})
		if err != nil {
			t.Fatalf("Load(%q): %v", in, err)
		}
		if !c.AllowLocalFiles {
			t.Errorf("index %q should enable local file reads", in)
		}
	}
}

func TestLoadRejectsEmptyIndexURL(t *testing.T) {
	if _, err := Load([]string{"-index-url", "   "}); err == nil {
		t.Fatal("expected an error for an empty index URL")
	}
}

func TestEnvironmentIsUsedAndFlagsWin(t *testing.T) {
	t.Setenv("PTE_ADDR", ":7777")
	t.Setenv("PTE_MAX_ITEMS", "3")
	t.Setenv("PTE_OFFLINE", "true")

	c, err := Load(nil)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Addr != ":7777" || c.MaxItemsPerFile != 3 || !c.Offline {
		t.Errorf("environment not applied: %+v", c)
	}

	c, err = Load([]string{"-addr", ":6666"})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Addr != ":6666" {
		t.Errorf("Addr = %q, want the flag to win over the environment", c.Addr)
	}
}

func TestMalformedEnvironmentFallsBackToDefault(t *testing.T) {
	t.Setenv("PTE_MAX_ITEMS", "not-a-number")
	t.Setenv("PTE_OFFLINE", "maybe")

	c, err := Load(nil)
	if err != nil {
		t.Fatalf("Load should not fail on malformed env: %v", err)
	}
	if c.MaxItemsPerFile != 0 || c.Offline {
		t.Errorf("malformed env should fall back to defaults: %+v", c)
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
