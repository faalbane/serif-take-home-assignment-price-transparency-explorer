package fetch

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func newTestFetcher(t *testing.T) *Fetcher {
	t.Helper()
	f := New(t.TempDir())
	f.Client = &http.Client{Timeout: 5 * time.Second}
	f.MaxAttempts = 3
	return f
}

func read(t *testing.T, r *Result) string {
	t.Helper()
	defer r.Body.Close()
	b, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(b)
}

func TestOpenCachesAndServesFromCache(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"ok":true}`)
	}))
	defer srv.Close()

	f := newTestFetcher(t)

	first, err := f.Open(context.Background(), srv.URL+"/in-network.json")
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	if first.FromCache {
		t.Error("first fetch should not come from cache")
	}
	if got, want := read(t, first), `{"ok":true}`; got != want {
		t.Errorf("body = %q, want %q", got, want)
	}

	second, err := f.Open(context.Background(), srv.URL+"/in-network.json")
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	if !second.FromCache {
		t.Error("second fetch should come from cache")
	}
	read(t, second)

	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Errorf("origin hits = %d, want 1 (the cache should have absorbed the second read)", got)
	}
}

func TestOpenRetriesServerErrorsThenSucceeds(t *testing.T) {
	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&attempts, 1) < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		io.WriteString(w, `{"recovered":true}`)
	}))
	defer srv.Close()

	f := newTestFetcher(t)
	res, err := f.Open(context.Background(), srv.URL+"/flaky.json")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if got, want := read(t, res), `{"recovered":true}`; got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
	if got := atomic.LoadInt32(&attempts); got != 3 {
		t.Errorf("attempts = %d, want 3", got)
	}
}

func TestOpenDoesNotRetryNotFound(t *testing.T) {
	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	f := newTestFetcher(t)
	if _, err := f.Open(context.Background(), srv.URL+"/gone.json"); err == nil {
		t.Fatal("expected an error for 404")
	} else if !strings.Contains(err.Error(), "404") {
		t.Errorf("error = %v, want it to mention the status", err)
	}
	if got := atomic.LoadInt32(&attempts); got != 1 {
		t.Errorf("attempts = %d, want 1 (a 404 will not fix itself)", got)
	}
}

func TestFailedDownloadLeavesNoCacheFile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1000")
		io.WriteString(w, "truncated")
		// Closing without writing the promised bytes.
		if hj, ok := w.(http.Hijacker); ok {
			conn, _, _ := hj.Hijack()
			conn.Close()
		}
	}))
	defer srv.Close()

	f := newTestFetcher(t)
	f.MaxAttempts = 1
	if _, err := f.Open(context.Background(), srv.URL+"/truncated.json"); err == nil {
		t.Fatal("expected an error for a truncated download")
	}

	entries, err := os.ReadDir(f.CacheDir)
	if err != nil {
		t.Fatalf("read cache dir: %v", err)
	}
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), ".partial-") {
			t.Errorf("a failed download left a committed cache file: %s", e.Name())
		} else {
			t.Errorf("a failed download left a partial file behind: %s", e.Name())
		}
	}
}

func TestOpenCachedOnlyNeverTouchesTheNetwork(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		io.WriteString(w, `{"ok":true}`)
	}))
	defer srv.Close()

	f := newTestFetcher(t)
	url := srv.URL + "/offline.json"

	if _, err := f.OpenCachedOnly(url); !errors.Is(err, ErrNotCached) {
		t.Fatalf("error = %v, want ErrNotCached", err)
	}
	if got := atomic.LoadInt32(&hits); got != 0 {
		t.Fatalf("origin hits = %d, want 0", got)
	}

	warm, err := f.Open(context.Background(), url)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	read(t, warm)

	cached, err := f.OpenCachedOnly(url)
	if err != nil {
		t.Fatalf("OpenCachedOnly after warm: %v", err)
	}
	if !cached.FromCache {
		t.Error("FromCache should be true")
	}
	read(t, cached)
}

func TestLocalFilesAreGatedAndFragmentsStripped(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "local.json")
	if err := os.WriteFile(path, []byte(`{"local":true}`), 0o644); err != nil {
		t.Fatal(err)
	}

	f := newTestFetcher(t)
	if _, err := f.Open(context.Background(), "file://"+path); err == nil {
		t.Fatal("local files must be rejected unless explicitly allowed")
	}

	f.AllowLocalFiles = true
	// The "#0" discriminator lets one fixture stand in for several index
	// entries and must not become part of the path.
	res, err := f.Open(context.Background(), "file://"+path+"#0")
	if err != nil {
		t.Fatalf("Open local: %v", err)
	}
	if got, want := read(t, res), `{"local":true}`; got != want {
		t.Errorf("body = %q, want %q", got, want)
	}

	if _, err := f.Open(context.Background(), "file://"+dir+"/missing.json"); err == nil {
		t.Error("expected an error for a missing local file")
	}
}

func TestCachePathIsStableUniqueAndReadable(t *testing.T) {
	f := New("/tmp/cache")

	a := f.cachePath("https://example.test/a/2026-08-31_fidelis_in-network.json")
	b := f.cachePath("https://example.test/b/2026-08-31_fidelis_in-network.json")

	if a == b {
		t.Error("different URLs with the same basename must not collide")
	}
	if a != f.cachePath("https://example.test/a/2026-08-31_fidelis_in-network.json") {
		t.Error("cache path must be stable for the same URL")
	}
	if !strings.HasSuffix(a, "2026-08-31_fidelis_in-network.json") {
		t.Errorf("cache path %q should keep a readable suffix", a)
	}

	// Percent-encoded path segments must decode rather than leak escapes.
	enc := f.cachePath("https://example.test/Centene%20Corporate/x.json")
	if strings.Contains(filepath.Base(enc), "%") {
		t.Errorf("cache path %q should not contain percent escapes", enc)
	}

	// Traversal attempts in the URL must not escape the cache directory.
	trav := f.cachePath("https://example.test/../../etc/passwd")
	if !strings.HasPrefix(trav, "/tmp/cache/") {
		t.Errorf("cache path %q escaped the cache directory", trav)
	}
}

func TestContextCancellationStopsFetching(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	f := newTestFetcher(t)
	f.MaxAttempts = 10

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	start := time.Now()
	if _, err := f.Open(ctx, srv.URL+"/slow.json"); err == nil {
		t.Fatal("expected an error")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("fetch ignored context cancellation (took %s)", elapsed)
	}
}
