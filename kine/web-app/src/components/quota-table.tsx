import { useEffect, useMemo, useState } from 'react'
import { Icon } from '@/components/icon'
import { cn } from '@/lib/utils'
import { formatResetTime, getRemainingPercentage, type QuotaRow } from '@/lib/quota'

const PAGE_SIZE = 10

/** "Today, 12:00 PM" / "Tomorrow, 9:05 AM" / "Dec 3, 7:40 AM". */
function formatResetTimeDisplay(resetTime: string | null | undefined): string | null {
  if (!resetTime) return null
  const date = new Date(resetTime)
  if (Number.isNaN(date.getTime())) return null
  const now = new Date()
  const today = new Date(now.getFullYear(), now.getMonth(), now.getDate())
  const tomorrow = new Date(today)
  tomorrow.setDate(tomorrow.getDate() + 1)

  let day: string
  if (date >= today && date < tomorrow) day = 'Today'
  else if (date >= tomorrow && date < new Date(tomorrow.getTime() + 86_400_000)) day = 'Tomorrow'
  else day = date.toLocaleDateString('en-US', { month: 'short', day: 'numeric' })

  const time = date.toLocaleTimeString('en-US', { hour: 'numeric', minute: '2-digit', hour12: true })
  return `${day}, ${time}`
}

function colorClasses(remaining: number) {
  if (remaining > 70)
    return { text: 'text-green-600', bg: 'bg-green-500', bgLight: 'bg-green-500/10', emoji: '🟢' }
  if (remaining >= 30)
    return { text: 'text-yellow-600', bg: 'bg-yellow-500', bgLight: 'bg-yellow-500/10', emoji: '🟡' }
  return { text: 'text-red-600', bg: 'bg-red-500', bgLight: 'bg-red-500/10', emoji: '🔴' }
}

const CREDIT_COLORS = {
  text: 'text-blue-600',
  bg: 'bg-blue-500',
  bgLight: 'bg-blue-500/10',
  emoji: '💰',
}

/**
 * Per-account quota rows, ported from ProviderLimits/QuotaTable.js: name,
 * remaining bar, used/total, reset countdown, and a hide action.
 */
