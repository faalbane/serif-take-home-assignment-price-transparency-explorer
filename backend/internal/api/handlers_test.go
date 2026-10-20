package api

import (
	"encoding/csv"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/faalbane/price-transparency-explorer/internal/fetch"
	"github.com/faalbane/price-transparency-explorer/internal/ingest"
	"github.com/faalbane/price-transparency-explorer/internal/tic"
)

// newTestServer ingests the committed fixtures through the real pipeline --
// index parse, stream parse, normalize, build -- and returns a ready handler.
// Nothing touches the network.
func newTestServer(t *testing.T) http.Handler {
	t.Helper()
	indexPath := writeFixtureIndex(t)

	f := fetch.New("")
	f.AllowLocalFiles = true

	svc := ingest.NewService(ingest.Config{IndexURL: indexPath, Fetcher: f})
	if err := svc.Run(t.Context()); err != nil {
		t.Fatalf("ingest fixtures: %v", err)
	}
	return NewServer(svc, indexPath).Routes(Options{})
}

// writeFixtureIndex builds a TiC index pointing at the committed rate fixtures
// by absolute path, so the test is independent of the working directory.
//
// It references both fixtures, which means these tests exercise the same
// multi-file, per-source-failure-isolation path the live two-file ingest takes.
func writeFixtureIndex(t *testing.T) string {
	t.Helper()
	abs := func(rel string) string {
		p, err := filepath.Abs(rel)
		if err != nil {
			t.Fatalf("resolve %s: %v", rel, err)
		}
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("fixture missing: %v", err)
		}
		return p
	}

	idx := tic.Index{
		ReportingEntityName: "Centene Management Company LLC",
		ReportingEntityType: "Third Party Administrator",
		LastUpdatedOn:       "2026-08-31",
		Version:             "2.0.0",
		ReportingStructure: []tic.ReportingStructure{
			{
				ReportingPlans: []tic.ReportingPlan{
					{PlanName: "Ambetter from Fidelis Care Bronze", PlanIDType: "hios", PlanID: "25303NY005", PlanMarketType: "individual", IssuerName: "Fidelis Care"},
					{PlanName: "Ambetter from Fidelis Care Gold", PlanIDType: "hios", PlanID: "25303NY017", PlanMarketType: "individual", IssuerName: "Fidelis Care"},
				},
				InNetworkFiles: []tic.FileRef{{Description: "in-network file", Location: "file://" + abs("../../testdata/in-network.sample.json")}},
			},
			{
				ReportingPlans: []tic.ReportingPlan{
					{PlanName: "Malformed Fixture Plan", PlanIDType: "hios", PlanID: "99999NY001", PlanMarketType: "individual", IssuerName: "Test Issuer"},
				},
				InNetworkFiles: []tic.FileRef{{Description: "in-network file", Location: "file://" + abs("../../testdata/in-network.malformed.json")}},
			},
		},
	}

	body, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		t.Fatalf("marshal index: %v", err)
	}
	path := filepath.Join(t.TempDir(), "index.json")
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("write index: %v", err)
	}
	return path
}

// newNotReadyServer returns a handler whose ingest has not run.
func newNotReadyServer(t *testing.T) http.Handler {
	t.Helper()
	svc := ingest.NewService(ingest.Config{IndexURL: "unused", Fetcher: fetch.New("")})
	return NewServer(svc, "unused").Routes(Options{})
}

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode response: %v\nbody: %s", err, rec.Body.String())
	}
	return v
}

