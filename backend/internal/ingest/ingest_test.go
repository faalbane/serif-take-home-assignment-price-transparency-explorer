package ingest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/faalbane/price-transparency-explorer/internal/fetch"
	"github.com/faalbane/price-transparency-explorer/internal/store"
	"github.com/faalbane/price-transparency-explorer/internal/tic"
)

// serveFixtures starts an HTTP server exposing an index plus the named rate
// fixtures, so ingest runs over the real network path.
func serveFixtures(t *testing.T, rateFiles ...string) (indexURL string) {
	t.Helper()

	mux := http.NewServeMux()
	var structures []tic.ReportingStructure

	for i, rel := range rateFiles {
		body, err := os.ReadFile(filepath.Join("../../testdata", rel))
		if err != nil {
			t.Fatalf("read fixture %s: %v", rel, err)
		}
		path := "/rates/" + rel
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write(body)
		})
		structures = append(structures, tic.ReportingStructure{
			ReportingPlans: []tic.ReportingPlan{{PlanID: "PLAN-" + rel, IssuerName: "Fidelis Care"}},
			InNetworkFiles: []tic.FileRef{{Description: "in-network file", Location: "@BASE@" + path}},
		})
		_ = i
	}

	// A path that always fails, for per-source isolation tests.
	mux.HandleFunc("/rates/missing.json", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})
	mux.HandleFunc("/rates/garbage.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"in_network":[{"billing_code":"1"},`)) // truncated
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	idx := tic.Index{
		ReportingEntityName: "Fixture Payer",
		ReportingEntityType: "Third Party Administrator",
		Version:             "2.0.0",
		LastUpdatedOn:       "2026-08-31",
		ReportingStructure:  structures,
	}
	raw, err := json.Marshal(idx)
	if err != nil {
		t.Fatal(err)
	}
	withBase := strings.ReplaceAll(string(raw), "@BASE@", srv.URL)

	mux.HandleFunc("/index.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io := []byte(withBase)
		w.Write(io)
	})
	return srv.URL + "/index.json"
}

func newService(t *testing.T, indexURL string, mutate func(*Config)) *Service {
	t.Helper()
	f := fetch.New(t.TempDir())
	f.MaxAttempts = 1
	cfg := Config{IndexURL: indexURL, Fetcher: f}
	if mutate != nil {
		mutate(&cfg)
	}
	return NewService(cfg)
}

func TestRunIngestsEveryIndexedFile(t *testing.T) {
	indexURL := serveFixtures(t, "in-network.sample.json", "in-network.malformed.json")
	svc := newService(t, indexURL, nil)

	if svc.Store() != nil {
		t.Error("Store should be nil before the first run")
	}
	if st := svc.Status(); st.Ready || st.Phase != PhaseIdle {
		t.Errorf("initial status = %+v, want idle and not ready", st)
	}

	if err := svc.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	st := svc.Status()
	if !st.Ready || st.Phase != PhaseReady {
		t.Fatalf("status after run = %+v", st)
	}
	if len(st.Files) != 2 {
		t.Fatalf("file progress entries = %d, want 2", len(st.Files))
	}
	for _, f := range st.Files {
		if f.Status != "ok" {
			t.Errorf("file %s status = %q (%s)", f.URL, f.Status, f.Error)
		}
		if f.Items == 0 {
			t.Errorf("file %s reported no items", f.URL)
		}
		if f.Bytes == 0 {
			t.Errorf("file %s reported no bytes read", f.URL)
		}
	}
	if st.Elapsed <= 0 {
		t.Error("elapsed should be positive")
	}

	data := svc.Store()
	if data == nil {
		t.Fatal("Store is nil after a successful run")
	}
	c := data.Counts()
	if c.Sources != 2 {
		t.Errorf("sources = %d, want 2", c.Sources)
	}
	// 26 rows from the sample fixture, 3 from the malformed one.
	if c.Rows != 29 {
		t.Errorf("rows = %d, want 29", c.Rows)
	}
	// Plans from the index must travel to the store for traceability.
	if len(data.Plans()) != 2 {
		t.Errorf("plans = %d, want 2", len(data.Plans()))
	}
	// Both sources must carry row counts and a unique label.
	labels := map[string]bool{}
	for _, src := range data.Sources() {
		if src.Rows == 0 {
			t.Errorf("source %d has no rows", src.ID)
		}
		if labels[src.Label] {
			t.Errorf("duplicate source label %q", src.Label)
		}
		labels[src.Label] = true
	}
}

