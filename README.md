# Price Transparency Explorer

Search negotiated in-network rates out of CMS **Transparency in Coverage**
machine-readable files.

It starts from the Centene/Fidelis index file, follows it to the in-network rate
files it references, streams and normalizes them, and exposes a search API and a
UI where you look up a billing code and narrow by provider NPI or EIN.

**Stack:** Go 1.23 (standard library only) · Vue 3 + TypeScript + Vite

![Search results](docs/screenshots/02-search-results.jpg)

---

## Quick start

**Requirements:** Go 1.23+ and Node 20+. No other dependencies — the backend
has zero third-party Go modules.

```bash
# See it working immediately, on bundled fixtures. No download.
make demo          # ready in under a second → http://localhost:8080

# The real thing: ingests both live Fidelis files (263 MB, ~14s first run)
make run           # → http://localhost:8080

# Two dev servers with hot reload
make dev           # backend :8080, Vite :5173 → http://localhost:5173
```

Without `make`: `cd backend && go run ./cmd/server` and, separately,
`cd frontend && npm install && npm run dev`.

![Landing](docs/screenshots/01-landing.jpg)

**First real run takes ~14 seconds.** The server reads the index and ingests
both in-network files it references. The UI shows real per-file progress rather
than an indefinite spinner. Files are cached in `backend/.cache` (273 MB,
gitignored), so restarts take ~6s. `make offline` then serves cache-only.

### Flags

| Flag | Default | Purpose |
|---|---|---|
| `-addr` | `:8080` | listen address |
| `-index-url` | the Centene/Fidelis index | point at a different TiC index |
| `-cache-dir` | `.cache` | cache location; empty disables caching |
| `-static-dir` | *(none)* | serve a built frontend from here |
| `-max-items` | `0` | cap records per file (fast iteration) |
| `-max-files` | `0` | cap how many in-network files to ingest |
| `-offline` | `false` | serve only from cache |

Each has a `PTE_`-prefixed environment equivalent. `-max-items` caps *parsing*,
not downloading — use `make demo` to avoid the download entirely.

