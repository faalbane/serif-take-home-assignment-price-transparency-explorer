package store

import (
	"fmt"
	"math"
	"os"
	"strings"
	"testing"

	"github.com/faalbane/price-transparency-explorer/internal/tic"
)

// buildFromFixture streams a fixture into a Store the same way ingest does.
func buildFromFixture(t *testing.T, paths ...string) *Store {
	t.Helper()
	b := NewBuilder()
	for _, p := range paths {
		f, err := os.Open(p)
		if err != nil {
			t.Fatalf("open %s: %v", p, err)
		}
		sid := b.AddSource(Source{URL: p, Description: "in-network file"})
		meta, stats, err := tic.StreamInNetwork(f, tic.StreamHandler{
			ProviderRef: func(r tic.ProviderReference) error { b.AddProviderReference(sid, r); return nil },
			Item:        func(i *tic.InNetworkItem) error { b.AddItem(sid, i); return nil },
		})
		f.Close()
		if err != nil {
			t.Fatalf("stream %s: %v", p, err)
		}
		b.UpdateSource(sid, func(s *Source) {
			s.Status = "ok"
			s.ParseStats = stats
			s.ReportingEntityName = meta.ReportingEntityName
			s.Version = meta.Version
			s.LastUpdatedOn = meta.LastUpdatedOn
			s.Plans = []tic.ReportingPlan{{PlanID: "25303NY005", PlanName: "Ambetter Bronze", IssuerName: "Fidelis Care"}}
		})
	}
	return b.Build()
}

const sampleFixture = "../../testdata/in-network.sample.json"
const malformedFixture = "../../testdata/in-network.malformed.json"

func TestBuildFlattensToExpectedRowCount(t *testing.T) {
	s := buildFromFixture(t, sampleFixture)
	c := s.Counts()

	// Fan-out is (price node x provider group in the referenced group).
	// Hand-computed from the fixture: 1 + 1 + 17 + (1 + 2) + 3 + 1 = 26.
	if got, want := c.Rows, 26; got != want {
		t.Errorf("rows = %d, want %d", got, want)
	}
	if got, want := c.Items, 6; got != want {
		t.Errorf("items = %d, want %d", got, want)
	}
	if got, want := c.ProviderGroups, 25; got != want {
		t.Errorf("provider groups = %d, want %d", got, want)
	}
	if got, want := c.DistinctCodes, 6; got != want {
		t.Errorf("distinct codes = %d, want %d", got, want)
	}
	if got, want := c.Sources, 1; got != want {
		t.Errorf("sources = %d, want %d", got, want)
	}
}

func TestSearchRequiresCodeAndReturnsRows(t *testing.T) {
	s := buildFromFixture(t, sampleFixture)

	res := s.Search(Query{BillingCode: "10006"})
	if got, want := res.Total, 1; got != want {
		t.Fatalf("total = %d, want %d", got, want)
	}
	row := s.Row(res.RowIDs[0])
	if got, want := row.Rate, 69.85; got != want {
		t.Errorf("rate = %v, want %v", got, want)
	}
	item := s.Item(row.Item)
	if item.BillingCodeType != "CPT" || item.BillingCode != "10006" {
		t.Errorf("item = %s/%s, want 10006/CPT", item.BillingCode, item.BillingCodeType)
	}
	g := s.Group(row.Group)
	if got, want := g.EINDisplay, "11-3471596"; got != want {
		t.Errorf("EIN display = %q, want %q", got, want)
	}
	if got, want := g.BusinessName, "Minkowitz Consulting Pathologists PC"; got != want {
		t.Errorf("business name = %q, want %q", got, want)
	}

	// An unknown code is an empty result, not an error.
	if res := s.Search(Query{BillingCode: "ZZZZZ"}); res.Total != 0 || len(res.RowIDs) != 0 {
		t.Errorf("unknown code returned %d rows, want 0", res.Total)
	}
}

