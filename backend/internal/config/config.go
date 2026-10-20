// Package config resolves runtime configuration from flags and environment
// variables, with flags taking precedence.
package config

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// DefaultIndexURL is the Centene/Fidelis Transparency in Coverage index file
// used for this exercise. A newer file can be found at
// https://www.centene.com/price-transparency-files.html and passed with
// -index-url if this one is rotated out.
const DefaultIndexURL = "https://www.centene.com/content/dam/centene/Centene%20Corporate/json/DOCUMENT/2026-08-31_fidelis_index.json"

// Config is the resolved runtime configuration.
type Config struct {
	Addr            string
	IndexURL        string
	CacheDir        string
	StaticDir       string
	AllowedOrigins  []string
	MaxItemsPerFile int
	MaxFiles        int
	Offline         bool
	AllowLocalFiles bool
}

// Load parses flags and environment variables.
func Load(args []string) (Config, error) {
	fs := flag.NewFlagSet("server", flag.ContinueOnError)

	var c Config
	fs.StringVar(&c.Addr, "addr", env("PTE_ADDR", ":8080"),
		"address to listen on")
	fs.StringVar(&c.IndexURL, "index-url", env("PTE_INDEX_URL", DefaultIndexURL),
		"Transparency in Coverage index file URL")
	fs.StringVar(&c.CacheDir, "cache-dir", env("PTE_CACHE_DIR", ".cache"),
		"directory for cached source files; empty disables caching")
	fs.StringVar(&c.StaticDir, "static-dir", env("PTE_STATIC_DIR", ""),
		"directory of built frontend assets to serve; empty serves API only")
	origins := fs.String("allowed-origins", env("PTE_ALLOWED_ORIGINS", "http://localhost:5173,http://127.0.0.1:5173"),
		"comma-separated CORS origins for the API")
	fs.IntVar(&c.MaxItemsPerFile, "max-items", envInt("PTE_MAX_ITEMS", 0),
		"cap in_network records per file (0 = no cap); for fast local iteration")
	fs.IntVar(&c.MaxFiles, "max-files", envInt("PTE_MAX_FILES", 0),
		"cap how many in-network files to ingest (0 = all)")
	fs.BoolVar(&c.Offline, "offline", envBool("PTE_OFFLINE", false),
		"serve only files already present in the cache; never touch the network")
	fs.BoolVar(&c.AllowLocalFiles, "allow-local-files", envBool("PTE_ALLOW_LOCAL_FILES", false),
		"allow file:// and bare-path index/rate URLs (for fixtures and demos)")

	if err := fs.Parse(args); err != nil {
		return c, err
	}

	c.IndexURL = strings.TrimSpace(c.IndexURL)
	if c.IndexURL == "" {
		return c, fmt.Errorf("index-url must not be empty")
	}
	for _, o := range strings.Split(*origins, ",") {
		if o = strings.TrimSpace(o); o != "" {
			c.AllowedOrigins = append(c.AllowedOrigins, o)
		}
	}
	if c.CacheDir != "" {
		abs, err := filepath.Abs(c.CacheDir)
		if err != nil {
			return c, fmt.Errorf("resolve cache-dir: %w", err)
		}
		c.CacheDir = abs
	}
	// A local index path implies the caller wants local files.
	if !strings.Contains(c.IndexURL, "://") || strings.HasPrefix(c.IndexURL, "file://") {
		c.AllowLocalFiles = true
	}
	return c, nil
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && strings.TrimSpace(v) != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v, ok := os.LookupEnv(key); ok {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return n
		}
	}
	return def
}

func envBool(key string, def bool) bool {
	if v, ok := os.LookupEnv(key); ok {
		if b, err := strconv.ParseBool(strings.TrimSpace(v)); err == nil {
			return b
		}
	}
	return def
}