> **If the index URL has rotated.** CMS files are republished monthly. If the
> default 404s, take a current one from
> [centene.com/price-transparency-files.html](https://www.centene.com/price-transparency-files.html)
> and pass `-index-url`. The UI says this when ingest fails.

---

## What the source data actually looks like

I measured the files before designing anything. These numbers drove the design.

| | fidelis-ex | fidelis-es |
|---|---|---|
| File size | 89 MB | 174 MB |
| `in_network` items | 1,519 | 2,530 |
| Distinct billing codes | 802 | 1,221 |
| `negotiated_rates` groups | 54,371 | 91,742 |
| `negotiated_prices` nodes | 179,948 | 359,853 |
| `provider_references` entries | 1,355 | 1,908 |
| Provider groups | 8,030 | 6,835 |
| Distinct NPIs | 43,834 | 47,129 |
| Reporting plans | 88 | 6 |

Combined: **581,860 rate rows**, 1,308 distinct codes, 14,865 provider groups,
58,691 NPIs, 7,087 EINs, 94 plans. Rows per code are extremely skewed — median
**5**, p90 **106**, mean 445, max **208,332**.

What's actually interesting in here:

1. **Providers are never inline.** All 146,113 rate groups use
   `provider_references`; none use inline `provider_groups`. The rate and the
   provider identity live in different sections and must be joined by ID. (The
   parser handles inline groups anyway — the schema permits them.)
2. **Each rate group references exactly one provider reference**, which is what
   makes full flattening cheap: 581,860 rows, not millions.
3. **A billing code is not a unique key.** `490` is both a revenue code and a
   CPT code. `code_type` disambiguates.
4. **Revenue code 490 is filed under 328 different procedure names**, every one
   carrying the identical rate ladder ($3.76–$10,955.28) — 208,332 rows on one
   code. Summarizing per item name made typeahead return "490" ten times, so
   code summaries aggregate per (code, code type) and report a `name_count`.
5. **NPI `0` appears 30 times**, and `124507857` is nine digits — a leading zero
   lost to JSON number typing. 43,832 of 43,834 NPIs in fidelis-ex pass the CMS
   check digit. All reported as data-quality notes; all still searchable.
6. **EINs are dash-formatted** and indexed digits-only, so either spelling works.
7. **`expiration_date` is the sentinel `9999-12-31` throughout** — read as "does
   not expire" rather than a date in the year 9999.
8. **`setting` and `negotiated_type` have one value each** across the whole
   dataset, so those filter controls are hidden unless one is active.
9. **Both files share the description "in-network file"** in the index, so a
   source filter needs a derived label (`fidelis-ex (structure 0)`).

---

## Architecture

```
CMS index (30 KB)  ──ParseIndex──►  [{inNetworkURL, plans[]}]
                                            │
                        Fetcher: HTTP + disk cache + retry
                     (downloads concurrent, parsing sequential)
                                            │
                   StreamInNetwork — token-level json.Decoder
                            never holds the document
                                            │
          provider_references ──► ProviderGroup{gid, npis[], ein, name}
          in_network[]        ──► item × rate group × price × provider group
                                            │
                                      store.Builder
                       interning · enum ordinals · ref resolution
                                            │
                        store.Store (immutable, 581,860 rows)
              code → []rowID   ·   npi → []groupID   ·   ein → []groupID
                                            │
                              GET /api/rates?code=…
                                            │
                      Vue 3 SPA (search state lives in the URL)
```

| Package | Responsibility | Knows nothing about |
|---|---|---|
| `internal/tic` | CMS schema, index + streaming rate parsing, identifiers | HTTP, storage |
| `internal/fetch` | retrieving URLs, disk cache, retry | the CMS format |
| `internal/store` | normalized data, indexes, query execution | I/O, HTTP |
| `internal/ingest` | orchestration, progress, failure isolation | query semantics |
| `internal/api` | validation, response shaping | parsing, storage internals |

**Grain:** one row is **one negotiated price for one provider organization** —
the grain the files express once `provider_references` is resolved, and the one
a person can read.

**Threading:** the store is built once and never mutated, so reads need no
locks. Ingest runs on a background goroutine and swaps the finished store in
atomically. Clean under `-race`.

---

## API

| Endpoint | Purpose |
|---|---|
| `GET /api/health` | liveness + ingest progress; always `200` so a cold start isn't mistaken for a dead process |
| `GET /api/meta` | counts, sources with parse statistics, plans, facet domains, data notes |
| `GET /api/codes?q=` | billing-code typeahead over code, name and description |
| `GET /api/providers?npi=&ein=&q=` | resolve an identifier or name fragment |
| `GET /api/rates?code=` | **the search.** `code` required; the rest narrow |
| `GET /api/rates.csv?code=` | the matched set as CSV |

`/api/rates` also takes `code_type`, `npi`, `ein`, `billing_class`, `setting`,
`negotiated_type`, `plan_id`, `source`, `min_rate`, `max_rate`, `sort`, `page`,
`page_size`.

```bash
curl 'localhost:8080/api/rates?code=99213&code_type=CPT'
curl 'localhost:8080/api/rates?code=99213&ein=61-1661781'   # dashed EIN fine
curl 'localhost:8080/api/rates?code=99213&npi=1003076902'
```

Every row carries what's priced, the price terms, the resolved provider group,
and a `source` block naming the file, entity, schema version and
`last_updated_on` — so an exported or linked row still says where it came from.

The response also carries `stats` (count/min/max/mean/median/p25/p75 plus a
24-bucket histogram over the **whole** match, not the page), `facets`,
`pagination`, and a `query` echo showing exactly how the server read your input,
including normalized identifiers and any warnings.

Identifiers resolve to an organization as you type, so a mistyped NPI is
distinguishable from a provider who simply doesn't price that code — both
otherwise return nothing. With too few results to have a distribution, the
values are listed instead of drawing a one-bar histogram.

![Provider filter](docs/screenshots/04-provider-filter.jpg)

---

## Design decisions and tradeoffs

**Go standard library only.** `encoding/json`'s token decoder streams a 174 MB
document; Go 1.22's `net/http` router handles method-and-path patterns. A
router or JSON library would buy nothing and cost the reviewer a dependency tree.

**Stream, don't load.** Unmarshaling these files whole needs well over a
gigabyte. The parser walks top-level keys and decodes array elements one at a
time, so peak memory tracks the normalized output, not the input.

*The tradeoff inside that:* each element is read into a `json.RawMessage` before
unmarshaling into its struct — one extra copy per record, which buys per-record
error isolation. A malformed record is counted and skipped while the stream
continues; decoding straight into the struct leaves the decoder's position
unreliable after a type error.

**Flatten eagerly, index narrowly.** Rows are indexed by billing code; every
other filter is a linear scan over that code's rows. Given the skew (median 5
rows per code), almost every query is microseconds, which is what makes it
reasonable to skip an index per filterable field on a dataset rebuilt each boot.
*Where that bites:* code 490's 208,332 rows take ~70ms.

