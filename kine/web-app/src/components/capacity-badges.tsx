import { Icon } from '@/components/icon'
import { cn } from '@/lib/utils'

/**
 * Port of src/shared/components/CapacityBadges.js. Only vision and reasoning
 * are surfaced; the reference's CAPACITY_META table has exactly these keys.
 */
const CAPACITY_META = {
  vision: { icon: 'visibility', label: 'Vision', desc: 'Supports image input', color: 'text-sky-600 dark:text-sky-400' },
  reasoning: { icon: 'neurology', label: 'Reasoning', desc: 'Reasoning / thinking model', color: 'text-violet-600 dark:text-violet-400' },
} as const

export type Caps = {
  vision?: boolean
  reasoning?: boolean
  contextWindow?: number
  maxOutput?: number
}

export function CapacityBadges({ caps, className }: { caps?: Caps | null; className?: string }) {
  if (!caps) return null
  const keys = (Object.keys(CAPACITY_META) as (keyof typeof CAPACITY_META)[]).filter((k) => caps[k])
  if (keys.length === 0) return null
  return (
    <span className={cn('inline-flex items-center gap-1', className)}>
      {keys.map((k) => {
        const meta = CAPACITY_META[k]
        return (
          <span key={k} title={`${meta.label}: ${meta.desc}`} className={cn('inline-flex', meta.color)}>
            <Icon name={meta.icon} className="text-[13px]" />
          </span>
        )
      })}
    </span>
  )
}
