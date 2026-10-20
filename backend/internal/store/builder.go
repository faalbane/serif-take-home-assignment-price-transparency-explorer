package store

import (
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/faalbane/price-transparency-explorer/internal/tic"
)

// Builder accumulates parsed TiC records and produces an immutable Store.
//
// Provider references are resolved lazily. The Fidelis files happen to emit
// provider_references before in_network, so most rows resolve on arrival, but
// the CMS schema does not guarantee key order. Rows whose reference has not
// been seen yet are deferred and resolved in Build; references that are never
// defined become explicitly "unresolved" groups so the rate itself is not lost.
type Builder struct {
	items   []Item
	groups  []ProviderGroup
	rows    []Row
	sources []Source

	// itemKey de-duplicates item metadata across sources.
	itemKey map[string]int32
	// groupKey de-duplicates provider groups within a source. Groups are
	// scoped per source because provider_group_id is only meaningful inside
	// the file that defined it.
	groupKey map[string]int32
	// refIndex maps (sourceID, provider_group_id) to the group ordinals that
	// reference resolves to.
	refIndex map[refKey][]int32

	// deferred holds rows whose provider reference was not yet defined.
	deferred []deferredRow

	// strings interns repeated string values.
	strings map[string]string
	// sliceCache interns repeated small string slices (service codes,
	// modifiers), which are overwhelmingly duplicates across rows.
	sliceCache map[string][]string

	// enums assigns a dense ordinal to each distinct value of a faceted
	// dimension, so faceting is a slice increment rather than a map write.
	enums [numFacetDims]*enumTable

	notes       []string
	danglingRef map[refKey]int
	placeholder map[string]int
}

type refKey struct {
	source int16
	id     int64
}

type deferredRow struct {
	row Row
	ref refKey
}

// NewBuilder returns an empty Builder.
func NewBuilder() *Builder {
	b := &Builder{
		itemKey:     make(map[string]int32),
		groupKey:    make(map[string]int32),
		refIndex:    make(map[refKey][]int32),
		strings:     make(map[string]string),
		sliceCache:  make(map[string][]string),
		danglingRef: make(map[refKey]int),
		placeholder: make(map[string]int),
	}
	for i := range b.enums {
		b.enums[i] = newEnumTable()
	}
	return b
}

// enumTable assigns dense ordinals to distinct strings, preserving first-seen
// order. The empty string always takes ordinal 0 so an absent value has a slot.
type enumTable struct {
	index  map[string]uint8
	values []string
}

func newEnumTable() *enumTable {
	t := &enumTable{index: map[string]uint8{"": 0}, values: []string{""}}
	return t
}

// id returns the ordinal for v, allocating one if needed.
//
// The ordinal is a uint8, which bounds a dimension at 256 distinct values. The
// faceted dimensions are CMS enumerations -- billing class, setting, negotiated
// type, billing code type -- so the real cardinality is under ten. Values past
// the limit collapse into ordinal 0 and surface as "(not reported)" rather than
// corrupting another value's count.
func (t *enumTable) id(v string) uint8 {
	if id, ok := t.index[v]; ok {
		return id
	}
	if len(t.values) >= 256 {
		return 0
	}
	id := uint8(len(t.values))
	t.index[v] = id
	t.values = append(t.values, v)
	return id
}

// AddSource registers an in-network file and returns its ordinal. Call this
// before adding any of that file's records.
func (b *Builder) AddSource(src Source) int16 {
	src.ID = len(b.sources)
	if src.Status == "" {
		src.Status = "pending"
	}
	b.sources = append(b.sources, src)
	return int16(src.ID)
}

// UpdateSource replaces a source's ingest metadata, for recording the outcome
// once the file has been read.
func (b *Builder) UpdateSource(id int16, fn func(*Source)) {
	if int(id) < len(b.sources) {
		fn(&b.sources[id])
	}
}

