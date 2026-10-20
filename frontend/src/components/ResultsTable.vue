<script setup lang="ts">
import { ref, watch } from 'vue'
import type { RateResult, Stats } from '../api/types'
import { count, formatNPI, money, prettyUrl, safeUrl, titleCase } from '../composables/useFormat'

/** The result table.
 *
 *  One row is one negotiated price for one provider organisation, which is the
 *  grain the source files actually express once provider_references are
 *  resolved. Each row expands to show its full provenance: the NPIs under the
 *  tax ID, the service codes and modifiers, and the source file it was filed
 *  in. */

const props = defineProps<{
  results: RateResult[]
  stats: Stats
  sort: string
  loading: boolean
  matchedNpi?: string
}>()

const emit = defineEmits<{ sort: [string]; filterEin: [string]; filterNpi: [string] }>()

// Expansion is keyed by row identity, not array index. Keying by index meant
// expanding row 3 and paging forward left row 3 of the NEXT page expanded,
// showing a different provider's provenance under a different rate.
const expanded = ref<Set<string>>(new Set())
const copied = ref<string | null>(null)

const rowKey = (row: RateResult, i: number) =>
  `${row.source.id}:${row.provider_group.provider_group_id}:${row.negotiated_rate}:${i}`

watch(
  () => props.results,
  () => {
    expanded.value = new Set()
    copied.value = null
  },
)

function toggle(key: string) {
  const next = new Set(expanded.value)
  if (next.has(key)) next.delete(key)
  else next.add(key)
  expanded.value = next
}

/** relativeToMedian positions a rate against the median of the whole result
 *  set, which is the comparison that makes a single number meaningful. */
function relativeToMedian(rate: number): { pct: number; label: string; tone: string } {
  const median = props.stats.median
  if (!median || !Number.isFinite(median) || median === 0) {
    return { pct: 0, label: '', tone: 'neutral' }
  }
  const pct = ((rate - median) / median) * 100
  if (Math.abs(pct) < 0.5) return { pct, label: 'at median', tone: 'neutral' }
  const sign = pct > 0 ? '+' : '−'
  return {
    pct,
    label: `${sign}${Math.abs(pct).toFixed(0)}% vs median`,
    tone: pct > 0 ? 'above' : 'below',
  }
}

const SORTS = [
  { value: 'rate_asc', label: 'Rate, low to high' },
  { value: 'rate_desc', label: 'Rate, high to low' },
  { value: 'provider', label: 'Provider name' },
  { value: 'npi_count', label: 'Group size' },
]

function cycleRateSort() {
  emit('sort', props.sort === 'rate_asc' ? 'rate_desc' : 'rate_asc')
}

async function copyRow(row: RateResult, key: string) {
  try {
    await navigator.clipboard.writeText(JSON.stringify(row, null, 2))
    copied.value = key
    setTimeout(() => {
      if (copied.value === key) copied.value = null
    }, 1600)
  } catch {
    // Clipboard access can be denied; the expanded panel already shows the data.
  }
}
</script>

