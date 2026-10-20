package tic

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// FlexInt64 decodes a JSON number or a JSON string into an int64.
//
// The CMS schema types NPIs and provider_group_ids as numbers, but payer files
// are not reliably schema-conformant and string-typed identifiers do occur.
// Accepting both costs one type switch and removes a whole class of ingest
// failure.
type FlexInt64 int64

// UnmarshalJSON implements json.Unmarshaler.
func (f *FlexInt64) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if s == "null" || s == `""` || s == "" {
		*f = 0
		return nil
	}
	s = strings.Trim(s, `"`)
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		// Some files emit NPIs with formatting characters.
		if d := digitsOnly(s); d != "" {
			if n2, err2 := strconv.ParseInt(d, 10, 64); err2 == nil {
				*f = FlexInt64(n2)
				return nil
			}
		}
		return fmt.Errorf("not an integer: %s", s)
	}
	*f = FlexInt64(n)
	return nil
}

// FlexStrings decodes either a JSON array of strings or a single JSON string
// into a slice. Numbers are accepted and stringified, because service codes and
// modifiers are sometimes emitted as numbers.
type FlexStrings []string

// UnmarshalJSON implements json.Unmarshaler.
func (f *FlexStrings) UnmarshalJSON(b []byte) error {
	trimmed := strings.TrimSpace(string(b))
	if trimmed == "null" || trimmed == "" {
		*f = nil
		return nil
	}
	if trimmed[0] == '[' {
		var raw []json.RawMessage
		if err := json.Unmarshal(b, &raw); err != nil {
			return err
		}
		out := make([]string, 0, len(raw))
		for _, r := range raw {
			if s, ok := scalarToString(r); ok && s != "" {
				out = append(out, s)
			}
		}
		*f = out
		return nil
	}
	if s, ok := scalarToString(b); ok {
		if s == "" {
			*f = nil
		} else {
			*f = []string{s}
		}
		return nil
	}
	return fmt.Errorf("unsupported value for string list: %s", trimmed)
}

func scalarToString(b json.RawMessage) (string, bool) {
	t := strings.TrimSpace(string(b))
	if t == "null" {
		return "", false
	}
	if len(t) > 0 && t[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return "", false
		}
		return strings.TrimSpace(s), true
	}
	return t, true
}

// TIN is the tax identification block attached to a provider group.
type TIN struct {
	Type         string `json:"type"`
	Value        string `json:"value"`
	BusinessName string `json:"business_name"`
}

// ProviderGroup is one tax-identity grouping of NPIs.
type ProviderGroup struct {
	NPI []FlexInt64 `json:"npi"`
	TIN TIN         `json:"tin"`
}

// ProviderReference is an addressable set of provider groups that rate records
// point at by ID.
type ProviderReference struct {
	ProviderGroupID FlexInt64       `json:"provider_group_id"`
	NetworkName     FlexStrings     `json:"network_name"`
	ProviderGroups  []ProviderGroup `json:"provider_groups"`
}

// NegotiatedPrice is a single negotiated price node.
type NegotiatedPrice struct {
	NegotiatedType        string      `json:"negotiated_type"`
	NegotiatedRate        float64     `json:"negotiated_rate"`
	ExpirationDate        string      `json:"expiration_date"`
	ServiceCode           FlexStrings `json:"service_code"`
	BillingClass          string      `json:"billing_class"`
	BillingCodeModifier   FlexStrings `json:"billing_code_modifier"`
	AdditionalInformation string      `json:"additional_information"`
	Setting               string      `json:"setting"`
}

// NegotiatedRateGroup ties a set of prices to the providers they apply to.
//
// Providers arrive either by reference (provider_references) or inline
// (provider_groups). The CMS guide permits both; the Centene files use
// references exclusively, but inline groups are handled so the parser is not
// payer-specific.
type NegotiatedRateGroup struct {
	NegotiatedPrices   []NegotiatedPrice `json:"negotiated_prices"`
	ProviderReferences []FlexInt64       `json:"provider_references"`
	ProviderGroups     []ProviderGroup   `json:"provider_groups"`
}