func TestRatesHappyPath(t *testing.T) {
	h := newTestServer(t)

	rec := get(t, h, "/api/rates?code=10006")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want JSON", ct)
	}

	resp := decode[RatesResponse](t, rec)
	// Both fixtures price 10006, so a correct multi-source ingest returns two
	// rows -- one per file -- not one.
	if resp.Pagination.Total != 2 {
		t.Fatalf("total = %d, want 2 (one row per source file)", resp.Pagination.Total)
	}
	sources := map[int]bool{}
	for _, r := range resp.Results {
		sources[r.Source.ID] = true
	}
	if len(sources) != 2 {
		t.Errorf("rows came from %d source(s), want 2", len(sources))
	}

	// Default sort is ascending by rate, so the cheaper of the two is first.
	row := resp.Results[0]
	if row.NegotiatedRate != 50.25 {
		t.Errorf("cheapest rate = %v, want 50.25", row.NegotiatedRate)
	}
	var fidelis *RateResult
	for i := range resp.Results {
		if resp.Results[i].NegotiatedRate == 69.85 {
			fidelis = &resp.Results[i]
		}
	}
	if fidelis == nil {
		t.Fatalf("the 69.85 row from the sample fixture is missing: %+v", resp.Results)
	}
	if fidelis.ProviderGroup.EINDisplay != "11-3471596" {
		t.Errorf("EIN = %q, want 11-3471596", fidelis.ProviderGroup.EINDisplay)
	}
	row = *fidelis
	// Provenance must travel with the row.
	if row.Source.URL == "" || row.Source.ReportingEntityName == "" {
		t.Errorf("source block incomplete: %+v", row.Source)
	}
	if row.Source.PlanCount == 0 {
		t.Error("source should carry the plan count from the index")
	}
	// The CMS no-expiration sentinel must not read as a real expiry.
	if row.ExpirationDate != "9999-12-31" || row.Expires {
		t.Errorf("expiration = %q expires=%v, want the sentinel reported as non-expiring", row.ExpirationDate, row.Expires)
	}
	if resp.Query.BillingCode != "10006" || resp.Query.Sort != "rate_asc" {
		t.Errorf("query echo = %+v", resp.Query)
	}
}

func TestRatesRequiresCode(t *testing.T) {
	h := newTestServer(t)

	for _, path := range []string{"/api/rates", "/api/rates?code=", "/api/rates?code=%20%20"} {
		rec := get(t, h, path)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s status = %d, want 400", path, rec.Code)
		}
		resp := decode[ErrorResponse](t, rec)
		if resp.Error != "code" {
			t.Errorf("%s error field = %q, want %q", path, resp.Error, "code")
		}
		if len(resp.Hints) == 0 {
			t.Errorf("%s returned no recovery hints", path)
		}
	}
}

func TestRatesProviderFilters(t *testing.T) {
	h := newTestServer(t)

	// NPI narrows to the one provider group that holds it.
	resp := decode[RatesResponse](t, get(t, h, "/api/rates?code=99213&npi=1205085222"))
	if resp.Pagination.Total != 1 {
		t.Fatalf("NPI filter total = %d, want 1", resp.Pagination.Total)
	}
	if resp.Results[0].ProviderGroup.MatchedNPI != "1205085222" {
		t.Errorf("matched_npi = %q, want the queried NPI", resp.Results[0].ProviderGroup.MatchedNPI)
	}

	// EIN accepts the dashed spelling and echoes the normalization.
	resp = decode[RatesResponse](t, get(t, h, "/api/rates?code=99213&ein=13-1740118"))
	if resp.Pagination.Total != 1 {
		t.Fatalf("EIN filter total = %d, want 1", resp.Pagination.Total)
	}
	if resp.Query.EIN == nil || resp.Query.EIN.Normalized != "131740118" {
		t.Errorf("EIN echo = %+v, want normalized 131740118", resp.Query.EIN)
	}
	if resp.Query.EIN.Display != "13-1740118" {
		t.Errorf("EIN display = %q", resp.Query.EIN.Display)
	}

	// Contradictory NPI + EIN is an empty result, not an error.
	resp = decode[RatesResponse](t, get(t, h, "/api/rates?code=99213&npi=1205085222&ein=200096809"))
	if resp.Pagination.Total != 0 {
		t.Errorf("contradictory filters total = %d, want 0", resp.Pagination.Total)
	}
}

