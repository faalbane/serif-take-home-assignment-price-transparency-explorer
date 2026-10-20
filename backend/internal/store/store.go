// Package store holds the normalized, searchable form of the ingested rate
// data and executes queries against it.
//
// The store is built once at ingest and is read-only afterwards, which is what
// makes lock-free concurrent reads safe. It performs no I/O and knows nothing
// about HTTP or the CMS file format beyond the types it is handed.
//
// Sizing, measured on the two live Fidelis files: 4,049 items, 539,801
// negotiated price nodes and 14,865 provider groups flatten to 581,860 rate
// rows. One row is one negotiated price for one provider group, which is the
// grain a user actually wants to see -- a single rate attached to a single
// organization with a single EIN.
package store

import (
	"sort"
	"strings"

	"github.com/faalbane/price-transparency-explorer/internal/tic"
)

// Source is one in-network file that contributed rows, with the index metadata
// needed to trace any row back to its origin.
type Source struct {
	ID             int    `json:"id"`
	StructureIndex int    `json:"structure_index"`
	Description    string `json:"description"`
	URL            string `json:"url"`
	// Label is a short unique name for this file. The index gives both Fidelis
	// in-network files the identical description "in-network file", so the
	// description alone cannot label a filter control.
	Label string `json:"label"`

	ReportingEntityName string `json:"reporting_entity_name"`
	ReportingEntityType string `json:"reporting_entity_type"`
	LastUpdatedOn       string `json:"last_updated_on"`
	Version             string `json:"version"`

	Plans []tic.ReportingPlan `json:"plans"`

	// Ingest outcome for this file.
	Status     string         `json:"status"` // "ok" | "failed" | "pending"
	Error      string         `json:"error,omitempty"`
	Bytes      int64          `json:"bytes"`
	DurationMS int64          `json:"duration_ms"`
	ParseStats tic.ParseStats `json:"parse_stats"`
	Rows       int            `json:"rows"`
}

// Item is the billable item/service metadata shared by every row that came
// from the same in_network entry.
type Item struct {
	BillingCode     string `json:"billing_code"`
	BillingCodeType string `json:"billing_code_type"`
	// BillingCodeTypeID is the ordinal of BillingCodeType in the store's code
	// type domain, so faceting can count into a slice instead of a map.
	BillingCodeTypeID      uint8  `json:"-"`
	BillingCodeTypeVersion string `json:"billing_code_type_version"`
	Name                   string `json:"name"`
	Description            string `json:"description"`
	NegotiationArrangement string `json:"negotiation_arrangement"`
}

// ProviderGroup is one tax identity and the NPIs billing under it.
type ProviderGroup struct {
	// ProviderGroupID is the provider_group_id from the source file. For a
	// group discovered inline (not via provider_references) it is 0.
	ProviderGroupID int64    `json:"provider_group_id"`
	EIN             string   `json:"ein"`         // digits only
	EINDisplay      string   `json:"ein_display"` // NN-NNNNNNN
	TINType         string   `json:"tin_type"`    // "ein" or "npi"
	BusinessName    string   `json:"business_name"`
	NPIs            []string `json:"npis"`
	NetworkNames    []string `json:"network_names,omitempty"`
	// Unresolved marks a group synthesized for a provider_references ID that
	// was never defined in the file. The rate is still real and still shown;
	// only the provider identity is missing.
	Unresolved bool `json:"unresolved,omitempty"`
}

// Row is one negotiated price for one provider group. Field widths are chosen
// so half a million rows stay comfortably in memory; repeated strings are
// interned at build time.
type Row struct {
	Item   int32
	Group  int32
	Source int16

	Rate float64

	BillingClass   string
	Setting        string
	NegotiatedType string
	ExpirationDate string
	AdditionalInfo string

	// Enum ordinals mirror the string fields above. They exist so a facet pass
	// over a quarter-million rows is a slice increment rather than a map write;
	// revenue code 490 alone matches 208,332 rows.
	BillingClassID   uint8
	SettingID        uint8
	NegotiatedTypeID uint8

	ServiceCodes []string
	Modifiers    []string
}