**Facets exclude their own filter.** Computed by counting how many predicates
each row fails in a single pass. Selecting "institutional" doesn't drop the
"professional" count to zero and hide the option — and the facets stay on screen
when a search returns nothing, since that's exactly when they're the way out.

**Identifier problems are warnings, not rejections.** An NPI that fails its
check digit is still *in the data*. A tool that refuses to look it up can't be
used to investigate the data-quality problem it just surfaced. Only input with
no digits at all is a `400`.

**NPI and EIN intersect.** Supplying both requires a provider group to satisfy
both; the union would return rows contradicting one of the constraints.

**Statistics are returned unrounded.** Rounding in the API bakes in a
tie-breaking rule — Go rounds halves away from zero, other runtimes round to
even — and silently disagrees with anyone recomputing from the raw files.
Formatting is the frontend's job.

**Search state lives in the URL.** Every result is shareable and reloadable, and
the back button behaves. Text inputs record their value without searching; the
query runs on submit, so typing a code doesn't issue a request per character.

### Accepted limitations

- **In-memory only**, per the brief. Restarting re-ingests; only raw files persist.
- **One payer.** The index URL is a flag, but the UI assumes a single dataset.
- **Allowed-amount files are ignored.** The index references them and the
  reference is parsed, but out-of-network amounts are a different schema and a
  different question.
- **Plan filtering is coarse.** The index files one set of rates per *reporting
  structure*, not per plan, so plans sharing a file share its rates. Filtering
  narrows to the covering files — as precise as the source allows. The UI says so.
- **CSV export caps at 50,000 rows**, reachable on code 490, so it's reported in
  the response headers *and* as a trailing row rather than truncating silently.
- **A hostile index file could make the server fetch internal URLs.** Out of
  scope: the index is operator-configured and defaults to a trusted CDN.
- **Presentational components are untested.** `App.vue`, `SearchPanel`,
  `IngestBanner` and `DatasetPanel` are covered only by the typecheck; the
  logic-bearing components and composables are tested directly.
- **No end-to-end test.** The cold-start gate and the full search flow were
  verified by hand in a browser, not by Playwright.

---

## Error handling

| Failure | Behavior |
|---|---|
| Index unreachable or unparseable | Fatal to ingest; server stays up, UI shows the error and where to get a current file |
| One rate file fails | That source marked `failed` with its reason; the others still serve |
| Malformed record in a file | Counted in `skipped_*`; stream continues; counts surfaced in the UI |
| Dangling or empty `provider_references` | The **rate is kept**, provider marked `unresolved` — dropping it would lose real pricing |
| Interrupted download | Streamed to a temp file and renamed on success, so a partial file is never mistaken for a complete one |
| Request before ingest finishes | `503` + `Retry-After`; UI polls `/api/health` |
| Invalid parameter | `400` naming the field, with a recovery hint |
| Unknown billing code | `200`, empty, **with suggestions** — a valid question with no answer isn't an error |
| Page past the end | Its own UI state with a way back, not a false "no rates filed" |
| Panic in a handler, or in ingest | Recovered into a JSON `500` / a failed-ingest status; the process survives |

![Row provenance](docs/screenshots/03-row-provenance-dark.jpg)

---

## Verification

Unit tests are written alongside the code, so they can share its assumptions.
`scripts/crosscheck.py` doesn't: it rebuilds the expected dataset in Python
**directly from the raw CMS files** and compares against the running API.

```bash
make run      # in one terminal, wait for "ready"
make verify   # in another
```

It checks dataset totals, per-code totals and all six statistics plus histogram
conservation, NPI and EIN filtering in both spellings, NPI∩EIN intersection,
`billing_class` partitioning, a full paginated walk of the largest code (208,332
rows over 417 pages, comparing summed rates), sort ordering, `/api/codes`
summaries against the per-query statistics, and the CSV export's row count
against its own truncation headers.

```
ALL 674 CROSS-CHECKS PASSED
```

This is how four real bugs were found: statistics rounding, a CSV export
truncating at 500 while claiming 50,000, and — once the verifier was pointed at
`/api/codes`, which it originally didn't cover — a `min`/`max` aggregation that
chained its comparisons with `else if`, so any code whose rows weren't in
ascending order advertised `max == min`. An audit of 400 codes found 37 wrong
before the fix and 0 after.

Ingest provenance is visible in the app itself: the index URL used, every file
with its parse statistics and skip counts, the plans covered, and the
data-quality notes recorded during ingest.

![Dataset provenance](docs/screenshots/05-dataset-provenance.jpg)

### Tests