<template>
  <div class="results">
    <div class="results-bar">
      <div class="field sort-field">
        <label for="sort-select">Sort</label>
        <select id="sort-select" class="select" :value="sort" @change="emit('sort', ($event.target as HTMLSelectElement).value)">
          <option v-for="s in SORTS" :key="s.value" :value="s.value">{{ s.label }}</option>
        </select>
      </div>
    </div>

    <div class="table-scroll" :class="{ 'is-loading': loading }">
      <table class="table">
        <caption class="visually-hidden">
          Negotiated in-network rates, one row per provider organization
        </caption>
        <thead>
          <tr>
            <th
              scope="col"
              class="col-rate"
              :aria-sort="sort === 'rate_asc' ? 'ascending' : sort === 'rate_desc' ? 'descending' : 'none'"
            >
              <button class="th-sort" type="button" @click="cycleRateSort">
                Rate
                <span class="sort-arrow" aria-hidden="true">{{ sort === 'rate_desc' ? '↓' : sort === 'rate_asc' ? '↑' : '' }}</span>
                <span class="visually-hidden">
                  {{ sort === 'rate_asc' ? 'sorted ascending' : sort === 'rate_desc' ? 'sorted descending' : '' }}
                </span>
              </button>
            </th>
            <th scope="col" class="col-provider">Provider organization</th>
            <th scope="col" class="col-ein">EIN</th>
            <th scope="col" class="col-npis">NPIs</th>
            <th scope="col" class="col-class">Class / setting</th>
            <th scope="col" class="col-source">Source</th>
            <th scope="col" class="col-actions"><span class="visually-hidden">Row actions</span></th>
          </tr>
        </thead>
        <tbody>
          <template v-for="(row, i) in results" :key="rowKey(row, i)">
            <tr
              class="rate-row"
              :class="{ 'is-expanded': expanded.has(rowKey(row, i)) }"
              @click="toggle(rowKey(row, i))"
            >
              <td class="col-rate">
                <div class="rate nums">{{ money(row.negotiated_rate) }}</div>
                <div
                  v-if="relativeToMedian(row.negotiated_rate).label"
                  class="rate-delta"
                  :class="`is-${relativeToMedian(row.negotiated_rate).tone}`"
                >
                  {{ relativeToMedian(row.negotiated_rate).label }}
                </div>
              </td>

              <td class="col-provider">
                <div class="provider-name truncate" :title="row.provider_group.business_name">
                  {{ row.provider_group.business_name || '—' }}
                </div>
                <div v-if="row.provider_group.unresolved" class="badge badge-warn unresolved-badge">
                  unresolved reference {{ row.provider_group.provider_group_id }}
                </div>
                <div v-else-if="row.provider_group.network_names?.length" class="muted network truncate">
                  {{ row.provider_group.network_names.join(', ') }}
                </div>
              </td>

              <td class="col-ein">
                <button
                  v-if="row.provider_group.ein"
                  class="link-btn mono"
                  type="button"
                  :title="`Filter to EIN ${row.provider_group.ein_display}`"
                  @click.stop="emit('filterEin', row.provider_group.ein!)"
                >
                  {{ row.provider_group.ein_display }}
                </button>
                <span v-else class="muted">—</span>
              </td>

              <td class="col-npis nums">
                <span v-if="row.provider_group.matched_npi" class="badge badge-good matched-npi mono">
                  {{ formatNPI(row.provider_group.matched_npi) }}
                </span>
                <span v-else-if="row.provider_group.npi_count === 1 && row.provider_group.npis?.length" class="mono single-npi">
                  <button class="link-btn mono" type="button" @click.stop="emit('filterNpi', row.provider_group.npis![0]!)">
                    {{ formatNPI(row.provider_group.npis![0]!) }}
                  </button>
                </span>
                <span v-else class="muted npi-count">
                  {{ count(row.provider_group.npi_count) }} NPIs
                </span>
              </td>

              <td class="col-class">
                <span class="secondary">{{ row.billing_class || '—' }}</span>
                <span v-if="row.setting" class="muted setting"> · {{ row.setting }}</span>
              </td>

              <td class="col-source">
                <span class="badge badge-neutral src-badge" :title="row.source.url">
                  {{ row.source.label || row.source.description || `file ${row.source.id}` }}
                </span>
                <span class="muted src-date">{{ row.source.last_updated_on }}</span>
              </td>

              <td class="col-actions">
                <!-- A real button rather than a focusable <tr>. The row's own
                     keydown handlers also fired when Enter or Space was
                     pressed on the EIN and NPI buttons nested inside it, both
                     filtering AND toggling the row. -->
                <button
                  class="expand-btn"
                  type="button"
                  :aria-expanded="expanded.has(rowKey(row, i))"
                  :aria-controls="`detail-${rowKey(row, i)}`"
                  @click.stop="toggle(rowKey(row, i))"
                >
                  <span aria-hidden="true">{{ expanded.has(rowKey(row, i)) ? '▴' : '▾' }}</span>
                  <span class="visually-hidden">
                    Details for {{ money(row.negotiated_rate) }},
                    {{ row.provider_group.business_name || 'unnamed provider group' }}
                  </span>
                </button>
              </td>
            </tr>

            <tr v-if="expanded.has(rowKey(row, i))" class="detail-row">
              <td colspan="7" :id="`detail-${rowKey(row, i)}`">
                <div class="detail">
                  <section class="detail-sec">
                    <h4>What is priced</h4>
                    <dl>
                      <div><dt>Billing code</dt><dd class="mono">{{ row.billing_code }} <span class="muted">({{ [row.billing_code_type, row.billing_code_type_version].filter(Boolean).join(' ') }})</span></dd></div>
                      <div><dt>Short name</dt><dd :title="row.name">{{ titleCase(row.name) || '—' }}</dd></div>
                      <div><dt>Description</dt><dd :title="row.description">{{ titleCase(row.description) || '—' }}</dd></div>
                      <div><dt>Arrangement</dt><dd>{{ row.negotiation_arrangement || '—' }}</dd></div>
                    </dl>
                  </section>

                  <section class="detail-sec">
                    <h4>Price terms</h4>
                    <dl>
                      <div><dt>Negotiated rate</dt><dd class="mono">{{ money(row.negotiated_rate) }}</dd></div>
                      <div><dt>Negotiated type</dt><dd>{{ row.negotiated_type || '—' }}</dd></div>
                      <div><dt>Service codes</dt><dd class="mono">{{ row.service_codes?.join(', ') || '—' }}</dd></div>
                      <div><dt>Modifiers</dt><dd class="mono">{{ row.billing_code_modifiers?.join(', ') || 'none' }}</dd></div>
                      <div>
                        <dt>Expires</dt>
                        <dd>
                          <template v-if="row.expires">{{ row.expiration_date }}</template>
                          <template v-else-if="row.expiration_date">
                            no <span class="muted">(sentinel {{ row.expiration_date }})</span>
                          </template>
                          <template v-else>not reported</template>
                        </dd>
                      </div>
                      <div v-if="row.additional_information">
                        <dt>Additional info</dt><dd class="addl">{{ row.additional_information }}</dd>
                      </div>
                    </dl>
                  </section>

                  <section class="detail-sec">
                    <h4>
                      Provider group
                      <span class="muted">· {{ count(row.provider_group.npi_count) }} NPI{{ row.provider_group.npi_count === 1 ? '' : 's' }}</span>
                    </h4>
                    <dl>
                      <div><dt>Group id</dt><dd class="mono">{{ row.provider_group.provider_group_id }}</dd></div>
                      <div><dt>Tax id</dt><dd class="mono">{{ row.provider_group.ein_display || '—' }} <span class="muted">({{ row.provider_group.tin_type || 'unknown' }})</span></dd></div>
                    </dl>
                    <div v-if="row.provider_group.npis?.length" class="npi-list">
                      <button
                        v-for="npi in row.provider_group.npis"
                        :key="npi"
                        class="chip npi-chip mono"
                        :class="{ 'is-active': npi === row.provider_group.matched_npi }"
                        type="button"
                        @click.stop="emit('filterNpi', npi)"
                      >
                        {{ formatNPI(npi) }}
                      </button>
                      <span v-if="row.provider_group.npis_truncated" class="muted more-npis">
                        + {{ count(row.provider_group.npi_count - row.provider_group.npis.length) }} more not shown
                      </span>
                    </div>
                  </section>

                  <section class="detail-sec">
                    <h4>Source traceability</h4>
                    <dl>
                      <div><dt>Reporting entity</dt><dd>{{ row.source.reporting_entity_name }} <span class="muted">({{ row.source.reporting_entity_type }})</span></dd></div>
                      <div><dt>Last updated</dt><dd class="mono">{{ row.source.last_updated_on }}</dd></div>
                      <div><dt>Schema version</dt><dd class="mono">{{ row.source.version }}</dd></div>
                      <div><dt>File label</dt><dd class="mono">{{ row.source.label }}</dd></div>
                      <div><dt>Index structure</dt><dd class="mono">#{{ row.source.structure_index }} <span class="muted">· {{ count(row.source.plan_count) }} plans</span></dd></div>
                      <div>
                        <dt>File</dt>
                        <dd class="file-link">
                          <a
                            v-if="safeUrl(row.source.url)"
                            :href="safeUrl(row.source.url)"
                            target="_blank"
                            rel="noopener noreferrer"
                          >
                            {{ prettyUrl(row.source.url.split('/').pop() || row.source.url) }}
                          </a>
                          <span v-else class="mono muted">{{ row.source.url }}</span>
                        </dd>
                      </div>
                    </dl>
                    <button class="btn btn-sm copy-btn" type="button" @click.stop="copyRow(row, rowKey(row, i))">
                      {{ copied === rowKey(row, i) ? 'Copied' : 'Copy row as JSON' }}
                    </button>
                  </section>
                </div>
              </td>
            </tr>
          </template>
        </tbody>
      </table>
    </div>
  </div>