// AddProviderReference registers a provider_references entry for a source.
func (b *Builder) AddProviderReference(sourceID int16, ref tic.ProviderReference) {
	key := refKey{sourceID, int64(ref.ProviderGroupID)}
	ordinals := make([]int32, 0, len(ref.ProviderGroups))
	for _, g := range ref.ProviderGroups {
		ordinals = append(ordinals, b.internGroup(sourceID, int64(ref.ProviderGroupID), g, ref.NetworkName))
	}
	// A reference declared with no groups still needs an entry, otherwise rows
	// pointing at it would look dangling.
	b.refIndex[key] = append(b.refIndex[key], ordinals...)
}

func (b *Builder) internGroup(sourceID int16, gid int64, g tic.ProviderGroup, networks []string) int32 {
	npis := make([]string, 0, len(g.NPI))
	for _, n := range g.NPI {
		s := tic.NPIKey(n)
		if s == "0" {
			b.placeholder["npi_zero"]++
		}
		npis = append(npis, s)
	}
	sort.Strings(npis)
	npis = dedupeSorted(npis)

	einDigits := ""
	if strings.EqualFold(g.TIN.Type, "ein") {
		if id, err := tic.ParseEIN(g.TIN.Value); err == nil {
			einDigits = id.Normalized
			if !id.Valid {
				b.placeholder["ein_wrong_length"]++
			}
		}
	}

	pg := ProviderGroup{
		ProviderGroupID: gid,
		EIN:             einDigits,
		EINDisplay:      tic.FormatEIN(einDigits),
		TINType:         strings.ToLower(g.TIN.Type),
		BusinessName:    b.intern(strings.TrimSpace(g.TIN.BusinessName)),
		NPIs:            npis,
		NetworkNames:    networks,
	}

	// Identity for de-duplication: a group is the same group if its source,
	// reference id, tax identity and NPI set all match.
	k := fmt.Sprintf("%d|%d|%s|%s|%s", sourceID, gid, pg.TINType, g.TIN.Value, strings.Join(npis, ","))
	if idx, ok := b.groupKey[k]; ok {
		return idx
	}
	idx := int32(len(b.groups))
	b.groups = append(b.groups, pg)
	b.groupKey[k] = idx
	return idx
}

// AddItem flattens one in_network entry into rows.
//
// Flattening grain is (item x negotiated_rate group x negotiated price x
// provider group). On the live files that is 581,860 rows, so a single rate
// row carries exactly one price and exactly one provider organization -- the
// shape a user can actually read.
func (b *Builder) AddItem(sourceID int16, item *tic.InNetworkItem) {
	itemIdx := b.internItem(item)

	for gi := range item.NegotiatedRates {
		grp := &item.NegotiatedRates[gi]
		for pi := range grp.NegotiatedPrices {
			p := &grp.NegotiatedPrices[pi]
			billingClass := b.intern(strings.ToLower(strings.TrimSpace(p.BillingClass)))
			setting := b.intern(strings.ToLower(strings.TrimSpace(p.Setting)))
			negotiatedType := b.intern(strings.ToLower(strings.TrimSpace(p.NegotiatedType)))

			tmpl := Row{
				Item:           itemIdx,
				Source:         sourceID,
				Rate:           p.NegotiatedRate,
				BillingClass:   billingClass,
				Setting:        setting,
				NegotiatedType: negotiatedType,
				ExpirationDate: b.intern(strings.TrimSpace(p.ExpirationDate)),
				AdditionalInfo: b.intern(strings.TrimSpace(p.AdditionalInformation)),
				ServiceCodes:   b.internSlice(p.ServiceCode),
				Modifiers:      b.internSlice(p.BillingCodeModifier),

				BillingClassID:   b.enums[dimBillingClass].id(billingClass),
				SettingID:        b.enums[dimSetting].id(setting),
				NegotiatedTypeID: b.enums[dimNegotiatedType].id(negotiatedType),
			}

			// Inline provider groups: materialize them immediately.
			for _, ig := range grp.ProviderGroups {
				row := tmpl
				row.Group = b.internGroup(sourceID, 0, ig, nil)
				b.rows = append(b.rows, row)
			}

			// Referenced provider groups: resolve now if known, else defer.
			for _, refID := range grp.ProviderReferences {
				key := refKey{sourceID, int64(refID)}
				if ordinals, ok := b.refIndex[key]; ok {
					for _, g := range ordinals {
						row := tmpl
						row.Group = g
						b.rows = append(b.rows, row)
					}
					continue
				}
				b.deferred = append(b.deferred, deferredRow{row: tmpl, ref: key})
			}
		}
	}
}

