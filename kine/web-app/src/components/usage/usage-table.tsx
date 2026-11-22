import { Fragment, useCallback, useEffect, useState } from 'react'
import type { Column, Group, Row, ViewMode } from './helpers'
import { fmt, fmtCost, fmtTime } from './helpers'

function SortIcon({ field, sortBy, sortOrder }: { field: string; sortBy: string; sortOrder: 'asc' | 'desc' }) {
  if (sortBy !== field) return <span className="ml-1 opacity-20">↕</span>
  return <span className="ml-1">{sortOrder === 'asc' ? '↑' : '↓'}</span>
}

function ValueCells({ item, viewMode }: { item: Row | Group['summary']; viewMode: ViewMode }) {
  if (viewMode === 'tokens') {
    return (
      <>
        <td className="px-6 py-3 text-right text-text-muted">{fmt(item.promptTokens)}</td>
        <td className="px-6 py-3 text-right text-text-muted">{item.cachedTokens ? fmt(item.cachedTokens) : '—'}</td>
        <td className="px-6 py-3 text-right text-text-muted">{fmt(item.completionTokens)}</td>
        <td className="px-6 py-3 text-right font-medium">{fmt(item.totalTokens)}</td>
      </>
    )
  }
  return (
    <>
      <td className="px-6 py-3 text-right text-text-muted">{fmtCost(item.inputCost)}</td>
      <td className="px-6 py-3 text-right text-text-muted">{item.cachedCost ? fmtCost(item.cachedCost) : '—'}</td>
      <td className="px-6 py-3 text-right text-text-muted">{fmtCost(item.outputCost)}</td>
      <td className="px-6 py-3 text-right font-medium text-warning">{fmtCost(item.totalCost || item.cost)}</td>
    </>
  )
}

const VALUE_COLUMNS = {
  tokens: [
    { field: 'promptTokens', label: 'Input Tokens' },
    { field: 'cachedTokens', label: 'Cached' },
    { field: 'completionTokens', label: 'Output Tokens' },
    { field: 'totalTokens', label: 'Total Tokens' },
  ],
  costs: [
    { field: 'promptTokens', label: 'Input Cost' },
    { field: 'cachedCost', label: 'Cached Cost' },
    { field: 'completionTokens', label: 'Output Cost' },
    { field: 'cost', label: 'Total Cost' },
  ],
} as const

/**
 * Sortable usage table with expandable group rows, mirroring KineRouter's
 * UsageTable.js: a summary row per group and detail rows on expand, with the
 * expanded set persisted in localStorage.
 */
export function UsageTable({
  title, columns, groupedData, tableType, sortBy, sortOrder, onToggleSort, viewMode,
  storageKey, renderDetailCells, renderSummaryCells, emptyMessage,
}: {
  title?: string
  columns: Column[]
  groupedData: Group[]
  tableType: string
  sortBy: string
  sortOrder: 'asc' | 'desc'
  onToggleSort: (table: string, field: string) => void
  viewMode: ViewMode
  storageKey: string
  renderDetailCells: (item: Row) => React.ReactNode
  renderSummaryCells: (group: Group) => React.ReactNode
  emptyMessage: string
}) {
  const [expanded, setExpanded] = useState<Set<string>>(new Set())

  useEffect(() => {
    try {
      const saved = localStorage.getItem(storageKey)
      if (saved) setExpanded(new Set(JSON.parse(saved) as string[]))
    } catch { /* ignore */ }
  }, [storageKey])

  useEffect(() => {
    try {
      localStorage.setItem(storageKey, JSON.stringify([...expanded]))
    } catch { /* ignore */ }
  }, [expanded, storageKey])

  const toggleGroup = useCallback((key: string) => {
    setExpanded((prev) => {
      const next = new Set(prev)
      if (next.has(key)) next.delete(key)
      else next.add(key)
      return next
    })
  }, [])

  const valueColumns = VALUE_COLUMNS[viewMode]

  return (
    <div className="overflow-hidden rounded-lg border border-border bg-card">
      {title && <div className="border-b border-border bg-bg-alt/50 p-4 text-sm font-semibold">{title}</div>}
      <div className="overflow-x-auto">
        <table className="w-full text-left text-sm">
          <thead className="bg-bg-alt text-xs uppercase text-text-muted">
            <tr>
              {columns.map((col) => (
                <th
                  key={col.field}
                  className={`cursor-pointer px-6 py-3 hover:bg-surface-2 ${col.align === 'right' ? 'text-right' : ''}`}
                  onClick={() => onToggleSort(tableType, col.field)}
                >
                  {col.label}
                  <SortIcon field={col.field} sortBy={sortBy} sortOrder={sortOrder} />
                </th>
              ))}
              {valueColumns.map((col) => (
                <th
                  key={col.field}
                  className="cursor-pointer px-6 py-3 text-right hover:bg-surface-2"
                  onClick={() => onToggleSort(tableType, col.field)}
                >
                  {col.label}
                  <SortIcon field={col.field} sortBy={sortBy} sortOrder={sortOrder} />
                </th>
              ))}
            </tr>
          </thead>
          <tbody className="divide-y divide-border">
            {groupedData.map((group) => (
              <Fragment key={group.groupKey}>
                <tr
                  className="cursor-pointer transition-colors hover:bg-surface-2/50"
                  onClick={() => toggleGroup(group.groupKey)}
                >
                  <td className="px-6 py-3">
                    <div className="flex items-center gap-2">
                      <span
                        className={`material-symbols-outlined text-[18px] text-text-muted transition-transform ${
                          expanded.has(group.groupKey) ? 'rotate-90' : ''
                        }`}
                      >
                        chevron_right
                      </span>
                      <span className={`font-medium ${group.summary.pending > 0 ? 'text-primary' : ''}`}>
                        {group.groupKey}
                      </span>
                    </div>
                  </td>
                  {renderSummaryCells(group)}
                  <ValueCells item={group.summary} viewMode={viewMode} />
                </tr>
                {expanded.has(group.groupKey) &&
                  group.items.map((item) => (
                    <tr key={`detail-${item.key}`} className="transition-colors hover:bg-surface-2/20">
                      {renderDetailCells(item)}
                      <ValueCells item={item} viewMode={viewMode} />
                    </tr>
                  ))}
              </Fragment>
            ))}
            {groupedData.length === 0 && (
              <tr>
                <td colSpan={columns.length + 4} className="px-6 py-8 text-center text-text-muted">
                  {emptyMessage}
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </div>
  )
}

/** Placeholder cells for the summary row (account/model/key/endpoint columns). */
export function SummaryMetaCells({ cols, group }: { cols: number; group: Group }) {
  return (
    <>
      <td className="px-6 py-3 text-text-muted">—</td>
      {cols > 1 && (
        <td className="px-6 py-3 text-right">{fmt(group.summary.requests)}</td>
      )}
      {cols > 2 && (
        <td className="whitespace-nowrap px-6 py-3 text-right text-text-muted">{fmtTime(group.summary.lastUsed)}</td>
      )}
    </>
  )
}
