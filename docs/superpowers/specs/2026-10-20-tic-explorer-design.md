# Design: Searchable Price Transparency Explorer

Date: 2026-10-20
Author: Frank Albanese
Status: Approved

## Intended outcome

A reviewer clones the repo, runs two commands, and within ~10 seconds can type a
billing code (e.g. `10006`) and see every negotiated rate Fidelis Care has filed
for it, narrowable by provider NPI or EIN, with the source file and plan metadata
attached to every row.

The audience is Serif Health's engineering team evaluating a take-home. Success
means: the CMS index file is genuinely the source of truth, ingestion is resilient
on real (dirty) data, the API is a clean contract, the UI makes 580k rate rows
explorable, and the tradeoffs are written down honestly.

## Source data (measured, not assumed)

Index: `2026-08-31_fidelis_index.json`, 30 KB, schema version 2.0.0,
reporting entity "Centene Management Company LLC" (Third Party Administrator).
Two `reporting_structure` entries, each with exactly one `in_network_files` entry
and one `allowed_amount_file`.

| | fidelis-ex | fidelis-es |
|---|---|---|
| In-network file size | 89 MB | 174 MB |
| `in_network` items | 1,519 | 2,530 |
| Unique billing codes | 802 | 1,221 |
| `negotiated_rates` groups | 54,371 | 91,742 |
| `negotiated_prices` nodes | 179,948 | 359,853 |
| `provider_references` entries | 1,355 | 1,908 |
| Provider groups (gid x TIN) | 8,030 | 6,835 |
| Unique NPIs | 43,834 | 47,129 |
| Reporting plans | 88 | 6 |

Measured characteristics that drive the design:

1. **Every** rate group uses `provider_references` (ID pointers); **zero** use
   inline `provider_groups`. Provider identity must be resolved through the
   `provider_references` block, which appears *before* `in_network` in the file.
2. Every rate group references exactly **one** provider reference. Full
   flattening of (item x rate group x price node x provider group) yields
   **581,860 rows** total -- small enough to hold in memory.
3. All `tin.type` values are `ein`; EINs are dash-formatted (`11-3471596`).
   `business_name` is present on 100% of provider groups.
4. Dirty data: NPI `0` appears in both files. `network_name` is always `[]`.
5. `billing_code` is not unique -- 4,049 items collapse to 2,023 distinct codes
   because the same code appears under different `billing_code_type` (RC/CPT/HCPCS).
6. `expiration_date` is the sentinel `9999-12-31` throughout (no expiry).
7. `negotiation_arrangement` is always `ffs`; `negotiated_type` always `negotiated`;
   `billing_class` is 98% `institutional`.
8. `additional_information` is present on 85% of fidelis-ex price nodes and
   absent from fidelis-es -- fields are genuinely optional per file.
9. Files are pretty-printed, served without gzip, support HTTP range requests,
   and download in under 6 seconds from the CDN.

## Architecture

```
index.json (30 KB)
   | IndexParser  ->  []ReportingStructure{plans[], inNetworkFiles[]}
   v
Fetcher (HTTP + on-disk cache + retry/backoff)
   | io.Reader per in-network file
   v
StreamingParser (encoding/json token decoder -- never holds the doc in RAM)
   |-- provider_references  ->  ProviderGroup{gid, npis[], ein, businessName}
   |-- in_network[]         ->  flatten item x rateGroup x price x providerGroup
   v
Store (in-memory, read-only after build)
   codeKey("10006","CPT") -> []rowID     primary index
   npi int64              -> []groupKey  inverted index
   einDigits string       -> []groupKey  inverted index
   codePrefix trie-ish    -> typeahead
   v
HTTP API  /api/rates?code=&npi=&ein=&...   (+ /meta /codes /providers /health)
   v
Vue 3 + TypeScript SPA (Vite)
```

### Component responsibilities

- `internal/tic` -- CMS schema types and parsing only. Knows nothing about HTTP
  or storage. `index.go` parses the table-of-contents; `innetwork.go` streams a
  rate file and emits normalized records via callback; `normalize.go` holds the
  NPI/EIN normalization and validation rules.
