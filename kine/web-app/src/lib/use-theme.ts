import { useCallback, useEffect, useState } from 'react'

/**
 * Port of src/shared/hooks/useTheme.js + store/themeStore.js: `light` | `dark`
 * | `system`, persisted in localStorage, applied as `.dark` on <html>.
 */
export type Theme = 'light' | 'dark' | 'system'
const KEY = 'theme'

function systemDark() {
  return typeof window !== 'undefined' && window.matchMedia('(prefers-color-scheme: dark)').matches
}

function apply(theme: Theme) {
  const dark = theme === 'dark' || (theme === 'system' && systemDark())
  document.documentElement.classList.toggle('dark', dark)
}

export function useTheme() {
  const [theme, setThemeState] = useState<Theme>(() => {
    const saved = localStorage.getItem(KEY)
    return saved === 'light' || saved === 'dark' || saved === 'system' ? saved : 'system'
  })

  const setTheme = useCallback((next: Theme) => {
    localStorage.setItem(KEY, next)
    setThemeState(next)
    apply(next)
  }, [])

  useEffect(() => { apply(theme) }, [theme])

  // Only the "system" mode needs to follow the OS setting.
  useEffect(() => {
    if (theme !== 'system') return
    const mq = window.matchMedia('(prefers-color-scheme: dark)')
    const onChange = () => apply('system')
    mq.addEventListener('change', onChange)
    return () => mq.removeEventListener('change', onChange)
  }, [theme])

  return { theme, setTheme }
}
