import { useEffect, useMemo, useState } from 'react'
import { Icon } from '@/components/icon'
import { cn } from '@/lib/utils'
import { formatResetTime, getRemainingPercentage, type QuotaRow } from '@/lib/quota'

const PAGE_SIZE = 10

/** Monochrome ramp: the bar dims as the allowance drains. */
function meterTone(remaining: number) {
  if (remaining > 70) return 'bg-foreground'
  if (remaining >= 30) return 'bg-muted-foreground'
  return 'bg-muted-foreground/50'
}

/**
 * Per-account quota tiles: name, remaining meter, used/total, reset countdown,
 * and a hide action.
 */
export function QuotaTable({
  quotas = [],
  sortMode = 'default',
  showSortLabel = false,
  onHideQuota,
}: {
  quotas?: QuotaRow[]
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
  const canHide = typeof onHideQuota === 'function'

  return (
    <div className="space-y-2">
      {showSortLabel && (
        <div className="flex justify-end">
          <span className="rounded-md bg-foreground/5 px-2 py-1 text-[10px] text-text-muted">
            Sorted by account remaining
          </span>
        </div>
      )}

      <div className="grid grid-cols-1 gap-2 sm:grid-cols-2 xl:grid-cols-3">
        {rows.map((quota) => {
          const isUnlimited = quota.unlimited === true
          const isCredit = quota.isCreditBalance === true
          const tone = meterTone(quota.colorPercentage ?? quota.remaining)
          const countdown = quota.neverExpires ? '-' : formatResetTime(quota.resetAt)
          const seeDetails = !quota.neverExpires && !quota.resetAt && Boolean(quota.detailUrl)
          const recurring = quota.recurring !== false
          const countdownLabel = recurring ? `in ${countdown}` : `expires in ${countdown}`

          return (
            <div
              key={`${quota.name}-${quota.index}`}
              className="group/tile min-w-0 rounded-lg bg-foreground/[0.045] px-2.5 py-2"
            >
              <div className="flex items-baseline gap-1.5">
                <span className="truncate text-[11.5px] font-medium text-text-main">{quota.name}</span>
                {canHide && (
                  <button
                    type="button"
                    onClick={() => onHideQuota?.(quota)}
                    className="inline-flex size-4 shrink-0 cursor-pointer items-center justify-center rounded text-text-muted opacity-0 transition-opacity hover:text-text-main group-hover/tile:opacity-100 max-sm:opacity-100"
                    title="Hide this quota row"
                    aria-label={`Hide quota ${quota.name}`}
                  >
                    <Icon name="visibility_off" className="text-[12px]" />
                  </button>
                )}
                <span className="ml-auto shrink-0 text-right">
                  {isUnlimited ? (
                    <span className="text-[10px] font-medium uppercase tracking-wide text-muted-foreground">
                      Unlimited
                    </span>
                  ) : isCredit ? (
                    <span className="text-[13px] font-semibold tabular-nums text-text-main">
                      {quota.total.toFixed(2)}
                      <span className="ml-1 text-[10px] font-medium text-text-muted">{quota.currency || ''}</span>
                    </span>
                  ) : (
                    <span className="text-[13px] font-semibold tabular-nums text-text-main">{quota.remaining}%</span>
                  )}
                </span>
              </div>

              {!isUnlimited && !isCredit && (
                <div className="mt-1.5 h-[3px] overflow-hidden rounded-full bg-foreground/[0.07]">
                  <div
                    className={cn('h-full rounded-full transition-all duration-300', tone)}
                    style={{ width: `${Math.min(quota.remaining, 100)}%` }}
                  />
                </div>
              )}

              <div className="mt-1 flex min-w-0 items-center justify-between gap-2 text-[10px] text-text-muted">
                <span className="truncate tabular-nums">
                  {isUnlimited
                    ? `${quota.used.toLocaleString()} used`
                    : isCredit
                      ? 'Credit balance'
                      : `${quota.used.toLocaleString()} / ${quota.total > 0 ? quota.total.toLocaleString() : '∞'}${
                          quota.unit ? ` ${quota.unit}` : ''
                        }`}
                </span>
                {isCredit || quota.neverExpires ? (
                  <span className="shrink-0">{isCredit ? 'No reset' : 'Never expires'}</span>
                ) : countdown !== '-' ? (
                  <span className="inline-flex shrink-0 items-center gap-1 tabular-nums">
                    <Icon name="schedule" className="text-[11px]" />
                    {countdownLabel}
                  </span>
                ) : seeDetails && quota.detailUrl ? (
                  <a
                    href={quota.detailUrl}
                    target="_blank"
                    rel="noopener noreferrer"
                    className="inline-flex shrink-0 items-center gap-0.5 hover:text-text-main hover:underline"
                  >
                    See details
                    <Icon name="open_in_new" className="text-[11px]" />
                  </a>
                ) : quota.emptyMessage ? (
                  <span className="shrink-0">{quota.emptyMessage}</span>
                ) : (
                  <span className="shrink-0 italic">N/A</span>
                )}
              </div>
            </div>
          )
        })}
      </div>

      {totalPages > 1 && (
        <div className="flex items-center justify-between gap-2 pt-1 text-[10px] text-text-muted">
          <span>
            {pageStart}-{pageEnd} of {sorted.length}
          </span>
          <div className="flex items-center gap-1">
            <button
              type="button"
              onClick={() => setPage((p) => Math.max(1, p - 1))}
              disabled={page === 1}
              className="flex size-5 cursor-pointer items-center justify-center rounded text-text-main transition-colors hover:bg-foreground/5 disabled:cursor-not-allowed disabled:opacity-40"
              aria-label="Previous page"
            >
              <Icon name="chevron_left" className="text-[14px]" />
            </button>
            <span className="tabular-nums">
              {page} / {totalPages}
            </span>
            <button
              type="button"
              onClick={() => setPage((p) => Math.min(totalPages, p + 1))}
              disabled={page === totalPages}
              className="flex size-5 cursor-pointer items-center justify-center rounded text-text-main transition-colors hover:bg-foreground/5 disabled:cursor-not-allowed disabled:opacity-40"
              aria-label="Next page"
            >
              <Icon name="chevron_right" className="text-[14px]" />
            </button>
          </div>
        </div>
      )}
    </div>
  )
}
