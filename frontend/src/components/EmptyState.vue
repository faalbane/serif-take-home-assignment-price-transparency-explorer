<script setup lang="ts">
import type { CodeSummary } from '../api/types'
import { count, moneyCompact, titleCase } from '../composables/useFormat'

/** Empty and error states.
 *
 *  Three different "nothing here" situations need three different answers, and
 *  conflating them is the single most common way a search UI wastes a user's
 *  time:
 *   - nothing searched yet            -> show what is searchable
 *   - searched, no rows               -> explain which filter is responsible
 *   - request rejected or unreachable -> show the server's own message
 */

defineProps<{
  variant: 'initial' | 'no-results' | 'past-end' | 'error'
  code?: string
  /** codeExists distinguishes "this code has no rates at all" from "a filter
   *  removed them". Asserting the latter without evidence told users a code
   *  existed when it did not. */
  codeExists?: boolean
  suggestions?: CodeSummary[]
  topCodes?: CodeSummary[]
  activeFilters?: { label: string; value: string }[]
  total?: number
  page?: number
  totalPages?: number
  errorMessage?: string
  errorDetail?: string
  errorHints?: string[]
}>()

const emit = defineEmits<{ pick: [CodeSummary]; clearFilters: []; retry: []; firstPage: [] }>()
</script>

<template>
  <div class="empty card" :class="`is-${variant}`">
    <template v-if="variant === 'initial'">
      <h3 class="empty-title">Search negotiated rates by billing code</h3>
      <p class="empty-body secondary">
        Enter a CPT, HCPCS or revenue code to see every in-network rate the plan
        has filed for it, then narrow by provider NPI or EIN.
      </p>
      <div v-if="topCodes?.length" class="suggest">
        <span class="suggest-label">Codes with the most rate data</span>
        <div class="suggest-grid">
          <button v-for="c in topCodes" :key="`${c.billing_code}-${c.billing_code_type}`" class="suggest-card" type="button" @click="emit('pick', c)">
            <span class="sc-code mono">{{ c.billing_code }}</span>
            <span class="badge badge-neutral sc-type">{{ c.billing_code_type }}</span>
            <span class="sc-name truncate" :title="c.description">{{ titleCase(c.name) || titleCase(c.description) }}</span>
            <span class="sc-meta muted nums">
              {{ count(c.rows) }} rates · {{ moneyCompact(c.min_rate) }}–{{ moneyCompact(c.max_rate) }}
              <template v-if="c.name_count > 1"> · filed under {{ count(c.name_count) }} names</template>
            </span>
          </button>
        </div>
      </div>
    </template>

    <template v-else-if="variant === 'no-results'">
      <h3 class="empty-title">No rates match this search</h3>
      <p class="empty-body secondary">
        <template v-if="activeFilters?.length && codeExists">
          Code <code>{{ code }}</code> has rates, but none also satisfies
          {{ activeFilters.map((f) => f.label).join(' and ') }}. Try removing a filter.
        </template>
        <template v-else-if="activeFilters?.length">
          No rate matches <code>{{ code }}</code> with
          {{ activeFilters.map((f) => f.label).join(' and ') }}, and the code itself
          does not appear in the ingested files.
        </template>
        <template v-else>
          No in-network rate was filed for <code>{{ code }}</code> in the ingested files.
        </template>
      </p>

      <div v-if="activeFilters?.length" class="active-list">
        <span v-for="f in activeFilters" :key="f.label" class="badge badge-neutral">
          {{ f.label }}: <strong>{{ f.value }}</strong>
        </span>
        <button class="btn btn-sm" type="button" @click="emit('clearFilters')">Clear filters</button>
      </div>

      <div v-if="suggestions?.length" class="suggest">
        <span class="suggest-label">Did you mean</span>
        <div class="suggest-grid">
          <button v-for="c in suggestions" :key="`${c.billing_code}-${c.billing_code_type}`" class="suggest-card" type="button" @click="emit('pick', c)">
            <span class="sc-code mono">{{ c.billing_code }}</span>
            <span class="badge badge-neutral sc-type">{{ c.billing_code_type }}</span>
            <span class="sc-name truncate" :title="c.description">{{ titleCase(c.name) || titleCase(c.description) }}</span>
            <span class="sc-meta muted nums">{{ count(c.rows) }} rates</span>
          </button>
        </div>
      </div>
    </template>

    <template v-else-if="variant === 'past-end'">
      <h3 class="empty-title">Page {{ page }} is past the end of these results</h3>
      <p class="empty-body secondary">
        Code <code>{{ code }}</code> has {{ count(total ?? 0) }} rates across
        {{ count(totalPages ?? 0) }} pages. This link points beyond the last one.
      </p>
      <button class="btn btn-primary retry" type="button" @click="emit('firstPage')">
        Go to the first page
      </button>
    </template>

    <template v-else>
      <h3 class="empty-title is-error">{{ errorMessage || 'Something went wrong' }}</h3>
      <p v-if="errorDetail" class="empty-body secondary">{{ errorDetail }}</p>
      <ul v-if="errorHints?.length" class="hints">
        <li v-for="h in errorHints" :key="h">{{ h }}</li>
      </ul>
      <button class="btn btn-primary retry" type="button" @click="emit('retry')">Try again</button>
    </template>
  </div>
</template>

<style scoped>
.empty {
  padding: 28px 24px;
  text-align: center;
}

.empty-title {
  font-size: 17px;
}

.empty-title.is-error { color: var(--critical); }

.empty-body {
  margin: 8px auto 0;
  max-width: 62ch;
  font-size: 14px;
  line-height: 1.6;
}

.empty-body code {
  padding: 1px 5px;
  background: var(--surface-2);
  border-radius: 4px;
}

.active-list {
  display: flex;
  justify-content: center;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  margin-top: 14px;
}

.suggest {
  margin-top: 22px;
  text-align: left;
}

.suggest-label {
  display: block;
  margin-bottom: 9px;
  font-size: 11px;
  font-weight: 600;
  letter-spacing: 0.04em;
  text-transform: uppercase;
  color: var(--text-muted);
  text-align: center;
}

.suggest-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(236px, 1fr));
  gap: 8px;
}

.suggest-card {
  display: grid;
  grid-template-columns: auto auto minmax(0, 1fr);
  grid-template-rows: auto auto;
  align-items: center;
  gap: 4px 8px;
  padding: 9px 11px;
  background: var(--surface-2);
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  cursor: pointer;
  text-align: left;
  transition: border-color 120ms ease, background 120ms ease;
}

.suggest-card:hover {
  background: var(--surface-3);
  border-color: var(--series-1);
}

.sc-code { font-weight: 600; font-size: 13px; }
.sc-type { font-size: 10.5px; padding: 1px 6px; }
.sc-name { font-size: 12.5px; color: var(--text-secondary); }
.sc-meta { grid-column: 1 / -1; font-size: 11px; }

.hints {
  margin: 12px auto 0;
  padding-left: 18px;
  max-width: 62ch;
  text-align: left;
  font-size: 13px;
  color: var(--text-secondary);
  line-height: 1.6;
}

.retry { margin-top: 16px; }
</style>
