import { memo, useCallback, useEffect, useMemo, useRef, useState } from 'react'
import {
  BaseEdge, Controls, Handle, Position, ReactFlow,
  getBezierPath,
  type EdgeProps,
  type NodeProps,
} from '@xyflow/react'
import '@xyflow/react/dist/style.css'
import { ProviderIcon } from '@/components/provider-icon'

/** One connected provider rendered in the usage topology. */
export type TopoProvider = {
  provider: string
  name?: string
  nodeName?: string
  color?: string
  textIcon?: string
}

// Force-stop the animation if a provider stays active longer than this.
const FE_ACTIVE_TIMEOUT_MS = 60_000
const FE_ACTIVE_TICK_MS = 1000
const KAME_PARTICLE_COUNT = 6
const SPARK_COUNT = 5

function ProviderNode({ data }: NodeProps) {
  const d = data as unknown as {
    label: string; color: string; provider: string; textIcon: string; active: boolean
  }
  const { label, color, provider, textIcon, active } = d
  return (
    <div
      className="flex items-center gap-2.5 rounded-lg border-2 bg-bg px-4 py-2.5 transition-all duration-300"
      style={{
        borderColor: active ? color : 'var(--color-border)',
        boxShadow: active ? `0 0 16px ${color}40` : 'none',
        minWidth: 150,
      }}
    >
      {(['top', 'bottom', 'left', 'right'] as const).map((p) => (
        <Handle
          key={p}
          type="target"
          position={Position[p.charAt(0).toUpperCase() + p.slice(1) as 'Top']}
          id={p}
          style={{ opacity: 0, width: 0, height: 0, border: 0, background: 'transparent' }}
        />
      ))}
      <div
        className="flex size-8 shrink-0 items-center justify-center rounded-md"
        style={{ backgroundColor: `${color}15` }}
      >
        <ProviderIcon provider={provider} size={24} fallbackText={textIcon} className="rounded-sm object-contain" />
      </div>
      <span className="truncate text-base font-medium" style={{ color: active ? color : 'var(--color-text-main)' }}>
        {label}
      </span>
      {active && (
        <span className="relative flex size-2 shrink-0">
          <span
            className="absolute inline-flex size-full animate-ping rounded-full opacity-75"
            style={{ backgroundColor: color }}
          />
          <span className="relative inline-flex size-2 rounded-full" style={{ backgroundColor: color }} />
        </span>
      )}
    </div>
  )
}

function RouterNode({ data }: NodeProps) {
  const activeCount = (data as unknown as { activeCount: number }).activeCount || 0
  const powering = activeCount > 0
  return (
    <div
      className={`relative z-[1] flex min-w-[130px] items-center justify-center rounded-xl border-2 px-5 py-3 ${
        powering
          ? 'topology-router-core border-foreground/40 bg-gradient-to-br from-foreground/10 via-foreground/5 to-transparent'
          : 'border-primary bg-primary/5 shadow-md'
      }`}
    >
      {(['top', 'bottom', 'left', 'right'] as const).map((p) => (
        <Handle
          key={p}
          type="source"
          position={Position[p.charAt(0).toUpperCase() + p.slice(1) as 'Top']}
          id={p}
          style={{ opacity: 0, width: 0, height: 0, border: 0, background: 'transparent' }}
        />
      ))}
      <span
        aria-hidden
        className={`mr-2 size-5 shrink-0 bg-current mask-[url(/favicon.svg)] [mask-size:145%] mask-center mask-no-repeat ${
          powering ? 'text-foreground' : 'text-primary'
        }`}
      />
      <span className={`text-sm font-bold ${powering ? 'topology-router-label text-foreground' : 'text-primary'}`}>
        KineRouter
      </span>
      {activeCount > 0 && (
        <span className="topology-router-badge ml-2 rounded-full bg-foreground px-1.5 py-0.5 text-xs font-bold text-background">
          {activeCount}
        </span>
      )}
    </div>
  )
}

