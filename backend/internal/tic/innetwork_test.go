package tic

import (
	"errors"
	"os"
	"strings"
	"testing"
)

// collect drains a fixture into slices for assertions.
func collect(t *testing.T, path string) ([]ProviderReference, []InNetworkItem, FileMeta, ParseStats) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()

	var refs []ProviderReference
	var items []InNetworkItem
	meta, stats, err := StreamInNetwork(f, StreamHandler{
		ProviderRef: func(r ProviderReference) error { refs = append(refs, r); return nil },
		Item:        func(i *InNetworkItem) error { items = append(items, *i); return nil },
	})
	if err != nil {
		t.Fatalf("StreamInNetwork(%s): %v", path, err)
	}
	return refs, items, meta, stats
}

func TestStreamInNetworkSample(t *testing.T) {
	refs, items, meta, stats := collect(t, "../../testdata/in-network.sample.json")

	if got, want := meta.ReportingEntityName, "Centene Management Company LLC"; got != want {
		t.Errorf("ReportingEntityName = %q, want %q", got, want)
	}
	if got, want := meta.Version, "2.0.0"; got != want {
		t.Errorf("Version = %q, want %q", got, want)
	}
	if got, want := len(refs), 7; got != want {
		t.Errorf("provider references = %d, want %d", got, want)
	}
	if got, want := len(items), 6; got != want {
		t.Fatalf("items = %d, want %d", got, want)
	}
	if got, want := stats.Prices, 8; got != want {
		t.Errorf("price nodes = %d, want %d", got, want)
	}
	if got, want := stats.ProviderGroups, 25; got != want {
		t.Errorf("provider groups = %d, want %d", got, want)
	}
	if stats.SkippedItems != 0 || stats.SkippedProviderRefs != 0 {
		t.Errorf("clean fixture should skip nothing, got %+v", stats)
	}

	// First item: full field round-trip including modifier and service code.
	first := items[0]
	if first.BillingCode != "10006" || first.BillingCodeType != "CPT" {
		t.Errorf("first item = %s/%s, want 10006/CPT", first.BillingCode, first.BillingCodeType)
	}
	price := first.NegotiatedRates[0].NegotiatedPrices[0]
	if price.NegotiatedRate != 69.85 {
		t.Errorf("rate = %v, want 69.85", price.NegotiatedRate)
	}
	if got := strings.Join(price.ServiceCode, ","); got != "11" {
		t.Errorf("service_code = %q, want \"11\"", got)
	}
	if got := strings.Join(price.BillingCodeModifier, ","); got != "59" {
		t.Errorf("billing_code_modifier = %q, want \"59\"", got)
	}
	if got, want := first.NegotiatedRates[0].ProviderReferences[0], FlexInt64(1); got != want {
		t.Errorf("provider_reference = %d, want %d", got, want)
	}

	// Reference 1298 is the multi-TIN ambulance group; it must survive intact.
	var found bool
	for _, r := range refs {
		if r.ProviderGroupID == 1298 {
			found = true
			if got, want := len(r.ProviderGroups), 17; got != want {
				t.Errorf("ref 1298 groups = %d, want %d", got, want)
			}
		}
	}
	if !found {
		t.Error("provider reference 1298 not emitted")
	}
}

func TestStreamInNetworkMalformedIsIsolated(t *testing.T) {
	refs, items, meta, stats := collect(t, "../../testdata/in-network.malformed.json")

	if got, want := meta.ReportingEntityName, "Malformed Test Payer"; got != want {
		t.Errorf("ReportingEntityName = %q, want %q", got, want)
	}

	// 4 in_network records, 1 of which has negotiated_rates as a string.
	if got, want := len(items), 3; got != want {
		t.Errorf("accepted items = %d, want %d", got, want)
	}
	if got, want := stats.SkippedItems, 1; got != want {
		t.Errorf("skipped items = %d, want %d", got, want)
	}
	for _, it := range items {
		if it.Name == "BAD RECORD" {
			t.Error("BAD RECORD should have been skipped, not emitted")
		}
	}

	// 3 provider references, 1 with an object where an int belongs.
	if got, want := len(refs), 2; got != want {
		t.Errorf("accepted provider refs = %d, want %d", got, want)
	}
	if got, want := stats.SkippedProviderRefs, 1; got != want {
		t.Errorf("skipped provider refs = %d, want %d", got, want)
	}

	// A non-standard top-level key must be recorded, not fatal.
	if got, want := strings.Join(stats.UnknownTopLevelKeys, ","), "nonstandard_payer_extension"; got != want {
		t.Errorf("unknown keys = %q, want %q", got, want)
	}
}