func TestRunIsolatesAFailedSource(t *testing.T) {
	// One good file, one 404, one syntactically broken file.
	srvIndex := serveFixtures(t, "in-network.sample.json")
	base := strings.TrimSuffix(srvIndex, "/index.json")

	idx := tic.Index{
		ReportingEntityName: "Fixture Payer",
		Version:             "2.0.0",
		ReportingStructure: []tic.ReportingStructure{
			{InNetworkFiles: []tic.FileRef{{Description: "good", Location: base + "/rates/in-network.sample.json"}}},
			{InNetworkFiles: []tic.FileRef{{Description: "missing", Location: base + "/rates/missing.json"}}},
			{InNetworkFiles: []tic.FileRef{{Description: "garbage", Location: base + "/rates/garbage.json"}}},
		},
	}
	dir := t.TempDir()
	raw, _ := json.Marshal(idx)
	indexPath := filepath.Join(dir, "index.json")
	if err := os.WriteFile(indexPath, raw, 0o644); err != nil {
		t.Fatal(err)
	}

	f := fetch.New(t.TempDir())
	f.MaxAttempts = 1
	f.AllowLocalFiles = true
	svc := NewService(Config{IndexURL: indexPath, Fetcher: f})

	if err := svc.Run(context.Background()); err != nil {
		t.Fatalf("Run should succeed when at least one source works: %v", err)
	}

	st := svc.Status()
	if !st.Ready {
		t.Fatal("service should be ready: one source succeeded")
	}
	byStatus := map[string]int{}
	for _, fp := range st.Files {
		byStatus[fp.Status]++
		if fp.Status == "failed" && fp.Error == "" {
			t.Errorf("failed file %s carries no error message", fp.URL)
		}
	}
	if byStatus["ok"] != 1 || byStatus["failed"] != 2 {
		t.Errorf("file statuses = %v, want 1 ok and 2 failed", byStatus)
	}

	// The good source still answers queries.
	data := svc.Store()
	if got := data.Search(store.Query{BillingCode: "10006"}).Total; got != 1 {
		t.Errorf("search over the surviving source = %d rows, want 1", got)
	}
	// The failures are visible, with reasons, rather than silently dropped.
	failed := 0
	for _, src := range data.Sources() {
		if src.Status == "failed" {
			failed++
			if src.Error == "" {
				t.Errorf("source %d failed without an error message", src.ID)
			}
		}
	}
	if failed != 2 {
		t.Errorf("failed sources in the store = %d, want 2", failed)
	}
}

func TestRunFailsWhenEverySourceFails(t *testing.T) {
	srvIndex := serveFixtures(t, "in-network.sample.json")
	base := strings.TrimSuffix(srvIndex, "/index.json")

	idx := tic.Index{ReportingStructure: []tic.ReportingStructure{
		{InNetworkFiles: []tic.FileRef{{Location: base + "/rates/missing.json"}}},
	}}
	dir := t.TempDir()
	raw, _ := json.Marshal(idx)
	indexPath := filepath.Join(dir, "index.json")
	os.WriteFile(indexPath, raw, 0o644)

	f := fetch.New(t.TempDir())
	f.MaxAttempts = 1
	f.AllowLocalFiles = true
	svc := NewService(Config{IndexURL: indexPath, Fetcher: f})

	if err := svc.Run(context.Background()); err == nil {
		t.Fatal("expected an error when no source ingests")
	}
	st := svc.Status()
	if st.Phase != PhaseFailed || st.Ready {
		t.Errorf("status = %+v, want failed and not ready", st)
	}
	if st.Error == "" {
		t.Error("failed status should carry an error")
	}
	if svc.Store() != nil {
		t.Error("Store should stay nil when nothing ingested")
	}
}

