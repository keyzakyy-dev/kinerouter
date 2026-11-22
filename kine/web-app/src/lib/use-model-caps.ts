import { useEffect, useState } from 'react'
import { api, type ModelEntry } from '@/lib/api'
import { type Caps } from '@/components/capacity-badges'

/**
 * Port of src/shared/hooks/useModelCaps.js. `/api/models` carries `caps` per
 * routed model; this module-level cache maps both `provider/model` and the bare
 * model id so combo members and pickers can render badges without refetching.
 */
type CapsTable = { byFull: Record<string, Caps>; byId: Record<string, Caps> }

const EMPTY: CapsTable = { byFull: {}, byId: {} }
let cache: Promise<CapsTable> | null = null
let cached: CapsTable = EMPTY

function build(entries: ModelEntry[]): CapsTable {
  const byFull: Record<string, Caps> = {}
  const byId: Record<string, Caps> = {}
  for (const e of entries) {
    if (!e.caps) continue
    byFull[e.id] = e.caps
    const bare = e.id.includes('/') ? e.id.slice(e.id.indexOf('/') + 1) : e.id
    if (!byId[bare]) byId[bare] = e.caps
  }
  return { byFull, byId }
}

function load(): Promise<CapsTable> {
  cache ??= api
    .models()
    .then((r) => (cached = build(r.data)))
    .catch(() => (cache = null, EMPTY))
  return cache
}

/** Drop the cache after a custom-model edit (the reference's window event). */
export function invalidateModelCaps() {
  cache = null
}

export function useModelCaps(): CapsTable {
  const [table, setTable] = useState<CapsTable>(cached)
  useEffect(() => {
    let alive = true
    void load().then((t) => alive && setTable(t))
    return () => {
      alive = false
    }
  }, [])
  return table
}

/** Resolve caps for a routed model, combo member, or alias target. */
export function resolveCaps(
  table: CapsTable,
  model: string | undefined,
  aliases?: Record<string, string>,
): Caps | undefined {
  if (!model) return undefined
  const target = aliases?.[model] ?? model
  return table.byFull[target] ?? table.byId[target.includes('/') ? target.slice(target.indexOf('/') + 1) : target]
}