function TopologyEdge({
  id, sourceX, sourceY, targetX, targetY, sourcePosition, targetPosition, style, data,
}: EdgeProps) {
  const [edgePath] = getBezierPath({ sourceX, sourceY, sourcePosition, targetX, targetY, targetPosition })
  const active = !!(data as { active?: boolean } | undefined)?.active
  const stroke = (style?.stroke as string) || 'var(--color-border)'
  const filterId = `topo-electric-${id}`

  if (!active) return <BaseEdge id={id} path={edgePath} style={{ ...style, stroke }} />

  return (
    <g className="topology-edge-electric">
      <defs>
        <filter id={filterId} x="-40%" y="-40%" width="180%" height="180%">
          <feTurbulence type="fractalNoise" baseFrequency="0.9" numOctaves={2} seed={2} result="noise">
            <animate attributeName="baseFrequency" values="0.8;1.4;0.8" dur="0.25s" repeatCount="indefinite" />
          </feTurbulence>
          <feDisplacementMap in="SourceGraphic" in2="noise" scale={3.5} xChannelSelector="R" yChannelSelector="G" />
        </filter>
      </defs>
      <path d={edgePath} fill="none" stroke="var(--color-text-main)" strokeWidth={10} strokeOpacity={0.18}
        strokeLinecap="round" filter={`url(#${filterId})`} className="topology-edge-halo" />
      <path d={edgePath} fill="none" stroke="var(--color-text-main)" strokeWidth={5} strokeOpacity={0.6}
        strokeLinecap="round" filter={`url(#${filterId})`} className="topology-edge-plasma" />
      <BaseEdge id={id} path={edgePath} style={{ stroke: 'var(--color-text-main)', strokeWidth: 2.2, opacity: 1 }}
        className="topology-edge-kame" />
      {Array.from({ length: KAME_PARTICLE_COUNT }, (_, i) => (
        <circle
          key={`${id}-p-${i}`}
          r={i % 2 === 0 ? 4 : 2.5}
          fill="var(--color-text-main)"
          opacity={0.95}
          style={{ filter: 'drop-shadow(0 0 3px rgba(0,0,0,0.35))' }}
        >
          <animateMotion dur={`${0.4 + i * 0.08}s`} repeatCount="indefinite" path={edgePath} begin={`${i * 0.09}s`} />
        </circle>
      ))}
      {Array.from({ length: SPARK_COUNT }, (_, i) => (
        <circle key={`${id}-s-${i}`} r={1.8} fill="var(--color-text-main)" opacity={0}>
          <animate attributeName="opacity" values="0;1;0;0;1;0" dur={`${0.35 + (i % 3) * 0.1}s`}
            begin={`${i * 0.07}s`} repeatCount="indefinite" />
          <animateMotion dur={`${0.28 + i * 0.05}s`} repeatCount="indefinite" path={edgePath} begin={`${i * 0.11}s`} />
        </circle>
      ))}
    </g>
  )
}

const nodeTypes = { provider: ProviderNode, router: RouterNode }
const edgeTypes = { topology: TopologyEdge }

const NODE_W = 180
const NODE_H = 30
const ROUTER_W = 120
const ROUTER_H = 44
const NODE_GAP = 24

function buildLayout(
  providers: TopoProvider[],
  activeSet: Set<string>,
  lastSet: Set<string>,
  errorSet: Set<string>,
) {
  const count = providers.length
  if (!count) {
    return {
      nodes: [{ id: 'router', type: 'router', position: { x: 0, y: 0 }, data: { activeCount: 0 }, draggable: false }],
      edges: [],
    }
  }
  const minRx = ((NODE_W + NODE_GAP) * count) / (2 * Math.PI)
  const rx = Math.max(320, minRx)
  const ry = Math.max(200, rx * 0.55)

  const nodes: Record<string, unknown>[] = [{
    id: 'router', type: 'router',
    position: { x: -ROUTER_W / 2, y: -ROUTER_H / 2 },
    data: { activeCount: activeSet.size }, draggable: false,
  }]
  const edges: Record<string, unknown>[] = []

  const edgeStyle = (active: boolean, last: boolean, error: boolean) => {
    if (error) return { stroke: 'var(--color-danger)', strokeWidth: 2.5, opacity: 0.9 }
    if (active) return { stroke: 'var(--color-text-main)', strokeWidth: 3.5, opacity: 1 }
    if (last) return { stroke: 'var(--color-text-muted)', strokeWidth: 2, opacity: 0.7 }
    return { stroke: 'var(--color-border)', strokeWidth: 1, opacity: 0.3 }
  }

  providers.forEach((p, i) => {
    const id = (p.provider || '').toLowerCase()
    const active = activeSet.has(id)
    const last = !active && lastSet.has(id)
    const error = !active && errorSet.has(id)
    const nodeId = `provider-${p.provider}`
    const angle = -Math.PI / 2 + (2 * Math.PI * i) / count
    const cx = rx * Math.cos(angle)
    const cy = ry * Math.sin(angle)

    let sourceHandle: string
    let targetHandle: string
    if (Math.abs(angle + Math.PI / 2) < Math.PI / 4 || Math.abs(angle - (3 * Math.PI) / 2) < Math.PI / 4) {
      sourceHandle = 'top'; targetHandle = 'bottom'
    } else if (Math.abs(angle - Math.PI / 2) < Math.PI / 4) {
      sourceHandle = 'bottom'; targetHandle = 'top'
    } else if (cx > 0) {
      sourceHandle = 'right'; targetHandle = 'left'
    } else {
      sourceHandle = 'left'; targetHandle = 'right'
    }

    nodes.push({
      id: nodeId, type: 'provider',
      position: { x: cx - NODE_W / 2, y: cy - NODE_H / 2 },
      data: {
        label: p.name || p.nodeName || p.provider,
        color: p.color || '#6b7280',
        provider: p.provider,
        textIcon: p.textIcon || (p.provider || '?').slice(0, 2).toUpperCase(),
        active,
      },
      draggable: false,
    })
    edges.push({
      id: `e-${nodeId}`, type: 'topology', source: 'router', sourceHandle, target: nodeId, targetHandle,
      animated: false, data: { active }, style: edgeStyle(active, last, error),
    })
  })

  return { nodes, edges }
}