```bash
make test        # Go tests, frontend typecheck, frontend tests
make test-race   # race detector
make mutation    # verify the suites catch the bugs they claim to
make lint        # gofmt + go vet
```

Backend: **83.4% statement coverage**, clean under `-race` and `go vet`.
Frontend: **150 tests** (Vitest + Vue Test Utils) over the composables and the
components with real logic — 64% statements, 81% branches.

No test reaches the internet. Fixtures are served from loopback `httptest` or
the local filesystem, verified by running the suite with outbound traffic
blocked. `backend/testdata/in-network.malformed.json` is a deliberately hostile
fixture covering skipped records, inline provider groups, dangling references,
non-standard keys and coerced scalars.

### Do the tests actually work?

A passing suite proves nothing by itself: a test that still passes against
broken code is not protecting anything. `scripts/mutation-check.py` reverts
each bug found during development, runs the relevant suite, and asserts it
fails — then restores every file it touched.

```
16/16 reverted bugs were caught by the test suites
```

That covers the `else if` that reported `max == min`, the CSV clamp, the
unbounded zero-stripping, duplicated provider references, the page-offset
overflow, mangled float identifiers, formula injection, `javascript:` URLs,
the search-per-keystroke regression, and the rest.

Running it the first time found three backend bugs whose tests did **not**
catch them — an ascending-order fixture that passed against the broken
min/max, a test filed in the wrong package, and formula escaping with no test
at all. Those gaps are now closed, which is the entire point of the exercise.

---

## Performance

Measured on an M-series laptop against the live files.

| | |
|---|---|
| Cold start (download 263 MB + parse + index) | **13.8 s** |
| Warm start (cached, parse + index) | **6.2 s** |
| `make demo` (bundled fixtures) | **< 1 s** |
| Resident memory, fully ingested | **~450 MB** |
| Typical query (2,262 rows) | **0.4 ms** |
| Worst case (code 490, 208,332 rows) | **~70 ms** |
| 200 requests, 20-way concurrency, worst case | p50 488 ms, p95 672 ms |

The worst case started at 202 ms; single-pass faceting over build-time enum
ordinals, pdqsort with an explicit tiebreak, and reusing the rate sort for the
statistics brought it to ~70.

---

## Project layout

```
backend/
  cmd/server/     entry point, flags, graceful shutdown
  internal/tic/   CMS schema: index parse, streaming rate parse, identifiers
  internal/fetch/ HTTP + disk cache + retry
  internal/store/ normalized data, indexes, search, facets, statistics
  internal/ingest/orchestration, progress, failure isolation
  internal/api/   handlers, validation, routing, middleware
  testdata/       trimmed real fixtures, a malformed one, a demo index
frontend/src/     typed client, composables, components
scripts/          independent verification against the raw files
docs/             design spec and screenshots
```

---

## What I would do with more time

**Storage.** Get off "rebuild everything on every boot". DuckDB or Parquet over
the flattened rows gives persistence, out-of-core queries and SQL for the
aggregate questions this data invites. The normalization step already produces
the columnar shape that wants.

**Incremental ingest.** The index carries `last_updated_on` per file. Comparing
it against the cache makes restarts near-instant and a scheduled refresh cheap.

**The 70 ms worst case.** A page of 50 out of 208,332 doesn't need a full sort —
quickselect around the requested page, then sort only that page.

**Rate benchmarking.** The useful question isn't "what is this rate" but "how
does it compare" — to Medicare, to the market median for that code and geography,
to the same provider's other contracts. That needs a reference price set joined
per code and region, and I'd want to understand the real shape of that rather
than guess.

**Provider enrichment.** Joining NPIs against NPPES gives specialty and
geography, turning "who charges this" into "what does this cost near me".

**Multi-payer.** Several indexes ingested concurrently with the same code
compared across them. The main thing the single-dataset UI assumption blocks.

**Playwright.** The composables and logic-bearing components have unit tests;
the cold-start gate and the full search flow are still verified by hand.

**Out-of-network support.** `AllowedAmountFile` is already parsed. The honest
blocker is that `store.Row` has one `Rate` and the query path assumes one row
shape, so it needs a row-type discriminator rather than nullable columns.

---

## Notes on process

I used AI assistance. What I'd point at as the substance: measuring the source
files before designing (which is where the provider-reference indirection, the
328-name revenue code and the placeholder NPIs came from), choosing the
flattening grain, the facet semantics, treating identifier problems as warnings,
and writing an independent Python implementation to cross-check the Go one —
which is what caught the bugs above, including one in a path the verifier
initially didn't cover.

The design spec written before implementation is in [`docs/design/`](docs/design/).
