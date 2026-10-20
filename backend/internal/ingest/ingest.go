// Package ingest orchestrates the pipeline: read the index, fetch each
// in-network file it references, stream-parse them, and publish an immutable
// store.
//
// Ingest runs in the background so the HTTP server is listening immediately.
// The API reports progress through Service.Status while the first build is in
// flight, which is what lets the UI show a real cold-start banner instead of a
// spinner of unknown duration.
package ingest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/faalbane/price-transparency-explorer/internal/fetch"
	"github.com/faalbane/price-transparency-explorer/internal/store"
	"github.com/faalbane/price-transparency-explorer/internal/tic"
)

// Phase is a coarse ingest state, suitable for a status banner.
type Phase string

// Ingest phases, in order.
const (
	PhaseIdle        Phase = "idle"
	PhaseFetchIndex  Phase = "fetching_index"
	PhaseDownloading Phase = "downloading_rate_files"
	PhaseParsing     Phase = "parsing_rate_files"
	PhaseBuilding    Phase = "building_index"
	PhaseReady       Phase = "ready"
	PhaseFailed      Phase = "failed"
)

// FileProgress is per-file ingest progress.
type FileProgress struct {
	URL         string `json:"url"`
	Description string `json:"description"`
	Status      string `json:"status"` // pending | downloading | parsing | ok | failed
	Bytes       int64  `json:"bytes"`
	TotalBytes  int64  `json:"total_bytes"`
	FromCache   bool   `json:"from_cache"`
	Items       int    `json:"items"`
	Error       string `json:"error,omitempty"`
}

// Status is a snapshot of ingest state.
type Status struct {
	Phase     Phase          `json:"phase"`
	Message   string         `json:"message"`
	Ready     bool           `json:"ready"`
	Error     string         `json:"error,omitempty"`
	StartedAt *time.Time     `json:"started_at,omitempty"`
	ReadyAt   *time.Time     `json:"ready_at,omitempty"`
	Elapsed   float64        `json:"elapsed_seconds"`
	IndexURL  string         `json:"index_url"`
	Files     []FileProgress `json:"files"`
}

// Config configures an ingest run.
type Config struct {
	IndexURL string
	Fetcher  *fetch.Fetcher
	Logf     func(format string, args ...any)

	// MaxItemsPerFile caps in_network records per file. Zero means no cap. It
	// exists so local iteration and tests do not pay for a full ingest.
	MaxItemsPerFile int

	// MaxFiles caps how many in-network files are ingested. Zero means all.
	MaxFiles int

	// Offline serves only what is already in the disk cache.
	Offline bool
}

// Service owns the current store and the ingest status.
//
// The store is swapped atomically, so readers never see a partially built
// dataset and never block behind ingest.
type Service struct {
	cfg Config

	current atomic.Pointer[store.Store]

	mu     sync.RWMutex
	status Status
}

// NewService returns a Service that has not ingested anything yet.
func NewService(cfg Config) *Service {
	if cfg.Logf == nil {
		cfg.Logf = func(string, ...any) {}
	}
	return &Service{
		cfg:    cfg,
		status: Status{Phase: PhaseIdle, IndexURL: cfg.IndexURL, Message: "Ingest has not started yet."},
	}
}

// Store returns the current dataset, or nil if the first ingest has not
// completed.
func (s *Service) Store() *store.Store { return s.current.Load() }

// Status returns a snapshot of ingest state. The returned slice is a copy, so
// callers can serialize it without holding a lock.
func (s *Service) Status() Status {
	s.mu.RLock()
	defer s.mu.RUnlock()
	st := s.status
	st.Files = append([]FileProgress(nil), s.status.Files...)
	if st.StartedAt != nil {
		end := time.Now()
		if st.ReadyAt != nil {
			end = *st.ReadyAt
		}
		st.Elapsed = end.Sub(*st.StartedAt).Seconds()
	}
	return st
}

func (s *Service) setPhase(p Phase, msg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.Phase = p
	s.status.Message = msg
}

func (s *Service) updateFile(i int, fn func(*FileProgress)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if i >= 0 && i < len(s.status.Files) {
		fn(&s.status.Files[i])
	}
}

