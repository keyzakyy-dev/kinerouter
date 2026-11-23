import { useEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { NavLink, Outlet, useNavigate } from 'react-router-dom'
import { cn } from '@/lib/utils'
import { api } from '@/lib/api'
import { useAuth } from '@/lib/auth'
import { Modal } from '@/components/modal'
import { useCopyToClipboard } from '@/lib/use-copy-to-clipboard'

/** Fallback until /api/version reports how this binary was installed. */
const INSTALL_CMD = 'npm i -g kinerouter@latest --prefer-online'
const SHUTDOWN_COUNTDOWN_SEC = 3

/** Collapsed/expanded state survives reloads; local only, nothing is sent to the gateway. */
const COLLAPSE_KEY = 'kinerouter.sidebar'

/** Label + trailing chrome in a row: they fold away when the rail is collapsed. */
const HIDES = 'group-data-[collapsed=true]/sidebar:hidden'
/** The row becomes a 36×34 icon tile in the slim rail. */
const ROW_COLLAPSED =
  'group-data-[collapsed=true]/sidebar:mx-auto group-data-[collapsed=true]/sidebar:size-9 group-data-[collapsed=true]/sidebar:justify-center group-data-[collapsed=true]/sidebar:rounded-[10px] group-data-[collapsed=true]/sidebar:px-0'

/** Sidebar structure mirrors KineRouter's src/shared/components/Sidebar.js. */
type NavItem = { href: string; label: string; icon: string }

const navItems: NavItem[] = [
  { href: '/dashboard/endpoint', label: 'Endpoint & Key', icon: 'api' },
  { href: '/dashboard/providers', label: 'Providers', icon: 'dns' },
  { href: '/dashboard/combos', label: 'Combo & Vision Adapter', icon: 'layers' },
  { href: '/dashboard/usage', label: 'Usage', icon: 'bar_chart' },
  { href: '/dashboard/quota', label: 'Quota Tracker', icon: 'data_usage' },
  { href: '/dashboard/token-saver', label: 'Token Saver', icon: 'savings' },
  { href: '/dashboard/cli-tools', label: 'CLI Tools', icon: 'terminal' },
]

const systemItems: NavItem[] = [
  { href: '/dashboard/proxy-pools', label: 'Proxy Pools', icon: 'lan' },
  { href: '/dashboard/skills', label: 'Skills', icon: 'extension' },
]

const debugItems: NavItem[] = [
  { href: '/dashboard/console-log', label: 'Console Log', icon: 'receipt_long' },
  { href: '/dashboard/translator', label: 'Translator', icon: 'translate' },
]

const MEDIA_PROVIDER_KINDS = [
  { id: 'embedding', label: 'Embedding', icon: 'scatter_plot' },
  { id: 'image', label: 'Image', icon: 'image' },
  { id: 'video', label: 'Video', icon: 'movie' },
  { id: 'tts', label: 'Text to Speech', icon: 'record_voice_over' },
  { id: 'stt', label: 'Speech to Text', icon: 'mic' },
  { id: 'systemone', label: 'System One', icon: 'route' },
]
const MEDIA_ITEMS: NavItem[] = [
  ...MEDIA_PROVIDER_KINDS.map((k) => ({
    href: `/dashboard/media-providers/${k.id}`,
    label: k.label,
    icon: k.icon,
  })),
  { href: '/dashboard/media-providers/web', label: 'Web Fetch & Search', icon: 'travel_explore' },
]

/** Left edge of the bubble: the 64px slim rail plus a small gap. */
const TIP_LEFT = 72

/**
 * Hover bubble for the collapsed rail. Native `title` waits ~1s and can't be
 * styled, so this draws its own. Portalled to <body>: the <aside> clips with
 * overflow-hidden and the <nav> scrolls with overflow-y-auto, either of which
 * would cut an absolutely-positioned child off.
 */
function RailTip({ label, children }: { label?: string; children: React.ReactNode }) {
  const [top, setTop] = useState<number | null>(null)
  // Expanding mid-hover unmounts nothing, so the bubble would linger over a
  // now-labelled row; clear it whenever the rail stops being collapsed.
  useEffect(() => {
    if (!label) setTop(null)
  }, [label])
  if (!label) return <>{children}</>
  return (
    <span
      className="contents"
      onMouseEnter={(e) => {
        // display:contents gives the span no box of its own; measure the row.
        const r = e.currentTarget.firstElementChild?.getBoundingClientRect()
        if (r) setTop(r.top + r.height / 2)
      }}
      onMouseLeave={() => setTop(null)}
    >
      {children}
      {top !== null &&
        createPortal(
          <div
            role="tooltip"
            style={{ top, left: TIP_LEFT }}
            className="pointer-events-none fixed z-50 -translate-y-1/2 whitespace-nowrap rounded-md bg-surface-3 px-2.5 py-1 text-xs font-medium text-text-main shadow-lg ring-1 ring-border before:absolute before:top-1/2 before:-left-1 before:h-2 before:w-2 before:-translate-y-1/2 before:rotate-45 before:rounded-sm before:bg-surface-3"
          >
            {label}
          </div>,
          document.body,
        )}
    </span>
  )
}

function NavRow({
  item,
  collapsed,
  onNavigate,
}: {
  item: NavItem
  collapsed: boolean
  onNavigate?: () => void
}) {
  return (
    <RailTip label={collapsed ? item.label : undefined}>
    <NavLink
      to={item.href}
      end={item.href === '/dashboard/endpoint'}
      onClick={onNavigate}
      className={({ isActive }) =>
        cn(
          'flex items-center gap-3 rounded-lg px-3 py-1 transition-all group',
          ROW_COLLAPSED,
          isActive
            ? 'bg-primary/10 text-primary group-data-[collapsed=true]/sidebar:bg-primary group-data-[collapsed=true]/sidebar:text-white group-data-[collapsed=true]/sidebar:shadow-[0_4px_14px_-4px_rgba(229,106,74,0.7)]'
            : 'text-text-muted hover:bg-surface-2 hover:text-text-main',
        )
      }
    >
      {({ isActive }) => (
        <>
          <span
            className={cn(
              'material-symbols-outlined text-[18px] shrink-0',
              isActive ? 'fill-1' : 'group-hover:text-primary transition-colors',
            )}
          >
            {item.icon}
          </span>
          <span className={cn('text-[13px] font-medium whitespace-nowrap', HIDES)}>{item.label}</span>
        </>
      )}
    </NavLink>
    </RailTip>
  )
}

export function AppShell() {
  const { logout } = useAuth()
  const navigate = useNavigate()
  const [version, setVersion] = useState('')
  const [mediaOpen, setMediaOpen] = useState(false)
  const [enableTranslator, setEnableTranslator] = useState(false)
  const [latestVersion, setLatestVersion] = useState('')
  const [installCmd, setInstallCmd] = useState(INSTALL_CMD)
  const [showUpdate, setShowUpdate] = useState(false)
  const [countdown, setCountdown] = useState(0)
  const [disconnected, setDisconnected] = useState(false)
  const [collapsed, setCollapsed] = useState(() => localStorage.getItem(COLLAPSE_KEY) !== 'open')
  const timerRef = useRef<ReturnType<typeof setInterval> | null>(null)
  const { copied, copy } = useCopyToClipboard()

  useEffect(() => {
    api
      .version()
      .then((v) => {
        setVersion(v.version)
        if (v.installCommand) setInstallCmd(v.installCommand)
        if (v.hasUpdate) setLatestVersion(v.latestVersion || v.latest)
      })
      .catch(() => {})
    api
      .settings()
      .then((s) => setEnableTranslator(Boolean((s as Record<string, unknown>).enableTranslator)))
      .catch(() => {})
    return () => {
      if (timerRef.current) clearInterval(timerRef.current)
    }
  }, [])

  useEffect(() => {
    localStorage.setItem(COLLAPSE_KEY, collapsed ? 'closed' : 'open')
  }, [collapsed])

  // Cmd/Ctrl+B. Skipped while typing so it never eats a keystroke.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (!(e.metaKey || e.ctrlKey) || e.key.toLowerCase() !== 'b') return
      const t = e.target as HTMLElement | null
      if (t && (t.isContentEditable || /^(INPUT|TEXTAREA|SELECT)$/.test(t.tagName))) return
      e.preventDefault()
      setCollapsed((v) => !v)
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])

  // Copy the install command, count down, then stop the server so the user can
  // upgrade the binary. Same one-way flow as the reference sidebar.
  const copyAndShutdown = () => {
    copy(installCmd)
    navigator.clipboard?.writeText(installCmd).catch(() => {})
    let remaining = SHUTDOWN_COUNTDOWN_SEC
    setCountdown(remaining)
    setDisconnected(true)
    timerRef.current = setInterval(() => {
      remaining -= 1
      setCountdown(remaining)
      if (remaining <= 0) {
        if (timerRef.current) clearInterval(timerRef.current)
        api.shutdown().catch(() => {})
        setDisconnected(true)
      }
    }, 1000)
  }

  const handleLogout = async () => {
    await logout()
    navigate('/login', { replace: true })
  }

  return (
    <div className="flex min-h-screen">
      <aside
        data-collapsed={collapsed}
        className="group/sidebar sticky top-0 flex h-screen w-72 flex-col overflow-hidden border-r border-border-subtle bg-vibrancy backdrop-blur-xl transition-[width] duration-[280ms] ease-[cubic-bezier(0.77,0,0.175,1)] data-[collapsed=true]:w-16 motion-reduce:transition-none"
      >
        {/* Brand rail: mark, name and the collapse toggle. */}
        <div className="flex flex-col gap-2 px-3 pt-4 pb-3 group-data-[collapsed=true]/sidebar:px-2 group-data-[collapsed=true]/sidebar:pt-4">
          <div className="flex items-center gap-2.5 group-data-[collapsed=true]/sidebar:flex-col group-data-[collapsed=true]/sidebar:gap-2">
            <NavLink to="/dashboard" className="flex min-w-0 items-center gap-2.5" title={collapsed ? `KineRouter${version ? ` v${version}` : ''}` : undefined}>
              <span className="grid size-9 shrink-0 place-items-center rounded-[10px] bg-gradient-to-br from-brand-400 to-brand-700 text-white shadow-warm group-data-[collapsed=true]/sidebar:size-8 group-data-[collapsed=true]/sidebar:rounded-[10px]">
                <img src="/favicon.svg" alt="" className="size-5 w-auto" />
              </span>
              <span className="flex min-w-0 flex-col whitespace-nowrap group-data-[collapsed=true]/sidebar:hidden">
                <span className="text-[15px] font-semibold leading-tight tracking-tight text-text-main">KineRouter</span>
                <span className="flex items-center gap-1.5 text-[11px] leading-tight text-text-muted">
                  <span className="size-1.5 rounded-full bg-success" />
                  {version ? `v${version}` : 'local gateway'}
                </span>
              </span>
            </NavLink>
            <button
              onClick={() => setCollapsed((v) => !v)}
              title={collapsed ? 'Expand sidebar (Ctrl+B)' : 'Collapse sidebar (Ctrl+B)'}
              aria-label={collapsed ? 'Expand sidebar' : 'Collapse sidebar'}
              aria-expanded={!collapsed}
              className="ml-auto inline-flex size-7 cursor-pointer items-center justify-center rounded-md text-text-muted transition-colors hover:bg-surface-2 hover:text-text-main group-data-[collapsed=true]/sidebar:mx-auto"
            >
              <span className="material-symbols-outlined block text-[17px] leading-none">
                {collapsed ? 'left_panel_open' : 'left_panel_close'}
              </span>
            </button>
          </div>
          {latestVersion && (
            <>
              <div className="-m-1 hidden flex-col gap-1.5 rounded p-1 group-data-[collapsed=true]/sidebar:flex">
                <button
                  onClick={() => setShowUpdate(true)}
                  title={`Update to v${latestVersion}`}
                  className="mx-auto grid size-7 cursor-pointer place-items-center rounded-md border border-success/40 text-[13px] font-semibold text-success transition-colors hover:bg-success/10"
                >
                  ↑
                </button>
              </div>
              <div className="-m-1 flex flex-col gap-1.5 rounded p-1 group-data-[collapsed=true]/sidebar:hidden">
                <span className="text-xs font-semibold text-success">↑ New version available: v{latestVersion}</span>
                <div className="flex items-center gap-2">
                  <button
                    onClick={() => setShowUpdate(true)}
                    className="cursor-pointer rounded bg-success px-2 py-1 text-[11px] font-semibold text-white transition-colors hover:opacity-90"
                  >
                    Update now
                  </button>
                  <button
                    onClick={() => copy(installCmd)}
                    title="Copy install command"
                    className="min-w-0 flex-1 cursor-pointer text-left transition-opacity hover:opacity-80"
                  >
                    <code className="block truncate font-mono text-[10px] text-success/80">
                      {copied ? '✓ copied!' : installCmd}
                    </code>
                  </button>
                </div>
              </div>
            </>
          )}
        </div>

        {/* Navigation */}
        {/* RailTip wraps rows in display:contents, which `space-y-*` can't space,
            so the gaps come from flex `gap` on each list instead. */}
        <nav className="custom-scrollbar flex min-h-0 flex-1 flex-col gap-1 overflow-y-auto px-3 py-2 group-data-[collapsed=true]/sidebar:px-2">
          <p className={cn('mb-1 px-2 pt-1 text-[10px] font-semibold uppercase tracking-[0.09em] text-text-muted/60', HIDES)}>
            Gateway
          </p>
          {navItems.slice(0, 3).map((item) => (
            <NavRow key={item.href} item={item} collapsed={collapsed} />
          ))}

          <div className="mx-auto my-2 h-px w-6 bg-border-subtle group-data-[collapsed=false]/sidebar:hidden" />
          <p className={cn('mb-1 px-2 pt-1 text-[10px] font-semibold uppercase tracking-[0.09em] text-text-muted/60', HIDES)}>
            Insight
          </p>
          {navItems.slice(3, 6).map((item) => (
            <NavRow key={item.href} item={item} collapsed={collapsed} />
          ))}

          <div className="mx-auto my-2 h-px w-6 bg-border-subtle group-data-[collapsed=false]/sidebar:hidden" />
          <p className={cn('mb-1 px-2 pt-1 text-[10px] font-semibold uppercase tracking-[0.09em] text-text-muted/60', HIDES)}>
            Tools
          </p>
          {navItems.slice(6).map((item) => (
            <NavRow key={item.href} item={item} collapsed={collapsed} />
          ))}

          <div className="mt-1 flex flex-col gap-1 pt-2">
            <div className="mx-auto mb-2 h-px w-6 bg-border-subtle group-data-[collapsed=false]/sidebar:hidden" />
            <p className={cn('mb-1 px-2 text-[10px] font-semibold uppercase tracking-[0.09em] text-text-muted/60', HIDES)}>
              System
            </p>

            {/* Media Providers accordion. The slim rail keeps one icon instead of
                the whole sub-nav, and it lands on the first media page directly. */}
            <button
              onClick={() => setMediaOpen((v) => !v)}
              aria-expanded={mediaOpen}
              className="flex w-full items-center gap-3 rounded-lg px-3 py-1 transition-all group text-text-muted hover:bg-surface-2 hover:text-text-main group-data-[collapsed=true]/sidebar:hidden"
            >
              <span className="material-symbols-outlined text-[18px] shrink-0">perm_media</span>
              <span className="flex-1 text-left text-[13px] font-medium">Media Providers</span>
              <span
                className="material-symbols-outlined text-[14px] transition-transform"
                style={{ transform: mediaOpen ? 'rotate(180deg)' : 'rotate(0deg)' }}
              >
                expand_more
              </span>
            </button>
            <RailTip label={collapsed ? 'Media Providers' : undefined}>
              <NavLink
                to={MEDIA_ITEMS[0].href}
                className={({ isActive }) =>
                  cn(
                    'hidden items-center gap-3 rounded-lg px-3 py-1 transition-all group',
                    ROW_COLLAPSED,
                    'group-data-[collapsed=true]/sidebar:flex',
                    isActive
                      ? 'bg-primary/10 text-primary group-data-[collapsed=true]/sidebar:bg-primary group-data-[collapsed=true]/sidebar:text-white group-data-[collapsed=true]/sidebar:shadow-[0_4px_14px_-4px_rgba(229,106,74,0.7)]'
                      : 'text-text-muted hover:bg-surface-2 hover:text-text-main',
                  )
                }
              >
                {({ isActive }) => (
                  <>
                    <span
                      className={cn(
                        'material-symbols-outlined text-[18px] shrink-0',
                        isActive ? 'fill-1' : 'group-hover:text-primary transition-colors',
                      )}
                    >
                      perm_media
                    </span>
                    <span className={cn('text-[13px] font-medium whitespace-nowrap', HIDES)}>
                      Media Providers
                    </span>
                  </>
                )}
              </NavLink>
            </RailTip>
            {mediaOpen && !collapsed && (
              <div className="flex flex-col gap-1.5 pl-4">
                {MEDIA_ITEMS.map((item) => (
                  <NavRow key={item.href} item={item} collapsed={collapsed} />
                ))}
              </div>
            )}

            {systemItems.map((item) => (
              <NavRow key={item.href} item={item} collapsed={collapsed} />
            ))}

            {debugItems
              .filter((item) => item.href !== '/dashboard/translator' || enableTranslator)
              .map((item) => (
                <NavRow key={item.href} item={item} collapsed={collapsed} />
              ))}

            {/* Settings */}
            <RailTip label={collapsed ? 'Settings' : undefined}>
              <NavLink
                to="/dashboard/settings"
                className={({ isActive }) =>
                  cn(
                    'flex items-center gap-3 rounded-lg px-3 py-1 transition-all group',
                    ROW_COLLAPSED,
                    isActive
                      ? 'bg-primary/10 text-primary group-data-[collapsed=true]/sidebar:bg-primary group-data-[collapsed=true]/sidebar:text-white group-data-[collapsed=true]/sidebar:shadow-[0_4px_14px_-4px_rgba(229,106,74,0.7)]'
                      : 'text-text-muted hover:bg-surface-2 hover:text-text-main',
                  )
                }
              >
                {({ isActive }) => (
                  <>
                    <span
                      className={cn(
                        'material-symbols-outlined text-[18px] shrink-0',
                        isActive ? 'fill-1' : 'group-hover:text-primary transition-colors',
                      )}
                    >
                      settings
                    </span>
                    <span className={cn('text-[13px] font-medium whitespace-nowrap', HIDES)}>Settings</span>
                  </>
                )}
              </NavLink>
            </RailTip>
          </div>
        </nav>

        <div className="flex flex-col gap-1.5 px-3 pb-3 pt-1 group-data-[collapsed=true]/sidebar:items-center group-data-[collapsed=true]/sidebar:px-2">
          <RailTip label={collapsed ? 'Gateway online' : undefined}>
            <div className="flex items-center gap-2 py-1 group-data-[collapsed=true]/sidebar:mx-auto">
              <span className="size-1.5 shrink-0 rounded-full bg-success shadow-[0_0_0_3px_rgba(34,197,94,0.12)]" />
              <span className={cn('text-[11px] text-text-muted', HIDES)}>online</span>
            </div>
          </RailTip>
          <RailTip label={collapsed ? 'Log out' : undefined}>
            <button
              onClick={handleLogout}
              className={cn(
                'flex w-full cursor-pointer items-center gap-3 rounded-lg px-3 py-1 text-[13px] font-medium text-text-muted transition-colors hover:bg-surface-2 hover:text-text-main',
                ROW_COLLAPSED,
              )}
            >
              <span className="material-symbols-outlined text-[18px] shrink-0">logout</span>
              <span className={cn('whitespace-nowrap', HIDES)}>Log out</span>
            </button>
          </RailTip>
        </div>
      </aside>

      <main className="min-w-0 flex-1">
        <div className="mx-auto max-w-6xl px-6 py-8">
          <Outlet />
        </div>
      </main>

      <Modal isOpen={showUpdate} onClose={() => setShowUpdate(false)} title="Update KineRouter" size="sm">
        <div className="flex flex-col gap-4 text-sm">
          <p className="text-text-muted">
            Copy the install command, then run it in a terminal. The server stops so the binary can be replaced.
          </p>
          <code className="break-all rounded-lg border border-border bg-surface-2 p-3 font-mono text-xs text-text-main">
            {installCmd}
          </code>
          <div className="flex items-center justify-end gap-2">
            <button
              onClick={() => setShowUpdate(false)}
              className="rounded-lg px-3 py-1.5 text-sm font-medium text-text-muted hover:bg-surface-2"
            >
              Cancel
            </button>
            <button
              onClick={() => {
                setShowUpdate(false)
                copyAndShutdown()
              }}
              className="cursor-pointer rounded-lg bg-primary px-3 py-1.5 text-sm font-medium text-white"
            >
              Copy &amp; shut down
            </button>
          </div>
        </div>
      </Modal>

      {disconnected && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/80 p-6 backdrop-blur-sm">
          <div className="max-w-lg rounded-xl border border-white/10 bg-neutral-900/95 p-6 text-center text-white">
            <span className="material-symbols-outlined mb-3 text-[32px] text-danger">power_off</span>
            <h2 className="mb-2 text-xl font-semibold">
              {countdown > 0 ? `Server stops in ${countdown}s…` : 'Server Disconnected'}
            </h2>
            <p className="mb-6 text-sm text-text-muted">
              {countdown > 0
                ? 'Command copied. Paste it into a terminal to install the new version.'
                : 'The proxy server has been stopped. Run the install command, then reload.'}
            </p>
            <button
              onClick={() => globalThis.location.reload()}
              className="rounded-lg bg-surface-2 px-4 py-2 text-sm font-medium text-text-main"
            >
              Reload Page
            </button>
          </div>
        </div>
      )}
    </div>
  )
}

export function PageHeader({
  title,
  description,
  actions,
}: {
  title: string
  description?: string
  actions?: React.ReactNode
}) {
  return (
    <div className="mb-6 flex flex-wrap items-end justify-between gap-3">
      <div>
        <h1 className="text-xl font-semibold tracking-tight text-text-main">{title}</h1>
        {description && <p className="mt-1 text-xs text-text-muted">{description}</p>}
      </div>
      {actions && <div className="flex items-center gap-2">{actions}</div>}
    </div>
  )
}