func (b *Builder) internItem(item *tic.InNetworkItem) int32 {
	code := strings.TrimSpace(item.BillingCode)
	codeType := strings.ToUpper(strings.TrimSpace(item.BillingCodeType))
	k := code + "|" + codeType + "|" + item.BillingCodeTypeVersion + "|" + item.Name
	if idx, ok := b.itemKey[k]; ok {
		return idx
	}
	idx := int32(len(b.items))
	b.items = append(b.items, Item{
		BillingCode:            code,
		BillingCodeType:        codeType,
		BillingCodeTypeID:      b.enums[dimCodeType].id(codeType),
		BillingCodeTypeVersion: b.intern(item.BillingCodeTypeVersion),
		Name:                   strings.TrimSpace(item.Name),
		Description:            strings.TrimSpace(item.Description),
		NegotiationArrangement: b.intern(strings.ToLower(strings.TrimSpace(item.NegotiationArrangement))),
	})
	b.itemKey[k] = idx
	return idx
}

func (b *Builder) intern(s string) string {
	if s == "" {
		return ""
	}
	if got, ok := b.strings[s]; ok {
		return got
	}
	b.strings[s] = s
	return s
}

func (b *Builder) internSlice(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	k := strings.Join(in, "\x00")
	if got, ok := b.sliceCache[k]; ok {
		return got
	}
	cp := make([]string, len(in))
	for i, v := range in {
		cp[i] = b.intern(v)
	}
	b.sliceCache[k] = cp
	return cp
}

// Build resolves deferred references and returns the immutable Store.
func (b *Builder) Build() *Store {
	// Resolve rows whose provider reference arrived after them.
	unresolvedGroup := make(map[refKey]int32)
	for _, d := range b.deferred {
		if ordinals, ok := b.refIndex[d.ref]; ok && len(ordinals) > 0 {
			for _, g := range ordinals {
				row := d.row
				row.Group = g
				b.rows = append(b.rows, row)
			}
			continue
		}
		// Dangling reference. Keep the rate and say so explicitly: dropping it
		// would silently lose real pricing, and hiding it would misrepresent
		// the file.
		gi, ok := unresolvedGroup[d.ref]
		if !ok {
			gi = int32(len(b.groups))
			b.groups = append(b.groups, ProviderGroup{
				ProviderGroupID: d.ref.id,
				BusinessName:    fmt.Sprintf("Unresolved provider reference %d", d.ref.id),
				Unresolved:      true,
			})
			unresolvedGroup[d.ref] = gi
		}
		b.danglingRef[d.ref]++
		row := d.row
		row.Group = gi
		b.rows = append(b.rows, row)
	}
	b.deferred = nil

	s := &Store{
		items:         b.items,
		groups:        b.groups,
		rows:          b.rows,
		sources:       b.sources,
		byCode:        make(map[string][]int32, len(b.itemKey)),
		byNPI:         make(map[string][]int32),
		byEIN:         make(map[string][]int32),
		planToSources: make(map[string][]int16),
	}

	for i := range s.rows {
		code := s.items[s.rows[i].Item].BillingCode
		s.byCode[code] = append(s.byCode[code], int32(i))
	}

	for gi := range s.groups {
		g := &s.groups[gi]
		for _, npi := range g.NPIs {
			s.byNPI[npi] = appendUnique(s.byNPI[npi], int32(gi))
		}
		if g.EIN != "" {
			s.byEIN[g.EIN] = appendUnique(s.byEIN[g.EIN], int32(gi))
		}
	}

	for _, src := range s.sources {
		for _, p := range src.Plans {
			if p.PlanID == "" {
				continue
			}
			s.planToSources[p.PlanID] = appendUniqueI16(s.planToSources[p.PlanID], int16(src.ID))
		}
	}

	s.groupRows = make([]int, len(s.groups))
	for i := range s.rows {
		s.groupRows[s.rows[i].Group]++
		s.sources[s.rows[i].Source].Rows++
	}

	for d := range b.enums {
		s.facetDomain[d] = b.enums[d].values
	}
	// The source dimension is ordinal-indexed by source id, so its domain is
	// just the source descriptions in id order.
	srcDomain := make([]string, len(s.sources))
	for i := range s.sources {
		s.sources[i].Label = sourceLabel(&s.sources[i])
		srcDomain[i] = s.sources[i].Label
	}
	s.facetDomain[dimSource] = srcDomain

	s.codes = buildCodeSummaries(s)
	s.notes = b.buildNotes()
	return s
}