func TestRatesIdentifierWarningsDoNotBlockSearch(t *testing.T) {
	h := newTestServer(t)

	// The placeholder NPI 0 is real data in the Centene files. It must be
	// searchable and must carry a warning, not a rejection.
	rec := get(t, h, "/api/rates?code=00100&npi=0")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	resp := decode[RatesResponse](t, rec)
	if resp.Pagination.Total != 1 {
		t.Errorf("placeholder NPI total = %d, want 1", resp.Pagination.Total)
	}
	if len(resp.Query.Warnings) == 0 {
		t.Error("expected a warning about the malformed NPI")
	}
	if resp.Query.NPI == nil || resp.Query.NPI.Valid {
		t.Errorf("NPI echo should report Valid=false, got %+v", resp.Query.NPI)
	}

	// A checksum failure is also a warning, not a 400.
	rec = get(t, h, "/api/rates?code=10006&npi=1396747862")
	if rec.Code != http.StatusOK {
		t.Fatalf("bad-checksum NPI status = %d, want 200", rec.Code)
	}
	resp = decode[RatesResponse](t, rec)
	if len(resp.Query.Warnings) == 0 || !strings.Contains(strings.Join(resp.Query.Warnings, " "), "check-digit") {
		t.Errorf("warnings = %v, want a check-digit warning", resp.Query.Warnings)
	}
}

func TestRatesRejectsDigitlessIdentifiers(t *testing.T) {
	h := newTestServer(t)

	for _, path := range []string{"/api/rates?code=10006&npi=abc", "/api/rates?code=10006&ein=N/A"} {
		rec := get(t, h, path)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s status = %d, want 400", path, rec.Code)
		}
		if len(decode[ErrorResponse](t, rec).Hints) == 0 {
			t.Errorf("%s returned no hints", path)
		}
	}
}

func TestRatesInvalidParameters(t *testing.T) {
	h := newTestServer(t)

	tests := []struct{ path, wantField string }{
		{"/api/rates?code=10006&sort=bogus", "sort"},
		{"/api/rates?code=10006&min_rate=abc", "min_rate"},
		{"/api/rates?code=10006&max_rate=-5", "max_rate"},
		{"/api/rates?code=10006&min_rate=100&max_rate=10", "min_rate"},
		{"/api/rates?code=10006&source=-1", "source"},
		{"/api/rates?code=10006&source=notanumber", "source"},
		{"/api/rates?code=" + strings.Repeat("9", 40), "code"},
	}
	for _, tt := range tests {
		rec := get(t, h, tt.path)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s status = %d, want 400 (body: %s)", tt.path, rec.Code, rec.Body)
			continue
		}
		if got := decode[ErrorResponse](t, rec).Error; got != tt.wantField {
			t.Errorf("%s error field = %q, want %q", tt.path, got, tt.wantField)
		}
	}
}

func TestRatesToleratesJunkPagination(t *testing.T) {
	h := newTestServer(t)

	// Unparseable or absurd paging values fall back to defaults rather than
	// 400ing, because they are almost always a stale URL rather than intent.
	for _, path := range []string{
		"/api/rates?code=10006&page=abc&page_size=xyz",
		"/api/rates?code=10006&page=-3&page_size=0",
		"/api/rates?code=10006&page=1&page_size=99999",
		"/api/rates?code=10006&page=99999",
	} {
		rec := get(t, h, path)
		if rec.Code != http.StatusOK {
			t.Errorf("%s status = %d, want 200 (body: %s)", path, rec.Code, rec.Body)
			continue
		}
		resp := decode[RatesResponse](t, rec)
		if resp.Pagination.PageSize < 1 || resp.Pagination.PageSize > 500 {
			t.Errorf("%s page_size = %d, want it clamped to 1..500", path, resp.Pagination.PageSize)
		}
		if resp.Pagination.Page < 1 {
			t.Errorf("%s page = %d, want >= 1", path, resp.Pagination.Page)
		}
	}
}

func TestRatesUnknownCodeReturnsSuggestions(t *testing.T) {
	h := newTestServer(t)

	rec := get(t, h, "/api/rates?code=ZZZZZZ")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (an unknown code is a valid question)", rec.Code)
	}
	resp := decode[RatesResponse](t, rec)
	if resp.Pagination.Total != 0 {
		t.Errorf("total = %d, want 0", resp.Pagination.Total)
	}
	if len(resp.Suggestions) == 0 {
		t.Error("an empty result should come with suggestions")
	}
	if resp.Stats.Count != 0 {
		t.Errorf("stats count = %d, want 0", resp.Stats.Count)
	}
}