// Store is the immutable searchable dataset.
type Store struct {
	items   []Item
	groups  []ProviderGroup
	rows    []Row
	sources []Source

	// byCode maps a billing code to every row carrying it. Codes are not
	// unique across billing_code_type (the same number appears as both RC and
	// CPT), so code type is applied as a filter rather than folded into the key.
	byCode map[string][]int32

	// Inverted provider indexes map an identifier to group ordinals.
	byNPI map[string][]int32
	byEIN map[string][]int32

	// bySourcePlan maps a plan_id to the sources that cover it.
	planToSources map[string][]int16

	codes []CodeSummary

	// facetDomain holds the distinct value list per faceted dimension, indexed
	// by the ordinals stored on Row and Item.
	facetDomain [numFacetDims][]string

	// groupRows is the precomputed row count per provider group, so provider
	// lookups do not rescan half a million rows.
	groupRows []int

	notes []string
}

// CodeSummary powers billing-code typeahead and the "what can I search for"
// affordance. A reviewer does not know CPT codes by heart.
type CodeSummary struct {
	BillingCode     string  `json:"billing_code"`
	BillingCodeType string  `json:"billing_code_type"`
	Name            string  `json:"name"`
	Description     string  `json:"description"`
	Rows            int     `json:"rows"`
	MinRate         float64 `json:"min_rate"`
	MaxRate         float64 `json:"max_rate"`
}

// Sources returns ingest metadata for every file the store knows about,
// including files that failed.
func (s *Store) Sources() []Source { return s.sources }

// Notes returns data-quality observations recorded during the build.
func (s *Store) Notes() []string { return s.notes }

// Counts reports dataset size.
func (s *Store) Counts() Counts {
	return Counts{
		Rows:           len(s.rows),
		Items:          len(s.items),
		DistinctCodes:  len(s.byCode),
		ProviderGroups: len(s.groups),
		DistinctNPIs:   len(s.byNPI),
		DistinctEINs:   len(s.byEIN),
		Sources:        len(s.sources),
	}
}

// Counts is a summary of dataset size.
type Counts struct {
	Rows           int `json:"rows"`
	Items          int `json:"items"`
	DistinctCodes  int `json:"distinct_codes"`
	ProviderGroups int `json:"provider_groups"`
	DistinctNPIs   int `json:"distinct_npis"`
	DistinctEINs   int `json:"distinct_eins"`
	Sources        int `json:"sources"`
}

// Item returns item metadata by ordinal.
func (s *Store) Item(i int32) Item { return s.items[i] }

// Group returns a provider group by ordinal.
func (s *Store) Group(i int32) ProviderGroup { return s.groups[i] }

// Source returns a source by ordinal.
func (s *Store) Source(i int16) Source { return s.sources[i] }

// Row returns a row by ordinal.
func (s *Store) Row(i int32) Row { return s.rows[i] }

// FacetDomains lists the distinct values present for each filterable
// dimension, so the UI can build controls without hardcoding CMS enums.
type FacetDomains struct {
	BillingClasses   []string `json:"billing_classes"`
	Settings         []string `json:"settings"`
	NegotiatedTypes  []string `json:"negotiated_types"`
	BillingCodeTypes []string `json:"billing_code_types"`
	Arrangements     []string `json:"negotiation_arrangements"`
}

// FacetDomains returns the distinct value sets across the whole dataset. The
// tables are built once at ingest, so this is a copy rather than a scan.
func (s *Store) FacetDomains() FacetDomains {
	arrangements := map[string]bool{}
	for i := range s.items {
		addKey(arrangements, s.items[i].NegotiationArrangement)
	}
	return FacetDomains{
		BillingClasses:   withoutBlank(s.facetDomain[dimBillingClass]),
		Settings:         withoutBlank(s.facetDomain[dimSetting]),
		NegotiatedTypes:  withoutBlank(s.facetDomain[dimNegotiatedType]),
		BillingCodeTypes: withoutBlank(s.facetDomain[dimCodeType]),
		Arrangements:     sortedKeys(arrangements),
	}
}

