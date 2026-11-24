import { useEffect, useRef, useState } from 'react'
import { api, type SponsoredAd } from '@/lib/api'
import { Icon } from '@/components/icon'

/**
 * Sponsored banner for the ad-funded providers (free-pi, freebuff).
 *
 * The creative comes from the gateway, which records what it served. The
 * impression is reported only after the banner is painted — never on a timer,
 * and never for an ad that was not displayed. A failed fetch or report is
 * silent: an ad is decoration, it must not make the page look broken.
 */
export function SponsoredAdBanner({
  provider,
  connectionId,
}: {
  provider: 'free-pi' | 'freebuff'
  connectionId: string
}) {
  const [ad, setAd] = useState<SponsoredAd | null>(null)
  const reported = useRef(false)
  const disposed = useRef(false)

  function report() {
    if (reported.current || disposed.current) return
    reported.current = true
    const send = provider === 'free-pi' ? api.freePiImpression : api.freebuffImpression
    send(connectionId, ad?.adId ?? '').catch(() => {})
  }

  useEffect(() => {
    disposed.current = false
    reported.current = false
    setAd(null)
    if (!connectionId) return
    const load = provider === 'free-pi' ? api.freePiAd : api.freebuffAd
    load(connectionId)
      .then((res) => {
        if (!disposed.current) setAd(res.ad)
      })
      .catch(() => {})
    return () => {
      disposed.current = true
    }
  }, [provider, connectionId])

  useEffect(() => {
    if (!ad || ad.creative.imageUrl) return
    // Two frames: the text creative is guaranteed painted by then.
    let raf = requestAnimationFrame(() => {
      raf = requestAnimationFrame(report)
    })
    return () => cancelAnimationFrame(raf)
  }, [ad, provider, connectionId])

  if (!ad) return null

  return (
    <div
      className="mb-2 flex items-start gap-3 rounded-lg border border-border bg-surface-2 px-3 py-2.5"
      aria-label={provider === 'free-pi' ? 'Sponsored' : 'Sponsored by Freebuff Partner'}
    >
      {ad.creative.imageUrl && (
        <img
          src={ad.creative.imageUrl}
          alt=""
          className="size-10 shrink-0 rounded-lg bg-surface object-contain p-1"
          loading="eager"
          onLoad={report}
          onError={report}
        />
      )}
      <div className="min-w-0 flex-1">
        <p className="mb-0.5 flex items-center gap-1.5 text-[10px] font-medium uppercase tracking-wide text-text-muted">
          <Icon name="campaign" className="text-[12px]" />
          Sponsored{ad.creative.sponsorName ? ` · ${ad.creative.sponsorName}` : ''}
        </p>
        {ad.creative.title && <p className="truncate text-xs font-semibold text-text-main">{ad.creative.title}</p>}
        {ad.creative.body && <p className="line-clamp-2 text-[11px] text-text-muted">{ad.creative.body}</p>}
      </div>
      {ad.creative.cta && ad.clickUrl && (
        <a
          href={ad.clickUrl}
          target="_blank"
          rel="noreferrer sponsored"
          className="shrink-0 self-center rounded-lg border border-border px-2.5 py-1 text-[11px] font-medium text-text-main transition-colors hover:bg-surface-3"
          style={ad.creative.brandColor ? { borderColor: ad.creative.brandColor } : undefined}
        >
          {ad.creative.cta}
        </a>
      )}
    </div>
  )
}
