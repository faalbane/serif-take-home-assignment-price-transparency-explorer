<script setup lang="ts">
import { computed, nextTick, onUnmounted, ref, watch } from 'vue'
import { api } from '../api/client'
import type { CodeSummary } from '../api/types'
import { useDebouncedRef } from '../composables/useDebounce'
import { count, moneyCompact, titleCase } from '../composables/useFormat'

/** The required billing-code input, with typeahead.
 *
 *  Nobody knows CPT codes by heart, so a bare text box makes the whole tool
 *  unusable for a first-time visitor. Suggestions search code, short name and
 *  long description, and show how much rate data each code actually has. */

const props = defineProps<{
  modelValue: string
  codeType: string
  invalid?: boolean
}>()

const emit = defineEmits<{
  'update:modelValue': [string]
  'update:codeType': [string]
  /** pick emits the code and its code system together, so choosing a
   *  suggestion is one state change and one search rather than three. */
  pick: [{ code: string; codeType: string }]
  submit: []
}>()

const open = ref(false)
const highlighted = ref(-1)
const suggestions = ref<CodeSummary[]>([])
const loading = ref(false)
const query = ref(props.modelValue)
const debounced = useDebouncedRef(query, 180)
const inputEl = ref<HTMLInputElement | null>(null)
const listId = 'code-typeahead-list'

let controller: AbortController | null = null

watch(
  () => props.modelValue,
  (v) => {
    if (v !== query.value) query.value = v
  },
)

watch(debounced, async (q) => {
  controller?.abort()
  const mine = new AbortController()
  controller = mine
  loading.value = true
  try {
    const res = await api.codes(q.trim(), 10, mine.signal)
    // Identity guard: an aborted request rejects almost immediately, so
    // without this the list empties and visibly blinks between keystrokes,
    // and on a browser without AbortSignal.any a stale success could land on
    // top of a newer one.
    if (controller !== mine) return
    suggestions.value = res.results
    highlighted.value = -1
  } catch {
    if (controller !== mine) return
    suggestions.value = []
  } finally {
    if (controller === mine) loading.value = false
  }
})

const showList = computed(() => open.value && (suggestions.value.length > 0 || loading.value))

function onInput(event: Event) {
  const value = (event.target as HTMLInputElement).value
  query.value = value
  emit('update:modelValue', value)
  open.value = true
}

function choose(s: CodeSummary) {
  // Codes are not unique across code systems -- 490 exists as both RC and CPT --
  // so picking a suggestion pins its code type as well, in one emit.
  query.value = s.billing_code
  open.value = false
  highlighted.value = -1
  emit('pick', { code: s.billing_code, codeType: s.billing_code_type })
}

function onKeydown(event: KeyboardEvent) {
  if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
    if (!open.value) {
      event.preventDefault()
      open.value = true
      return
    }
    event.preventDefault()
    const n = suggestions.value.length
    if (n === 0) return
    const delta = event.key === 'ArrowDown' ? 1 : -1
    highlighted.value = (highlighted.value + delta + n) % n
    // The list scrolls; without this the last options are unreachable by
    // keyboard.
    nextTick(() => {
      document.getElementById(`code-opt-${highlighted.value}`)?.scrollIntoView({ block: 'nearest' })
    })
    return
  }
  if (event.key === 'Enter') {
    if (open.value && highlighted.value >= 0 && suggestions.value[highlighted.value]) {
      event.preventDefault()
      choose(suggestions.value[highlighted.value]!)
      return
    }
    open.value = false
    emit('submit')
    return
  }
  if (event.key === 'Escape') {
    if (open.value) {
      event.stopPropagation()
      open.value = false
      highlighted.value = -1
    }
  }
}

function onBlur() {
  // Delay so a click on a suggestion lands before the list unmounts.
  setTimeout(() => {
    open.value = false
    highlighted.value = -1
  }, 120)
}

onUnmounted(() => controller?.abort())
</script>

<template>
  <div class="typeahead">
    <div class="field">
      <label for="code-input">
        Billing code <span class="req" aria-hidden="true">*</span>
        <span class="visually-hidden">(required)</span>
      </label>
      <input
        id="code-input"
        ref="inputEl"
        class="input mono"
        :class="{ 'is-invalid': invalid }"
        :value="query"
        type="text"
        inputmode="search"
        autocomplete="off"
        spellcheck="false"
        placeholder="e.g. 99213, 490, A0427"
        role="combobox"
        :aria-expanded="showList"
        :aria-controls="listId"
        :aria-activedescendant="highlighted >= 0 ? `code-opt-${highlighted}` : undefined"
        aria-autocomplete="list"
        @input="onInput"
        @keydown="onKeydown"
        @focus="open = true"
        @blur="onBlur"
      />
      <span class="hint">Search by code, procedure name or description.</span>
    </div>

    <ul v-if="showList" :id="listId" class="list card" role="listbox" aria-label="Billing code suggestions">
      <li v-if="loading && suggestions.length === 0" class="opt is-loading muted">Searching…</li>
      <li
        v-for="(s, i) in suggestions"
        :id="`code-opt-${i}`"
        :key="`${s.billing_code}-${s.billing_code_type}`"
        class="opt"
        :class="{ 'is-highlighted': i === highlighted }"
        role="option"
        :aria-selected="i === highlighted"
        @mousedown.prevent="choose(s)"
        @mouseenter="highlighted = i"
      >
        <span class="opt-code mono">{{ s.billing_code }}</span>
        <span class="badge badge-neutral opt-type">{{ s.billing_code_type }}</span>
        <span class="opt-name truncate" :title="s.description || s.name">
          {{ titleCase(s.name) || titleCase(s.description) || '—' }}
        </span>
        <span class="opt-meta nums muted">
          {{ count(s.rows) }} rates · {{ moneyCompact(s.min_rate) }}–{{ moneyCompact(s.max_rate) }}
          <template v-if="s.name_count > 1"> · {{ count(s.name_count) }} names</template>
        </span>
      </li>
    </ul>
  </div>
</template>

<style scoped>
.typeahead {
  position: relative;
  min-width: 0;
}

.req {
  color: var(--critical);
}

.list {
  position: absolute;
  z-index: 40;
  top: calc(100% - 14px);
  left: 0;
  right: 0;
  margin: 0;
  padding: 5px;
  list-style: none;
  max-height: 340px;
  overflow-y: auto;
  box-shadow: var(--shadow);
  min-width: 420px;
}

.opt {
  display: grid;
  grid-template-columns: auto auto minmax(0, 1fr) auto;
  align-items: center;
  gap: 9px;
  padding: 8px 10px;
  border-radius: var(--radius-sm);
  cursor: pointer;
  font-size: 13px;
}

.opt.is-loading {
  grid-template-columns: 1fr;
  cursor: default;
}

.opt.is-highlighted {
  background: var(--surface-2);
}

.opt-code {
  font-weight: 600;
  font-size: 13px;
}

.opt-type {
  font-size: 11px;
  padding: 1px 6px;
}

.opt-meta {
  font-size: 11.5px;
  white-space: nowrap;
}

@media (max-width: 680px) {
  .list { min-width: 0; }
  .opt {
    grid-template-columns: auto auto minmax(0, 1fr);
  }
  .opt-meta {
    grid-column: 1 / -1;
    text-align: left;
  }
}
</style>
