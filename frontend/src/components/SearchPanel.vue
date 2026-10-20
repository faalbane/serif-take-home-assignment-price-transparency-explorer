<script setup lang="ts">
import { computed, ref } from 'vue'
import type { FacetDomains, PlanRef, SearchParams } from '../api/types'
import CodeTypeahead from './CodeTypeahead.vue'
import ProviderIdInput from './ProviderIdInput.vue'

/** The search form.
 *
 *  A billing code is required and sits on its own, visually primary. Provider
 *  identifiers are optional narrowing controls beside it. Everything else is
 *  behind "More filters", because a first-time visitor should see three inputs,
 *  not eleven. */

const props = defineProps<{
  params: SearchParams
  domains: FacetDomains | null
  plans: PlanRef[]
  loading: boolean
  invalidCode?: boolean
}>()

const emit = defineEmits<{
  /** patch records a value without searching (text inputs). */
  patch: [Partial<SearchParams>]
  /** update records a value and searches (selects, suggestion picks). */
  update: [Partial<SearchParams>]
  submit: []
  reset: []
}>()

const showAdvanced = ref(
  Boolean(props.params.min_rate || props.params.max_rate || props.params.plan_id || props.params.code_type),
)

const canSearch = computed(() => props.params.code.trim().length > 0)

const advancedCount = computed(
  () =>
    [props.params.code_type, props.params.min_rate, props.params.max_rate, props.params.plan_id].filter(Boolean)
      .length,
)

function submit() {
  if (canSearch.value) emit('submit')
}
</script>

<template>
  <form class="panel card" @submit.prevent="submit">
    <div class="primary-row">
      <div class="code-cell">
        <CodeTypeahead
          :model-value="params.code"
          :code-type="params.code_type"
          :invalid="invalidCode"
          @update:model-value="emit('patch', { code: $event })"
          @update:code-type="emit('patch', { code_type: $event })"
          @pick="emit('update', { code: $event.code, code_type: $event.codeType })"
          @submit="submit"
        />
      </div>

      <ProviderIdInput
        :model-value="params.npi"
        kind="npi"
        @update:model-value="emit('patch', { npi: $event })"
        @submit="submit"
      />

      <ProviderIdInput
        :model-value="params.ein"
        kind="ein"
        @update:model-value="emit('patch', { ein: $event })"
        @submit="submit"
      />

      <div class="actions">
        <button class="btn btn-primary search-btn" type="submit" :disabled="!canSearch || loading">
          <span v-if="loading" class="btn-spinner" aria-hidden="true" />
          {{ loading ? 'Searching' : 'Search' }}
        </button>
      </div>
    </div>

    <div class="secondary-row">
      <button class="btn btn-ghost btn-sm" type="button" :aria-expanded="showAdvanced" @click="showAdvanced = !showAdvanced">
        {{ showAdvanced ? '▾' : '▸' }} More filters
        <span v-if="advancedCount" class="badge badge-neutral adv-count">{{ advancedCount }}</span>
      </button>
      <button v-if="advancedCount || params.npi || params.ein" class="btn btn-ghost btn-sm" type="button" @click="emit('reset')">
        Reset filters
      </button>
    </div>

    <fieldset v-if="showAdvanced" class="advanced">
      <legend class="visually-hidden">Additional filters</legend>

      <div class="field">
        <label for="code-type">Code system</label>
        <select id="code-type" class="select" :value="params.code_type"
                @change="emit('update', { code_type: ($event.target as HTMLSelectElement).value })">
          <option value="">Any</option>
          <option v-for="t in domains?.billing_code_types ?? []" :key="t" :value="t">{{ t }}</option>
        </select>
        <span class="hint">Codes repeat across systems — 490 is both a revenue code and a CPT code.</span>
      </div>

      <div class="field">
        <label for="min-rate">Minimum rate</label>
        <input id="min-rate" class="input mono" type="number" min="0" step="0.01" inputmode="decimal"
               placeholder="0.00" :value="params.min_rate"
               @input="emit('patch', { min_rate: ($event.target as HTMLInputElement).value })"
               @change="emit('update', { min_rate: ($event.target as HTMLInputElement).value })"
               @keydown.enter.prevent="emit('update', { min_rate: ($event.target as HTMLInputElement).value })" />
      </div>

      <div class="field">
        <label for="max-rate">Maximum rate</label>
        <input id="max-rate" class="input mono" type="number" min="0" step="0.01" inputmode="decimal"
               placeholder="no limit" :value="params.max_rate"
               @input="emit('patch', { max_rate: ($event.target as HTMLInputElement).value })"
               @change="emit('update', { max_rate: ($event.target as HTMLInputElement).value })"
               @keydown.enter.prevent="emit('update', { max_rate: ($event.target as HTMLInputElement).value })" />
      </div>

      <div class="field plan-field">
        <label for="plan-id">Plan</label>
        <select id="plan-id" class="select" :value="params.plan_id"
                @change="emit('update', { plan_id: ($event.target as HTMLSelectElement).value })">
          <option value="">All plans</option>
          <option v-for="p in plans" :key="`${p.plan_id}-${p.plan_name}`" :value="p.plan_id">
            {{ p.plan_name }} ({{ p.plan_id }})
          </option>
        </select>
        <span class="hint">
          Narrows to the rate files covering that plan. The index files one set of rates per
          reporting structure, not per plan, so plans sharing a file share its rates.
        </span>
      </div>
    </fieldset>
  </form>
</template>

<style scoped>
.panel {
  padding: 16px 18px;
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.primary-row {
  display: grid;
  grid-template-columns: minmax(260px, 1.5fr) minmax(170px, 1fr) minmax(170px, 1fr) auto;
  gap: 14px;
  align-items: start;
}

.code-cell { min-width: 0; }

.actions {
  display: flex;
  align-items: flex-end;
  height: 100%;
  padding-top: 21px;
}

.search-btn {
  min-width: 112px;
  min-height: 40px;
}

.btn-spinner {
  width: 13px;
  height: 13px;
  border: 2px solid rgb(255 255 255 / 40%);
  border-top-color: #fff;
  border-radius: 50%;
  animation: spin 700ms linear infinite;
}

@keyframes spin { to { transform: rotate(360deg); } }

.secondary-row {
  display: flex;
  gap: 6px;
  align-items: center;
  flex-wrap: wrap;
}

.adv-count { font-size: 11px; padding: 1px 6px; }

.advanced {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(190px, 1fr));
  gap: 14px;
  margin: 0;
  padding: 14px 0 2px;
  border: 0;
  border-top: 1px solid var(--border);
}

.plan-field { grid-column: span 2; min-width: 0; }

@media (max-width: 960px) {
  .primary-row { grid-template-columns: 1fr 1fr; }
  .code-cell { grid-column: 1 / -1; }
  .actions { grid-column: 1 / -1; padding-top: 0; }
  .search-btn { width: 100%; }
}

@media (max-width: 560px) {
  .primary-row { grid-template-columns: 1fr; }
  .plan-field { grid-column: span 1; }
  .panel { padding: 14px; }
}
</style>