// Run performs one ingest and publishes the result.
//
// A failure to read the index is fatal to the run, because without it there is
// nothing to ingest. A failure on an individual rate file is not: that source
// is marked failed and the remaining sources still produce a usable dataset.
func (s *Service) Run(ctx context.Context) error {
	started := time.Now()
	s.mu.Lock()
	s.status = Status{
		Phase:     PhaseFetchIndex,
		Message:   "Reading the Transparency in Coverage index file.",
		IndexURL:  s.cfg.IndexURL,
		StartedAt: &started,
	}
	s.mu.Unlock()

	idx, err := s.loadIndex(ctx)
	if err != nil {
		s.fail(fmt.Sprintf("Could not read the index file: %v", err))
		return err
	}

	targets := idx.InNetworkTargets()
	if s.cfg.MaxFiles > 0 && len(targets) > s.cfg.MaxFiles {
		targets = targets[:s.cfg.MaxFiles]
	}
	if len(targets) == 0 {
		err := errors.New("the index contains no in-network file references")
		s.fail(err.Error())
		return err
	}

	s.cfg.Logf("index: %s v%s, %d reporting structures, %d in-network files",
		idx.ReportingEntityName, idx.Version, len(idx.ReportingStructure), len(targets))

	progress := make([]FileProgress, len(targets))
	for i, t := range targets {
		progress[i] = FileProgress{URL: t.Location, Description: t.Description, Status: "pending"}
	}
	s.mu.Lock()
	s.status.Files = progress
	s.mu.Unlock()

	// Phase 1: warm the cache for every file concurrently. Downloads are
	// network-bound and independent, so there is no reason to serialize them.
	s.setPhase(PhaseDownloading, fmt.Sprintf("Downloading %d in-network rate file(s).", len(targets)))
	bodies := s.downloadAll(ctx, targets)

	// Phase 2: parse sequentially into one builder. The builder is not
	// concurrency-safe by design -- it owns the interning tables -- and parsing
	// is CPU-bound against an already-warm cache.
	s.setPhase(PhaseParsing, "Parsing rate files and normalizing records.")
	b := store.NewBuilder()

	for i, t := range targets {
		src := store.Source{
			StructureIndex:      t.StructureIndex,
			Description:         t.Description,
			URL:                 t.Location,
			ReportingEntityName: idx.ReportingEntityName,
			ReportingEntityType: idx.ReportingEntityType,
			LastUpdatedOn:       idx.LastUpdatedOn,
			Version:             idx.Version,
			Plans:               t.Plans,
		}
		sid := b.AddSource(src)

		body := bodies[i]
		if body.err != nil {
			msg := body.err.Error()
			s.cfg.Logf("source %d failed: %v", i, body.err)
			s.updateFile(i, func(p *FileProgress) { p.Status = "failed"; p.Error = msg })
			b.UpdateSource(sid, func(sv *store.Source) { sv.Status = "failed"; sv.Error = msg })
			continue
		}

		s.updateFile(i, func(p *FileProgress) { p.Status = "parsing" })
		fileStart := time.Now()
		stats, meta, err := s.parseOne(ctx, i, sid, b, body)
		body.res.Body.Close()

		if err != nil {
			msg := err.Error()
			s.cfg.Logf("source %d parse failed: %v", i, err)
			s.updateFile(i, func(p *FileProgress) { p.Status = "failed"; p.Error = msg })
			b.UpdateSource(sid, func(sv *store.Source) {
				sv.Status = "failed"
				sv.Error = msg
				sv.ParseStats = stats
			})
			continue
		}

		dur := time.Since(fileStart)
		s.cfg.Logf("source %d parsed: %d items, %d prices, %d provider groups in %s (%d skipped)",
			i, stats.Items, stats.Prices, stats.ProviderGroups, dur.Round(time.Millisecond), stats.SkippedItems)
		s.updateFile(i, func(p *FileProgress) { p.Status = "ok"; p.Items = stats.Items })
		b.UpdateSource(sid, func(sv *store.Source) {
			sv.Status = "ok"
			sv.ParseStats = stats
			sv.Bytes = body.res.Bytes
			sv.DurationMS = dur.Milliseconds()
			// The rate file's own header wins over the index for entity
			// metadata, since it is the document the rates actually came from.
			if meta.ReportingEntityName != "" {
				sv.ReportingEntityName = meta.ReportingEntityName
			}
			if meta.ReportingEntityType != "" {
				sv.ReportingEntityType = meta.ReportingEntityType
			}
			if meta.LastUpdatedOn != "" {
				sv.LastUpdatedOn = meta.LastUpdatedOn
			}
			if meta.Version != "" {
				sv.Version = meta.Version
			}
		})
	}

	s.setPhase(PhaseBuilding, "Building search indexes.")
	st := b.Build()

	anyOK := false
	for _, src := range st.Sources() {
		if src.Status == "ok" {
			anyOK = true
		}
	}
	if !anyOK {
		err := errors.New("every in-network file failed to ingest")
		s.fail(err.Error())
		return err
	}

	s.current.Store(st)
	readyAt := time.Now()
	s.mu.Lock()
	s.status.Phase = PhaseReady
	s.status.Ready = true
	s.status.ReadyAt = &readyAt
	c := st.Counts()
	s.status.Message = fmt.Sprintf("Ready: %s rate rows across %d billing codes from %d file(s).",
		commas(c.Rows), c.DistinctCodes, c.Sources)
	s.mu.Unlock()

	s.cfg.Logf("ingest complete in %s: %+v", readyAt.Sub(started).Round(time.Millisecond), c)
	for _, n := range st.Notes() {
		s.cfg.Logf("data-quality note: %s", n)
	}
	return nil
}