// InNetworkItem is one billable item or service with its negotiated rates.
type InNetworkItem struct {
	NegotiationArrangement string                `json:"negotiation_arrangement"`
	Name                   string                `json:"name"`
	BillingCodeType        string                `json:"billing_code_type"`
	BillingCodeTypeVersion string                `json:"billing_code_type_version"`
	BillingCode            string                `json:"billing_code"`
	Description            string                `json:"description"`
	NegotiatedRates        []NegotiatedRateGroup `json:"negotiated_rates"`
	// Bundled arrangements carry a code list; retained for completeness.
	BundledCodes []json.RawMessage `json:"bundled_codes"`
}

// FileMeta is the header block of an in-network file.
type FileMeta struct {
	ReportingEntityName string `json:"reporting_entity_name"`
	ReportingEntityType string `json:"reporting_entity_type"`
	PlanName            string `json:"plan_name"`
	PlanID              string `json:"plan_id"`
	PlanIDType          string `json:"plan_id_type"`
	PlanMarketType      string `json:"plan_market_type"`
	LastUpdatedOn       string `json:"last_updated_on"`
	Version             string `json:"version"`
}

// ParseStats records what a stream pass saw, including what it had to skip.
//
// Skipped counts are a first-class result, not a side channel: a reviewer needs
// to know that ingest completed *and* how much data it could not read.
type ParseStats struct {
	Items               int      `json:"items"`
	RateGroups          int      `json:"rate_groups"`
	Prices              int      `json:"prices"`
	ProviderReferences  int      `json:"provider_references"`
	ProviderGroups      int      `json:"provider_groups"`
	SkippedItems        int      `json:"skipped_items"`
	SkippedProviderRefs int      `json:"skipped_provider_refs"`
	UnknownTopLevelKeys []string `json:"unknown_top_level_keys,omitempty"`
}

// StreamHandler receives records as they are decoded. Returning an error aborts
// the stream.
type StreamHandler struct {
	// Meta is called once, after the header scalars have been read. It may be
	// called after some records when the file puts arrays before scalars.
	Meta func(FileMeta) error
	// ProviderRef is called once per provider_references entry.
	ProviderRef func(ProviderReference) error
	// Item is called once per in_network entry. The pointer is not retained by
	// the parser, but the handler must not assume it stays valid after return.
	Item func(*InNetworkItem) error
}

// StreamInNetwork decodes an in-network rate file incrementally.
//
// Rate files for a single payer-state run to hundreds of megabytes (89 MB and
// 174 MB for the two Fidelis files), so the document is never materialized.
// The decoder walks the top-level object key by key and decodes array elements
// one at a time.
//
// Each array element is first read as a json.RawMessage and only then
// unmarshaled into its struct. That costs one extra copy per record and buys
// per-record error isolation: a record with an unexpected shape is counted in
// ParseStats.Skipped* and the stream continues, whereas decoding straight into
// the struct would leave the decoder's position unreliable after a type error.
//
// Key order is not assumed. If in_network appears before provider_references,
// records are still emitted in file order and the caller is responsible for
// resolving references once the stream completes -- which is why this parser
// emits raw reference IDs rather than resolved providers.
func StreamInNetwork(r io.Reader, h StreamHandler) (FileMeta, ParseStats, error) {
	var (
		meta  FileMeta
		stats ParseStats
	)
	dec := json.NewDecoder(r)
	dec.UseNumber()

	tok, err := dec.Token()
	if err != nil {
		return meta, stats, fmt.Errorf("read in-network file: %w", err)
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return meta, stats, fmt.Errorf("read in-network file: expected JSON object at top level, got %v", tok)
	}

	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return meta, stats, fmt.Errorf("read top-level key: %w", err)
		}
		key, ok := keyTok.(string)
		if !ok {
			return meta, stats, fmt.Errorf("read top-level key: expected string, got %v", keyTok)
		}

		switch key {
		case "provider_references":
			n, skipped, err := streamArray(dec, func(raw json.RawMessage) error {
				var ref ProviderReference
				if err := json.Unmarshal(raw, &ref); err != nil {
					return err
				}
				stats.ProviderGroups += len(ref.ProviderGroups)
				if h.ProviderRef != nil {
					return h.ProviderRef(ref)
				}
				return nil
			})
			if err != nil {
				return meta, stats, fmt.Errorf("provider_references: %w", err)
			}
			stats.ProviderReferences += n
			stats.SkippedProviderRefs += skipped

		case "in_network":
			n, skipped, err := streamArray(dec, func(raw json.RawMessage) error {
				var item InNetworkItem
				if err := json.Unmarshal(raw, &item); err != nil {
					return err
				}
				stats.RateGroups += len(item.NegotiatedRates)
				for i := range item.NegotiatedRates {
					stats.Prices += len(item.NegotiatedRates[i].NegotiatedPrices)
				}
				if h.Item != nil {
					return h.Item(&item)
				}
				return nil
			})
			if err != nil {
				return meta, stats, fmt.Errorf("in_network: %w", err)
			}
			stats.Items += n
			stats.SkippedItems += skipped

		case "reporting_entity_name", "reporting_entity_type", "plan_name",
			"plan_id", "plan_id_type", "plan_market_type", "last_updated_on",
			"version":
			var s string
			if err := decodeLoose(dec, &s); err != nil {
				return meta, stats, fmt.Errorf("decode %s: %w", key, err)
			}
			assignMetaField(&meta, key, s)

		default:
			// Unknown members are skipped without failing: payers add
			// non-standard keys, and the schema itself evolves.
			var discard json.RawMessage
			if err := dec.Decode(&discard); err != nil {
				return meta, stats, fmt.Errorf("skip unknown key %q: %w", key, err)
			}
			stats.UnknownTopLevelKeys = append(stats.UnknownTopLevelKeys, key)
		}
	}

	// Consume the closing brace so a trailing-garbage file surfaces as an error.
	if _, err := dec.Token(); err != nil && err != io.EOF {
		return meta, stats, fmt.Errorf("read closing delimiter: %w", err)
	}

	if h.Meta != nil {
		if err := h.Meta(meta); err != nil {
			return meta, stats, err
		}
	}
	return meta, stats, nil
}

