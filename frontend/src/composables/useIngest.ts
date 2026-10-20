import { onUnmounted, ref } from 'vue'
import { api, ApiError } from '../api/client'
import type { IngestStatus, MetaResponse } from '../api/types'

/** useIngest owns the cold-start gate.
 *
 *  Ingest takes roughly 13 seconds on a cold cache (263 MB of rate files) and
 *  about 6 on a warm one, so the UI cannot assume the data is there. This polls
 *  /api/health until the dataset is ready, then loads /api/meta once. Polling
 *  stops as soon as it succeeds -- it is a startup gate, not a heartbeat. */
export function useIngest() {
  const status = ref<IngestStatus | null>(null)
  const meta = ref<MetaResponse | null>(null)
  const error = ref<string | null>(null)
  const loading = ref(true)

  let timer: ReturnType<typeof setTimeout> | undefined
  let stopped = false
  let consecutiveFailures = 0
  /** generation invalidates in-flight poll chains. clearTimeout cannot cancel
   *  a poll that is already awaiting its request, so without this every Retry
   *  click while a request was in flight started a second permanent chain. */
  let generation = 0

  const stop = () => {
    stopped = true
    if (timer) clearTimeout(timer)
  }

  const loadMeta = async () => {
    try {
      meta.value = await api.meta()
      error.value = null
    } catch (err) {
      // Meta failing after health reported ready is unexpected; surface it
      // rather than showing an empty app.
      error.value = err instanceof ApiError ? err.message : 'Could not load dataset metadata.'
    } finally {
      loading.value = false
    }
  }

  const poll = async (gen = generation) => {
    if (stopped || gen !== generation) return
    try {
      const s = await api.health()
      if (gen !== generation) return
      status.value = s
      consecutiveFailures = 0

      if (s.ready) {
        stop()
        await loadMeta()
        return
      }
      if (s.phase === 'failed') {
        // Ingest gave up. Keep polling slowly: the operator may restart it,
        // and a stuck banner with no recovery path is worse than a retry.
        error.value = s.error || s.message
        loading.value = false
        timer = setTimeout(() => poll(gen), 5000)
        return
      }
    } catch (err) {
      if (gen !== generation) return
      consecutiveFailures++
      // The server may simply not be listening yet. Only call it an error once
      // a few attempts in a row have failed.
      if (consecutiveFailures >= 3) {
        error.value =
          err instanceof ApiError
            ? [err.message, err.detail, ...err.hints].filter(Boolean).join(' ')
            : 'Cannot reach the API.'
        loading.value = false
      }
    }
    // Poll briskly during a cold start: the whole wait is seconds, not minutes.
    timer = setTimeout(() => poll(gen), consecutiveFailures > 0 ? 1500 : 600)
  }

  /** refresh re-reads health and meta, for the retry button. */
  const refresh = async () => {
    stopped = false
    generation++ // retire any chain already awaiting a response
    consecutiveFailures = 0
    error.value = null
    loading.value = true
    if (timer) clearTimeout(timer)
    await poll()
  }

  void poll()
  onUnmounted(stop)

  return { status, meta, error, loading, refresh }
}
