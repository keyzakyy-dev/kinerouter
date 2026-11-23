import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { Icon } from '@/components/icon'
import { PageHeader } from '@/components/layout'
import { Badge } from '@/components/ui/badge'
import { Card } from '@/components/ui/card'
import { api, type CliToolStatus } from '@/lib/api'
import { CLI_TOOLS, MITM_TOOLS, type CliTool } from '@/lib/cli-tools'
import { cn } from '@/lib/utils'

/**
 * CLI Tools index, ported from
 * src/app/(dashboard)/dashboard/cli-tools/CLIToolsPageClient.js.
 * Status comes from GET /api/cli-tools/all-statuses; MITM tools link to the
 * MITM page instead of having a settings form of their own.
 */
// One hue per state: green only when a tool really points here, amber for a
// half-done setup, grey for missing/unknown, outline for docs-only tools.
const STATUS_STYLE: Record<string, string> = {
  Connected: 'bg-emerald-500/12 text-emerald-700 dark:text-emerald-400',
  'Not configured': 'bg-amber-500/12 text-amber-700 dark:text-amber-400',
  'Not installed': 'bg-surface-2 text-text-muted',
  Unknown: 'bg-surface-2 text-text-muted/70',
  Guide: 'border border-border text-text-muted',
  Loading: 'bg-surface-2 text-text-muted',
}

function statusOf(tool: CliTool, status?: CliToolStatus) {
  if (tool.form === 'guide') return 'Guide'
  if (!status) return 'Unknown'
  if (status.installed === false) return 'Not installed'
  if (status.hasKineRouter) return 'Connected'
  return 'Not configured'
}

export function ToolIcon({ tool, size = 32 }: { tool: { name: string; image?: string; color?: string }; size?: number }) {
  const [broken, setBroken] = useState(false)
  if (!tool.image || broken) {
    return (
      <span className="text-[13px] font-semibold" style={tool.color ? { color: tool.color } : undefined}>
        {tool.name.slice(0, 2).toUpperCase()}
      </span>
    )
  }
  return (
    <img
      src={`/providers/${tool.image}.png`}
      alt={tool.name}
      width={size}
      height={size}
      loading="lazy"
      decoding="async"
      className="rounded-lg object-contain"
      style={{ width: size, height: size }}
      onError={() => setBroken(true)}
    />
  )
}

export default function CliToolsPage() {
  const [statuses, setStatuses] = useState<Record<string, CliToolStatus>>({})
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    api.cliStatuses()
      .then(setStatuses)
      .catch(() => setStatuses({}))
      .finally(() => setLoading(false))
  }, [])

  return (
    <>
      <PageHeader
        title="CLI Tools"
        description={`${CLI_TOOLS.length} tools · point a CLI at this router in one click`}
      />

      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3">
        {CLI_TOOLS.map((tool) => {
          const label = loading ? 'Loading' : statusOf(tool, statuses[tool.id])
          return (
            <Link key={tool.id} to={`/dashboard/cli-tools/${tool.id}`} className="block">
              <Card className="h-full p-3 transition-colors hover:border-primary/50">
                <div className="flex items-center gap-3">
                  <div className="flex size-8 shrink-0 items-center justify-center">
                    <ToolIcon tool={tool} />
                  </div>
                  <div className="min-w-0 flex-1">
                    <h3 className="truncate text-sm font-medium text-text-main">{tool.name}</h3>
                    <span
                      className={cn(
                        'mt-1 inline-flex items-center gap-1 rounded px-1.5 py-0 text-[10px] font-medium leading-tight',
                        STATUS_STYLE[label],
                      )}
                    >
                      {label === 'Connected' && (
                        <span aria-hidden className="size-1 rounded-full bg-current" />
                      )}
                      {label}
                    </span>
                  </div>
                  <Icon name="chevron_right" className="text-[18px] text-text-muted" />
                </div>
              </Card>
            </Link>
          )
        })}
      </div>

      <div className="mt-8 flex flex-col gap-3">
        <div className="flex items-center gap-2 px-1">
          <Icon name="security" className="text-[18px] text-primary" />
          <h2 className="text-sm font-semibold text-text-main">MITM Tools</h2>
        </div>
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3">
          {MITM_TOOLS.map((tool) => (
            <Link key={tool.id} to="/dashboard/mitm" className="block">
              <Card className="p-3 transition-colors hover:border-primary/50">
                <div className="flex items-center justify-between gap-3">
                  <div className="flex min-w-0 items-center gap-3">
                    <div className="flex size-8 shrink-0 items-center justify-center">
                      <ToolIcon tool={tool} />
                    </div>
                    <div className="min-w-0">
                      <div className="flex items-center gap-2">
                        <h3 className="text-sm font-medium text-text-main">{tool.name}</h3>
                        <Badge size="sm">MITM</Badge>
                      </div>
                      <p className="truncate text-xs text-text-muted">{tool.description}</p>
                    </div>
                  </div>
                  <Icon name="chevron_right" className="text-[20px] text-text-muted" />
                </div>
              </Card>
            </Link>
          ))}
        </div>
      </div>
    </>
  )
}