export function QuotaTable({
  quotas = [],
  compact = false,
  sortMode = 'default',
  showSortLabel = false,
  onHideQuota,
}: {
  quotas?: QuotaRow[]
  compact?: boolean
  sortMode?: string
  showSortLabel?: boolean
  onHideQuota?: ((quota: QuotaRow) => void) | null
}) {
  const [page, setPage] = useState(1)

  const normalized = useMemo(
    () => quotas.map((q, index) => ({ ...q, index, remaining: getRemainingPercentage(q) })),
    [quotas],
  )

  const sorted = useMemo(() => {
    if (sortMode === 'remaining-asc')
      return [...normalized].sort((a, b) => a.remaining - b.remaining || a.name.localeCompare(b.name))
    if (sortMode === 'remaining-desc')
      return [...normalized].sort((a, b) => b.remaining - a.remaining || a.name.localeCompare(b.name))
    return normalized
  }, [normalized, sortMode])

  const totalPages = Math.max(1, Math.ceil(sorted.length / PAGE_SIZE))

  useEffect(() => setPage(1), [sortMode, quotas])
  useEffect(() => setPage((p) => Math.min(p, totalPages)), [totalPages])

  if (!quotas.length) return null

  const rows = sorted.slice((page - 1) * PAGE_SIZE, page * PAGE_SIZE)
  const pageStart = sorted.length === 0 ? 0 : (page - 1) * PAGE_SIZE + 1
  const pageEnd = Math.min(page * PAGE_SIZE, sorted.length)

  const cellPad = compact ? 'py-1 px-1.5' : 'py-2 px-3'
  const nameText = compact ? 'text-[11px]' : 'text-sm'
  const resetPrimary = compact ? 'text-[11px]' : 'text-sm'
  const resetSecondary = compact ? 'text-[10px] leading-tight' : 'text-xs'
  const canHide = typeof onHideQuota === 'function'

  return (
    <div className="space-y-2">
      <div className="flex items-center justify-between gap-2">
        <div className="text-[10px] text-text-muted">
          {sorted.length} quota{sorted.length > 1 ? 's' : ''}
        </div>
        {showSortLabel && (
          <div className="rounded-md border border-black/10 bg-black/[0.02] px-2 py-1 text-[10px] text-text-muted">
            Sorted by account remaining
          </div>
        )}
      </div>

      <div className="space-y-px">
        {rows.map((quota) => {
          const isUnlimited = quota.unlimited === true
          const isCredit = quota.isCreditBalance === true
          const colors = isCredit
            ? CREDIT_COLORS
            : colorClasses(quota.colorPercentage ?? quota.remaining)
          const countdown = quota.neverExpires ? '-' : formatResetTime(quota.resetAt)
          const seeDetails = !quota.neverExpires && !quota.resetAt && Boolean(quota.detailUrl)
          const resetDisplay = quota.emptyMessage
            ? quota.emptyMessage
            : quota.neverExpires
              ? 'Never expires'
              : seeDetails
                ? 'See details'
                : formatResetTimeDisplay(quota.resetAt)
          const resetSecondaryText = seeDetails && quota.detailUrl ? quota.detailUrl : resetDisplay
          const recurring = quota.recurring !== false
          const countdownLabel = recurring ? `in ${countdown}` : `expires in ${countdown}`

          const resetCell = (className: string) =>
            seeDetails ? (
              <a
                href={quota.detailUrl ?? '#'}
                target="_blank"
                rel="noopener noreferrer"
                className={cn(
                  className,
                  'inline-flex items-center gap-0.5 hover:text-text-main hover:underline',
                )}
                title={`Open usage details: ${resetSecondaryText}`}
                aria-label={`Open usage details for ${quota.name}`}
              >
                {resetDisplay}
                <Icon name="open_in_new" className="text-[12px]" />
              </a>
            ) : (
              resetDisplay
            )

          return (
            <div
              key={`${quota.name}-${quota.index}`}
              className={cn(
                'flex items-center gap-2 border-b border-black/5 transition-colors hover:bg-black/[0.02]',
                cellPad,
              )}
            >
              <div className="flex w-36 min-w-0 items-center gap-1.5">
                <span className="shrink-0 text-[10px]">{colors.emoji}</span>
                <span className={cn(nameText, 'truncate font-medium text-text-main')}>{quota.name}</span>
              </div>

              <div className={cn('min-w-0 flex-1', compact ? 'space-y-1' : 'space-y-1.5')}>
                {!isUnlimited && !isCredit && (
                  <div
                    className={cn(
                      'overflow-hidden rounded-full border',
                      compact ? 'h-1' : 'h-1.5',
                      colors.bgLight,
                      quota.remaining === 0 ? 'border-black/10' : 'border-transparent',
                    )}
                  >
                    <div
                      className={cn('h-full transition-all duration-300', colors.bg)}
                      style={{ width: `${Math.min(quota.remaining, 100)}%` }}
                    />
                  </div>
                )}

                <div
                  className={cn(
                    'flex min-w-0 items-center justify-between gap-1',
                    compact ? 'text-[10px]' : 'text-xs',
                  )}
                >
                  <span
                    className="truncate text-text-muted"
                    title={
                      isUnlimited
                        ? `${quota.used.toLocaleString()} used · Unlimited`
                        : isCredit
                          ? `Credit balance: ${quota.total.toFixed(2)} ${quota.currency || ''}`
                          : `${quota.used.toLocaleString()} / ${quota.total > 0 ? quota.total.toLocaleString() : '∞'}`
                    }
                  >
                    {isUnlimited
                      ? `${quota.used.toLocaleString()} used · Unlimited`
                      : isCredit
                        ? `Credit: ${quota.total.toFixed(2)} ${quota.currency || ''}`
                        : `${quota.used.toLocaleString()} / ${quota.total > 0 ? quota.total.toLocaleString() : '∞'}${
                            quota.unit ? ` ${quota.unit}` : ''
                          }`}
                  </span>
                  <span
                    className={cn(
                      'shrink-0 font-medium',
                      isUnlimited ? 'text-green-600' : isCredit ? 'text-blue-600' : colors.text,
                    )}
                  >
                    {isUnlimited ? 'Unlimited' : isCredit ? '' : `${quota.remaining}%`}
                  </span>
                </div>
              </div>

              <div className="min-w-0 shrink">
                {countdown !== '-' || resetDisplay ? (
                  compact ? (
                    <div
                      className={cn(resetPrimary, 'truncate font-medium text-text-main')}
                      title={resetSecondaryText || ''}
                    >
                      {countdown !== '-' ? countdownLabel : resetCell(resetPrimary)}
                    </div>
                  ) : (
                    <div className="min-w-0 space-y-0.5">
                      {countdown !== '-' && (
                        <div className={cn(resetPrimary, 'truncate font-medium text-text-main')}>
                          {countdownLabel}
                        </div>
                      )}
                      {resetDisplay && (
                        <div className={cn(resetSecondary, 'truncate text-text-muted')}>
                          {resetCell(resetSecondary)}
                        </div>
                      )}
                    </div>
                  )
                ) : (
                  <div className={cn(resetPrimary, 'italic text-text-muted')}>N/A</div>
                )}
              </div>

              {canHide && (
                <button
                  type="button"
                  onClick={() => onHideQuota?.(quota)}
                  className="inline-flex size-6 shrink-0 cursor-pointer items-center justify-center rounded-md text-text-muted transition-colors hover:bg-black/5 hover:text-text-main"
                  title="Hide this quota row"
                  aria-label={`Hide quota ${quota.name}`}
                >
                  <Icon name="visibility_off" className="text-[15px]" />
                </button>
              )}
            </div>
          )
        })}
      </div>

      {totalPages > 1 && (
        <div className="rounded-md border border-black/10 bg-black/[0.02] px-2 py-1.5">
          <div className="flex items-center justify-between gap-2 text-[10px] text-text-muted">
            <span>
              Showing {pageStart}-{pageEnd} of {sorted.length}
            </span>
            <span>
              Page {page} / {totalPages}
            </span>
          </div>
          <div className="mt-1.5 flex items-center justify-end gap-1">
            <button
              type="button"
              onClick={() => setPage((p) => Math.max(1, p - 1))}
              disabled={page === 1}
              className="flex h-6 cursor-pointer items-center rounded-md border border-black/10 px-2 text-[10px] text-text-main transition-colors hover:bg-black/5 disabled:cursor-not-allowed disabled:opacity-40"
            >
              Prev
            </button>
            <button
              type="button"
              onClick={() => setPage((p) => Math.min(totalPages, p + 1))}
              disabled={page === totalPages}
              className="flex h-6 cursor-pointer items-center rounded-md border border-black/10 px-2 text-[10px] text-text-main transition-colors hover:bg-black/5 disabled:cursor-not-allowed disabled:opacity-40"
            >
              Next
            </button>
          </div>
        </div>
      )}
    </div>
  )
}