// streamArray decodes an array element by element, isolating per-element
// failures. It returns the number of elements accepted and the number skipped.
func streamArray(dec *json.Decoder, fn func(json.RawMessage) error) (accepted, skipped int, err error) {
	tok, err := dec.Token()
	if err != nil {
		return 0, 0, err
	}
	if tok == nil {
		// An explicit null in place of the array. Nothing to read, but nothing
		// is wrong either -- optional members are legitimately null.
		return 0, 0, nil
	}
	d, ok := tok.(json.Delim)
	if !ok || d != '[' {
		return 0, 0, fmt.Errorf("expected array, got %v", tok)
	}

	for dec.More() {
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			// A syntax error here means the stream position is no longer
			// trustworthy; there is nothing to recover to.
			return accepted, skipped, fmt.Errorf("decode element %d: %w", accepted+skipped, err)
		}
		if err := fn(raw); err != nil {
			if isHandlerAbort(err) {
				return accepted, skipped, err
			}
			skipped++
			continue
		}
		accepted++
	}

	if _, err := dec.Token(); err != nil { // closing ']'
		return accepted, skipped, err
	}
	return accepted, skipped, nil
}

// abortError marks a handler error that must stop the whole stream, as opposed
// to a malformed record that should merely be skipped.
type abortError struct{ err error }

func (a abortError) Error() string { return a.err.Error() }
func (a abortError) Unwrap() error { return a.err }

// Abort wraps an error so that StreamInNetwork stops instead of skipping the
// current record.
func Abort(err error) error { return abortError{err} }

func isHandlerAbort(err error) bool {
	_, ok := err.(abortError)
	return ok
}

// decodeLoose decodes a scalar, tolerating a null or a number where a string is
// expected.
func decodeLoose(dec *json.Decoder, out *string) error {
	var raw json.RawMessage
	if err := dec.Decode(&raw); err != nil {
		return err
	}
	if s, ok := scalarToString(raw); ok {
		*out = s
	}
	return nil
}

func assignMetaField(m *FileMeta, key, val string) {
	switch key {
	case "reporting_entity_name":
		m.ReportingEntityName = val
	case "reporting_entity_type":
		m.ReportingEntityType = val
	case "plan_name":
		m.PlanName = val
	case "plan_id":
		m.PlanID = val
	case "plan_id_type":
		m.PlanIDType = val
	case "plan_market_type":
		m.PlanMarketType = val
	case "last_updated_on":
		m.LastUpdatedOn = val
	case "version":
		m.Version = val
	}
}