</template>

<style scoped>
.results-bar {
  display: flex;
  justify-content: flex-end;
  margin-bottom: 8px;
}

.sort-field {
  max-width: 190px;
}

.table-scroll {
  overflow-x: auto;
  background: var(--surface-1);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  transition: opacity 140ms ease;
}

.table-scroll.is-loading {
  opacity: 0.55;
}

.table {
  width: 100%;
  min-width: 940px;
  /* Fixed layout keeps column widths deterministic across pages, so the table
   * does not reflow every time a page of results with longer provider names
   * arrives. The provider column takes whatever is left over. */
  table-layout: fixed;
  border-collapse: collapse;
  font-size: 14px;
}

thead th {
  position: sticky;
  top: 0;
  z-index: 2;
  background: var(--surface-2);
  border-bottom: 1px solid var(--border);
  padding: 9px 12px;
  text-align: left;
  font-size: 11.5px;
  font-weight: 600;
  letter-spacing: 0.03em;
  text-transform: uppercase;
  color: var(--text-muted);
  white-space: nowrap;
}

.th-sort {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  background: none;
  border: 0;
  padding: 0;
  font: inherit;
  color: inherit;
  cursor: pointer;
}

.th-sort:hover { color: var(--text-primary); }

.sort-arrow {
  font-size: 12px;
  color: var(--series-1);
}