func buildCodeSummaries(s *Store) []CodeSummary {
	type agg struct {
		rows     int
		min, max float64
	}
	byKey := map[int32]*agg{}
	for i := range s.rows {
		r := &s.rows[i]
		a, ok := byKey[r.Item]
		if !ok {
			a = &agg{min: r.Rate, max: r.Rate}
			byKey[r.Item] = a
		}
		a.rows++
		if r.Rate < a.min {
			a.min = r.Rate
		}
		if r.Rate > a.max {
			a.max = r.Rate
		}
	}
	out := make([]CodeSummary, 0, len(byKey))
	for itemIdx, a := range byKey {
		it := s.items[itemIdx]
		out = append(out, CodeSummary{
			BillingCode:     it.BillingCode,
			BillingCodeType: it.BillingCodeType,
			Name:            it.Name,
			Description:     it.Description,
			Rows:            a.rows,
			MinRate:         a.min,
			MaxRate:         a.max,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].BillingCode != out[j].BillingCode {
			return out[i].BillingCode < out[j].BillingCode
		}
		return out[i].BillingCodeType < out[j].BillingCodeType
	})
	return out
}

func (b *Builder) buildNotes() []string {
	var notes []string
	if n := b.placeholder["npi_zero"]; n > 0 {
		notes = append(notes, fmt.Sprintf(
			"%d provider group NPI entries are the placeholder value 0. They are indexed as-is so the affected rows remain findable.", n))
	}
	if n := b.placeholder["ein_wrong_length"]; n > 0 {
		notes = append(notes, fmt.Sprintf(
			"%d provider group EINs are not 9 digits after normalization.", n))
	}
	if len(b.danglingRef) > 0 {
		total := 0
		for _, n := range b.danglingRef {
			total += n
		}
		notes = append(notes, fmt.Sprintf(
			"%d rate rows point at %d provider_references IDs that the file never defines. The rates are kept and the provider is reported as unresolved.",
			total, len(b.danglingRef)))
	}
	return notes
}

// sourceLabel derives a short, unique, human-readable name for a source file.
//
// It prefers a distinguishing token from the URL's file name -- the Fidelis
// files differ only by the "fidelis-ex" / "fidelis-es" segment -- and falls
// back to the reporting structure index.
func sourceLabel(src *Source) string {
	base := src.URL
	if i := strings.LastIndexAny(base, "/"); i >= 0 {
		base = base[i+1:]
	}
	base = strings.TrimSuffix(base, ".json")
	if unescaped, err := url.PathUnescape(base); err == nil {
		base = unescaped
	}
	// Keep the payer/segment token, dropping the date prefix and the generic
	// "_in-network" suffix that every file shares.
	base = strings.TrimSuffix(base, "_in-network")
	if parts := strings.Split(base, "_"); len(parts) > 1 {
		base = parts[len(parts)-1]
	}
	base = strings.TrimSpace(base)
	if base == "" {
		return fmt.Sprintf("Source %d (structure %d)", src.ID, src.StructureIndex)
	}
	return fmt.Sprintf("%s (structure %d)", base, src.StructureIndex)
}

func dedupeSorted(in []string) []string {
	if len(in) < 2 {
		return in
	}
	out := in[:1]
	for _, v := range in[1:] {
		if v != out[len(out)-1] {
			out = append(out, v)
		}
	}
	return out
}

func appendUnique(s []int32, v int32) []int32 {
	for _, x := range s {
		if x == v {
			return s
		}
	}
	return append(s, v)
}

func appendUniqueI16(s []int16, v int16) []int16 {
	for _, x := range s {
		if x == v {
			return s
		}
	}
	return append(s, v)
}
