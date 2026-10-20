import { emptySearchParams, type SearchParams } from '../api/types'

/** The search state lives in the URL query string.
 *
 *  That makes every result shareable and reloadable, and makes the browser's
 *  back button behave the way a user expects in a search tool. The URL is the
 *  single source of truth; the app reads it on load and writes it on search. */

const NUMERIC_KEYS = new Set<keyof SearchParams>(['page', 'page_size'])

export function readParamsFromUrl(search = window.location.search): SearchParams {
  const qs = new URLSearchParams(search)
  const params = emptySearchParams()

  for (const key of Object.keys(params) as (keyof SearchParams)[]) {
    const raw = qs.get(key)
    if (raw === null) continue
    if (NUMERIC_KEYS.has(key)) {
      const n = Number.parseInt(raw, 10)
      if (Number.isFinite(n) && n > 0) {
        ;(params[key] as number) = n
      }
      continue
    }
    ;(params[key] as string) = raw
  }

  // Guard against a hand-edited or stale URL carrying an unsupported sort.
  if (!['rate_asc', 'rate_desc', 'provider', 'npi_count'].includes(params.sort)) {
    params.sort = 'rate_asc'
  }
  if (params.page_size > 500) params.page_size = 500
  return params
}

export function writeParamsToUrl(params: SearchParams, replace = false) {
  const qs = new URLSearchParams()
  for (const [key, value] of Object.entries(params)) {
    const s = String(value ?? '').trim()
    if (s === '') continue
    if (key === 'page' && s === '1') continue
    if (key === 'page_size' && s === '50') continue
    if (key === 'sort' && s === 'rate_asc') continue
    qs.set(key, s)
  }
  const url = qs.toString() ? `${window.location.pathname}?${qs}` : window.location.pathname
  if (replace) window.history.replaceState(null, '', url)
  else window.history.pushState(null, '', url)
}
