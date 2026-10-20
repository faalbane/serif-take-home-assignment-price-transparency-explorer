package store

import (
	"cmp"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// Query is a rate search. BillingCode is the only required field, mirroring the
// API contract: a code is mandatory, provider identifiers and everything else
// narrow the result.
type Query struct {
	BillingCode     string
	BillingCodeType string

	// NPI and EIN are digits-only, already normalized by the caller.
	NPI string
	EIN string

	BillingClass   string
	Setting        string
	NegotiatedType string
	PlanID         string
	SourceID       *int

	MinRate *float64
	MaxRate *float64

	Sort     string // rate_asc | rate_desc | provider | npi_count
	Page     int    // 1-based
	PageSize int
	// Unpaged lifts the page-size clamp for bulk export. Interactive requests
	// must never set it: the clamp is what stops a query string from asking
	// for half a million rows in one response.
	Unpaged bool
}

// Stats summarizes the rate distribution of a full result set, not just the
// current page. Price transparency data is only interesting in aggregate: the
// spread between the cheapest and most expensive negotiated rate for the same
// code is the whole point.
type Stats struct {
	Count  int     `json:"count"`
	Min    float64 `json:"min"`
	Max    float64 `json:"max"`
	Mean   float64 `json:"mean"`
	Median float64 `json:"median"`
	P25    float64 `json:"p25"`
	P75    float64 `json:"p75"`
	// Histogram buckets the full result set for the distribution chart.
	Histogram []Bucket `json:"histogram"`
}

// Bucket is one histogram bin.
type Bucket struct {
	From  float64 `json:"from"`
	To    float64 `json:"to"`
	Count int     `json:"count"`
}

// MissingValue is the filter parameter that selects rows whose value for a
// dimension is absent from the source file.
//
// The empty string cannot serve this purpose: in a query string it is
// indistinguishable from "no filter". Facets emit this sentinel as their Value
// so that every facet the UI renders is one the UI can actually apply.
const MissingValue = "__missing__"

// FacetCount is one value of a facet and how many rows carry it.
//
// Value is what the client sends back as the filter parameter; Label is what it
// displays. They differ for the source dimension, where the parameter is a
// numeric id, and for absent values, where the parameter is MissingValue.
type FacetCount struct {
	Value string `json:"value"`
	Label string `json:"label"`
	Count int    `json:"count"`
}

// Facets are per-dimension counts. Each facet is computed with every filter
// applied EXCEPT its own, which is what makes the counts useful: selecting
// "institutional" should not reduce the professional count to zero and hide the
// option.
type Facets struct {
	BillingClass    []FacetCount `json:"billing_class"`
	Setting         []FacetCount `json:"setting"`
	BillingCodeType []FacetCount `json:"billing_code_type"`
	NegotiatedType  []FacetCount `json:"negotiated_type"`
	Source          []FacetCount `json:"source"`
}

// Result is the outcome of a search.
type Result struct {
	// RowIDs is the requested page, in sort order.
	RowIDs []int32
	Total  int
	Stats  Stats
	Facets Facets
	// MatchedCode is set when no rows carried the requested billing code and
	// the search fell back to its alternate zero-padded spelling.
	MatchedCode string
}

const (
	defaultPageSize = 50
	maxPageSize     = 500
)

// Faceted dimensions. These double as indexes into Store.facetDomain and into
// the per-request counter slices, which is what lets one pass over the
// candidate rows produce every facet at once.
type dim int

const (
	dimBillingClass dim = iota
	dimSetting
	dimNegotiatedType
	dimCodeType
	dimSource
	numFacetDims
)

// Search executes a query.
//
// The code index gives the candidate set in one map lookup; everything else is
// a linear scan over those candidates. The rows-per-code distribution on the
// live files is heavily skewed -- median 5, p90 106, mean 445, max 208,332 --
// so for almost every query the scan is a few microseconds, which is what makes
// it reasonable to skip a separate index per filterable field on a dataset
// rebuilt from scratch on every boot.
func (s *Store) Search(q Query) Result {
	code := strings.TrimSpace(q.BillingCode)
	candidates := s.byCode[code]

	res := Result{Stats: Stats{}}
	if len(candidates) == 0 {
		// Billing codes are frequently zero-padded in RC files ("0450") and
		// unpadded elsewhere. Try the other spelling before giving up, and
		// report the substitution: silently answering a different code than
		// the one that was asked for would be worse than finding nothing.
		if alt := alternateCodeSpelling(code); alt != "" {
			if alternates := s.byCode[alt]; len(alternates) > 0 {
				candidates = alternates
				res.MatchedCode = alt
			}
		}
	}

	if len(candidates) == 0 {
		return res
	}

	// Provider identifier filters reduce to a set of allowed provider groups.
	var allowedGroups map[int32]bool
	if q.NPI != "" || q.EIN != "" {
		allowedGroups = map[int32]bool{}
		if q.NPI != "" {
			for _, g := range s.byNPI[q.NPI] {
				allowedGroups[g] = true
			}
		}
		if q.EIN != "" {
			einGroups := map[int32]bool{}
			for _, g := range s.byEIN[q.EIN] {
				einGroups[g] = true
			}
			if q.NPI == "" {
				allowedGroups = einGroups
			} else {
				// Both supplied: intersect. A provider group must satisfy both
				// identifiers, which is the only reading of "narrow by NPI and
				// EIN" that cannot produce contradictory rows.
				for g := range allowedGroups {
					if !einGroups[g] {
						delete(allowedGroups, g)
					}
				}
			}
		}
		if len(allowedGroups) == 0 {
			return res
		}
	}

	var allowedSources map[int16]bool
	if q.PlanID != "" {
		allowedSources = map[int16]bool{}
		for _, sid := range s.planToSources[q.PlanID] {
			allowedSources[sid] = true
		}
		if len(allowedSources) == 0 {
			return res
		}
	}

	// Normalize the enum filters once. Stored values are already lowercased
	// (uppercased for code type) at build time, so comparison is ==, not a
	// case-folding call per row.
	fBillingClass := normalizeFilter(q.BillingClass, strings.ToLower)
	fSetting := normalizeFilter(q.Setting, strings.ToLower)
	fNegotiatedType := normalizeFilter(q.NegotiatedType, strings.ToLower)
	fCodeType := normalizeFilter(q.BillingCodeType, strings.ToUpper)

	// One pass produces both the matched set and every facet.
	//
	// For each row, count how many of the five faceted predicates it fails. A
	// row failing none is a full match and counts toward all five facets. A row
	// failing exactly one counts toward only that facet -- which is precisely
	// the "every filter except my own" semantics a usable facet needs. A row
	// failing two or more counts nowhere.
	counters := make([][]int, numFacetDims)
	for d := range counters {
		counters[d] = make([]int, len(s.facetDomain[d]))
	}

	matched := make([]int32, 0, min(len(candidates), 1024))
	for _, id := range candidates {
		r := &s.rows[id]

		// Non-faceted predicates. Failing any of these excludes the row from
		// the result and from every facet.
		if allowedGroups != nil && !allowedGroups[r.Group] {
			continue
		}
		if allowedSources != nil && !allowedSources[r.Source] {
			continue
		}
		if q.MinRate != nil && r.Rate < *q.MinRate {
			continue
		}
		if q.MaxRate != nil && r.Rate > *q.MaxRate {
			continue
		}

		item := &s.items[r.Item]

		fails := 0
		failed := dimBillingClass
		if fBillingClass != noFilter && r.BillingClass != fBillingClass {
			fails++
			failed = dimBillingClass
		}
		if fSetting != noFilter && r.Setting != fSetting {
			fails++
			failed = dimSetting
		}
		if fNegotiatedType != noFilter && r.NegotiatedType != fNegotiatedType {
			fails++
			failed = dimNegotiatedType
		}
		if fCodeType != noFilter && item.BillingCodeType != fCodeType {
			fails++
			failed = dimCodeType
		}
		if q.SourceID != nil && int(r.Source) != *q.SourceID {
			fails++
			failed = dimSource
		}

		switch fails {
		case 0:
			matched = append(matched, id)
			counters[dimBillingClass][r.BillingClassID]++
			counters[dimSetting][r.SettingID]++
			counters[dimNegotiatedType][r.NegotiatedTypeID]++
			counters[dimCodeType][item.BillingCodeTypeID]++
			counters[dimSource][r.Source]++
		case 1:
			switch failed {
			case dimBillingClass:
				counters[dimBillingClass][r.BillingClassID]++
			case dimSetting:
				counters[dimSetting][r.SettingID]++
			case dimNegotiatedType:
				counters[dimNegotiatedType][r.NegotiatedTypeID]++
			case dimCodeType:
				counters[dimCodeType][item.BillingCodeTypeID]++
			case dimSource:
				counters[dimSource][r.Source]++
			}
		}
	}

	res.Total = len(matched)

	// Sort before computing statistics: when the requested order is by rate,
	// the sorted row list already gives the order statistics, so the
	// percentile and histogram work reuses it instead of sorting a second
	// quarter-million-element slice.
	s.sortRows(matched, q.Sort)
	switch q.Sort {
	case "", "rate_asc":
		res.Stats = s.statsFromSorted(matched, false)
	case "rate_desc":
		res.Stats = s.statsFromSorted(matched, true)
	default:
		res.Stats = s.computeStats(matched)
	}

	res.Facets = Facets{
		BillingClass:    s.facetFrom(dimBillingClass, counters[dimBillingClass]),
		Setting:         s.facetFrom(dimSetting, counters[dimSetting]),
		NegotiatedType:  s.facetFrom(dimNegotiatedType, counters[dimNegotiatedType]),
		BillingCodeType: s.facetFrom(dimCodeType, counters[dimCodeType]),
		Source:          s.facetFrom(dimSource, counters[dimSource]),
	}

	pageSize := q.PageSize
	if pageSize <= 0 {
		pageSize = defaultPageSize
	}
	if pageSize > maxPageSize && !q.Unpaged {
		pageSize = maxPageSize
	}
	page := q.Page
	if page <= 0 {
		page = 1
	}

	// Clamp the offset before multiplying. A page number large enough to
	// overflow int would otherwise produce a negative start that slips past an
	// upper-bound-only guard and panics on the slice.
	start := len(matched)
	if page <= 1 {
		start = 0
	} else if offset := (page - 1) * pageSize; offset >= 0 && offset < len(matched) {
		start = offset
	}
	end := start + pageSize
	if end > len(matched) || end < start {
		end = len(matched)
	}
	res.RowIDs = matched[start:end]
	return res
}

// alternateCodeSpelling toggles revenue-code zero padding, so "450" finds a
// code stored as "0450" and vice versa.
//
// Deliberately narrow: it only converts between the two spellings of a 3-or-4
// digit revenue code. An unbounded TrimLeft would turn the CPT anesthesia code
// "00100" into "100" and return a completely different code's rates, which is
// worse than returning nothing.
func alternateCodeSpelling(code string) string {
	if !isAllDigits(code) {
		return ""
	}
	switch len(code) {
	case 4:
		if code[0] == '0' {
			return code[1:]
		}
	case 3:
		return "0" + code
	}
	return ""
}

// noFilter is the sentinel meaning "this dimension is unfiltered". It cannot
// be the empty string, because the empty string is a legitimate value to filter
// ON -- rows whose field is absent from the source file.
const noFilter = "\x00unset"

// normalizeFilter maps a raw query parameter to the value compared against a
// row: noFilter when nothing was supplied, the empty string when the caller
// asked for absent values, and the case-normalized value otherwise.
func normalizeFilter(raw string, fold func(string) string) string {
	raw = strings.TrimSpace(raw)
	switch raw {
	case "":
		return noFilter
	case MissingValue:
		return ""
	default:
		return fold(raw)
	}
}

func isAllDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

// facetFrom turns a counter slice into sorted, user-facing facet values.
func (s *Store) facetFrom(d dim, counts []int) []FacetCount {
	domain := s.facetDomain[d]
	out := make([]FacetCount, 0, len(counts))
	for i, c := range counts {
		if c == 0 {
			continue
		}
		raw := ""
		if i < len(domain) {
			raw = domain[i]
		}
		label, value := raw, raw
		if raw == "" {
			label, value = "(not reported)", MissingValue
		}
		// The source filter parameter is the numeric source id; every other
		// dimension filters on its own value.
		if d == dimSource {
			value = strconv.Itoa(i)
		}
		out = append(out, FacetCount{Value: value, Label: label, Count: c})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Label < out[j].Label
	})
	return out
}

