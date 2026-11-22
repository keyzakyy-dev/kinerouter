/** Bulk-selection helpers, ported from src/shared/utils/proxyPoolSelection.js. */

export type SelectablePool = { id: string; isActive?: boolean }

export function filterProxyPools<T extends SelectablePool>(pools: T[], status: string): T[] {
  if (status === 'enabled') return pools.filter((p) => p.isActive === true)
  if (status === 'disabled') return pools.filter((p) => p.isActive !== true)
  return pools
}

/** Bulk actions must never include rows hidden by the current filter. */
export function getVisibleProxyPoolSelection<T extends SelectablePool>(
  pools: T[],
  selectedIds: string[],
): string[] {
  const selected = new Set(selectedIds)
  return pools.filter((p) => selected.has(p.id)).map((p) => p.id)
}

export function updateProxyPoolSelection<T extends SelectablePool>(
  pools: T[],
  selectedIds: string[],
  { id, anchorId, checked, shiftKey }: {
    id: string
    anchorId: string | null
    checked: boolean
    shiftKey: boolean
  },
): string[] {
  const ids = pools.map((p) => p.id)
  const selected = new Set(getVisibleProxyPoolSelection(pools, selectedIds))
  const index = ids.indexOf(id)
  if (index === -1) return [...selected]
  const anchor = shiftKey && anchorId ? ids.indexOf(anchorId) : -1
  const range = anchor === -1 ? [id] : ids.slice(Math.min(anchor, index), Math.max(anchor, index) + 1)
  for (const target of range) {
    if (checked) selected.add(target)
    else selected.delete(target)
  }
  return ids.filter((target) => selected.has(target))
}
