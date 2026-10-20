<script setup lang="ts">
import { computed } from 'vue'
import type { Pagination } from '../api/types'
import { count } from '../composables/useFormat'

const props = defineProps<{ pagination: Pagination; loading: boolean }>()
const emit = defineEmits<{ page: [number]; pageSize: [number] }>()

const PAGE_SIZES = [25, 50, 100, 250]

const range = computed(() => {
  const { page, page_size, total } = props.pagination
  if (total === 0) return { from: 0, to: 0 }
  const from = (page - 1) * page_size + 1
  return { from, to: Math.min(total, page * page_size) }
})

/** A windowed page list, so 417 pages do not render 417 buttons. */
const pages = computed<(number | '…')[]>(() => {
  const { page, total_pages } = props.pagination
  if (total_pages <= 7) return Array.from({ length: total_pages }, (_, i) => i + 1)

  const out: (number | '…')[] = [1]
  const lo = Math.max(2, page - 1)
  const hi = Math.min(total_pages - 1, page + 1)
  if (lo > 2) out.push('…')
  for (let p = lo; p <= hi; p++) out.push(p)
  if (hi < total_pages - 1) out.push('…')
  out.push(total_pages)
  return out
})
</script>

<template>
  <nav v-if="pagination.total > 0" class="pager" aria-label="Result pages">
    <p class="pager-range nums secondary">
      {{ count(range.from) }}–{{ count(range.to) }} of {{ count(pagination.total) }}
    </p>

    <div class="pager-controls">
      <button class="btn btn-sm" type="button" :disabled="!pagination.has_prev || loading" @click="emit('page', pagination.page - 1)">
        ‹ Prev
      </button>

      <template v-for="(p, i) in pages" :key="`${p}-${i}`">
        <span v-if="p === '…'" class="gap muted" aria-hidden="true">…</span>
        <button
          v-else
          class="btn btn-sm page-btn"
          :class="{ 'is-current': p === pagination.page }"
          type="button"
          :disabled="loading"
          :aria-current="p === pagination.page ? 'page' : undefined"
          @click="emit('page', p as number)"
        >
          {{ p }}
        </button>
      </template>

      <button class="btn btn-sm" type="button" :disabled="!pagination.has_next || loading" @click="emit('page', pagination.page + 1)">
        Next ›
      </button>
    </div>

    <label class="pager-size">
      <span class="visually-hidden">Rows per page</span>
      <select class="select select-sm" :value="pagination.page_size" :disabled="loading"
              @change="emit('pageSize', Number(($event.target as HTMLSelectElement).value))">
        <option v-for="n in PAGE_SIZES" :key="n" :value="n">{{ n }} / page</option>
      </select>
    </label>
  </nav>
</template>

<style scoped>
.pager {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 14px;
  flex-wrap: wrap;
  margin-top: 12px;
}

.pager-range {
  margin: 0;
  font-size: 13px;
}

.pager-controls {
  display: flex;
  align-items: center;
  gap: 4px;
  flex-wrap: wrap;
}

.page-btn {
  min-width: 34px;
  padding: 5px 8px;
  font-variant-numeric: tabular-nums;
}

.page-btn.is-current {
  background: var(--series-1);
  border-color: var(--series-1);
  color: #fff;
  font-weight: 600;
}

.gap { padding: 0 2px; }

.select-sm {
  width: auto;
  padding: 5px 8px;
  font-size: 13px;
}

@media (max-width: 560px) {
  .pager { justify-content: center; }
}
</style>