// sortRows orders the matched set.
//
// slices.SortFunc is pdqsort and is not stable, so every comparator breaks ties
// on the row ordinal. That keeps results byte-identical across requests -- a
// paginated API whose ordering shifts between pages is broken -- without paying
// for a stable sort over a quarter-million rows.
func (s *Store) sortRows(ids []int32, mode string) {
	switch mode {
	case "rate_desc":
		slices.SortFunc(ids, func(a, b int32) int {
			if c := cmpFloat(s.rows[b].Rate, s.rows[a].Rate); c != 0 {
				return c
			}
			return cmp.Compare(a, b)
		})
	case "provider":
		slices.SortFunc(ids, func(a, b int32) int {
			if c := strings.Compare(s.groups[s.rows[a].Group].BusinessName, s.groups[s.rows[b].Group].BusinessName); c != 0 {
				return c
			}
			if c := cmpFloat(s.rows[a].Rate, s.rows[b].Rate); c != 0 {
				return c
			}
			return cmp.Compare(a, b)
		})
	case "npi_count":
		slices.SortFunc(ids, func(a, b int32) int {
			if c := cmp.Compare(len(s.groups[s.rows[b].Group].NPIs), len(s.groups[s.rows[a].Group].NPIs)); c != 0 {
				return c
			}
			if c := cmpFloat(s.rows[a].Rate, s.rows[b].Rate); c != 0 {
				return c
			}
			return cmp.Compare(a, b)
		})
	default: // rate_asc
		slices.SortFunc(ids, func(a, b int32) int {
			if c := cmpFloat(s.rows[a].Rate, s.rows[b].Rate); c != 0 {
				return c
			}
			return cmp.Compare(a, b)
		})
	}
}