/**
 * Radial connected-provider graph around the router. Active providers get an
 * electric beam, the most recent one amber, an erroring one red.
 * ponytail: fitView drives the layout; no drag/pan persistence is needed.
 */
export const ProviderTopology = memo(function ProviderTopology({
  providers = [], activeRequests = [], lastProvider = '', errorProvider = '',
}: {
  providers?: TopoProvider[]
  activeRequests?: { provider?: string }[]
  lastProvider?: string
  errorProvider?: string
}) {
  const activeKey = useMemo(
    () => activeRequests.map((r) => r.provider?.toLowerCase()).filter(Boolean).sort().join(','),
    [activeRequests],
  )
  const rawActiveSet = useMemo(() => new Set(activeKey ? activeKey.split(',') : []), [activeKey])
  const lastSet = useMemo(() => new Set(lastProvider ? [lastProvider.toLowerCase()] : []), [lastProvider])
  const errorSet = useMemo(() => new Set(errorProvider ? [errorProvider.toLowerCase()] : []), [errorProvider])

  // Track firstSeen per active provider; drop ones running too long (stuck BE).
  const firstSeenRef = useRef<Record<string, number>>({})
  const [tick, setTick] = useState(0)

  useEffect(() => {
    const seen = firstSeenRef.current
    const now = Date.now()
    for (const p of rawActiveSet) if (!seen[p]) seen[p] = now
    for (const p of Object.keys(seen)) if (!rawActiveSet.has(p)) delete seen[p]
  }, [rawActiveSet])

  useEffect(() => {
    if (!rawActiveSet.size) return
    const id = setInterval(() => setTick((t) => t + 1), FE_ACTIVE_TICK_MS)
    return () => clearInterval(id)
  }, [rawActiveSet])

  const activeSet = useMemo(() => {
    const now = Date.now()
    const out = new Set<string>()
    for (const p of rawActiveSet) {
      const ts = firstSeenRef.current[p]
      if (!ts || now - ts < FE_ACTIVE_TIMEOUT_MS) out.add(p)
    }
    return out
  }, [rawActiveSet, tick])

  const { nodes, edges } = useMemo(
    () => buildLayout(providers, activeSet, lastSet, errorSet),
    [providers, activeSet, lastSet, errorSet],
  )

  const containerRef = useRef<HTMLDivElement | null>(null)
  const rfRef = useRef<{ fitView: (o: { padding: number; duration: number }) => void } | null>(null)
  const fitOpts = { padding: 0.2, duration: 200 }
  const onInit = useCallback((instance: unknown) => {
    rfRef.current = instance as { fitView: (o: { padding: number; duration: number }) => void }
    setTimeout(() => rfRef.current?.fitView(fitOpts), 50)
  }, [])

  useEffect(() => {
    const el = containerRef.current
    if (!el) return
    const ro = new ResizeObserver(() => rfRef.current?.fitView(fitOpts))
    ro.observe(el)
    return () => ro.disconnect()
  }, [])

  return (
    <div
      ref={containerRef}
      className="h-[320px] w-full min-w-0 rounded-lg border border-border bg-bg-alt/30 sm:h-[480px]"
    >
      {providers.length === 0 ? (
        <div className="flex h-full items-center justify-center text-sm text-text-muted">
          No providers connected
        </div>
      ) : (
        <ReactFlow
          nodes={nodes as never}
          edges={edges as never}
          nodeTypes={nodeTypes}
          edgeTypes={edgeTypes}
          fitView
          fitViewOptions={fitOpts}
          minZoom={0.1}
          maxZoom={2}
          onInit={onInit}
          proOptions={{ hideAttribution: true }}
          panOnDrag
          zoomOnScroll
          zoomOnPinch
          zoomOnDoubleClick
          preventScrolling={false}
          nodesDraggable={false}
          nodesConnectable={false}
          elementsSelectable={false}
        >
          <Controls showInteractive={false} className="react-flow-controls-custom" />
        </ReactFlow>
      )}
    </div>
  )
})