func TestSearchFanOutAcrossMultiTINReference(t *testing.T) {
	s := buildFromFixture(t, sampleFixture)

	// A0427 points at reference 1298, which holds 17 ambulance provider groups.
	res := s.Search(Query{BillingCode: "A0427"})
	if got, want := res.Total, 17; got != want {
		t.Fatalf("A0427 rows = %d, want %d", got, want)
	}
	// Every row carries the same rate but a distinct provider organization.
	eins := map[string]bool{}
	for _, id := range res.RowIDs {
		r := s.Row(id)
		if r.Rate != 310.8 {
			t.Errorf("rate = %v, want 310.8", r.Rate)
		}
		eins[s.Group(r.Group).EIN] = true
	}
	if got, want := len(eins), 17; got != want {
		t.Errorf("distinct EINs = %d, want %d", got, want)
	}
}

func TestSearchFilterByNPI(t *testing.T) {
	s := buildFromFixture(t, sampleFixture)

	// 1396747861 belongs only to provider group 1, which prices 10006.
	res := s.Search(Query{BillingCode: "10006", NPI: "1396747861"})
	if got, want := res.Total, 1; got != want {
		t.Errorf("matching NPI total = %d, want %d", got, want)
	}

	// A valid NPI that does not price this code yields nothing.
	res = s.Search(Query{BillingCode: "10006", NPI: "1104955491"})
	if got, want := res.Total, 0; got != want {
		t.Errorf("non-matching NPI total = %d, want %d", got, want)
	}

	// A NPI absent from the dataset yields nothing rather than everything.
	res = s.Search(Query{BillingCode: "10006", NPI: "9999999999"})
	if got, want := res.Total, 0; got != want {
		t.Errorf("unknown NPI total = %d, want %d", got, want)
	}
}

func TestSearchFilterByEIN(t *testing.T) {
	s := buildFromFixture(t, sampleFixture)

	// Both dashed and bare spellings normalize to the same key upstream; the
	// store is handed digits only.
	res := s.Search(Query{BillingCode: "10006", EIN: "113471596"})
	if got, want := res.Total, 1; got != want {
		t.Errorf("EIN total = %d, want %d", got, want)
	}

	// 99213 points at reference 6, which has three TINs; filtering to one of
	// them must return exactly that one row.
	res = s.Search(Query{BillingCode: "99213"})
	if got, want := res.Total, 3; got != want {
		t.Fatalf("99213 total = %d, want %d", got, want)
	}
	res = s.Search(Query{BillingCode: "99213", EIN: "131740118"})
	if got, want := res.Total, 1; got != want {
		t.Fatalf("99213 filtered by EIN total = %d, want %d", got, want)
	}
	if got, want := s.Group(s.Row(res.RowIDs[0]).Group).BusinessName, "Northern Westchester Hospital"; got != want {
		t.Errorf("business name = %q, want %q", got, want)
	}
}

func TestSearchNPIAndEINIntersect(t *testing.T) {
	s := buildFromFixture(t, sampleFixture)

	// NPI and EIN that belong to the same group: the row survives.
	res := s.Search(Query{BillingCode: "99213", NPI: "1205085222", EIN: "131740118"})
	if got, want := res.Total, 1; got != want {
		t.Errorf("consistent NPI+EIN total = %d, want %d", got, want)
	}

	// NPI and EIN from different groups: no group satisfies both, so the result
	// is empty rather than the union.
	res = s.Search(Query{BillingCode: "99213", NPI: "1205085222", EIN: "200096809"})
	if got, want := res.Total, 0; got != want {
		t.Errorf("contradictory NPI+EIN total = %d, want %d", got, want)
	}
}

