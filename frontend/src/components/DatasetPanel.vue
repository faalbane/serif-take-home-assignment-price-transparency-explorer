<script setup lang="ts">
import { ref } from 'vue'
import type { MetaResponse } from '../api/types'
import { bytes, count, prettyUrl, safeUrl } from '../composables/useFormat'

/** Dataset provenance, collapsed by default.
 *
 *  This is the "where did this come from" panel: the index URL that was used,
 *  every in-network file ingested from it with its parse outcome, the plans it
 *  covers, and the data-quality notes the ingest recorded. A reviewer should be
 *  able to confirm the index really was the source of truth without reading the
 *  code. */

defineProps<{ meta: MetaResponse }>()

const open = ref(false)
const openSource = ref<number | null>(null)
</script>

<template>
  <section class="panel card">
    <button class="panel-head" type="button" :aria-expanded="open" @click="open = !open">
      <span class="chev" aria-hidden="true">{{ open ? '▾' : '▸' }}</span>
      <span class="panel-title">Dataset &amp; provenance</span>
      <span class="panel-summary muted nums">
        {{ count(meta.counts.rows) }} rates ·
        {{ count(meta.counts.distinct_codes) }} codes ·
        {{ count(meta.counts.provider_groups) }} provider groups ·
        {{ count(meta.plans.length) }} plans
      </span>
      <span v-if="meta.data_notes?.length" class="badge badge-warn note-badge">
        {{ meta.data_notes.length }} data note{{ meta.data_notes.length === 1 ? '' : 's' }}
      </span>
    </button>

    <div v-if="open" class="panel-body">
      <dl class="counts">
        <div><dt>Rate rows</dt><dd class="nums">{{ count(meta.counts.rows) }}</dd></div>
        <div><dt>Items / services</dt><dd class="nums">{{ count(meta.counts.items) }}</dd></div>
        <div><dt>Distinct codes</dt><dd class="nums">{{ count(meta.counts.distinct_codes) }}</dd></div>
        <div><dt>Provider groups</dt><dd class="nums">{{ count(meta.counts.provider_groups) }}</dd></div>
        <div><dt>Distinct NPIs</dt><dd class="nums">{{ count(meta.counts.distinct_npis) }}</dd></div>
        <div><dt>Distinct EINs</dt><dd class="nums">{{ count(meta.counts.distinct_eins) }}</dd></div>
      </dl>

      <div class="index-src">
        <span class="sec-label">Index file (source of truth)</span>
        <a
          v-if="safeUrl(meta.index_url)"
          class="mono index-url"
          :href="safeUrl(meta.index_url)"
          target="_blank"
          rel="noopener noreferrer"
        >{{ meta.index_url }}</a>
        <span v-else class="mono index-url muted">{{ meta.index_url }}</span>
      </div>

      <div class="sources">
        <span class="sec-label">In-network files referenced by the index</span>
        <ul class="src-list">
          <li v-for="src in meta.sources" :key="src.id" class="src">
            <button class="src-head" type="button" :aria-expanded="openSource === src.id"
                    @click="openSource = openSource === src.id ? null : src.id">
              <span class="badge" :class="src.status === 'ok' ? 'badge-good' : 'badge-critical'">{{ src.status }}</span>
              <span class="src-label mono">{{ src.label }}</span>
              <span class="src-meta muted nums">
                {{ count(src.rows) }} rates · {{ count(src.parse_stats.items) }} items ·
                {{ bytes(src.bytes) }} · {{ (src.duration_ms / 1000).toFixed(1) }}s parse
              </span>
            </button>

            <div v-if="openSource === src.id" class="src-detail">
              <p v-if="src.error" class="src-error">{{ src.error }}</p>
              <dl class="src-dl">
                <div class="is-wide">
                  <dt>URL</dt>
                  <dd>
                    <a
                      v-if="safeUrl(src.url)"
                      class="mono src-url"
                      :href="safeUrl(src.url)"
                      target="_blank"
                      rel="noopener noreferrer"
                    >{{ prettyUrl(src.url) }}</a>
                    <span v-else class="mono src-url muted">{{ src.url }}</span>
                  </dd>
                </div>
                <div><dt>Reporting entity</dt><dd>{{ src.reporting_entity_name }} <span class="muted">({{ src.reporting_entity_type }})</span></dd></div>
                <div><dt>Last updated</dt><dd class="mono">{{ src.last_updated_on }}</dd></div>
                <div><dt>Schema version</dt><dd class="mono">{{ src.version }}</dd></div>
                <div><dt>Index structure</dt><dd class="mono">#{{ src.structure_index }}</dd></div>
                <div><dt>Price nodes</dt><dd class="nums">{{ count(src.parse_stats.prices) }}</dd></div>
                <div><dt>Rate groups</dt><dd class="nums">{{ count(src.parse_stats.rate_groups) }}</dd></div>
                <div><dt>Provider refs / groups</dt><dd class="nums">{{ count(src.parse_stats.provider_references) }} / {{ count(src.parse_stats.provider_groups) }}</dd></div>
                <div>
                  <dt>Skipped records</dt>
                  <dd class="nums" :class="{ 'is-warn': src.parse_stats.skipped_items + src.parse_stats.skipped_provider_refs > 0 }">
                    {{ count(src.parse_stats.skipped_items) }} items,
                    {{ count(src.parse_stats.skipped_provider_refs) }} provider refs
                  </dd>
                </div>
                <div v-if="src.parse_stats.unknown_top_level_keys?.length">
                  <dt>Non-standard keys</dt>
                  <dd class="mono">{{ src.parse_stats.unknown_top_level_keys.join(', ') }}</dd>
                </div>
              </dl>

              <details class="plans">
                <summary>{{ count(src.plans.length) }} reporting plan{{ src.plans.length === 1 ? '' : 's' }}</summary>
                <ul class="plan-list">
                  <li v-for="(p, i) in src.plans" :key="`${p.plan_id}-${i}`">
                    <span class="mono plan-id">{{ p.plan_id }}</span>
                    <span class="plan-name">{{ p.plan_name }}</span>
                    <span class="muted plan-market">{{ p.plan_market_type }}</span>
                  </li>
                </ul>
              </details>
            </div>
          </li>
        </ul>
      </div>

      <div v-if="meta.data_notes?.length" class="notes">
        <span class="sec-label">Data-quality notes recorded during ingest</span>
        <ul>
          <li v-for="n in meta.data_notes" :key="n">{{ n }}</li>
        </ul>
      </div>
    </div>
  </section>
