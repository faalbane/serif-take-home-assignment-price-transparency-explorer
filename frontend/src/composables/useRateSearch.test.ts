import { beforeEach, describe, expect, it, vi } from 'vitest'
import { nextTick } from 'vue'
import { api, ApiError } from '../api/client'
import type { RatesResponse } from '../api/types'
import { useRateSearch } from './useRateSearch'

/** The search lifecycle is where the races are: requests are serialized by
 *  aborting the previous one, and the URL is the source of truth. These tests
 *  pin the behaviors that are invisible until they go wrong. */

function fakeResponse(code: string, total = 2): RatesResponse {
  return {
    query: { billing_code: code, sort: 'rate_asc', page: 1, page_size: 50 },
    pagination: { page: 1, page_size: 50, total, total_pages: 1, has_prev: false, has_next: false },
    stats: { count: total, min: 1, max: 2, mean: 1.5, median: 1.5, p25: 1.25, p75: 1.75, histogram: [] },
    facets: { billing_class: [], setting: [], billing_code_type: [], negotiated_type: [], source: [] },
    results: [],
    took_ms: 0.1,
  }
}

/** deferred lets a test control exactly when a request resolves. */
function deferred<T>() {
  let resolve!: (v: T) => void
  let reject!: (e: unknown) => void
  const promise = new Promise<T>((res, rej) => {
    resolve = res
    reject = rej
  })
  return { promise, resolve, reject }
}

beforeEach(() => {
  window.history.replaceState(null, '', '/')
  vi.restoreAllMocks()
})

describe('searching', () => {
  it('does not call the API when no code is set', async () => {
    const spy = vi.spyOn(api, 'rates')
    const s = useRateSearch()
    await s.search()
    expect(spy).not.toHaveBeenCalled()
    expect(s.hasSearched.value).toBe(false)
  })

  it('treats a whitespace-only code as empty', async () => {
    const spy = vi.spyOn(api, 'rates')
    const s = useRateSearch()
    s.params.value.code = '   '
    await s.search()
    expect(spy).not.toHaveBeenCalled()
  })

  it('stores the response and syncs the URL', async () => {
    vi.spyOn(api, 'rates').mockResolvedValue(fakeResponse('99213'))
    const s = useRateSearch()
    s.params.value.code = '99213'
    await s.search()

    expect(s.response.value?.query.billing_code).toBe('99213')
    expect(s.hasSearched.value).toBe(true)
    expect(s.loading.value).toBe(false)
    expect(window.location.search).toBe('?code=99213')
  })

  it('resets to page 1 on a fresh search', async () => {
    vi.spyOn(api, 'rates').mockResolvedValue(fakeResponse('99213'))
    const s = useRateSearch()
    s.params.value.code = '99213'
    s.params.value.page = 5
    await s.search()
    expect(s.params.value.page).toBe(1)
  })
})

describe('patch versus update', () => {
  it('patch records a value without searching', async () => {
    const spy = vi.spyOn(api, 'rates').mockResolvedValue(fakeResponse('9'))
    const s = useRateSearch()

    // Typing a five-character code used to issue five searches and five
    // history entries, which made the back button useless.
    for (const ch of ['9', '9', '2', '1', '3']) {
      s.patch({ code: s.params.value.code + ch })
    }
    await nextTick()

    expect(spy).not.toHaveBeenCalled()
    expect(s.params.value.code).toBe('99213')
  })

  it('update records a value and searches', async () => {
    const spy = vi.spyOn(api, 'rates').mockResolvedValue(fakeResponse('99213'))
    const s = useRateSearch()
    s.params.value.code = '99213'
    await s.update({ billing_class: 'professional' })

    expect(spy).toHaveBeenCalledTimes(1)
    expect(s.params.value.billing_class).toBe('professional')
  })

  it('update resets paging, except when the change is the page itself', async () => {
    vi.spyOn(api, 'rates').mockResolvedValue(fakeResponse('99213'))
    const s = useRateSearch()
    s.params.value.code = '99213'
    s.params.value.page = 4

    await s.update({ billing_class: 'professional' })
    expect(s.params.value.page).toBe(1)

    await s.update({ page: 3 })
    expect(s.params.value.page).toBe(3)
  })
})

