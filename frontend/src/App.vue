<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { api } from './api/client'
import type { CodeSummary, SearchParams } from './api/types'
import DatasetPanel from './components/DatasetPanel.vue'
import EmptyState from './components/EmptyState.vue'
import FacetFilters from './components/FacetFilters.vue'
import IngestBanner from './components/IngestBanner.vue'
import PaginationBar from './components/PaginationBar.vue'
import RateDistribution from './components/RateDistribution.vue'
import ResultsTable from './components/ResultsTable.vue'
import SearchPanel from './components/SearchPanel.vue'
import { count, titleCase } from './composables/useFormat'
import { useIngest } from './composables/useIngest'
import { useRateSearch } from './composables/useRateSearch'
import { useTheme } from './composables/useTheme'

const { status, meta, error: ingestError, refresh } = useIngest()
const { params, response, error, loading, hasSearched, hasResults, search, update, reset, syncFromUrl } =
  useRateSearch()
const { theme, cycle: cycleTheme } = useTheme()

/** The dataset gate. Everything below needs an ingested store, so the app shows
 *  real ingest progress instead of an empty table. */
const ready = computed(() => Boolean(meta.value && status.value?.ready))

const themeLabel = computed(() =>
  theme.value === 'system' ? 'Theme: system' : theme.value === 'light' ? 'Theme: light' : 'Theme: dark',
)

const codeHeading = computed(() => {
  const r = response.value
  if (!r || r.results.length === 0) return ''
  const first = r.results[0]!
  return titleCase(first.name) || titleCase(first.description) || first.billing_code
})

/** Active filters, for the no-results explanation: the user needs to know which
 *  of their constraints is responsible. */
const activeFilters = computed(() => {
  const out: { label: string; value: string }[] = []
  const p = params.value
  if (p.npi) out.push({ label: 'NPI', value: p.npi })
  if (p.ein) out.push({ label: 'EIN', value: p.ein })
  if (p.code_type) out.push({ label: 'code system', value: p.code_type })
  if (p.billing_class) out.push({ label: 'billing class', value: p.billing_class })
  if (p.setting) out.push({ label: 'setting', value: p.setting })
  if (p.source) out.push({ label: 'source file', value: p.source })
  if (p.min_rate) out.push({ label: 'min rate', value: p.min_rate })
  if (p.max_rate) out.push({ label: 'max rate', value: p.max_rate })
  if (p.plan_id) out.push({ label: 'plan', value: p.plan_id })
  return out
})

const csvHref = computed(() => api.csvUrl(params.value))

/** The rates on the current page, for the chart's small-result-set view. */
const pageRates = computed(() => response.value?.results.map((r) => r.negotiated_rate) ?? [])

const csvWillTruncate = computed(() => (response.value?.pagination.total ?? 0) > 50000)

function pickCode(c: CodeSummary) {
  // Pin the code system too: the same number exists under several systems.
  void update({ code: c.billing_code, code_type: c.billing_code_type })
}

function clearAllFilters() {
  void update({
    npi: '',
    ein: '',
    code_type: '',
    billing_class: '',
    setting: '',
    source: '',
    min_rate: '',
    max_rate: '',
    plan_id: '',
  })
}

function onFacetUpdate(patch: Partial<SearchParams>) {
  void update(patch)
}

const liveRegion = ref('')

function announce(text: string) {
  // Reassigning to the same string would not re-announce.
  liveRegion.value = ''
  requestAnimationFrame(() => {
    liveRegion.value = text
  })
}

async function runSearch() {
  await search()
  const r = response.value
  if (error.value) announce(`Search failed: ${error.value.message}`)
  else if (r) announce(`${count(r.pagination.total)} rates found for code ${r.query.billing_code}.`)
}

const onPopState = () => void syncFromUrl()

onMounted(() => {
  window.addEventListener('popstate', onPopState)
  // A shared or reloaded URL already carries a code; run it.
  if (params.value.code.trim()) {
    const waitForData = setInterval(() => {
      if (ready.value) {
        clearInterval(waitForData)
        void runSearch()
      }
    }, 250)
    onUnmounted(() => clearInterval(waitForData))
  }
})

onUnmounted(() => window.removeEventListener('popstate', onPopState))
</script>

