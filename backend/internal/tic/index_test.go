package tic

import (
	"os"
	"strings"
	"testing"
)

func TestParseIndex(t *testing.T) {
	f, err := os.Open("../../testdata/index.sample.json")
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer f.Close()

	idx, err := ParseIndex(f)
	if err != nil {
		t.Fatalf("ParseIndex: %v", err)
	}

	if got, want := idx.ReportingEntityName, "Centene Management Company LLC"; got != want {
		t.Errorf("ReportingEntityName = %q, want %q", got, want)
	}
	if got, want := idx.Version, "2.0.0"; got != want {
		t.Errorf("Version = %q, want %q", got, want)
	}
	if got, want := len(idx.ReportingStructure), 2; got != want {
		t.Fatalf("reporting structures = %d, want %d", got, want)
	}

	targets := idx.InNetworkTargets()
	if got, want := len(targets), 2; got != want {
		t.Fatalf("in-network targets = %d, want %d", got, want)
	}
	for i, tgt := range targets {
		if tgt.StructureIndex != i {
			t.Errorf("target %d: StructureIndex = %d, want %d", i, tgt.StructureIndex, i)
		}
		if tgt.Location == "" {
			t.Errorf("target %d: empty location", i)
		}
		if len(tgt.Plans) == 0 {
			t.Errorf("target %d: no plans carried through", i)
		}
	}
	if got, want := targets[0].Plans[0].IssuerName, "Fidelis Care"; got != want {
		t.Errorf("first plan issuer = %q, want %q", got, want)
	}
}

func TestParseIndexErrors(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantErr string
	}{
		{"not json", "this is not json", "parse index"},
		{"empty object", `{}`, "no reporting_structure"},
		{"empty structure list", `{"reporting_structure":[]}`, "no reporting_structure"},
		{"truncated", `{"reporting_structure":[{"in_net`, "parse index"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseIndex(strings.NewReader(tt.body))
			if err == nil {
				t.Fatal("expected an error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestInNetworkTargetsSkipsBlankLocations(t *testing.T) {
	idx := &Index{ReportingStructure: []ReportingStructure{{
		ReportingPlans: []ReportingPlan{{PlanID: "x"}},
		InNetworkFiles: []FileRef{
			{Description: "blank", Location: "   "},
			{Description: "good", Location: "https://example.test/in-network.json"},
		},
	}}}

	targets := idx.InNetworkTargets()
	if got, want := len(targets), 1; got != want {
		t.Fatalf("targets = %d, want %d", got, want)
	}
	if got, want := targets[0].Description, "good"; got != want {
		t.Errorf("description = %q, want %q", got, want)
	}
}