func TestSearchScalarFiltersAndRateRange(t *testing.T) {
	s := buildFromFixture(t, sampleFixture)

	// 17250 has three prices across two rate groups: 76.85, 84.06, 42.03.
	all := s.Search(Query{BillingCode: "17250"})
	if got, want := all.Total, 3; got != want {
		t.Fatalf("17250 total = %d, want %d", got, want)
	}

	minRate := 50.0
	res := s.Search(Query{BillingCode: "17250", MinRate: &minRate})
	if got, want := res.Total, 2; got != want {
		t.Errorf("min_rate=50 total = %d, want %d", got, want)
	}

	maxRate := 80.0
	res = s.Search(Query{BillingCode: "17250", MaxRate: &maxRate})
	if got, want := res.Total, 2; got != want {
		t.Errorf("max_rate=80 total = %d, want %d", got, want)
	}

	res = s.Search(Query{BillingCode: "17250", MinRate: &minRate, MaxRate: &maxRate})
	if got, want := res.Total, 1; got != want {
		t.Errorf("banded total = %d, want %d", got, want)
	}

	res = s.Search(Query{BillingCode: "17250", BillingClass: "professional"})
	if got, want := res.Total, 3; got != want {
		t.Errorf("billing_class filter total = %d, want %d", got, want)
	}
	res = s.Search(Query{BillingCode: "17250", BillingClass: "institutional"})
	if got, want := res.Total, 0; got != want {
		t.Errorf("non-matching billing_class total = %d, want %d", got, want)
	}

	// Code type disambiguates codes shared across code systems.
	res = s.Search(Query{BillingCode: "17250", BillingCodeType: "RC"})
	if got, want := res.Total, 0; got != want {
		t.Errorf("wrong code type total = %d, want %d", got, want)
	}
	res = s.Search(Query{BillingCode: "17250", BillingCodeType: "cpt"})
	if got, want := res.Total, 3; got != want {
		t.Errorf("case-insensitive code type total = %d, want %d", got, want)
	}
}

func TestSearchSortAndPagination(t *testing.T) {
	s := buildFromFixture(t, sampleFixture)

	asc := s.Search(Query{BillingCode: "17250", Sort: "rate_asc", PageSize: 10})
	got := make([]float64, 0, 3)
	for _, id := range asc.RowIDs {
		got = append(got, s.Row(id).Rate)
	}
	want := []float64{42.03, 76.85, 84.06}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("rate_asc = %v, want %v", got, want)
		}
	}

	desc := s.Search(Query{BillingCode: "17250", Sort: "rate_desc", PageSize: 10})
	if r := s.Row(desc.RowIDs[0]).Rate; r != 84.06 {
		t.Errorf("rate_desc first = %v, want 84.06", r)
	}

	// Pagination must not change the total.
	p1 := s.Search(Query{BillingCode: "17250", Sort: "rate_asc", Page: 1, PageSize: 2})
	p2 := s.Search(Query{BillingCode: "17250", Sort: "rate_asc", Page: 2, PageSize: 2})
	if p1.Total != 3 || p2.Total != 3 {
		t.Errorf("totals = %d/%d, want 3/3", p1.Total, p2.Total)
	}
	if len(p1.RowIDs) != 2 || len(p2.RowIDs) != 1 {
		t.Errorf("page sizes = %d/%d, want 2/1", len(p1.RowIDs), len(p2.RowIDs))
	}
	if s.Row(p1.RowIDs[0]).Rate != 42.03 || s.Row(p2.RowIDs[0]).Rate != 84.06 {
		t.Error("pages do not partition the sorted result")
	}

	// A page past the end is empty, not an error or a panic.
	pN := s.Search(Query{BillingCode: "17250", Page: 99, PageSize: 10})
	if pN.Total != 3 || len(pN.RowIDs) != 0 {
		t.Errorf("page 99 = %d rows (total %d), want 0 rows", len(pN.RowIDs), pN.Total)
	}

	// Oversized page sizes are clamped.
	big := s.Search(Query{BillingCode: "A0427", PageSize: 100000})
	if len(big.RowIDs) != 17 {
		t.Errorf("clamped page returned %d rows, want 17", len(big.RowIDs))
	}
}