<template>
  <div class="app">
    <header class="header">
      <div class="header-inner">
        <div class="brand">
          <h1 class="brand-title">Price Transparency Explorer</h1>
          <p class="brand-sub muted">
            Negotiated in-network rates from CMS Transparency in Coverage files
          </p>
        </div>

        <div class="header-meta">
          <span v-if="meta" class="badge badge-neutral nums entity-badge" :title="meta.index_url">
            {{ meta.sources[0]?.reporting_entity_name ?? 'Unknown payer' }}
          </span>
          <button class="btn btn-sm btn-ghost" type="button" :aria-label="themeLabel" :title="themeLabel" @click="cycleTheme">
            <span aria-hidden="true">{{ theme === 'system' ? '◐' : theme === 'light' ? '☀' : '☾' }}</span>
          </button>
        </div>
      </div>
    </header>

    <main class="main">
      <IngestBanner v-if="!ready" :status="status" :error="ingestError" @retry="refresh" />

      <template v-else>
        <SearchPanel
          :params="params"
          :domains="meta!.facet_domains"
          :plans="meta!.plans"
          :loading="loading"
          @update="onFacetUpdate"
          @submit="runSearch"
          @reset="reset"
        />

        <p class="visually-hidden" role="status" aria-live="polite">{{ liveRegion }}</p>

        <!-- Error from the request itself -->
        <EmptyState
          v-if="error"
          variant="error"
          :error-message="error.message"
          :error-detail="error.detail"
          :error-hints="error.hints"
          @retry="runSearch"
        />

        <!-- Nothing searched yet -->
        <EmptyState
          v-else-if="!hasSearched"
          variant="initial"
          :top-codes="meta!.top_codes ?? []"
          @pick="pickCode"
        />

        <!-- Searched, no rows -->
        <EmptyState
          v-else-if="!hasResults"
          variant="no-results"
          :code="response?.query.billing_code"
          :suggestions="response?.suggestions ?? []"
          :active-filters="activeFilters"
          @pick="pickCode"
          @clear-filters="clearAllFilters"
        />

        <!-- Results -->
        <template v-else>
          <div class="result-head">
            <div class="grow">
              <h2 class="result-title">
                <code class="mono result-code">{{ response!.query.billing_code }}</code>
                <span class="result-name">{{ codeHeading }}</span>
              </h2>
              <p class="result-sub muted nums">
                {{ count(response!.pagination.total) }} negotiated rate{{ response!.pagination.total === 1 ? '' : 's' }}
                across {{ count(response!.facets.source.length) }} source file{{ response!.facets.source.length === 1 ? '' : 's' }}
                · server {{ response!.took_ms.toFixed(response!.took_ms < 1 ? 3 : 1) }} ms
              </p>
            </div>
            <div class="result-actions">
              <a class="btn btn-sm" :href="csvHref" :download="`rates_${response!.query.billing_code}.csv`">
                Export CSV
              </a>
            </div>
          </div>

          <p v-if="csvWillTruncate" class="truncate-warn badge badge-warn">
            A CSV export of this search is capped at 50,000 rows; narrow the search to export everything.
          </p>

          <ul v-if="response!.query.warnings?.length" class="warnings">
            <li v-for="w in response!.query.warnings" :key="w" class="badge badge-warn">{{ w }}</li>
          </ul>

          <RateDistribution :stats="response!.stats" :page-rates="pageRates" />

          <FacetFilters :facets="response!.facets" :params="params" @update="onFacetUpdate" />

          <ResultsTable
            :results="response!.results"
            :stats="response!.stats"
            :sort="params.sort"
            :loading="loading"
            @sort="onFacetUpdate({ sort: $event })"
            @filter-ein="onFacetUpdate({ ein: $event, npi: '' })"
            @filter-npi="onFacetUpdate({ npi: $event, ein: '' })"
          />

          <PaginationBar
            :pagination="response!.pagination"
            :loading="loading"
            @page="onFacetUpdate({ page: $event })"
            @page-size="onFacetUpdate({ page_size: $event })"
          />
        </template>

        <DatasetPanel :meta="meta!" />
      </template>
    </main>

    <footer class="footer">
      <p class="muted">
        Data filed by the payer under the CMS Transparency in Coverage rule.
        Rates are what the plan reported, not a quote or a bill.
        <a href="https://github.com/CMSgov/price-transparency-guide" target="_blank" rel="noopener noreferrer">Schema reference</a>
      </p>
    </footer>
  </div>
</template>

<style scoped>
.app {
  min-height: 100vh;
  display: flex;
  flex-direction: column;
}

.header {
  position: sticky;
  top: 0;
  z-index: 30;
  background: color-mix(in srgb, var(--surface-1) 88%, transparent);
  backdrop-filter: blur(8px);
  border-bottom: 1px solid var(--border);
}

.header-inner,
.main,
.footer > p {
  width: 100%;
  max-width: var(--max-width);
  margin: 0 auto;
  padding: 0 16px;
}

.header-inner {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  min-height: 60px;
}

.brand-title {
  font-size: 16px;
  letter-spacing: -0.015em;
}

.brand-sub {
  margin: 1px 0 0;
  font-size: 12.5px;
}

.header-meta {
  display: flex;
  align-items: center;
  gap: 8px;
}

.entity-badge {
  max-width: 260px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.main {
  flex: 1;
  display: flex;
  flex-direction: column;
  gap: 14px;
  padding-top: 18px;
  padding-bottom: 36px;
}

.result-head {
  display: flex;
  align-items: flex-end;
  gap: 16px;
  flex-wrap: wrap;
  margin-top: 2px;
}

.result-title {
  display: flex;
  align-items: baseline;
  gap: 10px;
  flex-wrap: wrap;
  font-size: 19px;
}

.result-code {
  padding: 2px 8px;
  background: var(--surface-2);
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  font-size: 16px;
}

.result-name {
  color: var(--text-secondary);
  font-weight: 500;
  font-size: 16px;
}

.result-sub {
  margin: 3px 0 0;
  font-size: 13px;
}

.result-actions { display: flex; gap: 8px; }

.truncate-warn { align-self: flex-start; }

.warnings {
  display: flex;
  gap: 7px;
  flex-wrap: wrap;
  margin: 0;
  padding: 0;
  list-style: none;
}

.footer {
  border-top: 1px solid var(--border);
  padding: 16px 0;
}

.footer p {
  margin: 0;
  font-size: 12.5px;
  line-height: 1.6;
}

@media (max-width: 640px) {
  .brand-sub { display: none; }
  .entity-badge { max-width: 140px; }
  .result-title { font-size: 17px; }
}
</style>