tbody td {
  padding: 10px 12px;
  border-bottom: 1px solid var(--border);
  vertical-align: top;
}

.rate-row {
  cursor: pointer;
  transition: background 100ms ease;
}

.rate-row:hover { background: var(--surface-2); }
.rate-row.is-expanded { background: var(--surface-2); }
.expand-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 26px;
  height: 26px;
  padding: 0;
  background: transparent;
  border: 0;
  border-radius: var(--radius-sm);
  color: var(--text-muted);
  font-size: 11px;
  cursor: pointer;
}

.expand-btn:hover { background: var(--surface-3); color: var(--text-primary); }

.col-rate { width: 140px; }
.col-ein { width: 118px; }
.col-npis { width: 140px; }
.col-class { width: 160px; }
.col-source { width: 180px; }
.col-actions { width: 34px; text-align: right; }

.rate {
  font-size: 15px;
  font-weight: 600;
}

.rate-delta {
  margin-top: 1px;
  font-size: 11.5px;
  font-variant-numeric: tabular-nums;
}

.rate-delta.is-above { color: var(--warn); }
.rate-delta.is-below { color: var(--good); }
.rate-delta.is-neutral { color: var(--text-muted); }

.provider-name {
  font-weight: 500;
}

.network, .setting, .src-date {
  font-size: 12px;
}

.src-date { display: block; margin-top: 2px; font-size: 11.5px; }
.src-badge { font-size: 11px; }
.unresolved-badge { margin-top: 3px; font-size: 11px; }

.link-btn {
  background: none;
  border: 0;
  padding: 0;
  color: var(--accent);
  cursor: pointer;
  text-decoration: underline;
  text-decoration-style: dotted;
  text-underline-offset: 3px;
}

.link-btn:hover { text-decoration-style: solid; }

.matched-npi { font-size: 11.5px; }
.npi-count { font-size: 13px; }

/* Expanded detail -------------------------------------------------------- */

.detail-row td {
  background: var(--surface-0);
  padding: 0;
}

.detail {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(250px, 1fr));
  gap: 20px;
  padding: 18px 14px;
}

.detail-sec h4 {
  margin: 0 0 9px;
  font-size: 11.5px;
  font-weight: 600;
  letter-spacing: 0.04em;
  text-transform: uppercase;
  color: var(--text-muted);
}

.detail-sec dl {
  margin: 0;
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.detail-sec dl > div {
  display: grid;
  grid-template-columns: 112px minmax(0, 1fr);
  gap: 10px;
  font-size: 13px;
}

.detail-sec dt { color: var(--text-muted); }
.detail-sec dd { margin: 0; color: var(--text-secondary); overflow-wrap: anywhere; }

.addl { font-size: 12.5px; line-height: 1.45; }

.file-link a { overflow-wrap: anywhere; }

.npi-list {
  margin-top: 10px;
  display: flex;
  flex-wrap: wrap;
  gap: 5px;
}

.npi-chip {
  font-size: 11.5px;
  padding: 2px 8px;
}

.more-npis { font-size: 12px; align-self: center; }

.copy-btn { margin-top: 10px; }

@media (max-width: 760px) {
  .detail { grid-template-columns: 1fr; gap: 16px; }
  .detail-sec dl > div { grid-template-columns: 104px minmax(0, 1fr); }
}
</style>
