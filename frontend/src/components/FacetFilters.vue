<script setup lang="ts">
import { computed } from 'vue'
import type { Facets, SearchParams } from '../api/types'
import { count } from '../composables/useFormat'

/** Facet chips.
 *
 *  Each facet's counts come from the server with every filter applied except
 *  its own, so selecting a value never hides the alternatives. A dimension with
 *  only one value is not rendered -- the whole Fidelis dataset has a single
 *  `setting` ("outpatient") and a single `negotiated_type` ("negotiated"), and
 *  a control that cannot change anything is just noise. */

const props = defineProps<{
  facets: Facets
  params: SearchParams
}>()

const emit = defineEmits<{ update: [Partial<SearchParams>] }>()

interface Group {
  key: keyof SearchParams
  label: string
  options: { value: string; label: string; count: number }[]
}

const groups = computed<Group[]>(() => {
  const defs: { key: keyof SearchParams; label: string; src: Facets[keyof Facets] }[] = [
    { key: 'billing_class', label: 'Billing class', src: props.facets.billing_class },
    { key: 'code_type', label: 'Code system', src: props.facets.billing_code_type },
    { key: 'source', label: 'Source file', src: props.facets.source },
    { key: 'setting', label: 'Setting', src: props.facets.setting },
  ]
  return defs
    .filter((d) => {
      const options = d.src?.length ?? 0
      if (options === 0) return false
      // A dimension with one option normally cannot narrow anything, so it is
      // hidden as noise -- the whole dataset has a single `setting` value.
      //
      // But if a filter IS active on that dimension, the single remaining
      // option is the most useful control on the page: it is the value the
      // user could switch to, and the only way back from a selection that
      // emptied the table. Filtering code 490 to "professional" leaves exactly
      // one option, "institutional", carrying all 208,332 rows.
      if (options === 1) return String(props.params[d.key] ?? '') !== ''
      return true
    })
    .map((d) => ({
      key: d.key,
      label: d.label,
      options: d.src.map((f) => ({ value: f.value, label: f.label, count: f.count })),
    }))
})

function toggle(key: keyof SearchParams, value: string) {
  const current = String(props.params[key] ?? '')
  emit('update', { [key]: current === value ? '' : value } as Partial<SearchParams>)
}

const activeCount = computed(
  () =>
    groups.value.filter((g) => String(props.params[g.key] ?? '') !== '').length +
    (props.params.min_rate ? 1 : 0) +
    (props.params.max_rate ? 1 : 0) +
    (props.params.plan_id ? 1 : 0),
)
</script>

<template>
  <div v-if="groups.length" class="facets">
    <div
      v-for="group in groups"
      :key="String(group.key)"
      class="facet"
      role="group"
      :aria-labelledby="`facet-label-${String(group.key)}`"
    >
      <span :id="`facet-label-${String(group.key)}`" class="facet-label">{{ group.label }}</span>
      <div class="facet-chips">
        <button
          v-for="opt in group.options"
          :key="opt.value"
          class="chip"
          type="button"
          :aria-pressed="String(params[group.key] ?? '') === opt.value"
          @click="toggle(group.key, opt.value)"
        >
          {{ opt.label }}
          <span class="chip-count">{{ count(opt.count) }}</span>
        </button>
      </div>
    </div>

    <button
      v-if="activeCount > 0"
      class="btn btn-ghost btn-sm clear-all"
      type="button"
      @click="emit('update', { billing_class: '', code_type: '', source: '', setting: '', min_rate: '', max_rate: '', plan_id: '' })"
    >
      Clear {{ activeCount }} filter{{ activeCount === 1 ? '' : 's' }}
    </button>
  </div>
</template>

<style scoped>
.facets {
  display: flex;
  align-items: flex-start;
  gap: 22px;
  flex-wrap: wrap;
  padding: 12px 14px;
  background: var(--surface-1);
  border: 1px solid var(--border);
  border-radius: var(--radius);
}

.facet {
  display: flex;
  flex-direction: column;
  gap: 6px;
  min-width: 0;
}

.facet-label {
  font-size: 11px;
  font-weight: 600;
  letter-spacing: 0.04em;
  text-transform: uppercase;
  color: var(--text-muted);
}

.facet-chips {
  display: flex;
  gap: 6px;
  flex-wrap: wrap;
}

.clear-all {
  margin-left: auto;
  align-self: center;
}

@media (max-width: 640px) {
  .facets { gap: 14px; }
  .clear-all { margin-left: 0; }
}
</style>