func TestStatsMath(t *testing.T) {
	s := buildFromFixture(t, sampleFixture)
	st := s.Search(Query{BillingCode: "17250"}).Stats

	if st.Count != 3 {
		t.Fatalf("count = %d, want 3", st.Count)
	}
	if st.Min != 42.03 || st.Max != 84.06 {
		t.Errorf("min/max = %v/%v, want 42.03/84.06", st.Min, st.Max)
	}
	if want := 67.65; math.Abs(st.Mean-want) > 0.01 {
		t.Errorf("mean = %v, want %v", st.Mean, want)
	}
	if st.Median != 76.85 {
		t.Errorf("median = %v, want 76.85", st.Median)
	}
	// Linear interpolation between order statistics: p25 of [42.03, 76.85,
	// 84.06] sits halfway between the first two values.
	if want := 59.44; math.Abs(st.P25-want) > 0.01 {
		t.Errorf("p25 = %v, want %v", st.P25, want)
	}
	if want := 80.455; math.Abs(st.P75-want) > 0.01 {
		t.Errorf("p75 = %v, want %v", st.P75, want)
	}

	total := 0
	for _, b := range st.Histogram {
		total += b.Count
	}
	if total != st.Count {
		t.Errorf("histogram totals %d, want %d (the maximum value must land in a bucket)", total, st.Count)
	}

	// A single-value result set must not produce a zero-width histogram.
	one := s.Search(Query{BillingCode: "10006"}).Stats
	if len(one.Histogram) != 1 || one.Histogram[0].Count != 1 {
		t.Errorf("single-row histogram = %+v, want one bucket of 1", one.Histogram)
	}
	if one.Median != 69.85 || one.P25 != 69.85 || one.P75 != 69.85 {
		t.Errorf("single-row percentiles = %v/%v/%v, want all 69.85", one.P25, one.Median, one.P75)
	}
}

func TestFacetsExcludeTheirOwnFilter(t *testing.T) {
	s := buildFromFixture(t, sampleFixture, malformedFixture)

	// Across both fixtures 10006 exists as professional in each file.
	res := s.Search(Query{BillingCode: "10006"})
	if res.Total < 2 {
		t.Fatalf("expected 10006 in both fixtures, got %d rows", res.Total)
	}

	// Filtering to one source must leave the other source visible in the
	// source facet, otherwise the user cannot switch back.
	filtered := s.Search(Query{BillingCode: "10006", BillingClass: "professional"})
	if len(filtered.Facets.BillingClass) == 0 {
		t.Fatal("billing_class facet is empty under its own filter")
	}
	sum := 0
	for _, f := range filtered.Facets.BillingClass {
		sum += f.Count
	}
	if sum != res.Total {
		t.Errorf("billing_class facet sums to %d, want the unfiltered total %d", sum, res.Total)
	}

	// A facet for a dimension that IS filtered elsewhere must respect it.
	bySource := s.Search(Query{BillingCode: "10006", BillingClass: "nonexistent-class"})
	if bySource.Total != 0 {
		t.Errorf("total = %d, want 0", bySource.Total)
	}
	for _, f := range bySource.Facets.Source {
		if f.Count != 0 {
			// Source facet keeps the billing_class filter, so everything is 0
			// and the facet is simply absent.
			t.Errorf("source facet %q count = %d, want the billing_class filter applied", f.Value, f.Count)
		}
	}
}

func TestDanglingProviderReferenceKeepsTheRate(t *testing.T) {
	s := buildFromFixture(t, malformedFixture)

	res := s.Search(Query{BillingCode: "11111"})
	if got, want := res.Total, 1; got != want {
		t.Fatalf("dangling-reference rows = %d, want %d (the rate must not be dropped)", got, want)
	}
	g := s.Group(s.Row(res.RowIDs[0]).Group)
	if !g.Unresolved {
		t.Error("provider group should be marked Unresolved")
	}
	if g.ProviderGroupID != 424242 {
		t.Errorf("unresolved group id = %d, want 424242", g.ProviderGroupID)
	}

	var noted bool
	for _, n := range s.Notes() {
		if strings.Contains(n, "never defines") {
			noted = true
		}
	}
	if !noted {
		t.Errorf("expected a data-quality note about dangling references, got %v", s.Notes())
	}
}

func TestInlineProviderGroupsBecomeRows(t *testing.T) {
	s := buildFromFixture(t, malformedFixture)

	res := s.Search(Query{BillingCode: "0450"})
	if got, want := res.Total, 1; got != want {
		t.Fatalf("inline-group rows = %d, want %d", got, want)
	}
	g := s.Group(s.Row(res.RowIDs[0]).Group)
	if got, want := g.BusinessName, "Inline Group Hospital"; got != want {
		t.Errorf("business name = %q, want %q", got, want)
	}
	// Reachable by its NPI even though it never appeared in provider_references.
	if got := s.Search(Query{BillingCode: "0450", NPI: "1215030697"}).Total; got != 1 {
		t.Errorf("inline group NPI lookup = %d rows, want 1", got)
	}
}