func (s *Service) fail(msg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.Phase = PhaseFailed
	s.status.Ready = s.current.Load() != nil
	s.status.Error = msg
	s.status.Message = msg
}

func (s *Service) loadIndex(ctx context.Context) (*tic.Index, error) {
	var (
		res *fetch.Result
		err error
	)
	if s.cfg.Offline {
		res, err = s.cfg.Fetcher.OpenCachedOnly(s.cfg.IndexURL)
	} else {
		res, err = s.cfg.Fetcher.Open(ctx, s.cfg.IndexURL)
	}
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	return tic.ParseIndex(res.Body)
}

type fetched struct {
	res *fetch.Result
	err error
}

func (s *Service) downloadAll(ctx context.Context, targets []tic.InNetworkTarget) []fetched {
	out := make([]fetched, len(targets))
	var wg sync.WaitGroup
	for i, t := range targets {
		wg.Add(1)
		go func(i int, loc string) {
			defer wg.Done()
			s.updateFile(i, func(p *FileProgress) { p.Status = "downloading" })

			var (
				res *fetch.Result
				err error
			)
			if s.cfg.Offline {
				res, err = s.cfg.Fetcher.OpenCachedOnly(loc)
			} else {
				res, err = s.cfg.Fetcher.Open(ctx, loc)
			}
			out[i] = fetched{res: res, err: err}
			if err == nil {
				s.updateFile(i, func(p *FileProgress) {
					p.TotalBytes = res.Bytes
					p.FromCache = res.FromCache
					p.Status = "pending"
				})
			}
		}(i, t.Location)
	}
	wg.Wait()
	return out
}

// parseOne streams a single rate file into the builder.
func (s *Service) parseOne(ctx context.Context, fileIdx int, sid int16, b *store.Builder, f fetched) (tic.ParseStats, tic.FileMeta, error) {
	counter := &countingReader{r: f.res.Body}

	// Report byte progress without making the hot path pay for it.
	done := make(chan struct{})
	defer close(done)
	go func() {
		t := time.NewTicker(250 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				n := counter.count()
				s.updateFile(fileIdx, func(p *FileProgress) { p.Bytes = n })
			}
		}
	}()

	items := 0
	meta, stats, err := tic.StreamInNetwork(counter, tic.StreamHandler{
		ProviderRef: func(r tic.ProviderReference) error {
			b.AddProviderReference(sid, r)
			return nil
		},
		Item: func(item *tic.InNetworkItem) error {
			if err := ctx.Err(); err != nil {
				return tic.Abort(err)
			}
			if s.cfg.MaxItemsPerFile > 0 && items >= s.cfg.MaxItemsPerFile {
				return tic.Abort(errItemLimit)
			}
			items++
			b.AddItem(sid, item)
			return nil
		},
	})

	if errors.Is(err, errItemLimit) {
		s.cfg.Logf("source %d: stopped after the configured item limit of %d", fileIdx, s.cfg.MaxItemsPerFile)
		err = nil
	}
	s.updateFile(fileIdx, func(p *FileProgress) { p.Bytes = counter.count() })
	return stats, meta, err
}

var errItemLimit = errors.New("item limit reached")

type countingReader struct {
	r io.Reader
	n atomic.Int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	if n > 0 {
		c.n.Add(int64(n))
	}
	return n, err
}

func (c *countingReader) count() int64 { return c.n.Load() }

func commas(n int) string {
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	var out []byte
	for i, ch := range []byte(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, ch)
	}
	return string(out)
}
