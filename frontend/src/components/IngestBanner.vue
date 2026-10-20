<script setup lang="ts">
import { computed } from 'vue'
import type { IngestStatus } from '../api/types'
import { bytes, count, seconds } from '../composables/useFormat'

const props = defineProps<{
  status: IngestStatus | null
  error: string | null
}>()

defineEmits<{ retry: [] }>()

const PHASE_LABELS: Record<string, string> = {
  idle: 'Starting up',
  fetching_index: 'Reading the Transparency in Coverage index',
  downloading_rate_files: 'Downloading in-network rate files',
  parsing_rate_files: 'Parsing and normalizing rate records',
  building_index: 'Building search indexes',
  ready: 'Ready',
  failed: 'Ingest failed',
}

const phaseLabel = computed(() => PHASE_LABELS[props.status?.phase ?? 'idle'] ?? 'Working')

/** Overall progress is dominated by the download, which is the only phase with
 *  a known total. Parsing is reported per file by record count instead. */
const downloadProgress = computed(() => {
  const files = props.status?.files ?? []
  const total = files.reduce((sum, f) => sum + (f.total_bytes > 0 ? f.total_bytes : 0), 0)
  if (total === 0) return null
  const done = files.reduce((sum, f) => sum + Math.min(f.bytes, f.total_bytes || f.bytes), 0)
  return Math.min(100, Math.round((done / total) * 100))
})

const failed = computed(() => props.status?.phase === 'failed' || !!props.error)
</script>

<template>
  <div class="gate">
    <div class="card gate-card">
      <div class="gate-head">
        <span class="spinner" :class="{ 'is-failed': failed }" aria-hidden="true" />
        <div class="grow">
          <!-- Only the phase is a live region. The byte counters update every
               600ms, and announcing the whole card on each poll made a screen
               reader read the full download progress for 13 seconds straight. -->
          <h2 class="gate-title" role="status" aria-live="polite">
            {{ failed ? 'Could not load the rate data' : phaseLabel }}
          </h2>
          <p class="gate-msg secondary">
            {{ error || status?.message || 'Contacting the API…' }}
          </p>
        </div>
        <span v-if="status && status.elapsed_seconds > 0" class="badge badge-neutral nums">
          {{ seconds(status.elapsed_seconds) }}
        </span>
      </div>

      <p v-if="!failed" class="gate-explain muted">
        The server is reading the CMS index file and ingesting every in-network rate
        file it references — about 263 MB for the two Fidelis files. This takes
        roughly 13 seconds on a cold cache and 6 on a warm one, and only happens at
        startup.
      </p>

      <div v-if="downloadProgress !== null && !failed" class="progress" :aria-valuenow="downloadProgress"
           role="progressbar" aria-valuemin="0" aria-valuemax="100"
           aria-label="Download progress" :aria-valuetext="`${downloadProgress}% downloaded`">
        <div class="progress-fill" :style="{ width: `${downloadProgress}%` }" />
      </div>

      <ul v-if="status?.files?.length" class="files" aria-hidden="true">
        <li v-for="file in status.files" :key="file.url" class="file">
          <span class="file-dot" :class="`is-${file.status}`" aria-hidden="true" />
          <span class="file-name mono truncate" :title="file.url">
            {{ file.url.split('/').pop() }}
          </span>
          <span class="file-state secondary">
            <template v-if="file.status === 'failed'">{{ file.error || 'failed' }}</template>
            <template v-else-if="file.status === 'ok'">
              {{ count(file.items) }} items<template v-if="file.from_cache"> · cached</template>
            </template>
            <template v-else-if="file.status === 'parsing'">parsing… {{ count(file.items) }} items</template>
            <template v-else-if="file.total_bytes > 0">
              {{ bytes(file.bytes) }} / {{ bytes(file.total_bytes) }}
            </template>
            <template v-else>waiting…</template>
          </span>
        </li>
      </ul>

      <div v-if="failed" class="gate-actions">
        <button class="btn btn-primary" @click="$emit('retry')">Retry</button>
        <p class="muted gate-help">
          If the index URL has been rotated out, a current one is listed at
          <a href="https://www.centene.com/price-transparency-files.html" target="_blank" rel="noopener noreferrer">
            centene.com/price-transparency-files
          </a>. Pass it with <code>-index-url</code>.
        </p>
      </div>
    </div>
  </div>
</template>

<style scoped>
.gate {
  display: grid;
  place-items: center;
  min-height: 70vh;
  padding: 24px 16px;
}

.gate-card {
  width: 100%;
  max-width: 620px;
  padding: 26px;
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.gate-head {
  display: flex;
  align-items: flex-start;
  gap: 14px;
}

.gate-title {
  font-size: 17px;
}

.gate-msg {
  margin: 4px 0 0;
  font-size: 14px;
}

.gate-explain {
  margin: 0;
  font-size: 13px;
  line-height: 1.6;
}

.spinner {
  flex: none;
  width: 18px;
  height: 18px;
  margin-top: 3px;
  border: 2px solid var(--border-strong);
  border-top-color: var(--series-1);
  border-radius: 50%;
  animation: spin 800ms linear infinite;
}

.spinner.is-failed {
  border-color: var(--critical);
  border-top-color: var(--critical);
  animation: none;
}

@keyframes spin {
  to { transform: rotate(360deg); }
}

.progress {
  height: 6px;
  background: var(--surface-3);
  border-radius: 999px;
  overflow: hidden;
}

.progress-fill {
  height: 100%;
  background: var(--series-1);
  border-radius: 999px;
  transition: width 200ms ease;
}

.files {
  margin: 0;
  padding: 0;
  list-style: none;
  display: flex;
  flex-direction: column;
  gap: 8px;
  border-top: 1px solid var(--border);
  padding-top: 14px;
}

.file {
  display: grid;
  grid-template-columns: 8px minmax(0, 1fr) auto;
  align-items: center;
  gap: 10px;
  font-size: 13px;
}

.file-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--border-strong);
}

.file-dot.is-downloading,
.file-dot.is-parsing { background: var(--series-1); }
.file-dot.is-ok { background: var(--good); }
.file-dot.is-failed { background: var(--critical); }

.file-state {
  font-size: 12px;
  font-variant-numeric: tabular-nums;
  text-align: right;
}

.gate-actions {
  display: flex;
  flex-direction: column;
  gap: 10px;
  border-top: 1px solid var(--border);
  padding-top: 14px;
}

.gate-help {
  margin: 0;
  font-size: 13px;
  line-height: 1.6;
}

@media (max-width: 520px) {
  .gate-card { padding: 18px; }
  .file { grid-template-columns: 8px minmax(0, 1fr); }
  .file-state { grid-column: 2; text-align: left; }
}
</style>
