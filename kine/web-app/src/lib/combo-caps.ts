import type { Caps } from '@/components/capacity-badges'

/**
 * Port of aggregateComboCapabilities (open-sse/providers/capabilities.js).
 * Union for input modalities, intersection for tools, first member wins for
 * reasoning/thinking, min context window, max output. Nested combos recurse.
 */
export type FullCaps = Caps & {
  pdf?: boolean
  audioInput?: boolean
  videoInput?: boolean
  tools?: boolean
  thinkingFormat?: string
}

const INPUT_KEYS = ['vision', 'pdf', 'audioInput', 'videoInput'] as const

export function aggregateComboCapabilities(
  models: string[],
  comboByName: Record<string, string[]>,
  getCaps: (model: string) => FullCaps | undefined,
  depth = 0,
): FullCaps | undefined {
  if (!models?.length || depth > 6) return undefined
  let out: FullCaps = { vision: false, pdf: false, audioInput: false, videoInput: false, tools: true, reasoning: false }
  let first = true
  let ctx = Number.POSITIVE_INFINITY
  let maxOut = 0

  for (const member of models) {
    const caps = comboByName[member]
      ? aggregateComboCapabilities(comboByName[member], comboByName, getCaps, depth + 1)
      : getCaps(member)
    if (!caps) continue

    for (const k of INPUT_KEYS) if (caps[k]) out[k] = true
    out.tools = out.tools && caps.tools !== false
    if (first) {
      out.reasoning = !!caps.reasoning
      out.thinkingFormat = caps.thinkingFormat
      first = false
    }
    if (caps.contextWindow && caps.contextWindow > 0) ctx = Math.min(ctx, caps.contextWindow)
    if (caps.maxOutput && caps.maxOutput > 0) maxOut = Math.max(maxOut, caps.maxOutput)
  }

  if (Number.isFinite(ctx)) out.contextWindow = ctx
  if (maxOut > 0) out.maxOutput = maxOut
  return out
}

/** `fmtK` from the reference page: 200000 → "200K", 1e6 → "1M". */
export function fmtK(n: number | undefined): string {
  if (!n || !Number.isFinite(n)) return '-'
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(n % 1_000_000 === 0 ? 0 : 1)}M`
  return `${Math.round(n / 1000)}K`
}
