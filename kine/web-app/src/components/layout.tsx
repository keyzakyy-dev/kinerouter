import { useEffect, useRef, useState } from 'react'
import { NavLink, Outlet, useNavigate } from 'react-router-dom'
import { cn } from '@/lib/utils'
import { api } from '@/lib/api'
import { useAuth } from '@/lib/auth'
import { Modal } from '@/components/modal'
import { useCopyToClipboard } from '@/lib/use-copy-to-clipboard'

/** KineRouter is not on npm; the update command installs the Go binary. */
const INSTALL_CMD = 'go install github.com/keyzakyy-dev/kinerouter/cmd/kinerouter@latest'
const SHUTDOWN_COUNTDOWN_SEC = 3

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
  { href: '/dashboard/console-log', label: 'Console Log', icon: 'terminal' },
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
const COMBINED_WEB_ITEM = {
  id: 'web',
  label: 'Web Fetch & Search',
  icon: 'travel_explore',
  href: '/dashboard/media-providers/web',
}

function NavRow({ item, onNavigate }: { item: NavItem; onNavigate?: () => void }) {
  return (
    <NavLink
      to={item.href}
      end={item.href === '/dashboard/endpoint'}
      onClick={onNavigate}
      className={({ isActive }) =>
        cn(
          'flex items-center gap-3 rounded-lg px-3 py-1 transition-all group',
          isActive
            ? 'bg-primary/10 text-primary'
            : 'text-text-muted hover:bg-surface-2 hover:text-text-main',
        )
      }
    >
      {({ isActive }) => (
        <>
          <span
            className={cn(
              'material-symbols-outlined text-[18px]',
              isActive ? 'fill-1' : 'group-hover:text-primary transition-colors',
            )}
          >
            {item.icon}
          </span>
          <span className="text-[13px] font-medium">{item.label}</span>
        </>
      )}
    </NavLink>
  )
}

