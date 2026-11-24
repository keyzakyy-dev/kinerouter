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

export function readTheme(): Theme {
  const saved = localStorage.getItem(KEY)
  return saved === 'light' || saved === 'dark' || saved === 'system' ? saved : 'system'
}

export function applyTheme(theme: Theme) {
  const dark = theme === 'dark' || (theme === 'system' && systemDark())
  document.documentElement.classList.toggle('dark', dark)
}

// Applied once at startup: the class must survive a refresh on any route, not
// only while the settings page happens to mount the hook.
let systemListener = false
export function initTheme() {
  applyTheme(readTheme())
  if (systemListener) return
  systemListener = true
  window.matchMedia('(prefers-color-scheme: dark)').addEventListener('change', () => {
    if (readTheme() === 'system') applyTheme('system')
  })
}

export function useTheme() {
  const [theme, setThemeState] = useState<Theme>(readTheme)

  const setTheme = useCallback((next: Theme) => {
    localStorage.setItem(KEY, next)
    setThemeState(next)
    applyTheme(next)
  }, [])

  useEffect(() => { applyTheme(theme) }, [theme])

  return { theme, setTheme }
}
