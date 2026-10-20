// Package tic implements parsing and normalization for CMS Transparency in
// Coverage (TiC) machine-readable files.
//
// The CMS schema is documented at https://github.com/CMSgov/price-transparency-guide.
// Two file types matter here:
//
//   - the "table of contents" index, which lists reporting plans and points at
//     rate files (see ParseIndex); and
//   - the "in-network rates" file, which carries negotiated prices nested under
//     items/services (see StreamInNetwork).
//
// Nothing in this package performs I/O beyond reading from an io.Reader, and
// nothing here knows about HTTP or storage.
package tic

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// Index mirrors the CMS table-of-contents schema.
//
// Real-world files vary: some payers emit `reporting_structure`, and the schema
// also permits a bare in-network file list. We model the documented shape and
// tolerate missing optional members rather than failing the whole parse.
type Index struct {
	ReportingEntityName string               `json:"reporting_entity_name"`
	ReportingEntityType string               `json:"reporting_entity_type"`
	LastUpdatedOn       string               `json:"last_updated_on"`
	Version             string               `json:"version"`
	ReportingStructure  []ReportingStructure `json:"reporting_structure"`
}

// ReportingStructure groups a set of plans with the rate files that cover them.
type ReportingStructure struct {
	ReportingPlans    []ReportingPlan `json:"reporting_plans"`
	InNetworkFiles    []FileRef       `json:"in_network_files"`
	AllowedAmountFile *FileRef        `json:"allowed_amount_file"`
}

// ReportingPlan identifies one plan covered by a reporting structure.
type ReportingPlan struct {
	PlanName       string `json:"plan_name"`
	PlanIDType     string `json:"plan_id_type"`
	PlanID         string `json:"plan_id"`
	PlanMarketType string `json:"plan_market_type"`
	IssuerName     string `json:"issuer_name"`
}

// FileRef is a pointer to a downloadable machine-readable file.
type FileRef struct {
	Description string `json:"description"`
	Location    string `json:"location"`
}

// ParseIndex decodes a TiC index file.
//
// The index is small (tens of KB for the Centene/Fidelis file), so a plain
// decode is appropriate here; the in-network files are the ones that need
// streaming.
func ParseIndex(r io.Reader) (*Index, error) {
	var idx Index
	dec := json.NewDecoder(r)
	if err := dec.Decode(&idx); err != nil {
		return nil, fmt.Errorf("parse index: %w", err)
	}
	if len(idx.ReportingStructure) == 0 {
		return nil, fmt.Errorf("parse index: no reporting_structure entries found")
	}
	return &idx, nil
}

// InNetworkTarget is one in-network file selected from an index, carrying the
// plan context it was found under. The plan context is what makes a rate row
// traceable back to the plans it applies to.
type InNetworkTarget struct {
	// StructureIndex is the position in Index.ReportingStructure, used as a
	// stable human-facing identifier for the source.
	StructureIndex int
	Description    string
	Location       string
	Plans          []ReportingPlan
}

// InNetworkTargets walks every reporting structure and returns each in-network
// file reference together with the plans it covers.
//
// Entries with an empty location are skipped rather than returned as errors: a
// malformed single reference should not invalidate an otherwise usable index.
func (idx *Index) InNetworkTargets() []InNetworkTarget {
	var out []InNetworkTarget
	for i, rs := range idx.ReportingStructure {
		for _, f := range rs.InNetworkFiles {
			loc := strings.TrimSpace(f.Location)
			if loc == "" {
				continue
			}
			out = append(out, InNetworkTarget{
				StructureIndex: i,
				Description:    f.Description,
				Location:       loc,
				Plans:          rs.ReportingPlans,
			})
		}
	}
	return out
}
