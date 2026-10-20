// Package api exposes the search API over HTTP.
//
// Every handler is defensive about input and about ingest state: the server
// starts listening before the data is ready, so each data endpoint has to be
// able to say "not ready yet" rather than panic on a nil store.
package api

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/faalbane/price-transparency-explorer/internal/ingest"
	"github.com/faalbane/price-transparency-explorer/internal/store"
	"github.com/faalbane/price-transparency-explorer/internal/tic"
)

// maxNPIsPerRow caps how many NPIs travel with a single row. One provider group
// in the live files carries hundreds; the UI shows a sample and a count.
const maxNPIsPerRow = 25

// csvExportCap bounds a single CSV export. It is above the largest realistic
// single-code result set in the Fidelis data (208,332 rows for revenue code
// 490) only when combined with a filter, so exports above it are reported as
// truncated rather than silently cut.
const csvExportCap = 50000

// Server holds the handler dependencies.
type Server struct {
	Ingest *ingest.Service
	// IndexURL is echoed in /api/meta for provenance.
	IndexURL string
}

// NewServer returns a Server.
func NewServer(svc *ingest.Service, indexURL string) *Server {
	return &Server{Ingest: svc, IndexURL: indexURL}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	if err := enc.Encode(v); err != nil {
		// The status line is already written; there is nothing useful left to
		// do but stop.
		return
	}
}

func writeError(w http.ResponseWriter, status int, msg, detail string, hints ...string) {
	writeJSON(w, status, ErrorResponse{Error: msg, Detail: detail, Hints: hints})
}

// Health reports liveness and ingest progress. It is always 200 so a load
// balancer does not kill the process during a cold start; readiness is in the
// body.
func (s *Server) Health(w http.ResponseWriter, r *http.Request) {
	st := s.Ingest.Status()
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, st)
}

// Meta returns dataset metadata and the controls the UI needs.
func (s *Server) Meta(w http.ResponseWriter, r *http.Request) {
	st := s.Ingest.Store()
	status := s.Ingest.Status()
	if st == nil {
		writeJSON(w, http.StatusServiceUnavailable, MetaResponse{
			IndexURL: s.IndexURL,
			Ingest:   status,
		})
		return
	}
	writeJSON(w, http.StatusOK, MetaResponse{
		IndexURL:  s.IndexURL,
		Counts:    st.Counts(),
		Sources:   st.Sources(),
		Plans:     st.Plans(),
		Facets:    st.FacetDomains(),
		DataNotes: st.Notes(),
		TopCodes:  st.SearchCodes("", 12),
		Ingest:    status,
	})
}

// Codes powers billing-code typeahead.
func (s *Server) Codes(w http.ResponseWriter, r *http.Request) {
	st, ok := s.requireStore(w)
	if !ok {
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	limit := intParam(r, "limit", 25)
	writeJSON(w, http.StatusOK, map[string]any{
		"query":   q,
		"results": st.SearchCodes(q, limit),
	})
}

// Providers resolves an identifier or a name fragment to provider groups, so a
// user can confirm who they are about to filter on.
func (s *Server) Providers(w http.ResponseWriter, r *http.Request) {
	st, ok := s.requireStore(w)
	if !ok {
		return
	}
	qs := r.URL.Query()
	var (
		npiKey, einKey string
		echo           = map[string]any{}
	)

	if raw := strings.TrimSpace(qs.Get("npi")); raw != "" {
		id, err := tic.ParseNPI(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid npi", err.Error(),
				"An NPI is a 10-digit number, for example 1396747861.")
			return
		}
		npiKey, echo["npi"] = id.Normalized, id
	}
	if raw := strings.TrimSpace(qs.Get("ein")); raw != "" {
		id, err := tic.ParseEIN(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid ein", err.Error(),
				"An EIN is a 9-digit number, with or without the dash, for example 11-3471596.")
			return
		}
		einKey, echo["ein"] = id.Normalized, id
	}
	name := strings.TrimSpace(qs.Get("q"))

	if npiKey == "" && einKey == "" && name == "" {
		writeError(w, http.StatusBadRequest, "nothing to look up",
			"Supply npi, ein, or q (a business-name fragment).")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"query":   echo,
		"name":    name,
		"results": st.SearchProviders(npiKey, einKey, name, intParam(r, "limit", 25)),
	})
}

