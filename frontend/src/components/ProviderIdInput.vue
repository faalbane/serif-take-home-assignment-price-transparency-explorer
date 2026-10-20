<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { api } from '../api/client'
import type { ProviderHit } from '../api/types'
import { useDebouncedRef } from '../composables/useDebounce'
import { count } from '../composables/useFormat'

/** An optional NPI or EIN filter that resolves the identifier as you type.
 *
 *  Without this, a typo in a 10-digit number is indistinguishable from "this
 *  provider does not price this code" -- both return nothing. Showing the
 *  resolved organisation name turns a dead end into a clear answer. */

const props = defineProps<{
  modelValue: string
  kind: 'npi' | 'ein'
}>()

const emit = defineEmits<{ 'update:modelValue': [string]; submit: [] }>()

const LABELS = {
  npi: { label: 'Provider NPI', placeholder: '1396747861', digits: 10, help: '10-digit National Provider Identifier' },
  ein: { label: 'Provider EIN', placeholder: '11-3471596', digits: 9, help: '9-digit tax ID, with or without the dash' },
} as const

const meta = computed(() => LABELS[props.kind])

const raw = ref(props.modelValue)
const debounced = useDebouncedRef(raw, 300)
const hits = ref<ProviderHit[]>([])
const resolving = ref(false)
const lookedUp = ref(false)

let controller: AbortController | null = null

const digits = computed(() => raw.value.replace(/\D/g, ''))

/** Structural problems are warnings, never blocks: the source files contain
 *  invalid identifiers, and a tool that refuses to look them up cannot be used
 *  to investigate the data-quality problem it just surfaced. */
const warning = computed(() => {
  if (digits.value === '') return ''
  if (digits.value.length !== meta.value.digits) {
    return `${props.kind.toUpperCase()} is normally ${meta.value.digits} digits; this is ${digits.value.length}.`
  }
  if (props.kind === 'npi' && !npiChecksumOk(digits.value)) {
    return 'Fails the NPI check-digit test. Still searchable — some filed NPIs are invalid.'
  }
  return ''
})

/** The CMS NPI check digit: Luhn over the identifier prefixed with "80840". */
function npiChecksumOk(npi: string): boolean {
  if (npi.length !== 10) return false
  const payload = `80840${npi.slice(0, 9)}`
  const check = Number(npi[9])
  let sum = 0
  for (let i = 0; i < payload.length; i++) {
    let d = Number(payload[payload.length - 1 - i])
    if (i % 2 === 0) {
      d *= 2
      if (d > 9) d -= 9
    }
    sum += d
  }
  return (sum + check) % 10 === 0
}

watch(
  () => props.modelValue,
  (v) => {
    if (v !== raw.value) raw.value = v
  },
)

async function resolve() {
  const d = digits.value
  controller?.abort()
  hits.value = []
  lookedUp.value = false
  if (d.length < meta.value.digits) return

  controller = new AbortController()
  resolving.value = true
  try {
    const res = await api.providers({ [props.kind]: d, limit: 5 }, controller.signal)
    hits.value = res.results
    lookedUp.value = true
  } catch {
    // Resolution is a convenience; a failure must not stop the search.
    hits.value = []
  } finally {
    resolving.value = false
  }
}

watch(debounced, resolve)

// A value restored from a shared URL never fires the watcher, so resolve it on
// mount too -- otherwise a link with an NPI in it shows no provider name.
onMounted(() => {
  if (digits.value.length >= meta.value.digits) void resolve()
})

function onInput(event: Event) {
  const value = (event.target as HTMLInputElement).value
  raw.value = value
  emit('update:modelValue', value)
}

function clear() {
  raw.value = ''
  // Reset the resolution too, or the previously matched organization keeps
  // showing under an empty field until the debounce catches up.
  hits.value = []
  lookedUp.value = false
  resolving.value = false
  controller?.abort()
  emit('update:modelValue', '')
  emit('submit')
}

onUnmounted(() => controller?.abort())
</script>

<template>
  <div class="field">
    <label :for="`${kind}-input`">{{ meta.label }} <span class="muted opt-tag">optional</span></label>
    <div class="input-wrap">
      <input
        :id="`${kind}-input`"
        class="input mono"
        :value="raw"
        type="text"
        inputmode="numeric"
        autocomplete="off"
        spellcheck="false"
        :placeholder="meta.placeholder"
        :aria-describedby="`${kind}-help`"
        @input="onInput"
        @keydown.enter="$emit('submit')"
      />
      <button v-if="raw" class="clear" type="button" :aria-label="`Clear ${meta.label}`" @click="clear">×</button>
    </div>

    <span v-if="warning" :id="`${kind}-help`" class="hint warn">{{ warning }}</span>
    <span v-else-if="resolving" :id="`${kind}-help`" class="hint">Resolving…</span>
    <span v-else-if="lookedUp && hits.length === 0" :id="`${kind}-help`" class="hint warn">
      Not found in the ingested files.
    </span>
    <span v-else-if="hits.length > 0" :id="`${kind}-help`" class="hint resolved">
      <strong class="resolved-name">{{ hits[0]!.business_name || 'Unnamed group' }}</strong>
      <span class="muted"> · {{ count(hits[0]!.rows) }} rates</span>
      <span v-if="hits.length > 1" class="muted"> · +{{ hits.length - 1 }} more group{{ hits.length > 2 ? 's' : '' }}</span>
    </span>
    <span v-else :id="`${kind}-help`" class="hint">{{ meta.help }}</span>
  </div>
</template>

<style scoped>
.opt-tag {
  font-weight: 400;
  text-transform: none;
  letter-spacing: 0;
  font-size: 11px;
}

.input-wrap {
  position: relative;
}

.clear {
  position: absolute;
  right: 4px;
  top: 50%;
  transform: translateY(-50%);
  width: 26px;
  height: 26px;
  display: grid;
  place-items: center;
  background: transparent;
  border: 0;
  border-radius: var(--radius-sm);
  color: var(--text-muted);
  font-size: 18px;
  line-height: 1;
  cursor: pointer;
}

.clear:hover {
  background: var(--surface-2);
  color: var(--text-primary);
}

.hint.resolved {
  color: var(--text-secondary);
}

.resolved-name {
  font-weight: 500;
  color: var(--text-primary);
}
</style>
