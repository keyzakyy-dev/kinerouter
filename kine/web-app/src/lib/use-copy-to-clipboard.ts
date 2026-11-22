import { useCallback, useRef, useState } from 'react'

/**
 * Copy-to-clipboard with "Copied" feedback, ported from
 * src/shared/hooks/useCopyToClipboard.js. `copied` is the id of the last copy
 * (or null) so one page can show feedback on several buttons at once.
 */
export function useCopyToClipboard(resetDelay = 2000) {
  const [copied, setCopied] = useState<string | null>(null)
  const timeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  const copy = useCallback(
    (text: string, id = 'default') => {
      const write = async () => {
        if (navigator?.clipboard?.writeText) {
          await navigator.clipboard.writeText(text)
        } else {
          // Insecure origin / permission denied: fall back to a hidden textarea.
          const textarea = document.createElement('textarea')
          textarea.value = text
          textarea.style.position = 'fixed'
          textarea.style.opacity = '0'
          document.body.appendChild(textarea)
          textarea.select()
          document.execCommand('copy')
          document.body.removeChild(textarea)
        }
      }
      write().catch(() => {})
      setCopied(id)
      if (timeoutRef.current) clearTimeout(timeoutRef.current)
      timeoutRef.current = setTimeout(() => setCopied(null), resetDelay)
    },
    [resetDelay],
  )

  return { copied, copy }
}