// Rates is the primary search endpoint. A billing code is required; provider
// identifiers and the remaining parameters narrow the result.
func (s *Server) Rates(w http.ResponseWriter, r *http.Request) {
	st, ok := s.requireStore(w)
	if !ok {
		return
	}

	q, echo, err := parseRatesQuery(r)
	if err != nil {
		var ve *validationError
		if errors.As(err, &ve) {
			writeError(w, http.StatusBadRequest, ve.Field, ve.Message, ve.Hints...)
			return
		}
		writeError(w, http.StatusBadRequest, "invalid request", err.Error())
		return
	}

	start := time.Now()
	res := st.Search(q)
	results := buildResults(st, res.RowIDs, echo)

	resp := RatesResponse{
		Query:      echo,
		Pagination: paginate(echo.Page, echo.PageSize, res.Total),
		Stats:      res.Stats,
		Facets:     res.Facets,
		Results:    results,
		TookMS:     float64(time.Since(start).Microseconds()) / 1000,
	}
	// An empty result is a valid answer, not an error -- but it should come
	// with a way forward.
	if res.Total == 0 {
		resp.Suggestions = st.SearchCodes(q.BillingCode, 8)
		if len(resp.Suggestions) == 0 {
			resp.Suggestions = st.SearchCodes("", 8)
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// RatesCSV returns the full matched result set as CSV, ignoring pagination.
// Exporting what you are looking at is the first thing anyone wants from a
// price table.
func (s *Server) RatesCSV(w http.ResponseWriter, r *http.Request) {
	st, ok := s.requireStore(w)
	if !ok {
		return
	}
	q, echo, err := parseRatesQuery(r)
	if err != nil {
		var ve *validationError
		if errors.As(err, &ve) {
			writeError(w, http.StatusBadRequest, ve.Field, ve.Message, ve.Hints...)
			return
		}
		writeError(w, http.StatusBadRequest, "invalid request", err.Error())
		return
	}

	// Export the whole matched set, capped so a single request cannot stream
	// the entire dataset. Revenue code 490 alone matches 208,332 rows, so the
	// cap is reachable in normal use and must never truncate silently.
	q.Page = 1
	q.PageSize = csvExportCap
	res := st.Search(q)
	truncated := res.Total > len(res.RowIDs)

	filename := fmt.Sprintf("rates_%s.csv", sanitizeFilename(q.BillingCode))
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	// Machine-readable truncation signal for scripted consumers, who will not
	// read the trailing comment row.
	w.Header().Set("X-Total-Rows", strconv.Itoa(res.Total))
	w.Header().Set("X-Exported-Rows", strconv.Itoa(len(res.RowIDs)))
	w.Header().Set("X-Truncated", strconv.FormatBool(truncated))

	cw := csv.NewWriter(w)
	defer cw.Flush()
	_ = cw.Write([]string{
		"billing_code", "billing_code_type", "name", "description",
		"negotiated_rate", "negotiated_type", "billing_class", "setting",
		"service_codes", "billing_code_modifiers", "expiration_date",
		"provider_group_id", "business_name", "ein", "npi_count", "npis",
		"additional_information", "source_url", "reporting_entity_name", "last_updated_on",
	})
	for _, row := range buildResults(st, res.RowIDs, echo) {
		_ = cw.Write([]string{
			row.BillingCode, row.BillingCodeType, row.Name, row.Description,
			strconv.FormatFloat(row.NegotiatedRate, 'f', -1, 64), row.NegotiatedType,
			row.BillingClass, row.Setting,
			strings.Join(row.ServiceCodes, " "), strings.Join(row.Modifiers, " "),
			row.ExpirationDate,
			strconv.FormatInt(row.ProviderGroup.ProviderGroupID, 10),
			row.ProviderGroup.BusinessName, row.ProviderGroup.EINDisplay,
			strconv.Itoa(row.ProviderGroup.NPICount), strings.Join(row.ProviderGroup.NPIs, " "),
			row.AdditionalInformation,
			row.Source.URL, row.Source.ReportingEntityName, row.Source.LastUpdatedOn,
		})
	}

	// And a human-readable signal for anyone opening the file in a spreadsheet.
	if truncated {
		_ = cw.Write([]string{fmt.Sprintf(
			"# TRUNCATED: this export contains %d of %d matching rows (limit %d). Narrow the search with a provider identifier, rate range or billing class to export the full set.",
			len(res.RowIDs), res.Total, csvExportCap)})
	}
}

func (s *Server) requireStore(w http.ResponseWriter) (*store.Store, bool) {
	st := s.Ingest.Store()
	if st == nil {
		status := s.Ingest.Status()
		w.Header().Set("Retry-After", "5")
		writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{
			Error:  "dataset not ready",
			Detail: status.Message,
			Hints:  []string{"Poll /api/health for ingest progress."},
		})
		return nil, false
	}
	return st, true
}

// validationError carries a user-facing message and recovery hints.
type validationError struct {
	Field   string
	Message string
	Hints   []string
}

func (e *validationError) Error() string { return e.Field + ": " + e.Message }

func parseRatesQuery(r *http.Request) (store.Query, QueryEcho, error) {
	qs := r.URL.Query()
	var (
		q    store.Query
		echo QueryEcho
	)

	code := strings.TrimSpace(qs.Get("code"))
	if code == "" {
		return q, echo, &validationError{
			Field:   "code",
			Message: "a billing code is required",
			Hints: []string{
				"Example: /api/rates?code=10006",
				"Use /api/codes?q=<text> to find a code by name or description.",
			},
		}
	}
	if len(code) > 32 {
		return q, echo, &validationError{Field: "code", Message: "a billing code cannot be longer than 32 characters"}
	}
	q.BillingCode = code
	echo.BillingCode = code

	q.BillingCodeType = strings.TrimSpace(qs.Get("code_type"))
	echo.BillingCodeType = q.BillingCodeType
	q.BillingClass = strings.TrimSpace(qs.Get("billing_class"))
	echo.BillingClass = q.BillingClass
	q.Setting = strings.TrimSpace(qs.Get("setting"))
	echo.Setting = q.Setting
	q.NegotiatedType = strings.TrimSpace(qs.Get("negotiated_type"))
	echo.NegotiatedType = q.NegotiatedType
	q.PlanID = strings.TrimSpace(qs.Get("plan_id"))
	echo.PlanID = q.PlanID

	if raw := strings.TrimSpace(qs.Get("source")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			return q, echo, &validationError{Field: "source", Message: "must be a non-negative source id",
				Hints: []string{"Source ids are listed in /api/meta."}}
		}
		q.SourceID = &n
		echo.SourceID = &n
	}

	if raw := strings.TrimSpace(qs.Get("npi")); raw != "" {
		id, err := tic.ParseNPI(raw)
		if err != nil {
			return q, echo, &validationError{Field: "npi", Message: err.Error(),
				Hints: []string{"An NPI is a 10-digit number, for example 1396747861."}}
		}
		q.NPI = id.Normalized
		echo.NPI = &id
		echo.Warnings = append(echo.Warnings, prefixAll("npi", id.Warnings)...)
	}
	if raw := strings.TrimSpace(qs.Get("ein")); raw != "" {
		id, err := tic.ParseEIN(raw)
		if err != nil {
			return q, echo, &validationError{Field: "ein", Message: err.Error(),
				Hints: []string{"An EIN is a 9-digit number, with or without the dash, for example 11-3471596."}}
		}
		q.EIN = id.Normalized
		echo.EIN = &id
		echo.Warnings = append(echo.Warnings, prefixAll("ein", id.Warnings)...)
	}

	minRate, err := floatParam(qs.Get("min_rate"), "min_rate")
	if err != nil {
		return q, echo, err
	}
	maxRate, err := floatParam(qs.Get("max_rate"), "max_rate")
	if err != nil {
		return q, echo, err
	}
	if minRate != nil && maxRate != nil && *minRate > *maxRate {
		return q, echo, &validationError{Field: "min_rate", Message: "min_rate cannot be greater than max_rate"}
	}
	q.MinRate, q.MaxRate = minRate, maxRate
	echo.MinRate, echo.MaxRate = minRate, maxRate

	sort := strings.TrimSpace(qs.Get("sort"))
	switch sort {
	case "", "rate_asc":
		sort = "rate_asc"
	case "rate_desc", "provider", "npi_count":
	default:
		return q, echo, &validationError{Field: "sort", Message: "unknown sort order " + strconv.Quote(sort),
			Hints: []string{"Valid values: rate_asc, rate_desc, provider, npi_count."}}
	}
	q.Sort = sort
	echo.Sort = sort

	page := intParam(r, "page", 1)
	if page < 1 {
		page = 1
	}
	pageSize := intParam(r, "page_size", 50)
	if pageSize < 1 {
		pageSize = 50
	}
	if pageSize > 500 {
		pageSize = 500
	}
	q.Page, q.PageSize = page, pageSize
	echo.Page, echo.PageSize = page, pageSize

	return q, echo, nil
}

func prefixAll(field string, in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	for _, s := range in {
		out = append(out, field+": "+s)
	}
	return out
}

func floatParam(raw, field string) (*float64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	f, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return nil, &validationError{Field: field, Message: "must be a number"}
	}
	if f < 0 {
		return nil, &validationError{Field: field, Message: "must not be negative"}
	}
	return &f, nil
}