// withoutBlank returns a sorted copy with the empty placeholder removed, since
// "not reported" is not a value a user can filter on.
func withoutBlank(in []string) []string {
	out := make([]string, 0, len(in))
	for _, v := range in {
		if v != "" {
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}

// Plans returns every reporting plan across all sources, de-duplicated on
// (plan_id, plan_name). The live index contains duplicate plan_id values -- the
// same HIOS id under different plan names -- so plan_id alone is not a key.
func (s *Store) Plans() []PlanRef {
	seen := map[string]bool{}
	var out []PlanRef
	for _, src := range s.sources {
		for _, p := range src.Plans {
			k := p.PlanID + "\x00" + p.PlanName
			if seen[k] {
				continue
			}
			seen[k] = true
			out = append(out, PlanRef{
				PlanID:         p.PlanID,
				PlanIDType:     p.PlanIDType,
				PlanName:       p.PlanName,
				PlanMarketType: p.PlanMarketType,
				IssuerName:     p.IssuerName,
				SourceID:       src.ID,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].PlanName != out[j].PlanName {
			return out[i].PlanName < out[j].PlanName
		}
		return out[i].PlanID < out[j].PlanID
	})
	return out
}

// PlanRef is a plan as exposed by the API.
type PlanRef struct {
	PlanID         string `json:"plan_id"`
	PlanIDType     string `json:"plan_id_type"`
	PlanName       string `json:"plan_name"`
	PlanMarketType string `json:"plan_market_type"`
	IssuerName     string `json:"issuer_name"`
	SourceID       int    `json:"source_id"`
}

func addKey(m map[string]bool, v string) {
	if v != "" {
		m[v] = true
	}
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// SearchCodes returns billing codes matching a free-text query, ranked so the
// most likely intent comes first: exact code, then code prefix, then a match in
// the short name, then the long description.
func (s *Store) SearchCodes(q string, limit int) []CodeSummary {
	if limit <= 0 || limit > 200 {
		limit = 25
	}
	q = strings.TrimSpace(q)
	if q == "" {
		// With no query, surface the codes with the most rate data.
		out := append([]CodeSummary(nil), s.codes...)
		sort.Slice(out, func(i, j int) bool { return out[i].Rows > out[j].Rows })
		if len(out) > limit {
			out = out[:limit]
		}
		return out
	}

	lower := strings.ToLower(q)
	type scored struct {
		c     CodeSummary
		score int
	}
	var hits []scored
	for _, c := range s.codes {
		score := 0
		switch {
		case strings.EqualFold(c.BillingCode, q):
			score = 100
		case strings.HasPrefix(strings.ToLower(c.BillingCode), lower):
			score = 80
		case strings.Contains(strings.ToLower(c.Name), lower):
			score = 50
		case strings.Contains(strings.ToLower(c.Description), lower):
			score = 30
		}
		if score > 0 {
			hits = append(hits, scored{c, score})
		}
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].score != hits[j].score {
			return hits[i].score > hits[j].score
		}
		if hits[i].c.Rows != hits[j].c.Rows {
			return hits[i].c.Rows > hits[j].c.Rows
		}
		return hits[i].c.BillingCode < hits[j].c.BillingCode
	})
	out := make([]CodeSummary, 0, limit)
	for i := range hits {
		if i >= limit {
			break
		}
		out = append(out, hits[i].c)
	}
	return out
}

// ProviderHit is a provider group returned by a provider lookup.
type ProviderHit struct {
	ProviderGroup
	// Rows is how many rate rows this group appears in.
	Rows int `json:"rows"`
	// MatchedNPI is set when the lookup matched on a specific NPI.
	MatchedNPI string `json:"matched_npi,omitempty"`
}

// SearchProviders resolves an identifier or name fragment to provider groups,
// so a user can confirm who they are filtering on before running a rate search.
func (s *Store) SearchProviders(npi, ein, q string, limit int) []ProviderHit {
	if limit <= 0 || limit > 200 {
		limit = 25
	}
	rowsPerGroup := s.groupRows

	var cands []int32
	switch {
	case npi != "":
		cands = s.byNPI[npi]
	case ein != "":
		cands = s.byEIN[ein]
	default:
		lower := strings.ToLower(strings.TrimSpace(q))
		if lower == "" {
			return nil
		}
		for i := range s.groups {
			if strings.Contains(strings.ToLower(s.groups[i].BusinessName), lower) {
				cands = append(cands, int32(i))
			}
		}
	}

	out := make([]ProviderHit, 0, len(cands))
	for _, gi := range cands {
		h := ProviderHit{ProviderGroup: s.groups[gi], Rows: rowsPerGroup[gi]}
		if npi != "" {
			h.MatchedNPI = npi
		}
		out = append(out, h)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Rows != out[j].Rows {
			return out[i].Rows > out[j].Rows
		}
		return out[i].BusinessName < out[j].BusinessName
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}
