// Response shapes mirrored from the Go API. Kept hand-written rather than
// generated: the surface is five endpoints, and a hand-written file documents
// which fields the UI actually depends on.

export interface Identifier {
  raw: string
  normalized: string
  display: string
  valid: boolean
  warnings?: string[]
}

export interface ProviderGroupView {
  provider_group_id: number
  business_name?: string
  tin_type?: string
  ein?: string
  ein_display?: string
  npi_count: number
  npis?: string[]
  npis_truncated?: boolean
  network_names?: string[]
  unresolved?: boolean
  matched_npi?: string
}

export interface SourceRef {
  id: number
  structure_index: number
  description?: string
  /** Short unique name for the file, since the index gives both Fidelis
   *  in-network files the identical description. */
  label: string
  url: string
  reporting_entity_name?: string
  reporting_entity_type?: string
  last_updated_on?: string
  version?: string
  plan_count: number
}

export interface RateResult {
  billing_code: string
  billing_code_type: string
  billing_code_type_version?: string
  name?: string
  description?: string
  negotiation_arrangement?: string

  negotiated_rate: number
  negotiated_type?: string
  billing_class?: string
  setting?: string
  service_codes?: string[]
  billing_code_modifiers?: string[]
  expiration_date?: string
  expires: boolean
  additional_information?: string

  provider_group: ProviderGroupView
  source: SourceRef
}

export interface Bucket {
  from: number
  to: number
  count: number
}

export interface Stats {
  count: number
  min: number
  max: number
  mean: number
  median: number
  p25: number
  p75: number
  histogram?: Bucket[]
}

export interface FacetCount {
  value: string
  label: string
  count: number
}

export interface Facets {
  billing_class: FacetCount[]
  setting: FacetCount[]
  billing_code_type: FacetCount[]
  negotiated_type: FacetCount[]
  source: FacetCount[]
}

export interface Pagination {
  page: number
  page_size: number
  total: number
  total_pages: number
  has_prev: boolean
  has_next: boolean
}

export interface QueryEcho {
  billing_code: string
  billing_code_type?: string
  billing_class?: string
  setting?: string
  negotiated_type?: string
  plan_id?: string
  source_id?: number
  npi?: Identifier
  ein?: Identifier
  min_rate?: number
  max_rate?: number
  sort: string
  page: number
  page_size: number
  warnings?: string[]
}

export interface CodeSummary {
  billing_code: string
  billing_code_type: string
  name: string
  description: string
  rows: number
  min_rate: number
  max_rate: number
  /** How many distinct item names the payer filed this code under. Revenue
   *  code 490 has 328, all with the same rate ladder. */
  name_count: number
}

export interface RatesResponse {
  query: QueryEcho
  pagination: Pagination
  stats: Stats
  facets: Facets
  results: RateResult[]
  suggestions?: CodeSummary[]
  took_ms: number
}

export interface ParseStats {
  items: number
  rate_groups: number
  prices: number
  provider_references: number
  provider_groups: number
  skipped_items: number
  skipped_provider_refs: number
  unknown_top_level_keys?: string[]
}

export interface ReportingPlan {
  plan_name: string
  plan_id_type: string
  plan_id: string
  plan_market_type: string
  issuer_name: string
}

export interface SourceInfo {
  id: number
  structure_index: number
  description: string
  url: string
  label: string
  reporting_entity_name: string
  reporting_entity_type: string
  last_updated_on: string
  version: string
  plans: ReportingPlan[]
  status: string
  error?: string
  bytes: number
  duration_ms: number
  parse_stats: ParseStats
  rows: number
}

export interface PlanRef {
  plan_id: string
  plan_id_type: string
  plan_name: string
  plan_market_type: string
  issuer_name: string
  source_id: number
}

export interface Counts {
  rows: number
  items: number
  distinct_codes: number
  provider_groups: number
  distinct_npis: number
  distinct_eins: number
  sources: number
}

export interface FacetDomains {
  billing_classes: string[]
  settings: string[]
  negotiated_types: string[]
  billing_code_types: string[]
  negotiation_arrangements: string[]
}

export type IngestPhase =
  | 'idle'
  | 'fetching_index'
  | 'downloading_rate_files'
  | 'parsing_rate_files'
  | 'building_index'
  | 'ready'
  | 'failed'

export interface FileProgress {
  url: string
  description: string
  status: 'pending' | 'downloading' | 'parsing' | 'ok' | 'failed'
  bytes: number
  total_bytes: number
  from_cache: boolean
  items: number
  error?: string
}

export interface IngestStatus {
  phase: IngestPhase
  message: string
  ready: boolean
  error?: string
  started_at?: string
  ready_at?: string
  elapsed_seconds: number
  index_url: string
  files: FileProgress[]
}

export interface MetaResponse {
  index_url: string
  counts: Counts
  sources: SourceInfo[]
  plans: PlanRef[]
  facet_domains: FacetDomains
  data_notes?: string[]
  top_codes?: CodeSummary[]
  ingest: IngestStatus
}

export interface ProviderHit extends ProviderGroupView {
  rows: number
}

export interface ApiErrorBody {
  error: string
  detail?: string
  hints?: string[]
}

/** Search parameters as the UI holds them: all strings, so they round-trip
 *  through the URL without coercion surprises. */
export interface SearchParams {
  code: string
  code_type: string
  npi: string
  ein: string
  billing_class: string
  setting: string
  plan_id: string
  source: string
  min_rate: string
  max_rate: string
  sort: string
  page: number
  page_size: number
}

export const emptySearchParams = (): SearchParams => ({
  code: '',
  code_type: '',
  npi: '',
  ein: '',
  billing_class: '',
  setting: '',
  plan_id: '',
  source: '',
  min_rate: '',
  max_rate: '',
  sort: 'rate_asc',
  page: 1,
  page_size: 50,
})
