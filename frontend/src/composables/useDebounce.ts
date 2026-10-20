import { onUnmounted, ref, watch, type Ref } from 'vue'

/** useDebouncedRef mirrors a ref after it has been quiet for `delay` ms.
 *  Used for typeahead so a fast typist produces one request, not eight. */
export function useDebouncedRef<T>(source: Ref<T>, delay = 200): Ref<T> {
  const debounced = ref(source.value) as Ref<T>
  let timer: ReturnType<typeof setTimeout> | undefined

  watch(source, (value) => {
    if (timer) clearTimeout(timer)
    timer = setTimeout(() => {
      debounced.value = value
    }, delay)
  })

  onUnmounted(() => {
    if (timer) clearTimeout(timer)
  })

  return debounced
}
