import { useEffect, useState } from 'react'
import { Icon } from '@/components/icon'
import { Modal } from '@/components/modal'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'

/** Port of src/shared/components/SmartPolicyModal.js. */
const QUICK_THRESHOLDS = [60, 70, 75, 80, 85, 90]

export type SmartPolicy = {
  smartPolicy: string
  sessionSticky: boolean
  confidenceThreshold: number
}

const POLICIES = [
  {
    id: 'smart',
    title: 'Smart Switch',
    icon: 'auto_mode',
    recommended: true,
    desc: 'Preserves warm prompt cache across turns. Seamlessly switches models if the conversation topic shifts with high confidence (≥80%).',
  },
  {
    id: 'strict',
    title: 'Strict Lock',
    icon: 'lock',
    desc: 'Evaluates System One on turn 1, then locks that model for the entire session. Adds +0ms latency and zero extra router calls (100% prompt cache reuse).',
  },
  {
    id: 'per-turn',
    title: 'Per-Turn',
    icon: 'refresh',
    desc: 'Evaluates System One independently on every prompt without session stickiness.',
  },
  {
    id: 'custom',
    title: 'Custom Threshold',
    icon: 'tune',
    desc: 'Configure your own confidence threshold required to trigger a model switch.',
  },
] as const

export function SmartPolicyModal({
  isOpen,
  onClose,
  comboName,
  currentPolicy = 'smart',
  currentThreshold = 0.8,
  onSave,
}: {
  isOpen: boolean
  onClose: () => void
  comboName?: string
  currentPolicy?: string
  currentThreshold?: number
  onSave: (p: SmartPolicy) => void
}) {
  const [policy, setPolicy] = useState('smart')
  const [threshold, setThreshold] = useState(80)

  useEffect(() => {
    if (!isOpen) return
    setPolicy(currentPolicy.startsWith('smart-switch') ? 'smart' : currentPolicy || 'smart')
    const pct = Math.round((currentThreshold || 0.8) * (currentThreshold <= 1 ? 100 : 1))
    setThreshold(pct || 80)
  }, [isOpen, currentPolicy, currentThreshold])

  const badge = (p: (typeof POLICIES)[number]) =>
    p.id === 'smart' ? 'Adaptive ≥80%' : p.id === 'strict' ? '1 Task = 1 Session' : p.id === 'per-turn' ? 'Dynamic' : `Adaptive ≥${threshold}%`

  return (
    <Modal isOpen={isOpen} onClose={onClose} title={`Router Policy — ${comboName || 'Combo'}`} size="lg">
      <div className="flex flex-col gap-4">
        <p className="text-xs text-text-muted">
          Choose how the System One router manages model selection across conversation turns:
        </p>

        <div className="flex flex-col gap-2.5">
          {POLICIES.map((p) => {
            const selected = policy === p.id
            return (
              <div
                key={p.id}
                onClick={() => setPolicy(p.id)}
                className={cn(
                  'flex cursor-pointer flex-col rounded-xl border p-3 transition-colors',
                  selected ? 'border-primary bg-primary/5' : 'border-border/60 hover:border-border hover:bg-surface-2/60',
                )}
              >
                <div className="flex items-center justify-between gap-2">
                  <div className="flex items-center gap-2">
                    <span
                      className={cn(
                        'flex size-4 shrink-0 items-center justify-center rounded-full border',
                        selected ? 'border-primary bg-primary text-white' : 'border-border bg-surface',
                      )}
                    >
                      {selected && <span className="size-1.5 rounded-full bg-white" />}
                    </span>
                    <Icon name={p.icon} className="text-[15px] text-text-muted" />
                    <span className="text-sm font-medium text-text-main">{p.title}</span>
                    {'recommended' in p && p.recommended && (
                      <span className="rounded bg-primary/10 px-1.5 py-0.5 text-[10px] font-semibold uppercase tracking-wide text-primary">
                        Recommended
                      </span>
                    )}
                  </div>
                  <Badge variant={p.id === 'smart' ? 'success' : 'default'} size="sm">{badge(p)}</Badge>
                </div>

                <p className="mt-1.5 pl-6 text-xs leading-relaxed text-text-muted">{p.desc}</p>

                {p.id === 'custom' && selected && (
                  <div
                    className="mt-3 flex flex-col gap-2.5 border-t border-border/40 pl-6 pt-3"
                    onClick={(e) => e.stopPropagation()}
                  >
                    <div className="flex items-center justify-between text-xs">
                      <span className="font-medium text-text-muted">Switch Confidence Threshold:</span>
                      <span className="font-mono text-sm font-semibold text-primary">{threshold}%</span>
                    </div>
                    <input
                      type="range" min={50} max={95} step={1} value={threshold}
                      onChange={(e) => setThreshold(Number(e.target.value))}
                      className="h-1.5 w-full cursor-pointer accent-primary"
                    />
                    <div className="flex items-center gap-1.5 pt-1">
                      <span className="mr-1 text-[10px] text-text-muted">Presets:</span>
                      {QUICK_THRESHOLDS.map((pct) => (
                        <button
                          key={pct}
                          type="button"
                          onClick={() => setThreshold(pct)}
                          className={cn(
                            'rounded px-2 py-0.5 font-mono text-[11px]',
                            threshold === pct ? 'bg-primary font-semibold text-white' : 'bg-surface-2 text-text-muted hover:text-text-main',
                          )}
                        >
                          {pct}%
                        </button>
                      ))}
                    </div>
                  </div>
                )}
              </div>
            )
          })}
        </div>

        <div className="flex items-start gap-2.5 rounded-xl border border-primary/20 bg-primary/5 p-3 text-xs">
          <Icon name="bolt" className="mt-0.5 shrink-0 text-[18px] text-primary" />
          <div className="leading-relaxed text-text-muted">
            <span className="font-medium text-text-main">Prompt Cache Benefits: </span>
            Retaining the same model across session turns keeps upstream prompt caches warm, reducing
            input token costs by up to <strong className="text-text-main">90%</strong> and cutting latency in half
            (Claude Prompt Cache, DeepSeek KV-cache, Gemini Implicit Cache).
          </div>
        </div>

        <div className="flex items-center justify-end gap-2 border-t border-border/40 pt-2">
          <Button variant="ghost" size="sm" onClick={onClose}>Cancel</Button>
          <Button
            size="sm"
            onClick={() => {
              onSave({
                smartPolicy: policy,
                sessionSticky: policy !== 'per-turn',
                confidenceThreshold: policy === 'custom' ? threshold / 100 : policy === 'smart' ? 0.8 : 0,
              })
              onClose()
            }}
          >
            Save Policy
          </Button>
        </div>
      </div>
    </Modal>
  )
}
