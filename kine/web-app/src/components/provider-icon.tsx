import { useState } from 'react'
import { cn } from '@/lib/utils'

/**
 * Provider logo with a text fallback, ported from
 * src/shared/components/ProviderIcon.js. Icons ship in public/providers/ and a
 * missing file falls back to the two-letter badge instead of a broken image.
 */

/** Ported from src/shared/utils/providerIcon.js — brands sharing one mark. */
const ICON_ALIASES: Record<string, string> = {
  'perplexity-agent': 'perplexity',
  'gitlab-duo': 'gitlab',
  'vercel-ai-gateway': 'vercel',
  'ollama-search': 'ollama',
  'minimax-code': 'minimax',
  'minimax-code-global': 'minimax',
  'opencode-zen': 'opencode',
  'deepseek-web': 'deepseek',
  'muse-web': 'muse',
  'gemini-web': 'gemini',
  'chatgpt-web': 'openai',
}

export function ProviderIcon({
  provider,
  size = 32,
  className,
  fallbackText,
}: {
  provider: string
  size?: number
  className?: string
  fallbackText?: string
}) {
  const [errored, setErrored] = useState(false)
  const label = fallbackText ?? (provider || '?').slice(0, 2).toUpperCase()
  const iconId = ICON_ALIASES[provider] ?? provider

  if (errored) {
    return (
      <span
        className={cn('inline-flex items-center justify-center rounded-lg font-bold', className)}
        style={{ width: size, height: size, fontSize: Math.max(10, Math.floor(size * 0.38)) }}
      >
        {label}
      </span>
    )
  }

  return (
    <img
      src={`/providers/${iconId}.png`}
      alt={provider}
      width={size}
      height={size}
      className={className}
      loading="lazy"
      decoding="async"
      onError={() => setErrored(true)}
    />
  )
}