func TestZeroPaddedCodeFallback(t *testing.T) {
	s := buildFromFixture(t, malformedFixture)

	// The file stores "0450"; a user typing "450" should still find it.
	if got := s.Search(Query{BillingCode: "450"}).Total; got != 1 {
		t.Errorf("unpadded lookup = %d rows, want 1", got)
	}
	if got := s.Search(Query{BillingCode: "0450"}).Total; got != 1 {
		t.Errorf("padded lookup = %d rows, want 1", got)
	}
}

func TestPlaceholderNPIIsIndexedAndNoted(t *testing.T) {
	s := buildFromFixture(t, sampleFixture)

	// The synthetic edge-case record carries NPI 0; it must stay findable.
	if got := s.Search(Query{BillingCode: "00100", NPI: "0"}).Total; got != 1 {
		t.Errorf("placeholder NPI lookup = %d rows, want 1", got)
	}
	var noted bool
	for _, n := range s.Notes() {
		if strings.Contains(n, "placeholder value 0") {
			noted = true
		}
	}
	if !noted {
		t.Errorf("expected a note about placeholder NPIs, got %v", s.Notes())
	}
}

func TestSearchCodesRanking(t *testing.T) {
	s := buildFromFixture(t, sampleFixture)

	hits := s.SearchCodes("10006", 10)
	if len(hits) == 0 || hits[0].BillingCode != "10006" {
		t.Fatalf("exact code did not rank first: %+v", hits)
	}
	if hits[0].Rows != 1 {
		t.Errorf("row count = %d, want 1", hits[0].Rows)
	}

	// Description search finds codes a user could not have named.
	hits = s.SearchCodes("needle", 10)
	if len(hits) == 0 {
		t.Error("description search found nothing for \"needle\"")
	}

	// An empty query surfaces the codes with the most data.
	hits = s.SearchCodes("", 3)
	if len(hits) != 3 {
		t.Fatalf("empty query returned %d, want 3", len(hits))
	}
	if hits[0].Rows < hits[1].Rows {
		t.Error("empty query should rank by row count descending")
	}
}

func TestSearchProviders(t *testing.T) {
	s := buildFromFixture(t, sampleFixture)

	byNPI := s.SearchProviders("1396747861", "", "", 10)
	if len(byNPI) != 1 || byNPI[0].BusinessName != "Minkowitz Consulting Pathologists PC" {
		t.Fatalf("NPI lookup = %+v", byNPI)
	}
	if byNPI[0].MatchedNPI != "1396747861" {
		t.Errorf("MatchedNPI = %q, want the queried NPI", byNPI[0].MatchedNPI)
	}

	byEIN := s.SearchProviders("", "113471596", "", 10)
	if len(byEIN) != 1 {
		t.Fatalf("EIN lookup = %+v", byEIN)
	}

	byName := s.SearchProviders("", "", "ambulance", 50)
	if len(byName) < 2 {
		t.Errorf("name search for \"ambulance\" = %d hits, want at least 2", len(byName))
	}

	if got := s.SearchProviders("", "", "", 10); got != nil {
		t.Errorf("empty provider query = %+v, want nil", got)
	}
}

func TestFacetDomainsAndPlans(t *testing.T) {
	s := buildFromFixture(t, sampleFixture)

	d := s.FacetDomains()
	if len(d.BillingClasses) == 0 || len(d.BillingCodeTypes) == 0 {
		t.Fatalf("facet domains incomplete: %+v", d)
	}
	wantTypes := map[string]bool{"CPT": true, "RC": true, "HCPCS": true}
	for _, ct := range d.BillingCodeTypes {
		delete(wantTypes, ct)
	}
	if len(wantTypes) != 0 {
		t.Errorf("missing code types in domains: %v", wantTypes)
	}

	plans := s.Plans()
	if len(plans) != 1 || plans[0].PlanID != "25303NY005" {
		t.Errorf("plans = %+v", plans)
	}
}

