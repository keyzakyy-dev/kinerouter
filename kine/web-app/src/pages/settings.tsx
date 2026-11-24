import { useCallback, useEffect, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { api, type Settings } from '@/lib/api'
import { useAuth } from '@/lib/auth'
import { useTheme, type Theme } from '@/lib/use-theme'
import { PageHeader } from '@/components/layout'
import { Icon } from '@/components/icon'
import { Modal, ConfirmModal } from '@/components/modal'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import { cn } from '@/lib/utils'

/** Port of src/app/(dashboard)/dashboard/profile/page.js, section for section. */

type Status = { type: '' | 'success' | 'error'; message: string }
const EMPTY: Status = { type: '', message: '' }

const THEME_OPTIONS: { value: Theme; icon: string; label: string }[] = [
  { value: 'light', icon: 'light_mode', label: 'Light' },
  { value: 'dark', icon: 'dark_mode', label: 'Dark' },
  { value: 'system', icon: 'contrast', label: 'System' },
]

const LOCALES: { id: string; name: string; flag: string }[] = [
  { id: 'en', name: 'English', flag: '🇺🇸' },
  { id: 'vi', name: 'Tiếng Việt', flag: '🇻🇳' },
  { id: 'zh-CN', name: '简体中文', flag: '🇨🇳' },
  { id: 'zh-TW', name: '繁體中文', flag: '🇹🇼' },
  { id: 'ja', name: '日本語', flag: '🇯🇵' },
  { id: 'pt-BR', name: 'Português (Brasil)', flag: '🇧🇷' },
  { id: 'pt-PT', name: 'Português (Portugal)', flag: '🇵🇹' },
  { id: 'ko', name: '한국어', flag: '🇰🇷' },
  { id: 'es', name: 'Español', flag: '🇪🇸' },
  { id: 'de', name: 'Deutsch', flag: '🇩🇪' },
  { id: 'fr', name: 'Français', flag: '🇫🇷' },
  { id: 'he', name: 'עברית', flag: '🇮🇱' },
  { id: 'ar', name: 'العربية', flag: '🇸🇦' },
  { id: 'ru', name: 'Русский', flag: '🇷🇺' },
  { id: 'pl', name: 'Polski', flag: '🇵🇱' },
  { id: 'cs', name: 'Čeština', flag: '🇨🇿' },
  { id: 'nl', name: 'Nederlands', flag: '🇳🇱' },
  { id: 'tr', name: 'Türkçe', flag: '🇹🇷' },
  { id: 'uk', name: 'Українська', flag: '🇺🇦' },
  { id: 'tl', name: 'Tagalog', flag: '🇵🇭' },
  { id: 'id', name: 'Indonesia', flag: '🇮🇩' },
  { id: 'th', name: 'ไทย', flag: '🇹🇭' },
  { id: 'km', name: 'ខ្មែរ', flag: '🇰🇭' },
  { id: 'hi', name: 'हिन्दी', flag: '🇮🇳' },
  { id: 'bn', name: 'বাংলা', flag: '🇧🇩' },
  { id: 'ur', name: 'اردو', flag: '🇵🇰' },
  { id: 'ro', name: 'Română', flag: '🇷🇴' },
  { id: 'sv', name: 'Svenska', flag: '🇸🇪' },
  { id: 'it', name: 'Italiano', flag: '🇮🇹' },
  { id: 'el', name: 'Ελληνικά', flag: '🇬🇷' },
  { id: 'hu', name: 'Magyar', flag: '🇭🇺' },
  { id: 'fi', name: 'Suomi', flag: '🇫🇮' },
  { id: 'da', name: 'Dansk', flag: '🇩🇰' },
  { id: 'no', name: 'Norsk', flag: '🇳🇴' },
  { id: 'fa', name: 'فارسی', flag: '🇮🇷' },
]

function localeFromCookie(): string {
  const hit = document.cookie.split(';').find((c) => c.trim().startsWith('locale='))
  const value = hit ? decodeURIComponent(hit.split('=')[1]) : 'en'
  return LOCALES.some((l) => l.id === value) ? value : 'en'
}

function Section({
  icon,
  title,
  subtitle,
  children,
}: {
  icon: string
  title: string
  subtitle?: string
  children: React.ReactNode
}) {
  return (
    <section className="overflow-hidden rounded-xl bg-surface">
      <div className="flex items-center gap-3 px-5 py-4">
        <Icon name={icon} className="text-[18px] text-text-muted" />
        <div className="min-w-0">
          <h2 className="text-sm font-semibold text-text-main">{title}</h2>
          {subtitle && <p className="text-xs text-text-muted">{subtitle}</p>}
        </div>
      </div>
      {children}
    </section>
  )
}

function Row({
  title,
  desc,
  right,
  children,
  className,
}: {
  title: string
  desc?: string
  right?: React.ReactNode
  children?: React.ReactNode
  className?: string
}) {
  return (
    <div className={cn('border-t border-border px-5 py-4', className)}>
      <div className="flex items-start justify-between gap-4">
        <div className="min-w-0 flex-1">
          <p className="text-[13px] font-medium text-text-main">{title}</p>
          {desc && <p className="mt-0.5 text-xs leading-relaxed text-text-muted">{desc}</p>}
        </div>
        {right}
      </div>
      {children}
    </div>
  )
}

function Field({
  label,
  hint,
  action,
  children,
}: {
  label: string
  hint?: string
  action?: React.ReactNode
  children: React.ReactNode
}) {
  return (
    <div className="flex flex-col gap-1.5">
      <span className="flex items-center justify-between gap-2">
        <span className="text-[11px] font-medium uppercase tracking-wide text-text-muted">{label}</span>
        {action}
      </span>
      {children}
      {hint && <span className="text-[11px] leading-relaxed text-text-muted">{hint}</span>}
    </div>
  )
}

/** Borderless input used across the page; keeps the shared Input behaviour. */
function TextInput(props: React.ComponentProps<typeof Input>) {
  return <Input {...props} className={cn('border-0 bg-surface-2 shadow-none', props.className)} />
}

function Notice({ status }: { status: Status }) {
  if (!status.message) return null
  return (
    <p className={cn('text-xs sm:text-sm', status.type === 'error' ? 'text-danger' : 'text-success')}>
      {status.message}
    </p>
  )
}

const AuthModeCard = {
  password: { title: 'Password only', desc: 'Keep legacy password login.' },
  sso: { title: 'SSO only', desc: 'Require SSO for dashboard access.' },
  both: { title: 'Both', desc: 'Allow password or SSO login.' },
} as const

export default function SettingsPage() {
  const navigate = useNavigate()
  const { logout } = useAuth()
  const { theme, setTheme } = useTheme()

  const [settings, setSettings] = useState<Settings | null>(null)
  const [version, setVersion] = useState('')
  const [locale, setLocale] = useState(localeFromCookie)
  const [langOpen, setLangOpen] = useState(false)
  const [shutdownOpen, setShutdownOpen] = useState(false)
  const [shuttingDown, setShuttingDown] = useState(false)

  // Security
  const [passwords, setPasswords] = useState({ current: '', next: '', confirm: '' })
  const [passStatus, setPassStatus] = useState<Status>(EMPTY)
  const [passLoading, setPassLoading] = useState(false)

  // Database backup
  const importFileRef = useRef<HTMLInputElement>(null)
  const pendingImport = useRef<File | null>(null)
  const [dbLoading, setDbLoading] = useState(false)
  const [dbStatus, setDbStatus] = useState<Status>(EMPTY)
  const [dbAuth, setDbAuth] = useState<{ open: boolean; mode: 'export' | 'import'; password: string }>({
    open: false,
    mode: 'export',
    password: '',
  })

  // SSO
  const [ssoExpanded, setSsoExpanded] = useState(false)
  const [ssoTab, setSsoTab] = useState<'saml' | 'oidc'>('saml')
  const [authMode, setAuthMode] = useState('password')
  const [oidcForm, setOidcForm] = useState({
    issuerUrl: '',
    clientId: '',
    scopes: 'openid profile email',
    loginLabel: 'Sign in with OIDC',
  })
  const [oidcSecret, setOidcSecret] = useState('')
  const [oidcStatus, setOidcStatus] = useState<Status>(EMPTY)
  const [oidcTestStatus, setOidcTestStatus] = useState<Status>(EMPTY)
  const [oidcLoading, setOidcLoading] = useState(false)
  const [oidcTestLoading, setOidcTestLoading] = useState(false)
  const [samlForm, setSamlForm] = useState({
    entryPoint: '',
    issuer: 'urn:kinerouter:sp',
    cert: '',
    loginLabel: 'Sign in with SAML SSO',
    attributeEmail: 'email',
    attributeName: 'name',
  })
  const [samlStatus, setSamlStatus] = useState<Status>(EMPTY)
  const [samlTestStatus, setSamlTestStatus] = useState<Status>(EMPTY)
  const [samlLoading, setSamlLoading] = useState(false)
  const [samlTestLoading, setSamlTestLoading] = useState(false)
  const [samlGuide, setSamlGuide] = useState(false)
  const idpFileRef = useRef<HTMLInputElement>(null)
  const certFileRef = useRef<HTMLInputElement>(null)

  // Network
  const [proxyForm, setProxyForm] = useState({ url: '', noProxy: '' })
  const [proxyStatus, setProxyStatus] = useState<Status>(EMPTY)
  const [proxyLoading, setProxyLoading] = useState(false)
  const [proxyTestLoading, setProxyTestLoading] = useState(false)

  const origin = window.location.origin
  const oidcRedirectUri = `${origin}/api/auth/oidc/callback`
  const samlAcsUrl = `${origin}/api/auth/saml/acs`
  const samlMetadataUrl = `${origin}/api/auth/saml/metadata`

  const hydrate = useCallback((data: Settings) => {
    setSettings(data)
    setAuthMode(String(data.authMode ?? 'password'))
    setSsoTab(data.ssoType === 'oidc' ? 'oidc' : 'saml')
    if (['sso', 'saml', 'oidc', 'both'].includes(String(data.authMode))) setSsoExpanded(true)
    setOidcForm({
      issuerUrl: String(data.oidcIssuerUrl ?? ''),
      clientId: String(data.oidcClientId ?? ''),
      scopes: String(data.oidcScopes ?? 'openid profile email'),
      loginLabel: String(data.oidcLoginLabel ?? 'Sign in with OIDC'),
    })
    setOidcSecret('')
    setSamlForm({
      entryPoint: String(data.samlEntryPoint ?? ''),
      issuer: String(data.samlIssuer ?? 'urn:kinerouter:sp'),
      cert: String(data.samlCert ?? ''),
      loginLabel: String(data.samlLoginLabel ?? 'Sign in with SAML SSO'),
      attributeEmail: String(data.samlAttributeEmail ?? 'email'),
      attributeName: String(data.samlAttributeName ?? 'name'),
    })
    setProxyForm({
      url: String(data.outboundProxyUrl ?? ''),
      noProxy: String(data.outboundNoProxy ?? ''),
    })
  }, [])

  const load = useCallback(async () => {
    try {
      const [s, v] = await Promise.all([api.settings(), api.version().catch(() => null)])
      hydrate(s)
      if (v) setVersion(v.version)
    } catch (e) {
      setDbStatus({ type: 'error', message: e instanceof Error ? e.message : 'Failed to load settings' })
    }
  }, [hydrate])

  useEffect(() => { void load() }, [load])

  // Patches merge into the current settings only; re-hydrating the forms here
  // would discard text the user is still typing (the reference does the same).
  const patch = async (p: Record<string, unknown>) => {
    const next = await api.patchSettings(p)
    setSettings((prev) => ({ ...(prev ?? {}), ...next }))
    return next
  }

  // ---------------------------------------------------------------- security

  const saveRequireLogin = async (v: boolean) => {
    try { await patch({ requireLogin: v }) } catch { /* tolerant like the reference */ }
  }

  const changePassword = async (e: React.FormEvent) => {
    e.preventDefault()
    if (passwords.next !== passwords.confirm) {
      setPassStatus({ type: 'error', message: 'Passwords do not match' })
      return
    }
    setPassLoading(true)
    setPassStatus(EMPTY)
    try {
      await patch({ currentPassword: passwords.current, newPassword: passwords.next })
      setPassStatus({ type: 'success', message: 'Password updated successfully' })
      setPasswords({ current: '', next: '', confirm: '' })
    } catch (err) {
      setPassStatus({ type: 'error', message: err instanceof Error ? err.message : 'Failed to update password' })
    } finally {
      setPassLoading(false)
    }
  }

  // ---------------------------------------------------------------- database

  const exportDatabase = async (password: string) => {
    setDbLoading(true)
    setDbStatus(EMPTY)
    try {
      const payload = await api.exportDatabase(password)
      const blob = new Blob([JSON.stringify(payload, null, 2)], { type: 'application/json' })
      const url = URL.createObjectURL(blob)
      const a = document.createElement('a')
      a.href = url
      a.download = `kinerouter-backup-${new Date().toISOString().replace(/[.:]/g, '-')}.json`
      document.body.appendChild(a)
      a.click()
      a.remove()
      URL.revokeObjectURL(url)
      setDbStatus({ type: 'success', message: 'Database backup downloaded' })
    } catch (err) {
      setDbStatus({ type: 'error', message: err instanceof Error ? err.message : 'Failed to export database' })
    } finally {
      setDbLoading(false)
    }
  }

  const runImport = async (password: string) => {
    const file = pendingImport.current
    if (!file) return
    setDbLoading(true)
    try {
      const payload = JSON.parse(await file.text()) as Record<string, unknown>
      await api.importDatabase(payload, password)
      await load()
      setDbStatus({ type: 'success', message: 'Database imported successfully' })
    } catch (err) {
      setDbStatus({ type: 'error', message: err instanceof Error ? err.message : 'Invalid backup file' })
    } finally {
      pendingImport.current = null
      setDbLoading(false)
    }
  }

  const confirmDbAuth = async () => {
    const { mode, password } = dbAuth
    setDbAuth({ open: false, mode: 'export', password: '' })
    if (mode === 'export') await exportDatabase(password)
    else await runImport(password)
  }

  // ---------------------------------------------------------------- SSO

  const saveOidc = async () => {
    setOidcStatus(EMPTY)
    setOidcTestStatus(EMPTY)
    if (authMode !== 'password' && (!oidcForm.issuerUrl.trim() || !oidcForm.clientId.trim() || !oidcSecret.trim()) && !settings?.oidcConfigured) {
      setOidcStatus({ type: 'error', message: 'Issuer URL, client ID, and client secret are required to enable OIDC.' })
      return
    }
    setOidcLoading(true)
    try {
      await patch({
        authMode,
        ssoType: 'oidc',
        oidcIssuerUrl: oidcForm.issuerUrl.trim(),
        oidcClientId: oidcForm.clientId.trim(),
        oidcScopes: oidcForm.scopes.trim() || 'openid profile email',
        oidcLoginLabel: oidcForm.loginLabel.trim() || 'Sign in with OIDC',
        ...(oidcSecret.trim() ? { oidcClientSecret: oidcSecret.trim() } : {}),
      })
      setOidcStatus({
        type: 'success',
        message:
          authMode === 'sso' ? 'OIDC login enabled'
            : authMode === 'both' ? 'Password and OIDC login enabled'
              : 'OIDC settings saved',
      })
    } catch (err) {
      setOidcStatus({ type: 'error', message: err instanceof Error ? err.message : 'Failed to save OIDC settings' })
    } finally {
      setOidcLoading(false)
    }
  }

  const testOidc = async () => {
    if (!oidcForm.issuerUrl.trim() || !oidcForm.clientId.trim()) {
      setOidcTestStatus({ type: 'error', message: 'Issuer URL and client ID are required to test the connection.' })
      return
    }
    setOidcTestLoading(true)
    setOidcStatus(EMPTY)
    setOidcTestStatus(EMPTY)
    try {
      // Persist first so the probe sees the same secret the login flow will use.
      const saved = await patch({
        authMode,
        ssoType: 'oidc',
        oidcIssuerUrl: oidcForm.issuerUrl.trim(),
        oidcClientId: oidcForm.clientId.trim(),
        oidcScopes: oidcForm.scopes.trim() || 'openid profile email',
        oidcLoginLabel: oidcForm.loginLabel.trim() || 'Sign in with OIDC',
        ...(oidcSecret.trim() ? { oidcClientSecret: oidcSecret.trim() } : {}),
      })
      const data = await api.oidcTest({
        oidcIssuerUrl: String(saved.oidcIssuerUrl ?? oidcForm.issuerUrl),
        oidcClientId: String(saved.oidcClientId ?? oidcForm.clientId),
        oidcScopes: String(saved.oidcScopes ?? oidcForm.scopes),
      })
      if (data.ok) {
        const suffix =
          data.clientSecretTested === undefined ? ''
            : data.clientSecretValid === true ? ' Client secret validated too.'
              : ' Client secret was not checked.'
        setOidcTestStatus({ type: 'success', message: `Connection OK. Discovery loaded from ${data.issuerUrl}.${suffix}` })
      } else {
        setOidcTestStatus({ type: 'error', message: data.error || 'OIDC connection test failed' })
      }
    } catch (err) {
      setOidcTestStatus({ type: 'error', message: err instanceof Error ? err.message : 'OIDC connection test failed' })
    } finally {
      setOidcTestLoading(false)
    }
  }

  const saveSaml = async () => {
    setSamlLoading(true)
    setSamlStatus(EMPTY)
    setSamlTestStatus(EMPTY)
    try {
      await patch({
        authMode,
        ssoType: 'saml',
        samlEntryPoint: samlForm.entryPoint.trim(),
        samlIssuer: samlForm.issuer.trim() || 'urn:kinerouter:sp',
        samlCert: samlForm.cert.trim(),
        samlLoginLabel: samlForm.loginLabel.trim() || 'Sign in with SAML SSO',
        samlAttributeEmail: samlForm.attributeEmail.trim() || 'email',
        samlAttributeName: samlForm.attributeName.trim() || 'name',
      })
      setSamlStatus({
        type: 'success',
        message:
          authMode === 'sso' ? 'SAML SSO login enabled'
            : authMode === 'both' ? 'Password and SAML SSO login enabled'
              : 'SAML 2.0 settings saved',
      })
    } catch (err) {
      setSamlStatus({ type: 'error', message: err instanceof Error ? err.message : 'Failed to save SAML settings' })
    } finally {
      setSamlLoading(false)
    }
  }

  const testSaml = async () => {
    setSamlTestLoading(true)
    setSamlStatus(EMPTY)
    setSamlTestStatus(EMPTY)
    try {
      const data = await api.samlTest({
        samlEntryPoint: samlForm.entryPoint.trim(),
        samlIssuer: samlForm.issuer.trim(),
        samlCert: samlForm.cert.trim(),
      })
      if (data.ok) setSamlTestStatus({ type: 'success', message: data.message || 'SAML configuration verified!' })
      else setSamlTestStatus({ type: 'error', message: data.error || 'SAML configuration test failed' })
    } catch (err) {
      setSamlTestStatus({ type: 'error', message: err instanceof Error ? err.message : 'SAML configuration test failed' })
    } finally {
      setSamlTestLoading(false)
    }
  }

  const readText = (file: File | undefined, done: (text: string) => void) => {
    if (!file) return
    const reader = new FileReader()
    reader.onload = () => done(String(reader.result ?? ''))
    reader.readAsText(file)
  }

  /** Pull SSO URL, entity id and cert out of IdP metadata XML (no DOMParser dep). */
  const importIdpMetadata = (file: File | undefined) => {
    readText(file, (xml) => {
      const doc = new DOMParser().parseFromString(xml, 'text/xml')
      if (doc.querySelector('parsererror')) {
        setSamlStatus({ type: 'error', message: 'Unable to parse valid SAML IdP metadata from XML file' })
        return
      }
      const entityId = doc.documentElement.getAttribute('entityID') || ''
      let ssoUrl = ''
      for (const node of Array.from(doc.getElementsByTagName('*'))) {
        if (node.localName !== 'SingleSignOnService') continue
        const location = node.getAttribute('Location') || ''
        if (!location) continue
        ssoUrl = location
        if ((node.getAttribute('Binding') || '').includes('HTTP-Redirect')) break
      }
      const certNode = Array.from(doc.getElementsByTagName('*')).find((n) => n.localName === 'X509Certificate')
      const cert = certNode?.textContent?.trim() ?? ''
      setSamlForm((prev) => ({
        ...prev,
        entryPoint: ssoUrl || prev.entryPoint,
        cert: cert || prev.cert,
      }))
      setSamlStatus({
        type: 'success',
        message: `IdP Metadata imported! (SSO URL: ${ssoUrl ? 'found' : 'not found'}, EntityID: ${entityId ? 'found' : 'not found'}, Cert: ${cert ? 'found' : 'not found'})`,
      })
    })
  }

  // ---------------------------------------------------------------- network

  const toggleProxy = async (enabled: boolean) => {
    setProxyLoading(true)
    setProxyStatus(EMPTY)
    try {
      await patch({ outboundProxyEnabled: enabled })
      setProxyStatus({ type: 'success', message: enabled ? 'Proxy enabled' : 'Proxy disabled' })
    } catch (err) {
      setProxyStatus({ type: 'error', message: err instanceof Error ? err.message : 'Failed to update proxy settings' })
    } finally {
      setProxyLoading(false)
    }
  }

  const applyProxy = async (e: React.FormEvent) => {
    e.preventDefault()
    setProxyLoading(true)
    setProxyStatus(EMPTY)
    try {
      await patch({ outboundProxyUrl: proxyForm.url, outboundNoProxy: proxyForm.noProxy })
      setProxyStatus({ type: 'success', message: 'Proxy settings applied' })
    } catch (err) {
      setProxyStatus({ type: 'error', message: err instanceof Error ? err.message : 'Failed to update proxy settings' })
    } finally {
      setProxyLoading(false)
    }
  }

  const testProxy = async () => {
    const proxyUrl = proxyForm.url.trim()
    if (!proxyUrl) {
      setProxyStatus({ type: 'error', message: 'Please enter a Proxy URL to test' })
      return
    }
    setProxyTestLoading(true)
    setProxyStatus(EMPTY)
    try {
      const data = await api.proxyTest(proxyUrl)
      setProxyStatus(
        data.ok
          ? { type: 'success', message: `Proxy test OK (${data.status}) in ${data.elapsedMs}ms` }
          : { type: 'error', message: data.error || 'Proxy test failed' },
      )
    } catch (err) {
      setProxyStatus({ type: 'error', message: err instanceof Error ? err.message : 'Proxy test failed' })
    } finally {
      setProxyTestLoading(false)
    }
  }

  // ---------------------------------------------------------------- top level

  const chooseLocale = async (next: string) => {
    setLangOpen(false)
    try {
      await api.setLocale(next)
      setLocale(next)
    } catch { /* cookie write failed; keep the old value */ }
  }

  const handleShutdown = async () => {
    setShuttingDown(true)
    try { await api.shutdown() } catch { /* server exits mid-response */ }
    setShuttingDown(false)
    setShutdownOpen(false)
  }

  const handleLogout = async () => {
    await logout()
    navigate('/login', { replace: true })
  }

  if (!settings) {
    return <p className="py-12 text-center text-xs text-text-muted">Loading settings…</p>
  }

  const requireLogin = settings.requireLogin === true
  const fallbackRR = settings.fallbackStrategy === 'round-robin'
  const comboRR = settings.comboStrategy === 'round-robin'
  const sticky = settings.stickyRoundRobinLimit || 3
  const comboSticky = settings.comboStickyRoundRobinLimit || 1
  const ssoActive = ['sso', 'oidc', 'saml'].includes(String(settings.authMode))
  const currentLocale = LOCALES.find((l) => l.id === locale) ?? LOCALES[0]

  return (
    <>
      <PageHeader title="Settings" description="Theme, security, SSO, routing defaults, and network." />

      <div className="space-y-4">
        <Section icon="computer" title="Local Mode" subtitle="Running on your machine">
          <Row
            title="Appearance"
            desc="Theme applied to the dashboard and topology views."
            right={
              <div className="inline-flex shrink-0 items-center gap-0.5 rounded-lg bg-surface-2 p-1">
                {THEME_OPTIONS.map((option) => (
                  <button
                    key={option.value}
                    type="button"
                    onClick={() => setTheme(option.value)}
                    className={cn(
                      'flex cursor-pointer items-center gap-1.5 rounded-md px-2.5 py-1 text-xs font-medium transition-colors',
                      theme === option.value
                        ? 'bg-card text-text-main shadow-[0_1px_2px_rgba(0,0,0,0.08)]'
                        : 'text-text-muted hover:text-text-main',
                    )}
                  >
                    <Icon name={option.icon} className="text-[15px]" />
                    {option.label}
                  </button>
                ))}
              </div>
            }
          />

          <div className="border-t border-border px-5 py-4">
            <div className="flex flex-wrap items-center justify-between gap-3 rounded-lg bg-surface-2 px-3 py-2.5">
              <div className="min-w-0">
                <p className="text-[11px] font-medium uppercase tracking-wide text-text-muted">
                  Database
                </p>
                <p className="mt-0.5 break-all font-mono text-xs text-text-main">
                  {'~/.kinerouter/db/data.sqlite'}
                  <span className="ml-1.5 text-text-muted">(or $DATA_DIR/db/data.sqlite)</span>
                </p>
              </div>
              <div className="flex flex-wrap gap-2">
                <Button
                  variant="secondary"
                  size="sm"
                  disabled={dbLoading}
                  onClick={() => setDbAuth({ open: true, mode: 'export', password: '' })}
                >
                  <Icon name="download" /> Download
                </Button>
                <Button
                  variant="ghost"
                  size="sm"
                  disabled={dbLoading}
                  onClick={() => importFileRef.current?.click()}
                >
                  <Icon name="upload" /> Import
                </Button>
                <input
                  ref={importFileRef}
                  type="file"
                  accept="application/json,.json"
                  className="hidden"
                  onChange={(e) => {
                    const file = e.target.files?.[0]
                    e.target.value = ''
                    if (!file) return
                    pendingImport.current = file
                    setDbStatus(EMPTY)
                    setDbAuth({ open: true, mode: 'import', password: '' })
                  }}
                />
              </div>
            </div>
            <div className="mt-2">
              <Notice status={dbStatus} />
            </div>
          </div>
        </Section>

        <Section icon="language" title="Language">
          {/* ponytail: locale persists server-side; no bundled translation
              catalogue yet. Add a message table when strings exist to translate. */}
          <Row
            title={currentLocale.name}
            desc="Display language for the dashboard."
            right={
              <button
                onClick={() => setLangOpen(true)}
                className="flex cursor-pointer items-center gap-2 rounded-lg bg-surface-2 px-3 py-1.5 text-xs font-medium text-text-main transition-colors hover:opacity-80"
              >
                <span className="text-base leading-none">{currentLocale.flag}</span>
                Change
              </button>
            }
          />
        </Section>

        <Section icon="shield" title="Security" subtitle="Dashboard access and password.">
          <Row
            title="Require login"
            desc="When ON, the dashboard asks for a password. When OFF, anyone with access gets in."
            right={<Switch checked={requireLogin} onCheckedChange={(v) => void saveRequireLogin(v)} />}
          />

          {requireLogin && (
            <form onSubmit={changePassword} className="border-t border-border px-5 py-4">
              <div className="flex flex-col gap-4">
                {settings.hasPassword && (
                  <Field label="Current password">
                    <TextInput
                      type="password"
                      placeholder="Enter current password"
                      value={passwords.current}
                      onChange={(e) => setPasswords({ ...passwords, current: e.target.value })}
                      required
                    />
                  </Field>
                )}
                <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
                  <Field label="New password">
                    <TextInput
                      type="password"
                      placeholder="Enter new password"
                      value={passwords.next}
                      onChange={(e) => setPasswords({ ...passwords, next: e.target.value })}
                      required
                    />
                  </Field>
                  <Field label="Confirm new password">
                    <TextInput
                      type="password"
                      placeholder="Confirm new password"
                      value={passwords.confirm}
                      onChange={(e) => setPasswords({ ...passwords, confirm: e.target.value })}
                      required
                    />
                  </Field>
                </div>
                <Notice status={passStatus} />
                <div>
                  <Button type="submit" size="sm" disabled={passLoading}>
                    {settings.hasPassword ? 'Update password' : 'Set password'}
                  </Button>
                </div>
              </div>
            </form>
          )}
        </Section>

          {/* SSO */}
          <section className="overflow-hidden rounded-xl bg-surface">
            <button
              type="button"
              onClick={() => setSsoExpanded((v) => !v)}
              className="flex w-full cursor-pointer items-center gap-3 px-5 py-4 text-left"
            >
              <Icon name="lock_open" className="text-[18px] text-text-muted" />
              <div className="min-w-0 flex-1">
                <h2 className="text-sm font-semibold text-text-main">Single Sign-On</h2>
                <p className="mt-0.5 text-xs text-text-muted">
                  {ssoActive
                    ? `${settings.ssoType === 'saml' ? 'SAML 2.0' : 'OIDC'} active. Password login is off`
                    : settings.authMode === 'both'
                      ? `Password + ${settings.ssoType === 'saml' ? 'SAML 2.0' : 'OIDC'} active`
                      : 'Optional via Okta, Entra ID, Keycloak, or any OIDC provider'}
                </p>
              </div>
              <Icon
                name={ssoExpanded ? 'expand_less' : 'expand_more'}
                className="shrink-0 text-[18px] text-text-muted"
              />
            </button>

            {ssoExpanded && (
              <div>
                <div className="flex flex-wrap items-center gap-2 border-t border-border px-5 py-3">
                  <div className="inline-flex items-center gap-0.5 rounded-lg bg-surface-2 p-1">
                    {(['saml', 'oidc'] as const).map((tab) => (
                      <button
                        key={tab}
                        type="button"
                        onClick={() => setSsoTab(tab)}
                        className={cn(
                          'cursor-pointer rounded-md px-3 py-1 text-xs font-medium transition-colors',
                          ssoTab === tab
                            ? 'bg-card text-text-main shadow-[0_1px_2px_rgba(0,0,0,0.08)]'
                            : 'text-text-muted hover:text-text-main',
                        )}
                      >
                        {tab === 'saml' ? 'SAML 2.0' : 'OIDC'}
                      </button>
                    ))}
                  </div>
                  <div className="inline-flex items-center gap-0.5 rounded-lg bg-surface-2 p-1">
                    {(Object.keys(AuthModeCard) as (keyof typeof AuthModeCard)[]).map((mode) => {
                      const active =
                        mode === 'password'
                          ? authMode === 'password'
                          : mode === 'sso'
                            ? ['sso', 'saml', 'oidc'].includes(authMode)
                            : authMode === 'both'
                      return (
                        <button
                          key={mode}
                          type="button"
                          onClick={() => setAuthMode(mode)}
                          title={AuthModeCard[mode].desc}
                          className={cn(
                            'cursor-pointer rounded-md px-3 py-1 text-xs font-medium transition-colors',
                            active
                              ? 'bg-card text-text-main shadow-[0_1px_2px_rgba(0,0,0,0.08)]'
                              : 'text-text-muted hover:text-text-main',
                          )}
                        >
                          {mode === 'sso' ? `${ssoTab === 'saml' ? 'SAML' : 'OIDC'} only` : AuthModeCard[mode].title}
                        </button>
                      )
                    })}
                  </div>
                </div>

                {ssoTab === 'saml' ? (
                  <div className="space-y-4 border-t border-border px-5 py-4">
                    <div className="overflow-hidden rounded-lg bg-surface-2">
                      <button
                        type="button"
                        onClick={() => setSamlGuide((v) => !v)}
                        className="flex w-full cursor-pointer items-center justify-between gap-2 p-3 text-left"
                      >
                        <div className="flex items-center gap-2.5">
                          <Icon name="menu_book" className="text-[16px] text-text-muted" />
                          <div>
                            <p className="text-xs font-medium text-text-main">IdP setup guidelines</p>
                            <p className="text-[11px] text-text-muted">
                              Steps for AWS IAM Identity Center, Entra ID, Okta, Keycloak, Authentik
                            </p>
                          </div>
                        </div>
                        <Icon
                          name="expand_more"
                          className={cn('text-[16px] text-text-muted transition-transform', samlGuide && 'rotate-180')}
                        />
                      </button>
                      {samlGuide && (
                        <div className="space-y-3 border-t border-border p-3 text-xs text-text-main">
                          <div className="rounded-md bg-surface p-3">
                            <p className="mb-1.5 text-[11px] font-medium uppercase tracking-wide text-text-muted">
                              Service provider values
                            </p>
                            <ul className="space-y-1 text-[11px] text-text-muted">
                              <li>
                                ACS URL <code className="break-all font-mono text-text-main">{samlAcsUrl}</code>
                              </li>
                              <li>
                                Entity ID <code className="break-all font-mono text-text-main">{samlForm.issuer || 'urn:kinerouter:sp'}</code>
                              </li>
                              <li>
                                NameID <code className="font-mono text-text-main">EmailAddress</code> or{' '}
                                <code className="font-mono text-text-main">Unspecified</code>
                              </li>
                            </ul>
                          </div>
                          <div className="grid grid-cols-1 gap-2 md:grid-cols-2">
                            <div className="flex flex-col gap-1.5 rounded-md bg-surface p-3">
                              <p className="text-xs font-medium text-text-main">AWS IAM Identity Center</p>
                              <ol className="list-decimal space-y-1 pl-4 text-[11px] text-text-muted">
                                <li>Applications → <b>Add application</b> → <b>Add custom SAML 2.0 application</b>.</li>
                                <li>Set <b>Application ACS URL</b> to <code className="font-mono text-text-main">{samlAcsUrl}</code>.</li>
                                <li>Set <b>Application SAML audience</b> to <code className="font-mono text-text-main">{samlForm.issuer || 'urn:kinerouter:sp'}</code>.</li>
                                <li>Map <code className="font-mono text-text-main">Subject</code> or <code className="font-mono text-text-main">email</code> to <code className="font-mono text-text-main">{'${user:email}'}</code>.</li>
                                <li>Download the <b>IAM Identity Center SAML metadata XML</b> and import it below.</li>
                              </ol>
                            </div>
                            <div className="flex flex-col gap-1.5 rounded-md bg-surface p-3">
                              <p className="text-xs font-medium text-text-main">Microsoft Entra ID</p>
                              <ol className="list-decimal space-y-1 pl-4 text-[11px] text-text-muted">
                                <li>Enterprise Applications → <b>New application</b> → <b>Create your own application</b>.</li>
                                <li>Select <b>Single sign-on</b> → <b>SAML</b>.</li>
                                <li><b>Identifier (Entity ID):</b> <code className="font-mono text-text-main">{samlForm.issuer || 'urn:kinerouter:sp'}</code></li>
                                <li><b>Reply URL (ACS):</b> <code className="font-mono text-text-main">{samlAcsUrl}</code></li>
                                <li>Download <b>Federation Metadata XML</b> and import or copy the X.509 certificate.</li>
                              </ol>
                            </div>
                            <div className="flex flex-col gap-1.5 rounded-md bg-surface p-3">
                              <p className="text-xs font-medium text-text-main">Okta / Auth0</p>
                              <ol className="list-decimal space-y-1 pl-4 text-[11px] text-text-muted">
                                <li>Applications → <b>Create App Integration</b> → <b>SAML 2.0</b>.</li>
                                <li><b>Single Sign-On URL:</b> <code className="font-mono text-text-main">{samlAcsUrl}</code></li>
                                <li><b>Audience URI (SP Entity ID):</b> <code className="font-mono text-text-main">{samlForm.issuer || 'urn:kinerouter:sp'}</code></li>
                                <li>Name ID format: <i>EmailAddress</i>.</li>
                                <li>Download the IdP metadata XML or copy the X.509 cert.</li>
                              </ol>
                            </div>
                            <div className="flex flex-col gap-1.5 rounded-md bg-surface p-3">
                              <p className="text-xs font-medium text-text-main">Keycloak / Authentik</p>
                              <ol className="list-decimal space-y-1 pl-4 text-[11px] text-text-muted">
                                <li>Clients → <b>Create client</b> → Select <b>SAML</b>.</li>
                                <li><b>Client ID:</b> <code className="font-mono text-text-main">{samlForm.issuer || 'urn:kinerouter:sp'}</code></li>
                                <li><b>Master SAML Processing URL:</b> <code className="font-mono text-text-main">{samlAcsUrl}</code></li>
                                <li>Export the SAML Descriptor XML or copy the IdP certificate PEM.</li>
                              </ol>
                            </div>
                          </div>
                        </div>
                      )}
                    </div>

                    <div className="flex flex-wrap items-center justify-between gap-3 rounded-lg bg-surface-2 px-3 py-2.5">
                      <div>
                        <p className="text-xs font-medium text-text-main">Import IdP metadata</p>
                        <p className="text-[11px] text-text-muted">Fills SSO URL, issuer, and certificate from XML</p>
                      </div>
                      <Button
                        type="button"
                        variant="ghost"
                        size="sm"
                        onClick={() => idpFileRef.current?.click()}
                      >
                        <Icon name="upload_file" /> Upload XML
                      </Button>
                      <input
                        ref={idpFileRef}
                        type="file"
                        accept=".xml,application/xml,text/xml"
                        className="hidden"
                        onChange={(e) => {
                          const file = e.target.files?.[0]
                          e.target.value = ''
                          importIdpMetadata(file)
                        }}
                      />
                    </div>

                    <div className="grid grid-cols-1 gap-4">
                      <Field label="Single Sign-On Service URL">
                        <TextInput
                          placeholder="https://idp.example.com/app/saml/sso/..."
                          value={samlForm.entryPoint}
                          onChange={(e) => setSamlForm((p) => ({ ...p, entryPoint: e.target.value }))}
                          disabled={samlLoading}
                        />
                      </Field>
                      <Field label="SP Entity ID / Audience">
                        <TextInput
                          placeholder="urn:kinerouter:sp"
                          value={samlForm.issuer}
                          onChange={(e) => setSamlForm((p) => ({ ...p, issuer: e.target.value }))}
                          disabled={samlLoading}
                        />
                      </Field>
                      <Field
                        label="IdP X.509 Certificate"
                        hint="Paste a raw Base64 certificate or a PEM block."
                        action={
                          <button
                            type="button"
                            onClick={() => certFileRef.current?.click()}
                            className="cursor-pointer text-[11px] font-medium text-text-muted underline-offset-2 hover:text-text-main hover:underline"
                          >
                            Upload file
                          </button>
                        }
                      >
                        <textarea
                          rows={4}
                          placeholder={'-----BEGIN CERTIFICATE-----\nMIIC...\n-----END CERTIFICATE-----'}
                          value={samlForm.cert}
                          onChange={(e) => setSamlForm((p) => ({ ...p, cert: e.target.value }))}
                          className="w-full resize-y rounded-lg border-0 bg-surface-2 p-2.5 font-mono text-[11px] text-text-main outline-none placeholder:text-text-muted focus-visible:ring-1 focus-visible:ring-ring"
                          disabled={samlLoading}
                        />
                        <input
                          ref={certFileRef}
                          type="file"
                          accept=".crt,.pem,.cer,text/plain"
                          className="hidden"
                          onChange={(e) => {
                            const file = e.target.files?.[0]
                            e.target.value = ''
                            readText(file, (text) => {
                              setSamlForm((p) => ({ ...p, cert: text.trim() }))
                              setSamlStatus({ type: 'success', message: 'Certificate file loaded into configuration.' })
                            })
                          }}
                        />
                      </Field>
                      <div className="grid grid-cols-1 gap-4 sm:grid-cols-3">
                        <Field label="Login button label">
                          <TextInput
                            placeholder="Sign in with SAML SSO"
                            value={samlForm.loginLabel}
                            onChange={(e) => setSamlForm((p) => ({ ...p, loginLabel: e.target.value }))}
                          />
                        </Field>
                        <Field label="Email claim">
                          <TextInput
                            placeholder="email"
                            value={samlForm.attributeEmail}
                            onChange={(e) => setSamlForm((p) => ({ ...p, attributeEmail: e.target.value }))}
                          />
                        </Field>
                        <Field label="Display name claim">
                          <TextInput
                            placeholder="name"
                            value={samlForm.attributeName}
                            onChange={(e) => setSamlForm((p) => ({ ...p, attributeName: e.target.value }))}
                          />
                        </Field>
                      </div>
                    </div>

                    <div className="divide-y divide-border rounded-lg bg-surface-2 px-3">
                      <div className="flex items-center justify-between gap-2 py-2.5">
                        <div className="min-w-0">
                          <p className="text-[11px] uppercase tracking-wide text-text-muted">ACS callback URL</p>
                          <code className="block break-all font-mono text-[11px] text-text-main">{samlAcsUrl}</code>
                        </div>
                        <button
                          type="button"
                          onClick={() => {
                            void navigator.clipboard.writeText(samlAcsUrl)
                            setSamlStatus({ type: 'success', message: 'ACS URL copied to clipboard!' })
                          }}
                          className="shrink-0 cursor-pointer rounded-md px-2 py-1 text-[11px] font-medium text-text-muted transition-colors hover:bg-surface hover:text-text-main"
                        >
                          Copy
                        </button>
                      </div>
                      <div className="flex items-center justify-between gap-2 py-2.5">
                        <div className="min-w-0">
                          <p className="text-[11px] uppercase tracking-wide text-text-muted">SP XML metadata</p>
                          <code className="block break-all font-mono text-[11px] text-text-main">{samlMetadataUrl}</code>
                        </div>
                        <a
                          href={samlMetadataUrl}
                          target="_blank"
                          rel="noopener noreferrer"
                          download="kinerouter-sp-metadata.xml"
                          className="shrink-0 rounded-md px-2 py-1 text-[11px] font-medium text-text-muted transition-colors hover:bg-surface hover:text-text-main"
                        >
                          Download
                        </a>
                      </div>
                    </div>

                    <div className="flex flex-wrap gap-2">
                      <Button type="button" size="sm" disabled={samlLoading} onClick={() => void saveSaml()}>
                        Save SAML settings
                      </Button>
                      <Button type="button" variant="ghost" size="sm" disabled={samlTestLoading} onClick={() => void testSaml()}>
                        Test settings
                      </Button>
                    </div>
                    <Notice status={samlTestStatus} />
                    <Notice status={samlStatus} />
                  </div>
                ) : (
                  <div className="space-y-4 border-t border-border px-5 py-4">
                    <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
                      <Field label="Issuer URL" hint="Discovery document base of the provider.">
                        <TextInput
                          placeholder="https://auth.example.com/application/o/kinerouter/"
                          value={oidcForm.issuerUrl}
                          onChange={(e) => setOidcForm((p) => ({ ...p, issuerUrl: e.target.value }))}
                          disabled={oidcLoading}
                        />
                      </Field>
                      <Field label="Client ID">
                        <TextInput
                          placeholder="kinerouter-dashboard"
                          value={oidcForm.clientId}
                          onChange={(e) => setOidcForm((p) => ({ ...p, clientId: e.target.value }))}
                          disabled={oidcLoading}
                        />
                      </Field>
                      <Field label="Client secret" hint="Write-only after saving.">
                        <TextInput
                          type="password"
                          placeholder="Leave blank to keep existing"
                          value={oidcSecret}
                          onChange={(e) => setOidcSecret(e.target.value)}
                          disabled={oidcLoading}
                        />
                      </Field>
                      <Field label="Scopes">
                        <TextInput
                          placeholder="openid profile email"
                          value={oidcForm.scopes}
                          onChange={(e) => setOidcForm((p) => ({ ...p, scopes: e.target.value }))}
                          disabled={oidcLoading}
                        />
                      </Field>
                      <Field label="Login button label">
                        <TextInput
                          placeholder="Sign in with OIDC"
                          value={oidcForm.loginLabel}
                          onChange={(e) => setOidcForm((p) => ({ ...p, loginLabel: e.target.value }))}
                          disabled={oidcLoading}
                        />
                      </Field>
                    </div>

                    <div className="rounded-lg bg-surface-2 px-3 py-2.5">
                      <p className="text-[11px] uppercase tracking-wide text-text-muted">Redirect URI</p>
                      <code className="block break-all font-mono text-[11px] text-text-main">{oidcRedirectUri}</code>
                    </div>

                    <div className="flex flex-wrap gap-2">
                      <Button type="button" size="sm" disabled={oidcLoading} onClick={() => void saveOidc()}>
                        Save OIDC settings
                      </Button>
                      <Button type="button" variant="ghost" size="sm" disabled={oidcTestLoading} onClick={() => void testOidc()}>
                        Test connection
                      </Button>
                    </div>
                    <Notice status={oidcTestStatus} />
                    <Notice status={oidcStatus} />
                  </div>
                )}

                {ssoActive && (
                  <p className="border-t border-border px-5 py-3 text-[11px] text-warning">
                    {settings.ssoType === 'saml' ? 'SAML 2.0' : 'OIDC'} login is active. Password login is
                    disabled until you switch back.
                  </p>
                )}
                {settings.authMode === 'both' && (
                  <p className="border-t border-border px-5 py-3 text-[11px] text-warning">
                    Password and {settings.ssoType === 'saml' ? 'SAML 2.0' : 'OIDC'} login are both active.
                  </p>
                )}
              </div>
            )}
          </section>

          {/* Routing strategy */}
          <Section icon="route" title="Routing Strategy" subtitle="How requests pick an account or combo model.">
            <Row
              title="Round robin"
              desc="Cycle through accounts to spread load."
              right={
                <Switch
                  checked={fallbackRR}
                  onCheckedChange={(v) => void patch({ fallbackStrategy: v ? 'round-robin' : 'fill-first' })}
                />
              }
            />

            {fallbackRR && (
              <div className="flex items-center justify-between gap-4 border-t border-border bg-surface-2/50 px-5 py-3">
                <div className="min-w-0">
                  <p className="text-[13px] font-medium text-text-main">Sticky limit</p>
                  <p className="mt-0.5 text-[11px] text-text-muted">Calls per account before switching</p>
                </div>
                <TextInput
                  type="number"
                  min={1}
                  max={10}
                  defaultValue={sticky}
                  className="w-16 shrink-0 text-center font-mono"
                  onChange={(e) => {
                    const n = parseInt(e.target.value, 10)
                    if (!Number.isNaN(n) && n >= 1) void patch({ stickyRoundRobinLimit: n })
                  }}
                />
              </div>
            )}

            <Row
              title="Combo round robin"
              desc="Rotate through combo providers instead of always starting with the first."
              className="mt-0"
              right={
                <Switch
                  checked={comboRR}
                  onCheckedChange={(v) => void patch({ comboStrategy: v ? 'round-robin' : 'fallback' })}
                />
              }
            />

            {comboRR && (
              <div className="flex items-center justify-between gap-4 border-t border-border bg-surface-2/50 px-5 py-3">
                <div className="min-w-0">
                  <p className="text-[13px] font-medium text-text-main">Combo sticky limit</p>
                  <p className="mt-0.5 text-[11px] text-text-muted">Calls per combo model before switching</p>
                </div>
                <TextInput
                  type="number"
                  min={1}
                  max={100}
                  defaultValue={comboSticky}
                  className="w-16 shrink-0 text-center font-mono"
                  onChange={(e) => {
                    const n = parseInt(e.target.value, 10)
                    if (!Number.isNaN(n) && n >= 1) void patch({ comboStickyRoundRobinLimit: n })
                  }}
                />
              </div>
            )}

            <p className="border-t border-border px-5 py-3 text-[11px] leading-relaxed text-text-muted">
              {fallbackRR
                ? `Distributing across all accounts, ${sticky} call${sticky === 1 ? '' : 's'} per account.`
                : 'Using accounts in priority order (fill first).'}
              {comboRR
                ? ` Combos rotate after ${comboSticky} call${comboSticky === 1 ? '' : 's'} per model.`
                : ' Combos always start with their first model.'}
            </p>
          </Section>

          {/* Network */}
          <Section icon="wifi" title="Network" subtitle="Outbound traffic from the gateway.">
            <Row
              title="Outbound proxy"
              desc="Route OAuth and provider requests through a proxy."
              right={
                <Switch
                  checked={settings.outboundProxyEnabled === true}
                  disabled={proxyLoading}
                  onCheckedChange={(v) => void toggleProxy(v)}
                />
              }
            />

            {settings.outboundProxyEnabled === true && (
              <form onSubmit={applyProxy} className="space-y-3 border-t border-border px-5 py-4">
                <Field label="Proxy URL" hint="Leave empty to inherit the env proxy, if any.">
                  <TextInput
                    placeholder="http://127.0.0.1:7897"
                    value={proxyForm.url}
                    onChange={(e) => setProxyForm((p) => ({ ...p, url: e.target.value }))}
                    disabled={proxyLoading}
                  />
                </Field>
                <Field label="No proxy" hint="Comma-separated hosts that bypass the proxy.">
                  <TextInput
                    placeholder="localhost,127.0.0.1"
                    value={proxyForm.noProxy}
                    onChange={(e) => setProxyForm((p) => ({ ...p, noProxy: e.target.value }))}
                    disabled={proxyLoading}
                  />
                </Field>
                <div className="flex flex-wrap gap-2 pt-1">
                  <Button type="submit" size="sm" disabled={proxyLoading}>
                    Apply
                  </Button>
                  <Button type="button" variant="ghost" size="sm" disabled={proxyTestLoading || proxyLoading} onClick={() => void testProxy()}>
                    Test URL
                  </Button>
                </div>
              </form>
            )}

            {proxyStatus.message && (
              <div className="border-t border-border px-5 py-3">
                <Notice status={proxyStatus} />
              </div>
            )}
          </Section>

          {/* Observability */}
          <Section icon="monitoring" title="Observability">
            <Row
              title="Record request details"
              desc="Store request bodies and timings for the logs view."
              right={
                <Switch
                  checked={settings.enableObservability === true}
                  onCheckedChange={(v) => void patch({ enableObservability: v })}
                />
              }
            />
          </Section>

          <div className="flex flex-col gap-2 sm:flex-row">
            <Button
              variant="ghost"
              onClick={() => setShutdownOpen(true)}
              className="text-danger hover:bg-danger/10"
            >
              <Icon name="power_settings_new" /> Shutdown
            </Button>
            <Button variant="ghost" onClick={() => void handleLogout()}>
              <Icon name="logout" /> Logout
            </Button>
          </div>

          <div className="pt-2 text-center text-[11px] text-text-muted">
            <p>KineRouter{version ? ` v${version}` : ''}</p>
            <p className="mt-1">
              {['localhost', '127.0.0.1', '::1'].includes(window.location.hostname)
                ? 'Local Mode - All data stored on your machine'
                : 'Remote Mode'}
            </p>
          </div>
        </div>

      {/* Language picker */}
      <Modal isOpen={langOpen} onClose={() => setLangOpen(false)} title="Select Language" size="full">
        <div className="grid grid-cols-[repeat(auto-fill,minmax(100px,1fr))] gap-2">
          {LOCALES.map((item) => {
            const active = item.id === locale
            return (
              <button
                key={item.id}
                onClick={() => void chooseLocale(item.id)}
                title={item.name}
                className={cn(
                  'flex w-full cursor-pointer flex-col items-center justify-start gap-1 rounded-lg px-2 py-3 text-xs font-medium transition-colors',
                  active
                    ? 'bg-surface-2 text-text-main ring-1 ring-ring'
                    : 'text-text-main hover:bg-surface-2',
                )}
              >
                <span className="text-2xl">{item.flag}</span>
                <span className="flex h-8 items-center text-center leading-tight">{item.name}</span>
                {active && <Icon name="check" className="text-[14px] text-text-muted" />}
              </button>
            )
          })}
        </div>
      </Modal>

      <ConfirmModal
        isOpen={shutdownOpen}
        onClose={() => setShutdownOpen(false)}
        onConfirm={handleShutdown}
        title="Close Proxy"
        message="Are you sure you want to close the proxy server?"
        confirmLabel="Close"
        variant="danger"
        loading={shuttingDown}
      />

      <Modal
        isOpen={dbAuth.open}
        onClose={() => setDbAuth({ open: false, mode: 'export', password: '' })}
        title="Confirm Password"
        size="sm"
        footer={
          <div className="flex justify-end gap-2">
            <Button variant="ghost" onClick={() => setDbAuth({ open: false, mode: 'export', password: '' })} disabled={dbLoading}>
              Cancel
            </Button>
            <Button onClick={() => void confirmDbAuth()} disabled={dbLoading || !dbAuth.password}>Confirm</Button>
          </div>
        }
      >
        <p className="mb-3 text-sm text-text-muted">
          Enter your current password to {dbAuth.mode === 'export' ? 'export' : 'import'} the database.
        </p>
        <Input
          type="password"
          autoFocus
          placeholder="Current password"
          value={dbAuth.password}
          onChange={(e) => setDbAuth((s) => ({ ...s, password: e.target.value }))}
          onKeyDown={(e) => {
            if (e.key === 'Enter' && dbAuth.password) void confirmDbAuth()
          }}
        />
      </Modal>
    </>
  )
}
