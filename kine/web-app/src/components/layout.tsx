import { useEffect, useRef, useState } from 'react'
import { Outlet, useLocation, useNavigate } from 'react-router-dom'
import {
  Sidebar,
  SidebarFooter,
  SidebarHeader,
  SidebarItem,
  SidebarNested,
  SidebarNav,
  SidebarSection,
  useSidebar,
} from '@/components/ui/sidebar'
import { api } from '@/lib/api'
import { useAuth } from '@/lib/auth'
import { Modal } from '@/components/modal'
import { useCopyToClipboard } from '@/lib/use-copy-to-clipboard'
import { Icon } from '@/components/icon'

/** Fallback until /api/version reports how this binary was installed. */
const INSTALL_CMD = 'npm i -g kinerouter@latest --prefer-online'
const SHUTDOWN_COUNTDOWN_SEC = 3

/** Collapsed state survives reloads; local only, nothing is sent to the gateway. */
const COLLAPSE_KEY = 'kinerouter.sidebar'

/** Sidebar structure mirrors KineRouter's src/shared/components/Sidebar.js. */
type NavItem = { href: string; label: string; icon: string }

const gatewayItems: NavItem[] = [
  { href: '/dashboard/endpoint', label: 'Endpoint & Key', icon: 'key' },
  { href: '/dashboard/providers', label: 'Providers', icon: 'dns' },
  { href: '/dashboard/combos', label: 'Combo & Vision Adapter', icon: 'layers' },
  { href: '/dashboard/jailbreak', label: 'Persona', icon: 'psychology' },
]

const insightItems: NavItem[] = [
  { href: '/dashboard/usage', label: 'Usage', icon: 'bar_chart' },
  { href: '/dashboard/quota', label: 'Quota Tracker', icon: 'data_usage' },
  { href: '/dashboard/token-saver', label: 'Token Saver', icon: 'savings' },
]

const toolItems: NavItem[] = [{ href: '/dashboard/cli-tools', label: 'CLI Tools', icon: 'terminal' }]

const systemItems: NavItem[] = [
  { href: '/dashboard/proxy-pools', label: 'Proxy Pools', icon: 'lan' },
  { href: '/dashboard/skills', label: 'Skills', icon: 'extension' },
]

