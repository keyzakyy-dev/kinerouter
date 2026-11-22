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
function statusOf(tool: CliTool, status?: CliToolStatus) {
  if (tool.form === 'guide') return { label: 'Guide', variant: 'default' as const }
  if (!status) return { label: 'Unknown', variant: 'secondary' as const }
  if (status.installed === false) return { label: 'Not installed', variant: 'secondary' as const }
  if (status.hasKineRouter) return { label: 'Connected', variant: 'success' as const }
  return { label: 'Not configured', variant: 'warning' as const }
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
          const s = statusOf(tool, statuses[tool.id])
          return (
            <Link key={tool.id} to={`/dashboard/cli-tools/${tool.id}`} className="block">
              <Card className="h-full p-3 transition-colors hover:border-primary/50">
                <div className="flex items-center gap-3">
                  <div className="flex size-8 shrink-0 items-center justify-center">
                    <ToolIcon tool={tool} />
                  </div>
                  <div className="min-w-0 flex-1">
                    <h3 className="truncate text-sm font-medium text-text-main">{tool.name}</h3>
                    <Badge variant={s.variant} size="sm" className={cn('mt-1', loading && 'opacity-50')}>
                      {loading ? 'Loading' : s.label}
                    </Badge>
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