describe('clearing the code', () => {
  it('aborts the in-flight request and resets, leaving loading false', async () => {
    const pending = deferred<RatesResponse>()
    vi.spyOn(api, 'rates').mockReturnValue(pending.promise)

    const s = useRateSearch()
    s.params.value.code = '99213'
    const inFlight = s.search()
    expect(s.loading.value).toBe(true)

    // Clear while the first request is still outstanding.
    s.params.value.code = ''
    await s.run()

    expect(s.loading.value).toBe(false)
    expect(s.response.value).toBeNull()
    expect(s.hasSearched.value).toBe(false)

    // The superseded response must not resurrect the old results.
    pending.resolve(fakeResponse('99213'))
    await inFlight
    await nextTick()
    expect(s.response.value).toBeNull()
    expect(s.hasSearched.value).toBe(false)
  })
})

describe('request serialization', () => {
  it('ignores a superseded response that resolves after a newer one', async () => {
    const first = deferred<RatesResponse>()
    const second = deferred<RatesResponse>()
    const spy = vi.spyOn(api, 'rates')
    spy.mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise)

    const s = useRateSearch()
    s.params.value.code = 'AAA'
    const a = s.search()
    s.params.value.code = 'BBB'
    const b = s.search()

    // The newer request lands first, then the older one.
    second.resolve(fakeResponse('BBB'))
    await b
    first.resolve(fakeResponse('AAA'))
    await a
    await nextTick()

    expect(s.response.value?.query.billing_code).toBe('BBB')
  })

  it('ignores an error from a superseded request', async () => {
    const first = deferred<RatesResponse>()
    const second = deferred<RatesResponse>()
    const spy = vi.spyOn(api, 'rates')
    spy.mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise)

    const s = useRateSearch()
    s.params.value.code = 'AAA'
    const a = s.search()
    s.params.value.code = 'BBB'
    const b = s.search()

    second.resolve(fakeResponse('BBB'))
    await b
    first.reject(new ApiError(400, { error: 'code', detail: 'stale failure' }, 'x'))
    await a
    await nextTick()

    expect(s.error.value).toBeNull()
    expect(s.response.value?.query.billing_code).toBe('BBB')
  })
})

describe('errors', () => {
  it('surfaces the server sentence rather than the field name', async () => {
    vi.spyOn(api, 'rates').mockRejectedValue(
      new ApiError(400, { error: 'min_rate', detail: 'min_rate cannot be greater than max_rate' }, 'x'),
    )
    const s = useRateSearch()
    s.params.value.code = '10006'
    await s.search()

    expect(s.error.value?.message).toBe('min_rate cannot be greater than max_rate')
    expect(s.error.value?.field).toBe('min_rate')
    expect(s.loading.value).toBe(false)
    expect(s.hasSearched.value).toBe(true)
  })

  it('clears a previous error on the next successful search', async () => {
    const spy = vi.spyOn(api, 'rates')
    spy.mockRejectedValueOnce(new ApiError(0, { error: 'Cannot reach the API' }, 'x'))
    spy.mockResolvedValueOnce(fakeResponse('99213'))

    const s = useRateSearch()
    s.params.value.code = '99213'
    await s.search()
    expect(s.error.value).not.toBeNull()

    await s.search()
    expect(s.error.value).toBeNull()
    expect(s.response.value).not.toBeNull()
  })
})

describe('syncFromUrl', () => {
  it('re-runs the search for a URL that carries a code', async () => {
    const spy = vi.spyOn(api, 'rates').mockResolvedValue(fakeResponse('0450'))
    const s = useRateSearch()
    window.history.replaceState(null, '', '/?code=0450&page=2')

    await s.syncFromUrl()

    expect(spy).toHaveBeenCalledTimes(1)
    // The page in the link must survive; search() would have reset it to 1.
    expect(s.params.value.page).toBe(2)
  })

  it('resets the view when navigating back to a URL with no code', async () => {
    vi.spyOn(api, 'rates').mockResolvedValue(fakeResponse('99213'))
    const s = useRateSearch()
    s.params.value.code = '99213'
    await s.search()
    expect(s.response.value).not.toBeNull()

    // Browser Back to the bare URL.
    window.history.replaceState(null, '', '/')
    await s.syncFromUrl()

    expect(s.response.value).toBeNull()
    expect(s.hasSearched.value).toBe(false)
    expect(s.params.value.code).toBe('')
  })
})

describe('reset', () => {
  it('clears the filters but keeps the code', async () => {
    vi.spyOn(api, 'rates').mockResolvedValue(fakeResponse('99213'))
    const s = useRateSearch()
    s.params.value.code = '99213'
    s.params.value.npi = '1003076902'
    s.params.value.billing_class = 'professional'

    await s.reset()

    expect(s.params.value.code).toBe('99213')
    expect(s.params.value.npi).toBe('')
    expect(s.params.value.billing_class).toBe('')
  })
})
