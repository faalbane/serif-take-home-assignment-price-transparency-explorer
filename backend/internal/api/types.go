package api

import (
	"github.com/faalbane/price-transparency-explorer/internal/ingest"
	"github.com/faalbane/price-transparency-explorer/internal/store"
	"github.com/faalbane/price-transparency-explorer/internal/tic"
)

// RateResult is one negotiated rate as the API presents it.
//
// Every row is self-describing: it carries the item it prices, the provider
// organization it applies to, and the source file and plan context it came
// from. That redundancy is deliberate -- a user who exports a row or shares a
// link should not need the rest of the response to know what they are looking
// at, and traceability back to the filed document is the point of the exercise.
type RateResult struct {
	// What is being priced.
	BillingCode            string `json:"billing_code"`
	BillingCodeType        string `json:"billing_code_type"`
	BillingCodeTypeVersion string `json:"billing_code_type_version,omitempty"`
	Name                   string `json:"name,omitempty"`
	Description            string `json:"description,omitempty"`
	NegotiationArrangement string `json:"negotiation_arrangement,omitempty"`

	// The price.
	NegotiatedRate float64  `json:"negotiated_rate"`
	NegotiatedType string   `json:"negotiated_type,omitempty"`
	BillingClass   string   `json:"billing_class,omitempty"`
	Setting        string   `json:"setting,omitempty"`
	ServiceCodes   []string `json:"service_codes,omitempty"`
	Modifiers      []string `json:"billing_code_modifiers,omitempty"`
	ExpirationDate string   `json:"expiration_date,omitempty"`
	// Expires is false for the CMS "no expiration" sentinel 9999-12-31, which
	// is what the Fidelis files use throughout.
	Expires               bool   `json:"expires"`
	AdditionalInformation string `json:"additional_information,omitempty"`

	// Who it applies to.
	ProviderGroup ProviderGroupView `json:"provider_group"`

	// Where it came from.
	Source SourceRef `json:"source"`
}

// ProviderGroupView is a provider group trimmed for transport. Large NPI lists
// are truncated, because a single group can carry hundreds of NPIs and the UI
// only shows a sample plus a count.
type ProviderGroupView struct {
	ProviderGroupID int64    `json:"provider_group_id"`
	BusinessName    string   `json:"business_name,omitempty"`
	TINType         string   `json:"tin_type,omitempty"`
	EIN             string   `json:"ein,omitempty"`
	EINDisplay      string   `json:"ein_display,omitempty"`
	NPICount        int      `json:"npi_count"`
	NPIs            []string `json:"npis,omitempty"`
	NPIsTruncated   bool     `json:"npis_truncated,omitempty"`
	NetworkNames    []string `json:"network_names,omitempty"`
	Unresolved      bool     `json:"unresolved,omitempty"`
	// MatchedNPI is the queried NPI when the row was reached by an NPI filter,
	// so the UI can highlight which of many NPIs caused the match.
	MatchedNPI string `json:"matched_npi,omitempty"`
}

// SourceRef is the provenance block attached to every row.
type SourceRef struct {
	ID                  int    `json:"id"`
	StructureIndex      int    `json:"structure_index"`
	Description         string `json:"description,omitempty"`
	URL                 string `json:"url"`
	ReportingEntityName string `json:"reporting_entity_name,omitempty"`
	ReportingEntityType string `json:"reporting_entity_type,omitempty"`
	LastUpdatedOn       string `json:"last_updated_on,omitempty"`
	Version             string `json:"version,omitempty"`
	PlanCount           int    `json:"plan_count"`
}

// QueryEcho reports how the server interpreted the request, including the
// normalized form of each identifier and any warnings about it. The UI renders
// these so a user is never left guessing why a search returned nothing.
type QueryEcho struct {
	BillingCode     string `json:"billing_code"`
	BillingCodeType string `json:"billing_code_type,omitempty"`
	BillingClass    string `json:"billing_class,omitempty"`
	Setting         string `json:"setting,omitempty"`
	NegotiatedType  string `json:"negotiated_type,omitempty"`
	PlanID          string `json:"plan_id,omitempty"`
	SourceID        *int   `json:"source_id,omitempty"`

	NPI *tic.Identifier `json:"npi,omitempty"`
	EIN *tic.Identifier `json:"ein,omitempty"`

	MinRate *float64 `json:"min_rate,omitempty"`
	MaxRate *float64 `json:"max_rate,omitempty"`

	Sort     string `json:"sort"`
	Page     int    `json:"page"`
	PageSize int    `json:"page_size"`

	// Warnings are non-fatal observations about the request.
	Warnings []string `json:"warnings,omitempty"`
}

// Pagination describes the returned slice.
type Pagination struct {
	Page       int  `json:"page"`
	PageSize   int  `json:"page_size"`
	Total      int  `json:"total"`
	TotalPages int  `json:"total_pages"`
	HasPrev    bool `json:"has_prev"`
	HasNext    bool `json:"has_next"`
}

// RatesResponse is the /api/rates body.
type RatesResponse struct {
	Query      QueryEcho    `json:"query"`
	Pagination Pagination   `json:"pagination"`
	Stats      store.Stats  `json:"stats"`
	Facets     store.Facets `json:"facets"`
	Results    []RateResult `json:"results"`
	// Suggestions help a user recover from a code that matched nothing.
	Suggestions []store.CodeSummary `json:"suggestions,omitempty"`
	TookMS      float64             `json:"took_ms"`
}

// MetaResponse is the /api/meta body: everything the UI needs to build its
// controls and show provenance without hardcoding CMS enums.
type MetaResponse struct {
	IndexURL  string              `json:"index_url"`
	Counts    store.Counts        `json:"counts"`
	Sources   []store.Source      `json:"sources"`
	Plans     []store.PlanRef     `json:"plans"`
	Facets    store.FacetDomains  `json:"facet_domains"`
	DataNotes []string            `json:"data_notes,omitempty"`
	TopCodes  []store.CodeSummary `json:"top_codes,omitempty"`
	Ingest    ingest.Status       `json:"ingest"`
}

// ErrorResponse is the body for every non-2xx reply.
type ErrorResponse struct {
	Error  string   `json:"error"`
	Detail string   `json:"detail,omitempty"`
	Hints  []string `json:"hints,omitempty"`
}
