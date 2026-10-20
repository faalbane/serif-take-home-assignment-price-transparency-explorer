import { onMounted, ref, watch } from 'vue'

export type Theme = 'light' | 'dark' | 'system'

const STORAGE_KEY = 'pte-theme'

/** useTheme stamps data-theme on <html>, which is what the CSS theme scopes
 *  key off. "system" removes the attribute and lets prefers-color-scheme win. */
export function useTheme() {
  const theme = ref<Theme>('system')

  const apply = (value: Theme) => {
    const root = document.documentElement
    if (value === 'system') root.removeAttribute('data-theme')
    else root.setAttribute('data-theme', value)
  }

  onMounted(() => {
    try {
      const saved = localStorage.getItem(STORAGE_KEY)
      if (saved === 'light' || saved === 'dark' || saved === 'system') theme.value = saved
    } catch {
      // Private browsing or blocked storage: the default is fine.
    }
    apply(theme.value)
  })

  watch(theme, (value) => {
    apply(value)
    try {
      localStorage.setItem(STORAGE_KEY, value)
    } catch {
      // Not being able to remember the preference is not worth an error.
    }
  })

  const cycle = () => {
    theme.value = theme.value === 'system' ? 'light' : theme.value === 'light' ? 'dark' : 'system'
  }

  return { theme, cycle }
}