- `internal/fetch` -- fetching with disk cache, timeouts, retry. Knows nothing
  about CMS schemas.
- `internal/store` -- in-memory indexes and query execution. Pure data; no I/O.
- `internal/ingest` -- orchestration: index -> files -> parser -> store, with
  per-file error isolation and progress reporting.
- `internal/api` -- request validation, query mapping, JSON responses.

Each unit is testable with a fixture and no network.

### Normalization target

One flat `RateRow` is the unit of search. Strings that repeat heavily
(descriptions, business names, settings) are interned so 582k rows stay cheap.

```go
type RateRow struct {
    ItemID       int32   // -> ItemMeta: code, type, version, name, description
    GroupKey     int32   // -> ProviderGroup: npis, ein, business_name
    SourceID     int16   // -> SourceFile: url, plans, reporting entity
    Rate         float64
    BillingClass uint8   // interned enum
    Setting      uint8
    NegotiatedType uint8
    ServiceCodes []string
    Modifiers    []string
    ExpirationDate string
    AdditionalInfo string
}
```

### API contract

| Endpoint | Purpose |
|---|---|
| `GET /api/health` | liveness + ingest phase/progress (frontend polls during cold start) |
| `GET /api/meta` | reporting entity, source files, plans, row counts, facet domains |
| `GET /api/codes?q=&limit=` | billing-code typeahead (reviewers do not know codes by heart) |
| `GET /api/providers?npi=&ein=&q=` | resolve an identifier to business name before searching |
| `GET /api/rates?code=` | the required search; `code` mandatory, everything else optional |

`/api/rates` optional params: `code_type`, `npi`, `ein`, `billing_class`,
`setting`, `plan_id`, `source`, `min_rate`, `max_rate`, `sort`, `page`, `page_size`.

Response carries `query` (echo of normalized input + warnings), `pagination`,
`stats` (count/min/max/mean/median/p25/p75), `facets` (counts per dimension), and
`results[]` where each row embeds its resolved `provider_group` and its `source`
block for traceability.

### Error handling

- Ingest: per-file isolation. A failed or malformed file marks that source
  `failed` with a message; other sources still serve. Malformed records inside a
  file increment a `skipped` counter instead of aborting the stream.
- Fetch: 30s dial / 10m total timeout, 3 retries with backoff, cache on success.
- API: invalid input -> `400` with a specific message, never a panic. Missing
  `code` -> `400`. Unknown code -> `200` with empty results plus suggestions.
- Recover middleware converts any panic into a `500` JSON body.
- Frontend: error banner with retry, loading skeletons, explicit empty states,
  and a cold-start banner driven by `/api/health`.

### Identifier rules

- NPI: strip non-digits; must be 10 digits; validated with the Luhn-80840
  checksum. Failing checksum is a **warning**, not a rejection (real files
  contain invalid NPIs -- e.g. `0` -- and we must still be able to look them up).
- EIN: strip non-digits; must be 9 digits; accepted with or without the dash.
  Indexed on digits, displayed dash-formatted.
- NPI `0` is indexed but flagged in `/api/meta` as a data-quality note.

### Testing

Go unit tests against committed trimmed fixtures (`backend/testdata`):
index parsing, streaming parse with provider-reference resolution, NPI/EIN
normalization + checksum, store search with every filter combination,
pagination/sort/stats math, and API handler status codes for good and bad input.
No test touches the network.

## Non-goals (explicit)

No database, no auth, no allowed-amount (out-of-network) file ingestion, no
multi-payer support, no persistence of the normalized index across restarts
beyond the raw-file disk cache.

## Follow-ups with more time

Documented in README: columnar/Parquet or DuckDB-backed store, incremental
ingest keyed on `last_updated_on`, range-request partial ingest, rate
benchmarking against a reference price (what Serif actually sells), and
provider-geography joins via the NPPES registry.