func TestRatesFacetsCarryValueAndLabel(t *testing.T) {
	h := newTestServer(t)

	resp := decode[RatesResponse](t, get(t, h, "/api/rates?code=10006"))
	if len(resp.Facets.BillingClass) == 0 {
		t.Fatal("billing_class facet is empty")
	}
	for _, f := range resp.Facets.BillingClass {
		if f.Value == "" || f.Label == "" {
			t.Errorf("facet missing value or label: %+v", f)
		}
	}
	for _, f := range resp.Facets.Source {
		if f.Value == "" || f.Label == "" {
			t.Errorf("source facet missing value or label: %+v", f)
		}
		// The source filter parameter must be the numeric id, because the
		// index gives both files the same description.
		if strings.ContainsAny(f.Value, " ()") {
			t.Errorf("source facet value %q should be a numeric id", f.Value)
		}
	}
}

func TestCodesTypeahead(t *testing.T) {
	h := newTestServer(t)

	rec := get(t, h, "/api/codes?q=10006")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := decode[struct {
		Query   string `json:"query"`
		Results []struct {
			BillingCode string `json:"billing_code"`
			Rows        int    `json:"rows"`
		} `json:"results"`
	}](t, rec)
	if len(body.Results) == 0 || body.Results[0].BillingCode != "10006" {
		t.Fatalf("results = %+v", body.Results)
	}

	// An empty query is valid and returns the highest-volume codes.
	rec = get(t, h, "/api/codes")
	if rec.Code != http.StatusOK {
		t.Errorf("empty query status = %d, want 200", rec.Code)
	}
}

func TestProvidersEndpoint(t *testing.T) {
	h := newTestServer(t)

	rec := get(t, h, "/api/providers?npi=1396747861")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	body := decode[struct {
		Results []struct {
			BusinessName string `json:"business_name"`
			Rows         int    `json:"rows"`
		} `json:"results"`
	}](t, rec)
	// The same NPI appears in both fixtures under different business names,
	// and provider groups are scoped per source file, so both are returned.
	if len(body.Results) != 2 {
		t.Fatalf("results = %+v, want one hit per source file", body.Results)
	}
	names := map[string]bool{}
	for _, r := range body.Results {
		names[r.BusinessName] = true
		if r.Rows == 0 {
			t.Errorf("provider hit %q should report how many rate rows it appears in", r.BusinessName)
		}
	}
	if !names["Minkowitz Consulting Pathologists PC"] {
		t.Errorf("expected the sample fixture provider, got %v", names)
	}

	// Nothing to look up is a 400 with guidance.
	rec = get(t, h, "/api/providers")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}

	// A digitless identifier is a 400; a name fragment is fine.
	if rec := get(t, h, "/api/providers?npi=hello"); rec.Code != http.StatusBadRequest {
		t.Errorf("bad npi status = %d, want 400", rec.Code)
	}
	if rec := get(t, h, "/api/providers?q=ambulance"); rec.Code != http.StatusOK {
		t.Errorf("name search status = %d, want 200", rec.Code)
	}
}

func TestMetaEndpoint(t *testing.T) {
	h := newTestServer(t)

	rec := get(t, h, "/api/meta")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	resp := decode[MetaResponse](t, rec)
	if resp.Counts.Rows == 0 || resp.Counts.DistinctCodes == 0 {
		t.Errorf("counts = %+v", resp.Counts)
	}
	if len(resp.Sources) == 0 {
		t.Fatal("no sources reported")
	}
	for _, src := range resp.Sources {
		if src.Label == "" {
			t.Errorf("source %d has no label", src.ID)
		}
	}
	if len(resp.Plans) == 0 {
		t.Error("no plans reported; the index plans are the traceability story")
	}
	if len(resp.Facets.BillingCodeTypes) == 0 {
		t.Error("no billing code types in facet domains")
	}
	if !resp.Ingest.Ready {
		t.Error("ingest status should report ready")
	}
	if len(resp.TopCodes) == 0 {
		t.Error("no top codes to seed the UI with")
	}
}

func TestHealthIsAlwaysOK(t *testing.T) {
	// Even before ingest, health must be 200 so a cold start is not mistaken
	// for a dead process.
	rec := get(t, newNotReadyServer(t), "/api/health")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	st := decode[ingest.Status](t, rec)
	if st.Ready {
		t.Error("Ready should be false before ingest")
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Error("health must not be cached")
	}
}