// cmpFloat orders float64 without NaN surprises; a NaN rate sorts last.
func cmpFloat(a, b float64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	case math.IsNaN(a) && !math.IsNaN(b):
		return 1
	case !math.IsNaN(a) && math.IsNaN(b):
		return -1
	default:
		return 0
	}
}

const histogramBuckets = 24

// statsFromSorted computes statistics from rows already ordered by rate,
// avoiding a redundant sort. Set descending when the rows run high to low.
func (s *Store) statsFromSorted(ids []int32, descending bool) Stats {
	st := Stats{Count: len(ids)}
	if len(ids) == 0 {
		return st
	}
	rates := make([]float64, len(ids))
	sum := 0.0
	for i, id := range ids {
		r := s.rows[id].Rate
		if descending {
			rates[len(ids)-1-i] = r
		} else {
			rates[i] = r
		}
		sum += r
	}
	return finishStats(st, rates, sum)
}

func (s *Store) computeStats(ids []int32) Stats {
	st := Stats{Count: len(ids)}
	if len(ids) == 0 {
		return st
	}
	rates := make([]float64, 0, len(ids))
	sum := 0.0
	for _, id := range ids {
		r := s.rows[id].Rate
		rates = append(rates, r)
		sum += r
	}
	sort.Float64s(rates)
	return finishStats(st, rates, sum)
}