</template>

<style scoped>
.panel { overflow: hidden; }

.panel-head {
  display: flex;
  align-items: center;
  gap: 10px;
  width: 100%;
  padding: 11px 14px;
  background: transparent;
  border: 0;
  cursor: pointer;
  text-align: left;
  flex-wrap: wrap;
}

.panel-head:hover { background: var(--surface-2); }

.chev { color: var(--text-muted); font-size: 11px; }

.panel-title { font-weight: 600; font-size: 14px; }

.panel-summary { font-size: 12.5px; }

.note-badge { margin-left: auto; }

.panel-body {
  padding: 4px 14px 16px;
  border-top: 1px solid var(--border);
  display: flex;
  flex-direction: column;
  gap: 18px;
}

.sec-label {
  display: block;
  margin-bottom: 7px;
  font-size: 11px;
  font-weight: 600;
  letter-spacing: 0.04em;
  text-transform: uppercase;
  color: var(--text-muted);
}

.counts {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(130px, 1fr));
  gap: 12px;
  margin: 14px 0 0;
}

.counts dt {
  font-size: 11px;
  letter-spacing: 0.03em;
  text-transform: uppercase;
  color: var(--text-muted);
}

.counts dd {
  margin: 2px 0 0;
  font-size: 17px;
  font-weight: 600;
}

.index-url { font-size: 12px; overflow-wrap: anywhere; }

.src-list, .plan-list {
  margin: 0;
  padding: 0;
  list-style: none;
}

.src {
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  margin-bottom: 7px;
  overflow: hidden;
}

.src-head {
  display: flex;
  align-items: center;
  gap: 10px;
  width: 100%;
  padding: 9px 11px;
  background: var(--surface-2);
  border: 0;
  cursor: pointer;
  text-align: left;
  flex-wrap: wrap;
}

.src-head:hover { background: var(--surface-3); }

.src-label { font-weight: 600; font-size: 13px; }
.src-meta { font-size: 11.5px; }

.src-detail { padding: 12px 11px; }

.src-error {
  margin: 0 0 10px;
  padding: 8px 10px;
  background: var(--critical-soft);
  color: var(--critical);
  border-radius: var(--radius-sm);
  font-size: 13px;
}

.src-dl {
  margin: 0;
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(280px, 1fr));
  gap: 7px 18px;
}

.src-dl > div {
  display: grid;
  grid-template-columns: 150px minmax(0, 1fr);
  gap: 10px;
  font-size: 12.5px;
}

/* A full-length URL in a narrow grid column breaks at every character.
 * Give it the whole row. */
.src-dl > div.is-wide {
  grid-column: 1 / -1;
}

.src-url {
  overflow-wrap: break-word;
  word-break: normal;
}

.src-dl dt { color: var(--text-muted); }
.src-dl dd { margin: 0; color: var(--text-secondary); overflow-wrap: anywhere; }
.src-dl dd.is-warn { color: var(--warn); }

.plans { margin-top: 12px; font-size: 13px; }
.plans summary { cursor: pointer; color: var(--accent); }

.plan-list {
  margin-top: 8px;
  max-height: 260px;
  overflow-y: auto;
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.plan-list li {
  display: grid;
  grid-template-columns: 110px minmax(0, 1fr) auto;
  gap: 10px;
  font-size: 12.5px;
  padding: 3px 0;
  border-bottom: 1px solid var(--border);
}

.plan-id { color: var(--text-secondary); }
.plan-name { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.plan-market { font-size: 11.5px; }

.notes ul {
  margin: 0;
  padding-left: 18px;
  font-size: 13px;
  color: var(--text-secondary);
  line-height: 1.6;
}

@media (max-width: 680px) {
  .src-dl > div { grid-template-columns: 130px minmax(0, 1fr); }
  .plan-list li { grid-template-columns: 100px minmax(0, 1fr); }
  .plan-market { grid-column: 2; }
}
</style>
