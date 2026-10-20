import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent } from 'vue'
import { mount, flushPromises, enableAutoUnmount } from '@vue/test-utils'
import { api } from '../api/client'
import type { IngestStatus, MetaResponse } from '../api/types'
import { useIngest } from './useIngest'

/** The cold-start gate. Ingest takes ~14s on a cold cache, so the UI cannot
 *  assume the data is there; these pin the polling behaviour that is only
 *  visible when it goes wrong. */

enableAutoUnmount(afterEach)

beforeEach(() => {
  vi.restoreAllMocks()
  vi.useFakeTimers()
})

afterEach(() => {
  vi.runOnlyPendingTimers()
  vi.useRealTimers()
})

const status = (over: Partial<IngestStatus> = {}): IngestStatus => ({
  phase: 'downloading_rate_files',
  message: 'Downloading in-network rate files.',
  ready: false,
  elapsed_seconds: 1,
  index_url: 'https://example.test/index.json',
  files: [],
  ...over,
})

const meta = (): MetaResponse => ({
  index_url: 'https://example.test/index.json',
  counts: { rows: 581860, items: 2696, distinct_codes: 1308, provider_groups: 14865, distinct_npis: 58691, distinct_eins: 7087, sources: 2 },
  sources: [],
  plans: [],
  facet_domains: { billing_classes: [], settings: [], negotiated_types: [], billing_code_types: [], negotiation_arrangements: [] },
  ingest: status({ ready: true, phase: 'ready' }),
})

/** Mount the composable inside a host component so lifecycle hooks run. */
function host() {
  let exposed!: ReturnType<typeof useIngest>
  const Comp = defineComponent({
    setup() {
      exposed = useIngest()
      return () => null
    },
  })
  const wrapper = mount(Comp)
  return { wrapper, get: () => exposed }
}

async function tick(ms = 800) {
  await vi.advanceTimersByTimeAsync(ms)
  await flushPromises()
}

/** Polls back off to 1500ms once a request has failed, so failure paths need
 *  a longer tick than the 600ms happy-path interval. */
const FAIL_INTERVAL = 1600

describe('polling until ready', () => {
  it('keeps polling while ingest is in progress, then loads meta once', async () => {
    const health = vi.spyOn(api, 'health')
    health.mockResolvedValueOnce(status({ phase: 'fetching_index' }))
    health.mockResolvedValueOnce(status({ phase: 'parsing_rate_files' }))
    health.mockResolvedValue(status({ ready: true, phase: 'ready' }))
    const metaSpy = vi.spyOn(api, 'meta').mockResolvedValue(meta())

    const { get } = host()
    await flushPromises()
    await tick()
    await tick()
    await tick()

    expect(health.mock.calls.length).toBeGreaterThanOrEqual(3)
    expect(metaSpy).toHaveBeenCalledTimes(1)
    expect(get().meta.value?.counts.rows).toBe(581860)
    expect(get().loading.value).toBe(false)
  })

  it('stops polling once ready, since it is a startup gate and not a heartbeat', async () => {
    const health = vi.spyOn(api, 'health').mockResolvedValue(status({ ready: true, phase: 'ready' }))
    vi.spyOn(api, 'meta').mockResolvedValue(meta())

    host()
    await flushPromises()
    await tick()
    const callsWhenReady = health.mock.calls.length

    await tick(5000)
    expect(health.mock.calls.length).toBe(callsWhenReady)
  })
})

describe('failure', () => {
  it('surfaces an ingest failure without hanging on the loading state', async () => {
    vi.spyOn(api, 'health').mockResolvedValue(
      status({ phase: 'failed', error: 'Could not read the index file: 404', message: 'failed' }),
    )
    const { get } = host()
    await flushPromises()
    await tick()

    expect(get().error.value).toContain('404')
    expect(get().loading.value).toBe(false)
  })

  it('tolerates a couple of unreachable polls before calling it an error', async () => {
    // The server may simply not be listening yet.
    const health = vi.spyOn(api, 'health').mockRejectedValue(new Error('ECONNREFUSED'))
    const { get } = host()
    await flushPromises()
    await tick(FAIL_INTERVAL)
    expect(get().error.value).toBeNull()

    await tick(FAIL_INTERVAL)
    await tick(FAIL_INTERVAL)
    expect(health.mock.calls.length).toBeGreaterThanOrEqual(3)
    expect(get().error.value).not.toBeNull()
  })
})

describe('refresh', () => {
  it('recovers after a failure', async () => {
    const health = vi.spyOn(api, 'health')
    health.mockRejectedValue(new Error('down'))
    vi.spyOn(api, 'meta').mockResolvedValue(meta())

    const { get } = host()
    await flushPromises()
    await tick(FAIL_INTERVAL)
    await tick(FAIL_INTERVAL)
    await tick(FAIL_INTERVAL)
    expect(get().error.value).not.toBeNull()

    health.mockResolvedValue(status({ ready: true, phase: 'ready' }))
    await get().refresh()
    await flushPromises()

    expect(get().error.value).toBeNull()
    expect(get().meta.value).not.toBeNull()
  })

  it('does not leave a second poll chain running', async () => {
    // clearTimeout cannot cancel a poll already awaiting its request, so each
    // retry used to start another permanent chain polling forever.
    const health = vi.spyOn(api, 'health').mockRejectedValue(new Error('down'))
    const { get } = host()
    await flushPromises()
    await tick(FAIL_INTERVAL)
    await tick(FAIL_INTERVAL)
    await tick(FAIL_INTERVAL)

    void get().refresh()
    void get().refresh()
    void get().refresh()
    await flushPromises()

    const before = health.mock.calls.length
    await tick(1600)
    const added = health.mock.calls.length - before

    // One chain means roughly one call per interval, not one per retry.
    expect(added).toBeLessThanOrEqual(2)
  })
})
