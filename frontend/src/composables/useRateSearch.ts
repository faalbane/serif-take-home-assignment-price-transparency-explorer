import { computed, ref } from 'vue'
import { api, ApiError } from '../api/client'
import type { RatesResponse, SearchParams } from '../api/types'
import { readParamsFromUrl, writeParamsToUrl } from './useUrlState'

export interface SearchError {
  message: string
  detail: string
  hints: string[]
  /** field names the server rejected, so the form can highlight them */
  field: string
}

/** useRateSearch owns the search lifecycle: parameters, the in-flight request,
 *  the last successful response, and URL synchronisation.
 *
 *  Requests are serialized by aborting the previous one. Without that, a user
 *  typing through a typeahead and hitting Enter can have an older, slower
 *  response land after a newer one and overwrite it. */
export function useRateSearch() {
  const params = ref<SearchParams>(readParamsFromUrl())
  const response = ref<RatesResponse | null>(null)
  const error = ref<SearchError | null>(null)
  const loading = ref(false)
  /** hasSearched distinguishes "nothing searched yet" from "searched, no hits",
   *  which need different empty states. */
  const hasSearched = ref(false)

  let inFlight: AbortController | null = null

  const hasResults = computed(() => (response.value?.results.length ?? 0) > 0)

  async function run(options: { pushUrl?: boolean } = {}) {
    const code = params.value.code.trim()
    if (!code) {
      // Clearing the code resets to the initial state rather than erroring:
      // the required field is enforced by the server, but the UI should not
      // scold someone for emptying an input.
      response.value = null
      error.value = null
      hasSearched.value = false
      writeParamsToUrl(params.value, true)
      return
    }

    inFlight?.abort()
    const controller = new AbortController()
    inFlight = controller

    loading.value = true
    error.value = null

    try {
      const res = await api.rates(params.value, controller.signal)
      if (controller.signal.aborted) return
      response.value = res
      hasSearched.value = true
      // Keep the URL in step with what is on screen.
      writeParamsToUrl(params.value, options.pushUrl === false)
    } catch (err) {
      if (err instanceof DOMException && err.name === 'AbortError') return
      if (err instanceof ApiError) {
        error.value = { message: err.message, detail: err.detail, hints: err.hints, field: err.message }
      } else {
        error.value = {
          message: 'Search failed',
          detail: err instanceof Error ? err.message : String(err),
          hints: [],
          field: '',
        }
      }
      hasSearched.value = true
    } finally {
      if (inFlight === controller) {
        loading.value = false
        inFlight = null
      }
    }
  }

  /** search runs a fresh query from page 1. */
  const search = () => {
    params.value.page = 1
    return run()
  }

  /** update applies a partial change and re-runs, resetting paging unless the
   *  change is itself a page change. */
  const update = (patch: Partial<SearchParams>) => {
    const keepPage = 'page' in patch
    params.value = { ...params.value, ...patch, ...(keepPage ? {} : { page: 1 }) }
    return run()
  }

  const reset = () => {
    const code = params.value.code
    params.value = { ...readParamsFromUrl(''), code }
    return run()
  }

  /** syncFromUrl re-reads the URL, for browser back/forward. */
  const syncFromUrl = () => {
    params.value = readParamsFromUrl()
    return params.value.code.trim() ? run({ pushUrl: false }) : Promise.resolve()
  }

  return { params, response, error, loading, hasSearched, hasResults, search, update, reset, run, syncFromUrl }
}