// finishStats derives the distribution summary from an ascending rate slice.
func finishStats(st Stats, rates []float64, sum float64) Stats {
	// Statistics are returned at full precision. Rounding here would bake a
	// tie-breaking rule into the API (Go rounds halves away from zero, other
	// runtimes round to even), and formatting is the presentation layer's job.
	st.Min = rates[0]
	st.Max = rates[len(rates)-1]
	st.Mean = sum / float64(len(rates))
	st.Median = percentile(rates, 0.50)
	st.P25 = percentile(rates, 0.25)
	st.P75 = percentile(rates, 0.75)
	st.Histogram = histogram(rates, histogramBuckets)
	return st
}

// percentile uses linear interpolation between order statistics (the same
// method as NumPy's default and SQL's PERCENTILE_CONT).
func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	if len(sorted) == 1 {
		return sorted[0]
	}
	pos := p * float64(len(sorted)-1)
	lo := int(math.Floor(pos))
	hi := int(math.Ceil(pos))
	if lo == hi {
		return sorted[lo]
	}
	frac := pos - float64(lo)
	return sorted[lo]*(1-frac) + sorted[hi]*frac
}

func histogram(sorted []float64, buckets int) []Bucket {
	if len(sorted) == 0 || buckets <= 0 {
		return nil
	}
	lo, hi := sorted[0], sorted[len(sorted)-1]
	if lo == hi {
		return []Bucket{{From: lo, To: hi, Count: len(sorted)}}
	}
	width := (hi - lo) / float64(buckets)
	out := make([]Bucket, buckets)
	for i := 0; i < buckets; i++ {
		out[i] = Bucket{From: lo + float64(i)*width, To: lo + float64(i+1)*width}
	}
	// Pin the outer edges to the real extremes. Accumulated floating-point
	// error in lo + n*width can leave the last bucket's upper bound just below
	// the maximum value it actually contains, which reads as a wrong axis
	// label even though no value is miscounted.
	out[0].From = lo
	out[buckets-1].To = hi
	for _, v := range sorted {
		idx := int((v - lo) / width)
		if idx >= buckets {
			idx = buckets - 1 // the maximum lands in the last bucket
		}
		if idx < 0 {
			idx = 0
		}
		out[idx].Count++
	}
	return out
}