const debugItems: NavItem[] = [
  { href: '/dashboard/console-log', label: 'Console Log', icon: 'receipt_long' },
  { href: '/dashboard/translator', label: 'Translator', icon: 'translate' },
  { href: '/dashboard/terminal', label: 'Terminal', icon: 'terminal' },
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

const SETTINGS_ITEM: NavItem = { href: '/dashboard/settings', label: 'Settings', icon: 'settings' }

/** Navbar crumb: every routable page in the same order the rail lists it. */
const ALL_NAV_ITEMS: NavItem[] = [
  ...gatewayItems,
  ...insightItems,
  ...toolItems,
  ...MEDIA_ITEMS,
  ...systemItems,
  ...debugItems,
  SETTINGS_ITEM,
]

/** The menu glyph, on the same Tabler set the rest of the dashboard uses. */
function MenuIcon({ icon }: { icon: string }) {
  return <Icon name={icon} className="text-[18px]" />
}

/** Same match NavLink uses: the endpoint page is exact, the rest cover their children. */
function isActive(pathname: string, href: string) {
  return pathname === href || (href !== '/dashboard/endpoint' && pathname.startsWith(`${href}/`))
}

function NavItems({ items }: { items: NavItem[] }) {
  const { pathname } = useLocation()
  const navigate = useNavigate()
  return (
    <>
      {items.map((item) => (
        <SidebarItem
          key={item.href}
          icon={<MenuIcon icon={item.icon} />}
          active={isActive(pathname, item.href)}
          onClick={() => navigate(item.href)}
        >
          {item.label}
        </SidebarItem>
      ))}
    </>
  )
}

/** Brand rail. The collapsed rail is all mark: the primitive's header expands on click. */
function Brand({ version }: { version: string }) {
  const { collapsed } = useSidebar()
  const mark = (
    <>
      <span
        aria-hidden
        className="size-7 shrink-0 bg-text-main mask-[url(/favicon.svg)] [mask-size:145%] mask-center mask-no-repeat"
      />
      {!collapsed && (
        <span className="flex min-w-0 flex-col whitespace-nowrap">
          <span className="font-display text-[15px] font-semibold leading-tight tracking-tight text-text-main">
            KineRouter
          </span>
          <span className="flex items-center gap-1.5 text-[11px] leading-tight text-text-muted">
            <span className="size-1.5 rounded-full bg-success" />
            {version ? (version.endsWith('-dev') ? `dev · ${version.replace(/-dev$/, '')}` : `v${version}`) : 'local gateway'}
          </span>
        </span>
      )}
    </>
  )

  if (collapsed) return <div className="flex min-w-0 items-center gap-2.5">{mark}</div>
  return (
    <a href="/dashboard" className="flex min-w-0 items-center gap-2.5">
      {mark}
    </a>
  )
}

/** Footer status text: the primitive's footer only has room for the dot at rail width. */

function UpdateBanner({ latestVersion, onUpdate, onCopy, copied }: {
  latestVersion: string
  onUpdate: () => void
  onCopy: () => void
  copied: string | null
}) {
  const { collapsed } = useSidebar()
  if (collapsed) {
    return (
      <button
        onClick={onUpdate}
        title={`Update to v${latestVersion}`}
        aria-label={`Update to v${latestVersion}`}
        className="mx-auto mb-1 grid size-7 cursor-pointer place-items-center rounded-md text-text-main transition-colors hover:bg-surface-3"
      >
        <Icon name="download" className="text-[15px]" />
      </button>
    )
  }
  return (
    <div className="mx-3 mb-2 rounded-lg bg-surface-2 p-2.5">
      <div className="flex items-baseline justify-between gap-2">
        <span className="text-[11px] font-semibold text-text-main">Update</span>
        <span className="truncate font-mono text-[10px] text-text-muted">v{latestVersion}</span>
      </div>
      <div className="mt-2 flex items-center gap-1.5">
        <button
          onClick={onUpdate}
          className="flex flex-1 cursor-pointer items-center justify-center gap-1 rounded-md bg-primary px-2 py-1.5 text-[11px] font-medium text-primary-foreground transition-opacity hover:opacity-90"
        >
          <Icon name="download" className="text-[13px]" />
          Install
        </button>
        <button
          onClick={onCopy}
          title="Copy install command"
          aria-label="Copy install command"
          className="grid size-7 cursor-pointer place-items-center rounded-md text-text-muted transition-colors hover:bg-surface-3 hover:text-text-main"
        >
          <Icon name={copied ? 'check' : 'content_copy'} className="text-[14px]" />
        </button>
      </div>
    </div>
  )
}

export function AppShell() {
  const { logout } = useAuth()
  const { pathname } = useLocation()
  const navigate = useNavigate()
  const [version, setVersion] = useState('')
  const [enableTranslator, setEnableTranslator] = useState(false)
  const [latestVersion, setLatestVersion] = useState('')
  const [installCmd, setInstallCmd] = useState(INSTALL_CMD)
  const [showUpdate, setShowUpdate] = useState(false)
  const [updating, setUpdating] = useState(false)
  const [updateMsg, setUpdateMsg] = useState('')
  const [releaseNotes, setReleaseNotes] = useState('')
  const [autoUpdate, setAutoUpdate] = useState(false)
  const [autoBusy, setAutoBusy] = useState(false)
  const [autoInterval, setAutoInterval] = useState(6)
  const [countdown, setCountdown] = useState(0)
  const [disconnected, setDisconnected] = useState(false)
  const [collapsed, setCollapsed] = useState(() => localStorage.getItem(COLLAPSE_KEY) !== 'open')
  const timerRef = useRef<ReturnType<typeof setInterval> | null>(null)
  const { copied, copy } = useCopyToClipboard()
  const currentLabel = ALL_NAV_ITEMS.find((item) => isActive(pathname, item.href))?.label

  useEffect(() => {
    api
      .version()
      .then((v) => {
        setVersion(v.version)
        if (v.installCommand) setInstallCmd(v.installCommand)
        if (v.releaseNotes) setReleaseNotes(v.releaseNotes)
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

  // Cmd/Ctrl+B, the habit from the previous rail. Skipped while typing.
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

  // A stale hour-long cache must not hide a release the user just asked about,
  // so opening the modal re-checks GitHub.
  const openUpdate = () => {
    setShowUpdate(true)
    setUpdateMsg('')
    api
      .versionStatus()
      .then((v) => {
        setAutoUpdate(Boolean(v.autoUpdateEnabled))
        if (v.checkIntervalHours) setAutoInterval(v.checkIntervalHours)
      })
      .catch(() => {})
    api
      .checkVersion()
      .then((v) => {
        if (v.latestVersion) setLatestVersion(v.latestVersion)
        if (v.installCommand) setInstallCmd(v.installCommand)
        if (v.releaseNotes) setReleaseNotes(v.releaseNotes)
      })
      .catch(() => {})
  }

  // Run the channel-aware updater, then wait for the replacement process to
  // bind and reload into it.
  const runUpdate = async () => {
    setUpdating(true)
    setUpdateMsg('')
    try {
      const res = await api.triggerUpdate()
      setUpdateMsg(res.message || 'Update installed. Restarting…')
      setTimeout(() => globalThis.location.reload(), 6000)
    } catch (err) {
      setUpdateMsg(err instanceof Error ? err.message : 'Update failed')
      setUpdating(false)
    }
  }

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
      <div className="sticky top-0 h-screen">
        <Sidebar
          variant="collapsible"
          width={256}
          collapsedWidth={64}
          collapsed={collapsed}
          onCollapsedChange={setCollapsed}
          aria-label="Dashboard"
          className="bg-[var(--color-sidebar)] backdrop-blur-xl"
        >
          <SidebarHeader>
            <Brand version={version} />
          </SidebarHeader>

          {latestVersion && (
            <UpdateBanner
              latestVersion={latestVersion}
              onUpdate={openUpdate}
              onCopy={() => copy(installCmd)}
              copied={copied}
            />
          )}

          <SidebarNav className="custom-scrollbar">
            <SidebarSection label="Gateway">
              <NavItems items={gatewayItems} />
            </SidebarSection>

            <SidebarSection label="Insight">
              <NavItems items={insightItems} />
            </SidebarSection>

            <SidebarSection label="Tools">
              <NavItems items={toolItems} />
            </SidebarSection>

            <SidebarSection label="System">
              <SidebarNested
                icon={<MenuIcon icon="perm_media" />}
                label="Media Providers"
                active={MEDIA_ITEMS.some((item) => isActive(pathname, item.href))}
              >
                {MEDIA_ITEMS.map((item) => (
                  <SidebarItem
                    key={item.href}
                    icon={<MenuIcon icon={item.icon} />}
                    active={isActive(pathname, item.href)}
                    onClick={() => navigate(item.href)}
                  >
                    {item.label}
                  </SidebarItem>
                ))}
              </SidebarNested>
              <NavItems items={systemItems} />
              <NavItems
                items={debugItems.filter(
                  (item) => item.href !== '/dashboard/translator' || enableTranslator,
                )}
              />
              <SidebarItem
                icon={<MenuIcon icon="settings" />}
                active={isActive(pathname, '/dashboard/settings')}
                onClick={() => navigate('/dashboard/settings')}
              >
                Settings
              </SidebarItem>
            </SidebarSection>
          </SidebarNav>

          <SidebarFooter>
            <button
              onClick={handleLogout}
              title="Log out"
              aria-label="Log out"
              className="flex size-7 shrink-0 cursor-pointer items-center justify-center rounded-md text-text-muted transition-colors hover:bg-surface-2 hover:text-text-main"
            >
              <Icon name="logout" className="text-[18px]" />
            </button>
          </SidebarFooter>
        </Sidebar>
      </div>

      <main className="min-w-0 flex-1 bg-[#fafafa] dark:bg-bg">
        <header className="sticky top-0 z-30 flex h-16 items-center gap-3 bg-[var(--color-sidebar)] px-6 backdrop-blur-xl">
          <button
            onClick={() => setCollapsed((v) => !v)}
            title="Toggle sidebar (Ctrl+B)"
            aria-label="Toggle sidebar"
            className="grid size-6 shrink-0 cursor-pointer place-items-center rounded text-text-muted transition-colors hover:bg-surface-2 hover:text-text-main"
          >
            <Icon
              name={collapsed ? 'left_panel_open' : 'left_panel_close'}
              className="text-[18px] leading-none"
            />
          </button>
          <nav aria-label="Breadcrumb" className="flex min-w-0 items-center gap-2 text-sm">
            <span className="text-text-muted">Dashboard</span>
            <span className="text-text-muted/50" aria-hidden>
              /
            </span>
            <span className="truncate font-medium text-text-main">{currentLabel ?? 'Overview'}</span>
          </nav>
        </header>
        <div className="mx-auto w-full max-w-7xl px-8 py-8">
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
          {installCmd.startsWith('docker') ? (
            <p className="text-xs text-text-muted">
              Docker images cannot replace themselves. Run the command above, then reload.
            </p>
          ) : (
            <p className="text-xs text-text-muted">
              Update now replaces this binary and restarts the server automatically.
            </p>
          )}
          <label className="flex cursor-pointer items-start gap-3 rounded-lg border border-border p-3">
            <input
              type="checkbox"
              checked={autoUpdate}
              disabled={autoBusy || installCmd.startsWith('docker')}
              onChange={(e) => {
                const next = e.target.checked
                setAutoUpdate(next)
                setAutoBusy(true)
                api
                  .setAutoUpdate(next)
                  .catch(() => setAutoUpdate(!next))
                  .finally(() => setAutoBusy(false))
              }}
              className="mt-0.5 size-4 accent-[var(--color-primary)]"
            />
            <span className="text-xs text-text-muted">
              <span className="block font-medium text-text-main">Install updates automatically</span>
              {installCmd.startsWith('docker')
                ? 'Docker images cannot update themselves; pull the new image instead.'
                : `Checks every ${autoInterval}h and installs stable releases, then restarts. Prereleases are never picked up automatically.`}
            </span>
          </label>
          {releaseNotes && (
            <div className="max-h-48 overflow-y-auto whitespace-pre-wrap rounded-lg border border-border bg-surface-2 p-3 text-xs leading-relaxed text-text-muted">
              {releaseNotes}
            </div>
          )}
          {updateMsg && <p className="text-xs text-text-muted">{updateMsg}</p>}
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
              className="cursor-pointer rounded-lg border border-border px-3 py-1.5 text-sm font-medium text-text-main hover:bg-surface-2"
            >
              Copy &amp; shut down
            </button>
            {!installCmd.startsWith('docker') && (
              <button
                onClick={runUpdate}
                disabled={updating}
                className="cursor-pointer rounded-lg bg-primary px-3 py-1.5 text-sm font-medium text-white disabled:opacity-60"
              >
                {updating ? 'Updating…' : 'Update now'}
              </button>
            )}
          </div>
        </div>
      </Modal>

      {disconnected && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/80 p-6 backdrop-blur-sm">
          <div className="max-w-lg rounded-xl border border-white/10 bg-neutral-900/95 p-6 text-center text-white">
            <Icon name="power_off" className="mb-3 block text-[32px] text-danger" />
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