func intParam(r *http.Request, name string, def int) int {
	raw := strings.TrimSpace(r.URL.Query().Get(name))
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return def
	}
	return n
}

func buildResults(st *store.Store, ids []int32, echo QueryEcho) []RateResult {
	out := make([]RateResult, 0, len(ids))
	matchedNPI := ""
	if echo.NPI != nil {
		matchedNPI = echo.NPI.Normalized
	}

	for _, id := range ids {
		row := st.Row(id)
		item := st.Item(row.Item)
		group := st.Group(row.Group)
		src := st.Source(row.Source)

		npis := group.NPIs
		truncated := false
		if len(npis) > maxNPIsPerRow {
			npis = npis[:maxNPIsPerRow]
			truncated = true
		}

		pg := ProviderGroupView{
			ProviderGroupID: group.ProviderGroupID,
			BusinessName:    group.BusinessName,
			TINType:         group.TINType,
			EIN:             group.EIN,
			EINDisplay:      group.EINDisplay,
			NPICount:        len(group.NPIs),
			NPIs:            npis,
			NPIsTruncated:   truncated,
			NetworkNames:    group.NetworkNames,
			Unresolved:      group.Unresolved,
		}
		// Surface the matched NPI even when it fell outside the truncated
		// sample, otherwise a filtered row looks like it does not contain the
		// NPI the user searched for.
		if matchedNPI != "" && containsString(group.NPIs, matchedNPI) {
			pg.MatchedNPI = matchedNPI
			if truncated && !containsString(npis, matchedNPI) {
				pg.NPIs = append([]string{matchedNPI}, npis...)
			}
		}

		out = append(out, RateResult{
			BillingCode:            item.BillingCode,
			BillingCodeType:        item.BillingCodeType,
			BillingCodeTypeVersion: item.BillingCodeTypeVersion,
			Name:                   item.Name,
			Description:            item.Description,
			NegotiationArrangement: item.NegotiationArrangement,

			NegotiatedRate:        row.Rate,
			NegotiatedType:        row.NegotiatedType,
			BillingClass:          row.BillingClass,
			Setting:               row.Setting,
			ServiceCodes:          row.ServiceCodes,
			Modifiers:             row.Modifiers,
			ExpirationDate:        row.ExpirationDate,
			Expires:               expires(row.ExpirationDate),
			AdditionalInformation: row.AdditionalInfo,

			ProviderGroup: pg,
			Source: SourceRef{
				ID:                  src.ID,
				StructureIndex:      src.StructureIndex,
				Description:         src.Description,
				URL:                 src.URL,
				ReportingEntityName: src.ReportingEntityName,
				ReportingEntityType: src.ReportingEntityType,
				LastUpdatedOn:       src.LastUpdatedOn,
				Version:             src.Version,
				PlanCount:           len(src.Plans),
			},
		})
	}
	return out
}

// expires reports whether an expiration date is a real date rather than the CMS
// "no expiration" sentinel.
func expires(date string) bool {
	if date == "" {
		return false
	}
	return date != "9999-12-31"
}

func containsString(hay []string, needle string) bool {
	for _, s := range hay {
		if s == needle {
			return true
		}
	}
	return false
}

func paginate(page, pageSize, total int) Pagination {
	totalPages := 0
	if pageSize > 0 {
		totalPages = (total + pageSize - 1) / pageSize
	}
	return Pagination{
		Page:       page,
		PageSize:   pageSize,
		Total:      total,
		TotalPages: totalPages,
		HasPrev:    page > 1,
		HasNext:    page < totalPages,
	}
}

func sanitizeFilename(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	out := b.String()
	if out == "" {
		return "export"
	}
	if len(out) > 40 {
		out = out[:40]
	}
	return out
}