func TestPlanFilterRestrictsToCoveringSources(t *testing.T) {
	b := NewBuilder()
	f, err := os.Open(sampleFixture)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sid := b.AddSource(Source{URL: sampleFixture})
	if _, _, err := tic.StreamInNetwork(f, tic.StreamHandler{
		ProviderRef: func(r tic.ProviderReference) error { b.AddProviderReference(sid, r); return nil },
		Item:        func(i *tic.InNetworkItem) error { b.AddItem(sid, i); return nil },
	}); err != nil {
		t.Fatal(err)
	}
	b.UpdateSource(sid, func(s *Source) {
		s.Plans = []tic.ReportingPlan{{PlanID: "PLAN-A"}}
	})
	s := b.Build()

	if got := s.Search(Query{BillingCode: "10006", PlanID: "PLAN-A"}).Total; got != 1 {
		t.Errorf("covered plan = %d rows, want 1", got)
	}
	if got := s.Search(Query{BillingCode: "10006", PlanID: "PLAN-MISSING"}).Total; got != 0 {
		t.Errorf("uncovered plan = %d rows, want 0", got)
	}
}

func TestCodeSummariesAggregateAcrossItemNames(t *testing.T) {
	// The live files file revenue code 490 under 328 distinct procedure names,
	// all carrying the same rate ladder. Summaries must collapse to one entry
	// per (code, code type) or typeahead returns the same code over and over.
	b := NewBuilder()
	sid := b.AddSource(Source{URL: "synthetic", Description: "in-network file"})
	b.AddProviderReference(sid, tic.ProviderReference{
		ProviderGroupID: 1,
		ProviderGroups: []tic.ProviderGroup{{
			NPI: []tic.FlexInt64{1396747861},
			TIN: tic.TIN{Type: "ein", Value: "11-3471596", BusinessName: "Test Group"},
		}},
	})

	for i, name := range []string{"KNEE ARTHROSCOPY", "SHOULDER ARTHROSCOPY", "EYELID REPAIR"} {
		b.AddItem(sid, &tic.InNetworkItem{
			NegotiationArrangement: "ffs",
			Name:                   name,
			BillingCode:            "490",
			BillingCodeType:        "RC",
			NegotiatedRates: []tic.NegotiatedRateGroup{{
				ProviderReferences: []tic.FlexInt64{1},
				NegotiatedPrices: []tic.NegotiatedPrice{
					{NegotiatedRate: float64(10 * (i + 1)), BillingClass: "institutional"},
					{NegotiatedRate: float64(100 * (i + 1)), BillingClass: "institutional"},
				},
			}},
		})
	}
	s := b.Build()

	var found []CodeSummary
	for _, c := range s.SearchCodes("490", 20) {
		if c.BillingCode == "490" {
			found = append(found, c)
		}
	}
	if len(found) != 1 {
		t.Fatalf("code 490 produced %d summaries, want 1: %+v", len(found), found)
	}
	got := found[0]
	if got.NameCount != 3 {
		t.Errorf("NameCount = %d, want 3", got.NameCount)
	}
	// Six price nodes, one provider group each.
	if got.Rows != 6 {
		t.Errorf("Rows = %d, want 6 (summed across every item name)", got.Rows)
	}
	if got.MinRate != 10 || got.MaxRate != 300 {
		t.Errorf("rate range = %v..%v, want 10..300", got.MinRate, got.MaxRate)
	}
	// The label is the name carrying the most rows; all three tie here, so just
	// require that it is one of them rather than encoding map iteration order.
	switch got.Name {
	case "KNEE ARTHROSCOPY", "SHOULDER ARTHROSCOPY", "EYELID REPAIR":
	default:
		t.Errorf("Name = %q, want one of the filed item names", got.Name)
	}

	// Searching must still find all rows regardless of which name was chosen.
	if total := s.Search(Query{BillingCode: "490"}).Total; total != 6 {
		t.Errorf("search total = %d, want 6", total)
	}
}