export function AppShell() {
  const { logout } = useAuth()
  const navigate = useNavigate()
  const [version, setVersion] = useState('')
  const [mediaOpen, setMediaOpen] = useState(false)
  const [enableTranslator, setEnableTranslator] = useState(false)
  const [latestVersion, setLatestVersion] = useState('')
  const [showUpdate, setShowUpdate] = useState(false)
  const [countdown, setCountdown] = useState(0)
  const [disconnected, setDisconnected] = useState(false)
  const timerRef = useRef<ReturnType<typeof setInterval> | null>(null)
  const { copied, copy } = useCopyToClipboard()

  useEffect(() => {
    api
      .version()
      .then((v) => {
        setVersion(v.version)
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

  // Copy the install command, count down, then stop the server so the user can
  // upgrade the binary. Same one-way flow as the reference sidebar.
  const copyAndShutdown = () => {
    copy(INSTALL_CMD)
    navigator.clipboard?.writeText(INSTALL_CMD).catch(() => {})
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
      <aside className="flex min-h-full w-72 flex-col border-r border-border-subtle bg-vibrancy backdrop-blur-xl transition-colors duration-300">
        {/* Traffic lights */}
        <div className="flex items-center gap-2 px-6 pt-5 pb-2">
          <div className="h-3 w-3 rounded-full bg-[#FF5F56]" />
          <div className="h-3 w-3 rounded-full bg-[#FFBD2E]" />
          <div className="h-3 w-3 rounded-full bg-[#27C93F]" />
        </div>

        {/* Logo */}
        <div className="flex flex-col gap-2 px-6 py-4">
          <NavLink to="/dashboard" className="flex items-center gap-3">
            <div className="flex size-9 items-center justify-center rounded-[10px] bg-gradient-to-br from-brand-500 to-brand-700 shadow-[var(--shadow-warm)]">
              <span className="material-symbols-outlined text-[20px] text-white">hub</span>
            </div>
            <div className="flex flex-col">
              <h1 className="text-lg font-semibold tracking-tight text-text-main">KineRouter</h1>
              <span className="text-xs text-text-muted">{version ? `v${version}` : 'local gateway'}</span>
            </div>
          </NavLink>
          {latestVersion && (
            <div className="-m-1 flex flex-col gap-1.5 rounded p-1">
              <span className="text-xs font-semibold text-success">↑ New version available: v{latestVersion}</span>
              <div className="flex items-center gap-2">
                <button
                  onClick={() => setShowUpdate(true)}
                  className="cursor-pointer rounded bg-success px-2 py-1 text-[11px] font-semibold text-white transition-colors hover:opacity-90"
                >
                  Update now
                </button>
                <button
                  onClick={() => copy(INSTALL_CMD)}
                  title="Copy install command"
                  className="min-w-0 flex-1 cursor-pointer text-left transition-opacity hover:opacity-80"
                >
                  <code className="block truncate font-mono text-[10px] text-success/80">
                    {copied ? '✓ copied!' : INSTALL_CMD}
                  </code>
                </button>
              </div>
            </div>
          )}
        </div>

        {/* Navigation */}
        <nav className="custom-scrollbar flex-1 space-y-0.5 overflow-y-auto px-4 py-2">
          {navItems.map((item) => (
            <NavRow key={item.href} item={item} />
          ))}

          <div className="mt-2 space-y-0.5 pt-3">
            <p className="mb-2 px-4 text-xs font-semibold uppercase tracking-wider text-text-muted/60">
              System
            </p>

            {/* Media Providers accordion */}
            <button
              onClick={() => setMediaOpen((v) => !v)}
              className={cn(
                'flex w-full items-center gap-3 rounded-lg px-3 py-1 transition-all group',
                'text-text-muted hover:bg-surface-2 hover:text-text-main',
              )}
            >
              <span className="material-symbols-outlined text-[18px]">perm_media</span>
              <span className="flex-1 text-left text-[13px] font-medium">Media Providers</span>
              <span
                className="material-symbols-outlined text-[14px] transition-transform"
                style={{ transform: mediaOpen ? 'rotate(180deg)' : 'rotate(0deg)' }}
              >
                expand_more
              </span>
            </button>
            {mediaOpen && (
              <div className="pl-4">
                {MEDIA_PROVIDER_KINDS.map((kind) => (
                  <NavLink
                    key={kind.id}
                    to={`/dashboard/media-providers/${kind.id}`}
                    className={({ isActive }) =>
                      cn(
                        'flex items-center gap-3 rounded-lg px-4 py-1 transition-all group',
                        isActive
                          ? 'bg-primary/10 text-primary'
                          : 'text-text-muted hover:bg-surface-2 hover:text-text-main',
                      )
                    }
                  >
                    <span className="material-symbols-outlined text-[16px]">{kind.icon}</span>
                    <span className="text-sm">{kind.label}</span>
                  </NavLink>
                ))}
                <NavLink
                  to={COMBINED_WEB_ITEM.href}
                  className={({ isActive }) =>
                    cn(
                      'flex items-center gap-3 rounded-lg px-4 py-1 transition-all group',
                      isActive
                        ? 'bg-primary/10 text-primary'
                        : 'text-text-muted hover:bg-surface-2 hover:text-text-main',
                    )
                  }
                >
                  <span className="material-symbols-outlined text-[16px]">
                    {COMBINED_WEB_ITEM.icon}
                  </span>
                  <span className="text-sm">{COMBINED_WEB_ITEM.label}</span>
                </NavLink>
              </div>
            )}

            {systemItems.map((item) => (
              <NavRow key={item.href} item={item} />
            ))}

            {debugItems
              .filter((item) => item.href !== '/dashboard/translator' || enableTranslator)
              .map((item) => (
                <NavRow key={item.href} item={item} />
              ))}

            {/* Settings */}
            <NavLink
              to="/dashboard/settings"
              className={({ isActive }) =>
                cn(
                  'flex items-center gap-3 rounded-lg px-3 py-1 transition-all group',
                  isActive
                    ? 'bg-primary/10 text-primary'
                    : 'text-text-muted hover:bg-surface-2 hover:text-text-main',
                )
              }
            >
              {({ isActive }) => (
                <>
                  <span
                    className={cn(
                      'material-symbols-outlined text-[18px]',
                      isActive ? 'fill-1' : 'group-hover:text-primary transition-colors',
                    )}
                  >
                    settings
                  </span>
                  <span className="text-[13px] font-medium">Settings</span>
                </>
              )}
            </NavLink>
          </div>
        </nav>

        <div className="p-3">
          <button
            onClick={handleLogout}
            className="flex w-full cursor-pointer items-center gap-3 rounded-lg px-3 py-1 text-[13px] font-medium text-text-muted transition-colors hover:bg-surface-2 hover:text-text-main"
          >
            <span className="material-symbols-outlined text-[18px]">logout</span>
            Log out
          </button>
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
            {INSTALL_CMD}
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