func TestFlexibleScalarCoercion(t *testing.T) {
	refs, items, _, _ := collect(t, "../../testdata/in-network.malformed.json")

	// provider_group_id arrives as the string "2".
	if got, want := refs[1].ProviderGroupID, FlexInt64(2); got != want {
		t.Errorf("string provider_group_id = %d, want %d", got, want)
	}
	// npi arrives as the string "1396747861".
	if got, want := refs[0].ProviderGroups[0].NPI[0], FlexInt64(1396747861); got != want {
		t.Errorf("string npi = %d, want %d", got, want)
	}
	// network_name arrives as a bare string rather than an array.
	if got, want := strings.Join(refs[0].NetworkName, ","), "Single String Network"; got != want {
		t.Errorf("scalar network_name = %q, want %q", got, want)
	}

	good := items[0]
	p := good.NegotiatedRates[0].NegotiatedPrices[0]
	// service_code arrives as a bare string, modifier as a bare number.
	if got, want := strings.Join(p.ServiceCode, ","), "11"; got != want {
		t.Errorf("scalar service_code = %q, want %q", got, want)
	}
	if got, want := strings.Join(p.BillingCodeModifier, ","), "59"; got != want {
		t.Errorf("numeric billing_code_modifier = %q, want %q", got, want)
	}
}

func TestInlineProviderGroupsAreParsed(t *testing.T) {
	_, items, _, _ := collect(t, "../../testdata/in-network.malformed.json")

	var inline *NegotiatedRateGroup
	for i := range items {
		if items[i].Name == "MISSING OPTIONAL FIELDS" {
			inline = &items[i].NegotiatedRates[0]
		}
	}
	if inline == nil {
		t.Fatal("record with inline provider_groups not found")
	}
	if got, want := len(inline.ProviderGroups), 1; got != want {
		t.Fatalf("inline provider groups = %d, want %d", got, want)
	}
	if got, want := inline.ProviderGroups[0].TIN.BusinessName, "Inline Group Hospital"; got != want {
		t.Errorf("business name = %q, want %q", got, want)
	}
	// Optional fields absent from the file must land as zero values, not errors.
	p := inline.NegotiatedPrices[0]
	if p.NegotiatedType != "" || p.ExpirationDate != "" || p.Setting != "" {
		t.Errorf("absent optional fields should be empty, got %+v", p)
	}
	if p.NegotiatedRate != 1200 {
		t.Errorf("rate = %v, want 1200", p.NegotiatedRate)
	}
}

func TestStreamInNetworkFatalErrors(t *testing.T) {
	tests := []struct {
		name, body, wantErr string
	}{
		{"top level array", `[]`, "expected JSON object"},
		{"garbage", `nope`, "read in-network file"},
		{"truncated array", `{"in_network":[{"billing_code":"1"},`, "decode element"},
		{"in_network not array", `{"in_network":{"a":1}}`, "expected array"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := StreamInNetwork(strings.NewReader(tt.body), StreamHandler{})
			if err == nil {
				t.Fatal("expected an error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestHandlerAbortStopsStream(t *testing.T) {
	sentinel := errors.New("stop now")
	f, err := os.Open("../../testdata/in-network.sample.json")
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer f.Close()

	seen := 0
	_, _, err = StreamInNetwork(f, StreamHandler{
		Item: func(*InNetworkItem) error {
			seen++
			if seen == 2 {
				return Abort(sentinel)
			}
			return nil
		},
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("error = %v, want it to wrap %v", err, sentinel)
	}
	if seen != 2 {
		t.Errorf("items seen = %d, want 2 (stream should stop on abort)", seen)
	}
}

func TestHandlerPlainErrorSkipsRecord(t *testing.T) {
	f, err := os.Open("../../testdata/in-network.sample.json")
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer f.Close()

	accepted := 0
	_, stats, err := StreamInNetwork(f, StreamHandler{
		Item: func(i *InNetworkItem) error {
			if i.BillingCodeType == "RC" {
				return errors.New("handler rejected this record")
			}
			accepted++
			return nil
		},
	})
	if err != nil {
		t.Fatalf("StreamInNetwork: %v", err)
	}
	if got, want := stats.SkippedItems, 1; got != want {
		t.Errorf("skipped = %d, want %d", got, want)
	}
	if got, want := stats.Items, 5; got != want {
		t.Errorf("accepted items in stats = %d, want %d", got, want)
	}
	if accepted != 5 {
		t.Errorf("handler accepted %d, want 5", accepted)
	}
}

func TestFlexInt64RejectsNonIntegersRatherThanMangling(t *testing.T) {
	// The digits-only fallback exists for formatted identifiers such as
	// "123-456-7890". Applied to a non-integer it silently remaps one
	// identifier onto another: 1234567893.0 becomes 12345678930, and a
	// provider_group_id of 3.0 becomes 30, attaching rows to a different
	// provider group entirely.
	for _, bad := range []string{"3.0", "1.0E+10", "1234567893.0", "1e3", "3.5"} {
		var v FlexInt64
		if err := v.UnmarshalJSON([]byte(bad)); err == nil {
			t.Errorf("UnmarshalJSON(%s) = %d with no error; a non-integer must not be salvaged", bad, int64(v))
		}
	}

	// The intended cases still work.
	for _, tc := range []struct {
		in   string
		want FlexInt64
	}{
		{`1396747861`, 1396747861},
		{`"1396747861"`, 1396747861},
		{`"139-674-7861"`, 1396747861},
		{`null`, 0},
		{`""`, 0},
	} {
		var v FlexInt64
		if err := v.UnmarshalJSON([]byte(tc.in)); err != nil {
			t.Errorf("UnmarshalJSON(%s) returned %v", tc.in, err)
			continue
		}
		if v != tc.want {
			t.Errorf("UnmarshalJSON(%s) = %d, want %d", tc.in, int64(v), int64(tc.want))
		}
	}
}