func TestCodeSummariesSeparateCodeSystems(t *testing.T) {
	s := buildFromFixture(t, sampleFixture)

	// 17250 is CPT in the fixture; a same-numbered RC code must not merge.
	cpt := 0
	for _, c := range s.SearchCodes("17250", 20) {
		if c.BillingCode == "17250" {
			cpt++
			if c.BillingCodeType != "CPT" {
				t.Errorf("unexpected code type %q", c.BillingCodeType)
			}
		}
	}
	if cpt != 1 {
		t.Errorf("summaries for 17250 = %d, want 1", cpt)
	}
}

// buildWideStore produces a store with `n` rows under one billing code, for
// exercising paths that only misbehave above the page-size clamp.
func buildWideStore(t *testing.T, code string, n int) *Store {
	t.Helper()
	b := NewBuilder()
	sid := b.AddSource(Source{URL: "synthetic", Description: "in-network file"})

	groups := make([]tic.ProviderGroup, 0, n)
	for i := 0; i < n; i++ {
		groups = append(groups, tic.ProviderGroup{
			NPI: []tic.FlexInt64{tic.FlexInt64(1000000000 + i)},
			TIN: tic.TIN{Type: "ein", Value: fmt.Sprintf("%09d", 100000000+i), BusinessName: fmt.Sprintf("Group %d", i)},
		})
	}
	b.AddProviderReference(sid, tic.ProviderReference{ProviderGroupID: 1, ProviderGroups: groups})
	b.AddItem(sid, &tic.InNetworkItem{
		NegotiationArrangement: "ffs",
		Name:                   "SYNTHETIC WIDE",
		BillingCode:            code,
		BillingCodeType:        "CPT",
		NegotiatedRates: []tic.NegotiatedRateGroup{{
			ProviderReferences: []tic.FlexInt64{1},
			NegotiatedPrices:   []tic.NegotiatedPrice{{NegotiatedRate: 10, BillingClass: "professional"}},
		}},
	})
	s := b.Build()
	if got := s.Search(Query{BillingCode: code}).Total; got != n {
		t.Fatalf("fixture built %d rows, want %d", got, n)
	}
	return s
}

func TestPageSizeIsClampedForInteractiveQueries(t *testing.T) {
	s := buildWideStore(t, "77777", 680)

	res := s.Search(Query{BillingCode: "77777", PageSize: 50000})
	if got, want := len(res.RowIDs), maxPageSize; got != want {
		t.Errorf("interactive page returned %d rows, want it clamped to %d", got, want)
	}
	if res.Total != 680 {
		t.Errorf("total = %d, want 680", res.Total)
	}
}

func TestUnpagedLiftsTheClampForExport(t *testing.T) {
	// The CSV export asks for up to 50,000 rows. Without Unpaged the query is
	// silently clamped to 500 and the export loses the rest of the data while
	// still reporting the higher cap.
	s := buildWideStore(t, "77777", 680)

	res := s.Search(Query{BillingCode: "77777", PageSize: 50000, Unpaged: true})
	if got, want := len(res.RowIDs), 680; got != want {
		t.Errorf("export returned %d rows, want all %d", got, want)
	}
	if res.Total != 680 {
		t.Errorf("total = %d, want 680", res.Total)
	}

	// The cap must still bound an export larger than it.
	small := s.Search(Query{BillingCode: "77777", PageSize: 100, Unpaged: true})
	if got, want := len(small.RowIDs), 100; got != want {
		t.Errorf("export honoured page size %d, want %d", got, want)
	}
}

func TestHugePageOffsetDoesNotPanic(t *testing.T) {
	s := buildFromFixture(t, sampleFixture)

	// A page number big enough to overflow the offset multiply must clamp, not
	// produce a negative slice bound.
	for _, page := range []int{1 << 40, 1 << 50, math.MaxInt / 500, math.MaxInt} {
		res := s.Search(Query{BillingCode: "A0427", Page: page, PageSize: 500})
		if len(res.RowIDs) != 0 {
			t.Errorf("page %d returned %d rows, want 0", page, len(res.RowIDs))
		}
		if res.Total != 17 {
			t.Errorf("page %d total = %d, want 17", page, res.Total)
		}
	}
}
