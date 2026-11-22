import { cn } from '@/lib/utils'

/**
 * Material Symbols icon. KineRouter's dashboard uses ~670 ligature icons, so the
 * ported pages keep the same icon names instead of mapping them to a JS set.
 */
export function Icon({
  name,
  className,
  filled,
}: {
  name: string
  className?: string
  filled?: boolean
}) {
  return (
    <span
      aria-hidden
      className={cn('material-symbols-outlined', filled && 'fill-1', className)}
    >
      {name}
    </span>
  )
}
