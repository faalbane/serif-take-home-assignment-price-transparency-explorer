import type {
  ApiErrorBody,
  CodeSummary,
  IngestStatus,
  MetaResponse,
  ProviderHit,
  RatesResponse,
  SearchParams,
} from './types'

/** ApiError carries the server's structured error so the UI can show the
 *  field, the detail and the recovery hints rather than a generic failure. */
export class ApiError extends Error {
  readonly status: number
  readonly detail: string
  readonly hints: string[]
  /** notReady marks a 503 during ingest, which the UI handles as "come back
   *  shortly" rather than as a failure. */
  readonly notReady: boolean

  constructor(status: number, body: Partial<ApiErrorBody>, fallback: string) {
    super(body.error || fallback)
    this.name = 'ApiError'
    this.status = status
    this.detail = body.detail ?? ''
    this.hints = body.hints ?? []
    this.notReady = status === 503
  }
}

const TIMEOUT_MS = 30_000

async function request<T>(path: string, signal?: AbortSignal): Promise<T> {
  // Compose the caller's signal with a timeout so a hung request cannot leave
  // a spinner running forever.
  const timeout = new AbortController()
  const timer = setTimeout(() => timeout.abort(new DOMException('Request timed out', 'TimeoutError')), TIMEOUT_MS)
  const signals = [timeout.signal, ...(signal ? [signal] : [])]
  const combined = typeof AbortSignal.any === 'function' ? AbortSignal.any(signals) : timeout.signal

  try {
    const res = await fetch(path, { signal: combined, headers: { Accept: 'application/json' } })

    if (!res.ok) {
      let body: Partial<ApiErrorBody> = {}
      try {
        body = await res.json()
      } catch {
        // A non-JSON error body (a proxy page, say) is still an error; fall
        // through to the status-based message.
      }
      throw new ApiError(res.status, body, `Request failed with status ${res.status}`)
    }
    return (await res.json()) as T
  } catch (err) {
    if (err instanceof ApiError) throw err
    if (err instanceof DOMException && err.name === 'AbortError') throw err
    if (err instanceof DOMException && err.name === 'TimeoutError') {
      throw new ApiError(0, { error: 'The request timed out', detail: 'The server did not respond within 30 seconds.' }, 'Timed out')
    }
    throw new ApiError(0, {
      error: 'Cannot reach the API',
      detail: err instanceof Error ? err.message : String(err),
      hints: ['Is the Go server running? Start it with `make backend` or `go run ./cmd/server`.'],
    }, 'Network error')
  } finally {
    clearTimeout(timer)
  }
}

/** toQuery drops empty values so the URL only carries parameters that are
 *  actually set -- which is what makes a shared link readable. */
export function toQuery(params: Partial<SearchParams>): URLSearchParams {
  const qs = new URLSearchParams()
  for (const [key, value] of Object.entries(params)) {
    if (value === undefined || value === null) continue
    const s = String(value).trim()
    if (s === '') continue
    // Defaults are implicit; leaving them out keeps shared URLs short.
    if (key === 'page' && s === '1') continue
    if (key === 'page_size' && s === '50') continue
    if (key === 'sort' && s === 'rate_asc') continue
    qs.set(key, s)
  }
  return qs
}

export const api = {
  health: (signal?: AbortSignal) => request<IngestStatus>('/api/health', signal),

  meta: (signal?: AbortSignal) => request<MetaResponse>('/api/meta', signal),

  rates: (params: Partial<SearchParams>, signal?: AbortSignal) =>
    request<RatesResponse>(`/api/rates?${toQuery(params).toString()}`, signal),

  codes: (q: string, limit = 12, signal?: AbortSignal) =>
    request<{ query: string; results: CodeSummary[] }>(
      `/api/codes?${new URLSearchParams({ q, limit: String(limit) }).toString()}`,
      signal,
    ),

  providers: (
    args: { npi?: string; ein?: string; q?: string; limit?: number },
    signal?: AbortSignal,
  ) => {
    const qs = new URLSearchParams()
    if (args.npi) qs.set('npi', args.npi)
    if (args.ein) qs.set('ein', args.ein)
    if (args.q) qs.set('q', args.q)
    qs.set('limit', String(args.limit ?? 10))
    return request<{ results: ProviderHit[] }>(`/api/providers?${qs.toString()}`, signal)
  },

  /** csvUrl is a plain link target rather than a fetch, so the browser's own
   *  download machinery handles a large export. */
  csvUrl: (params: Partial<SearchParams>) => {
    const qs = toQuery(params)
    qs.delete('page')
    qs.delete('page_size')
    return `/api/rates.csv?${qs.toString()}`
  },
}