func TestDataEndpointsReturn503BeforeIngest(t *testing.T) {
	h := newNotReadyServer(t)

	for _, path := range []string{"/api/rates?code=10006", "/api/codes", "/api/providers?npi=1396747861", "/api/meta"} {
		rec := get(t, h, path)
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%s status = %d, want 503", path, rec.Code)
		}
	}
	// A 503 on a data endpoint should tell the client when to come back.
	if ra := get(t, h, "/api/rates?code=10006").Header().Get("Retry-After"); ra == "" {
		t.Error("expected a Retry-After header")
	}
}

func TestCSVExport(t *testing.T) {
	h := newTestServer(t)

	rec := get(t, h, "/api/rates.csv?code=A0427")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") {
		t.Errorf("Content-Type = %q, want text/csv", ct)
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, "rates_A0427.csv") {
		t.Errorf("Content-Disposition = %q", cd)
	}
	if rec.Header().Get("X-Truncated") != "false" {
		t.Errorf("X-Truncated = %q, want false", rec.Header().Get("X-Truncated"))
	}
	if rec.Header().Get("X-Total-Rows") != "17" {
		t.Errorf("X-Total-Rows = %q, want 17", rec.Header().Get("X-Total-Rows"))
	}

	rows, err := csv.NewReader(strings.NewReader(rec.Body.String())).ReadAll()
	if err != nil {
		t.Fatalf("parse CSV: %v", err)
	}
	// Header plus one row per provider group in the referenced group.
	if len(rows) != 18 {
		t.Fatalf("CSV rows = %d, want 18 (header + 17)", len(rows))
	}
	if rows[0][0] != "billing_code" {
		t.Errorf("first column = %q, want billing_code", rows[0][0])
	}
	// Provenance columns must be present, so an exported file stands alone.
	header := strings.Join(rows[0], ",")
	for _, col := range []string{"source_url", "reporting_entity_name", "last_updated_on", "ein", "npis"} {
		if !strings.Contains(header, col) {
			t.Errorf("CSV header missing %q: %s", col, header)
		}
	}

	// A bad request must fail before any CSV is written.
	if rec := get(t, h, "/api/rates.csv"); rec.Code != http.StatusBadRequest {
		t.Errorf("missing code status = %d, want 400", rec.Code)
	}
}

func TestUnknownAPIPathIsJSON404(t *testing.T) {
	h := newTestServer(t)

	rec := get(t, h, "/api/nope")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want JSON so clients can parse the error", ct)
	}
	if len(decode[ErrorResponse](t, rec).Hints) == 0 {
		t.Error("a 404 should list the available endpoints")
	}
}

func TestPanicIsRecoveredAsJSON500(t *testing.T) {
	// The recover middleware is the backstop for anything the handlers did not
	// anticipate; a panic must not take the process down.
	srv := NewServer(ingest.NewService(ingest.Config{Fetcher: fetch.New("")}), "x")
	h := srv.Routes(Options{})

	boom := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") })
	wrapped := withRecover(boom, nil)

	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/rates?code=1", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if decode[ErrorResponse](t, rec).Error == "" {
		t.Error("500 body should carry an error message")
	}
	_ = h
}

func TestCORSOnlyForAllowedOrigins(t *testing.T) {
	svc := ingest.NewService(ingest.Config{Fetcher: fetch.New("")})
	h := NewServer(svc, "x").Routes(Options{AllowedOrigins: []string{"http://localhost:5173"}})

	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:5173" {
		t.Errorf("allowed origin header = %q", got)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/health", nil)
	req.Header.Set("Origin", "https://evil.test")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("disallowed origin got CORS header %q", got)
	}

	// Preflight is answered without reaching a handler.
	req = httptest.NewRequest(http.MethodOptions, "/api/rates", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Errorf("preflight status = %d, want 204", rec.Code)
	}
}

func TestOnlyGETIsRouted(t *testing.T) {
	h := newTestServer(t)

	req := httptest.NewRequest(http.MethodPost, "/api/rates?code=10006", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code == http.StatusOK {
		t.Errorf("POST to /api/rates returned 200; the API is read-only")
	}
}