func TestRunFailsOnAnUnreadableIndex(t *testing.T) {
	tests := []struct{ name, body string }{
		{"not json", "nope"},
		{"no structures", `{"reporting_structure":[]}`},
		{"no file references", `{"reporting_structure":[{"reporting_plans":[],"in_network_files":[]}]}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "index.json")
			os.WriteFile(path, []byte(tt.body), 0o644)

			f := fetch.New(t.TempDir())
			f.AllowLocalFiles = true
			svc := NewService(Config{IndexURL: path, Fetcher: f})

			if err := svc.Run(context.Background()); err == nil {
				t.Fatal("expected an error")
			}
			if st := svc.Status(); st.Phase != PhaseFailed || st.Error == "" {
				t.Errorf("status = %+v, want failed with an error", st)
			}
		})
	}
}

func TestMaxItemsAndMaxFilesLimits(t *testing.T) {
	indexURL := serveFixtures(t, "in-network.sample.json", "in-network.malformed.json")

	svc := newService(t, indexURL, func(c *Config) { c.MaxFiles = 1 })
	if err := svc.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := svc.Store().Counts().Sources; got != 1 {
		t.Errorf("sources with MaxFiles=1 = %d, want 1", got)
	}

	svc = newService(t, indexURL, func(c *Config) { c.MaxItemsPerFile = 2 })
	if err := svc.Run(context.Background()); err != nil {
		t.Fatalf("Run with item limit: %v", err)
	}
	for _, f := range svc.Status().Files {
		if f.Status != "ok" {
			t.Errorf("file %s status = %q, want ok", f.URL, f.Status)
		}
		if f.Items > 2 {
			t.Errorf("file %s ingested %d items, want at most 2", f.URL, f.Items)
		}
	}
	if svc.Store() == nil {
		t.Error("an item-limited run should still publish a store")
	}
}

func TestOfflineModeUsesOnlyTheCache(t *testing.T) {
	indexURL := serveFixtures(t, "in-network.sample.json")
	cacheDir := t.TempDir()

	// A cold offline run has nothing to read and must fail cleanly.
	f := fetch.New(cacheDir)
	f.MaxAttempts = 1
	cold := NewService(Config{IndexURL: indexURL, Fetcher: f, Offline: true})
	if err := cold.Run(context.Background()); err == nil {
		t.Fatal("offline run with an empty cache should fail")
	}
	if st := cold.Status(); !strings.Contains(st.Error, "cache") {
		t.Errorf("error = %q, want it to mention the cache", st.Error)
	}

	// Warm the cache online, then succeed offline against the same directory.
	warm := NewService(Config{IndexURL: indexURL, Fetcher: f})
	if err := warm.Run(context.Background()); err != nil {
		t.Fatalf("warming run: %v", err)
	}
	offline := NewService(Config{IndexURL: indexURL, Fetcher: f, Offline: true})
	if err := offline.Run(context.Background()); err != nil {
		t.Fatalf("offline run against a warm cache: %v", err)
	}
	if offline.Store().Counts().Rows == 0 {
		t.Error("offline run produced no rows")
	}
	for _, fp := range offline.Status().Files {
		if !fp.FromCache {
			t.Errorf("file %s was not served from cache in offline mode", fp.URL)
		}
	}
}

func TestStatusSnapshotIsACopy(t *testing.T) {
	indexURL := serveFixtures(t, "in-network.sample.json")
	svc := newService(t, indexURL, nil)
	if err := svc.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	a := svc.Status()
	if len(a.Files) == 0 {
		t.Fatal("no files in status")
	}
	a.Files[0].Status = "mutated-by-caller"

	if b := svc.Status(); b.Files[0].Status == "mutated-by-caller" {
		t.Error("Status returned a slice aliasing internal state")
	}
}

func TestCancelledContextStopsIngest(t *testing.T) {
	indexURL := serveFixtures(t, "in-network.sample.json")
	svc := newService(t, indexURL, nil)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := svc.Run(ctx); err == nil {
		t.Fatal("expected an error for a cancelled context")
	}
	if st := svc.Status(); st.Phase != PhaseFailed {
		t.Errorf("phase = %q, want failed", st.Phase)
	}
}

func TestRunRecoversFromAPanic(t *testing.T) {
	// Ingest runs on its own goroutine, outside the HTTP recover middleware.
	// A panic there must fail the ingest, not kill the process.
	indexURL := serveFixtures(t, "in-network.sample.json")
	svc := newService(t, indexURL, func(c *Config) {
		c.Logf = func(string, ...any) { panic("synthetic panic from the logger") }
	})

	err := svc.Run(context.Background())
	if err == nil {
		t.Fatal("expected an error after a panic")
	}
	if !strings.Contains(err.Error(), "panicked") {
		t.Errorf("error = %v, want it to mention the panic", err)
	}
	st := svc.Status()
	if st.Phase != PhaseFailed {
		t.Errorf("phase = %q, want failed", st.Phase)
	}
	if !strings.Contains(st.Error, "panicked") {
		t.Errorf("status error = %q, want it to mention the panic", st.Error)
	}
}
