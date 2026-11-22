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
  iconClass,
  title,
  subtitle,
  children,
}: {
  icon: string
  iconClass: string
  title: string
  subtitle?: string
  children: React.ReactNode
}) {
  return (
    <div className="rounded-xl border border-border bg-surface p-5">
      <div className="mb-4 flex items-center gap-3">
        <div className={cn('flex size-10 shrink-0 items-center justify-center rounded-lg', iconClass)}>
          <Icon name={icon} className="text-[20px]" />
        </div>
        <div className="min-w-0">
          <h3 className="text-base font-semibold text-text-main sm:text-lg">{title}</h3>
          {subtitle && <p className="text-xs text-text-muted">{subtitle}</p>}
        </div>
      </div>
      {children}
    </div>
  )
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

  // Patches merge into the current settings only — re-hydrating the forms here
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

      <div className="max-w-2xl">
        <div className="flex flex-col gap-6">
          {/* Local Mode + theme + database backup */}
          <div className="rounded-xl border border-border bg-surface p-5">
            <div className="mb-4 flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
              <div className="flex items-center gap-3 sm:gap-4">
                <div className="flex size-10 shrink-0 items-center justify-center rounded-lg bg-success/10 text-success sm:size-12">
                  <Icon name="computer" className="text-xl sm:text-2xl" />
                </div>
                <div>
                  <h2 className="text-lg font-semibold text-text-main sm:text-xl">Local Mode</h2>
                  <p className="text-sm text-text-muted">Running on your machine</p>
                </div>
              </div>
              <div className="inline-flex w-full rounded-lg bg-black/5 p-1 sm:w-auto dark:bg-white/5">
                {THEME_OPTIONS.map((option) => (
                  <button
                    key={option.value}
                    type="button"
                    onClick={() => setTheme(option.value)}
                    className={cn(
                      'flex flex-1 cursor-pointer items-center justify-center gap-1 rounded-md px-2 py-1.5 font-medium transition-all sm:flex-initial sm:gap-1.5 sm:px-3',
                      theme === option.value
                        ? 'bg-white text-text-main shadow-sm dark:bg-white/10'
                        : 'text-text-muted hover:text-text-main',
                    )}
                  >
                    <Icon name={option.icon} className="text-[18px]" />
                    <span className="text-xs capitalize sm:text-sm">{option.label}</span>
                  </button>
                ))}
              </div>
            </div>

            <div className="flex flex-col gap-3 border-t border-border pt-4">
              <div className="flex flex-col gap-2 rounded-lg border border-border bg-bg p-3 sm:flex-row sm:items-center sm:justify-between">
                <div>
                  <p className="text-sm font-medium text-text-main sm:text-base">Database Location</p>
                  <p className="break-all font-mono text-xs text-text-muted sm:text-sm">
                    {'~/.kinerouter/db/data.sqlite'} <span className="opacity-60">(or $DATA_DIR/db/data.sqlite)</span>
                  </p>
                </div>
              </div>
              <div className="flex flex-col gap-2 sm:flex-row">
                <Button
                  variant="secondary"
                  className="w-full sm:w-auto"
                  disabled={dbLoading}
                  onClick={() => setDbAuth({ open: true, mode: 'export', password: '' })}
                >
                  <Icon name="download" /> Download Backup
                </Button>
                <Button
                  variant="outline"
                  className="w-full sm:w-auto"
                  disabled={dbLoading}
                  onClick={() => importFileRef.current?.click()}
                >
                  <Icon name="upload" /> Import Backup
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
              <Notice status={dbStatus} />
            </div>
          </div>

          {/* Language */}
          <Section icon="language" iconClass="bg-info/10 text-info" title="Language">
            {/* ponytail: locale persists server-side; no bundled translation
                catalogue yet. Add a message table when strings exist to translate. */}
            <button
              onClick={() => setLangOpen(true)}
              className="flex w-full cursor-pointer items-center justify-between rounded-lg border border-border bg-bg p-3 transition-colors hover:border-primary/50"
            >
              <span className="text-sm text-text-muted">Display language</span>
              <span className="text-2xl">{currentLocale.flag}</span>
            </button>
          </Section>

          {/* Security */}
          <Section icon="shield" iconClass="bg-primary/10 text-primary" title="Security">
            <div className="flex flex-col gap-4">
              <div className="flex items-start justify-between gap-4 sm:items-center">
                <div className="min-w-0 flex-1">
                  <p className="text-sm font-medium text-text-main sm:text-base">Require login</p>
                  <p className="text-xs text-text-muted sm:text-sm">
                    When ON, dashboard requires password. When OFF, access without login.
                  </p>
                </div>
                <Switch checked={requireLogin} onCheckedChange={(v) => void saveRequireLogin(v)} />
              </div>

              {requireLogin && (
                <form onSubmit={changePassword} className="flex flex-col gap-4 border-t border-border/50 pt-4">
                  {settings.hasPassword && (
                    <div className="flex flex-col gap-2">
                      <label className="text-xs font-medium sm:text-sm">Current Password</label>
                      <Input
                        type="password"
                        placeholder="Enter current password"
                        value={passwords.current}
                        onChange={(e) => setPasswords({ ...passwords, current: e.target.value })}
                        required
                      />
                    </div>
                  )}
                  <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
                    <div className="flex flex-col gap-2">
                      <label className="text-xs font-medium sm:text-sm">New Password</label>
                      <Input
                        type="password"
                        placeholder="Enter new password"
                        value={passwords.next}
                        onChange={(e) => setPasswords({ ...passwords, next: e.target.value })}
                        required
                      />
                    </div>
                    <div className="flex flex-col gap-2">
                      <label className="text-xs font-medium sm:text-sm">Confirm New Password</label>
                      <Input
                        type="password"
                        placeholder="Confirm new password"
                        value={passwords.confirm}
                        onChange={(e) => setPasswords({ ...passwords, confirm: e.target.value })}
                        required
                      />
                    </div>
                  </div>
                  <Notice status={passStatus} />
                  <div className="pt-2">
                    <Button type="submit" disabled={passLoading} className="w-full sm:w-auto">
                      {settings.hasPassword ? 'Update Password' : 'Set Password'}
                    </Button>
                  </div>
                </form>
              )}
            </div>
          </Section>

          {/* SSO */}
          <div className="rounded-xl border border-border bg-surface p-5">
            <button
              type="button"
              onClick={() => setSsoExpanded((v) => !v)}
              className="flex w-full cursor-pointer items-center gap-3 text-left"
            >
              <div className="flex size-10 shrink-0 items-center justify-center rounded-lg bg-indigo-500/10 text-indigo-500">
                <Icon name="lock_open" className="text-[20px]" />
              </div>
              <div className="min-w-0 flex-1">
                <h3 className="text-base font-semibold text-text-main sm:text-lg">Single Sign-On (SSO)</h3>
                <p className="text-xs text-text-muted">
                  {ssoActive
                    ? `${settings.ssoType === 'saml' ? 'SAML 2.0' : 'OIDC'} SSO active`
                    : settings.authMode === 'both'
                      ? `Password + ${settings.ssoType === 'saml' ? 'SAML 2.0' : 'OIDC'} active`
                      : 'Optional SSO via Okta, Entra ID, Keycloak, or OIDC'}
                </p>
              </div>
              <Icon name={ssoExpanded ? 'expand_less' : 'expand_more'} className="shrink-0 text-text-muted" />
            </button>

            {ssoExpanded && (
              <div className="mt-4 flex flex-col gap-4">
                <p className="text-xs text-text-muted sm:text-sm">
                  Configure enterprise Single Sign-On (SSO) for dashboard access using SAML 2.0 or OIDC.
                </p>

                <div className="flex flex-col gap-2">
                  <label className="text-sm font-medium sm:text-base">SSO Protocol</label>
                  <div className="flex rounded-lg border border-border bg-black/5 p-1 dark:bg-white/5">
                    {(['saml', 'oidc'] as const).map((tab) => (
                      <button
                        key={tab}
                        type="button"
                        onClick={() => setSsoTab(tab)}
                        className={cn(
                          'flex-1 cursor-pointer rounded-md px-3 py-1.5 text-center text-xs font-medium transition-all sm:text-sm',
                          ssoTab === tab
                            ? 'bg-white text-text-main shadow-sm dark:bg-white/10'
                            : 'text-text-muted hover:text-text-main',
                        )}
                      >
                        {tab === 'saml' ? 'SAML 2.0' : 'OIDC'}
                      </button>
                    ))}
                  </div>
                </div>

                <div className="flex flex-col gap-2">
                  <label className="text-sm font-medium sm:text-base">Auth Mode</label>
                  <div className="grid grid-cols-1 gap-2 sm:grid-cols-3">
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
                          className={cn(
                            'cursor-pointer rounded-lg border p-3 text-left transition-colors',
                            active
                              ? 'border-primary bg-primary/5'
                              : 'border-border bg-bg hover:bg-black/5 dark:hover:bg-white/5',
                          )}
                        >
                          <p className="text-sm font-medium text-text-main sm:text-base">
                            {mode === 'sso' ? `${ssoTab === 'saml' ? 'SAML' : 'OIDC'} only` : AuthModeCard[mode].title}
                          </p>
                          <p className="mt-1 text-xs text-text-muted sm:text-sm">{AuthModeCard[mode].desc}</p>
                        </button>
                      )
                    })}
                  </div>
                </div>

                {ssoTab === 'saml' ? (
                  <div className="flex flex-col gap-4 border-t border-border/50 pt-2">
                    <div className="overflow-hidden rounded-lg border border-border bg-bg/80">
                      <button
                        type="button"
                        onClick={() => setSamlGuide((v) => !v)}
                        className="flex w-full cursor-pointer items-center justify-between gap-2 p-3 text-left transition-colors hover:bg-surface/50"
                      >
                        <div className="flex items-center gap-2">
                          <Icon name="menu_book" className="text-lg text-primary" />
                          <div>
                            <p className="text-xs font-semibold text-text-main sm:text-sm">
                              IdP Setup Guidelines &amp; Provider Configuration Instructions
                            </p>
                            <p className="text-[11px] text-text-muted">
                              Click to view setup steps for AWS IAM Identity Center, Okta, Entra ID, Keycloak, &amp; Authentik
                            </p>
                          </div>
                        </div>
                        <Icon
                          name="expand_more"
                          className={cn('text-lg text-text-muted transition-transform', samlGuide && 'rotate-180')}
                        />
                      </button>
                      {samlGuide && (
                        <div className="flex flex-col gap-3 border-t border-border bg-surface/30 p-4 text-xs text-text-main">
                          <div className="rounded border border-primary/20 bg-primary/5 p-2.5 text-xs text-primary">
                            <p className="mb-1 font-semibold">🔑 Required Service Provider (SP) Values for your IdP Setup:</p>
                            <ul className="list-disc space-y-1 pl-4 font-mono text-[11px]">
                              <li><b>Assertion Consumer Service (ACS) URL:</b> <code className="break-all rounded bg-bg px-1 py-0.5">{samlAcsUrl}</code></li>
                              <li><b>SP Entity ID / Audience URI:</b> <code className="break-all rounded bg-bg px-1 py-0.5">{samlForm.issuer || 'urn:kinerouter:sp'}</code></li>
                              <li><b>NameID Format:</b> <code className="rounded bg-bg px-1 py-0.5">EmailAddress</code> or <code className="rounded bg-bg px-1 py-0.5">Unspecified</code></li>
                            </ul>
                          </div>
                          <div className="grid grid-cols-1 gap-3 pt-1 md:grid-cols-2">
                            <div className="flex flex-col gap-1.5 rounded border border-border bg-bg/50 p-3">
                              <p className="flex items-center gap-1.5 font-semibold text-text-main"><span>☁️</span> AWS IAM Identity Center</p>
                              <ol className="list-decimal space-y-1 pl-4 text-text-muted">
                                <li>Applications → <b>Add application</b> → <b>Add custom SAML 2.0 application</b>.</li>
                                <li>Set <b>Application ACS URL</b> to <code className="font-mono text-text-main">{samlAcsUrl}</code>.</li>
                                <li>Set <b>Application SAML audience</b> to <code className="font-mono text-text-main">{samlForm.issuer || 'urn:kinerouter:sp'}</code>.</li>
                                <li>Map <code className="font-mono text-text-main">Subject</code> or <code className="font-mono text-text-main">email</code> to <code className="font-mono text-text-main">{'${user:email}'}</code>.</li>
                                <li>Download the <b>IAM Identity Center SAML metadata XML</b> and use 1-Click Import below.</li>
                              </ol>
                            </div>
                            <div className="flex flex-col gap-1.5 rounded border border-border bg-bg/50 p-3">
                              <p className="flex items-center gap-1.5 font-semibold text-text-main"><span>🔷</span> Microsoft Entra ID (Azure AD)</p>
                              <ol className="list-decimal space-y-1 pl-4 text-text-muted">
                                <li>Enterprise Applications → <b>New application</b> → <b>Create your own application</b>.</li>
                                <li>Select <b>Single sign-on</b> → <b>SAML</b>.</li>
                                <li><b>Identifier (Entity ID):</b> <code className="font-mono text-text-main">{samlForm.issuer || 'urn:kinerouter:sp'}</code></li>
                                <li><b>Reply URL (ACS):</b> <code className="font-mono text-text-main">{samlAcsUrl}</code></li>
                                <li>Download <b>Federation Metadata XML</b> and import or copy the X.509 certificate.</li>
                              </ol>
                            </div>
                            <div className="flex flex-col gap-1.5 rounded border border-border bg-bg/50 p-3">
                              <p className="flex items-center gap-1.5 font-semibold text-text-main"><span>🟢</span> Okta / Auth0</p>
                              <ol className="list-decimal space-y-1 pl-4 text-text-muted">
                                <li>Applications → <b>Create App Integration</b> → <b>SAML 2.0</b>.</li>
                                <li><b>Single Sign-On URL:</b> <code className="font-mono text-text-main">{samlAcsUrl}</code></li>
                                <li><b>Audience URI (SP Entity ID):</b> <code className="font-mono text-text-main">{samlForm.issuer || 'urn:kinerouter:sp'}</code></li>
                                <li>Name ID format: <i>EmailAddress</i>.</li>
                                <li>Download the IdP metadata XML or copy the X.509 cert.</li>
                              </ol>
                            </div>
                            <div className="flex flex-col gap-1.5 rounded border border-border bg-bg/50 p-3">
                              <p className="flex items-center gap-1.5 font-semibold text-text-main"><span>🛡️</span> Keycloak / Authentik</p>
                              <ol className="list-decimal space-y-1 pl-4 text-text-muted">
                                <li>Clients → <b>Create client</b> → Select <b>SAML</b>.</li>
                                <li><b>Client ID:</b> <code className="font-mono text-text-main">{samlForm.issuer || 'urn:kinerouter:sp'}</code></li>
                                <li><b>Master SAML Processing URL:</b> <code className="font-mono text-text-main">{samlAcsUrl}</code></li>
                                <li>Export the SAML Descriptor XML or copy the IDP certificate PEM.</li>
                              </ol>
                            </div>
                          </div>
                        </div>
                      )}
                    </div>

                    <div className="flex flex-col items-stretch justify-between gap-3 rounded-lg border border-dashed border-primary/40 bg-primary/5 p-3 sm:flex-row sm:items-center">
                      <div>
                        <p className="text-sm font-medium text-text-main">1-Click IdP Metadata XML Import</p>
                        <p className="text-xs text-text-muted">Auto-fill SSO URL, Issuer &amp; Cert from XML metadata</p>
                      </div>
                      <Button
                        type="button"
                        variant="outline"
                        size="sm"
                        onClick={() => idpFileRef.current?.click()}
                      >
                        <Icon name="upload_file" /> Upload Metadata XML
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
                      <div className="flex flex-col gap-2">
                        <label className="text-sm font-medium sm:text-base">Single Sign-On Service URL (samlEntryPoint)</label>
                        <Input
                          placeholder="https://idp.example.com/app/saml/sso/..."
                          value={samlForm.entryPoint}
                          onChange={(e) => setSamlForm((p) => ({ ...p, entryPoint: e.target.value }))}
                          disabled={samlLoading}
                        />
                      </div>
                      <div className="flex flex-col gap-2">
                        <label className="text-sm font-medium sm:text-base">SP Entity ID / Audience (samlIssuer)</label>
                        <Input
                          placeholder="urn:kinerouter:sp"
                          value={samlForm.issuer}
                          onChange={(e) => setSamlForm((p) => ({ ...p, issuer: e.target.value }))}
                          disabled={samlLoading}
                        />
                      </div>
                      <div className="flex flex-col gap-2">
                        <div className="flex items-center justify-between">
                          <label className="text-sm font-medium sm:text-base">IdP X.509 Certificate (samlCert)</label>
                          <Button type="button" variant="outline" size="sm" onClick={() => certFileRef.current?.click()}>
                            <Icon name="file_upload" /> Upload Certificate
                          </Button>
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
                        </div>
                        <textarea
                          rows={4}
                          placeholder={'-----BEGIN CERTIFICATE-----\nMIIC...\n-----END CERTIFICATE-----'}
                          value={samlForm.cert}
                          onChange={(e) => setSamlForm((p) => ({ ...p, cert: e.target.value }))}
                          className="w-full rounded-lg border border-border bg-bg p-2.5 font-mono text-xs text-text-main focus:border-primary focus:outline-none"
                          disabled={samlLoading}
                        />
                        <p className="text-xs text-text-muted">Paste raw Base64 certificate or PEM block.</p>
                      </div>
                      <div className="grid grid-cols-1 gap-4 sm:grid-cols-3">
                        <div className="flex flex-col gap-2">
                          <label className="text-sm font-medium sm:text-base">Login Button Label</label>
                          <Input
                            placeholder="Sign in with SAML SSO"
                            value={samlForm.loginLabel}
                            onChange={(e) => setSamlForm((p) => ({ ...p, loginLabel: e.target.value }))}
                          />
                        </div>
                        <div className="flex flex-col gap-2">
                          <label className="text-sm font-medium sm:text-base">Email Claim Attribute</label>
                          <Input
                            placeholder="email"
                            value={samlForm.attributeEmail}
                            onChange={(e) => setSamlForm((p) => ({ ...p, attributeEmail: e.target.value }))}
                          />
                        </div>
                        <div className="flex flex-col gap-2">
                          <label className="text-sm font-medium sm:text-base">Display Name Claim</label>
                          <Input
                            placeholder="name"
                            value={samlForm.attributeName}
                            onChange={(e) => setSamlForm((p) => ({ ...p, attributeName: e.target.value }))}
                          />
                        </div>
                      </div>
                    </div>

                    <div className="flex flex-col gap-2 rounded-lg border border-border bg-bg p-3 text-xs text-text-muted sm:text-sm">
                      <div className="flex items-center justify-between gap-2">
                        <div>
                          <p className="font-medium text-text-main">ACS Callback URL</p>
                          <code className="block break-all font-mono text-xs">{samlAcsUrl}</code>
                        </div>
                        <Button
                          type="button"
                          variant="outline"
                          size="sm"
                          onClick={() => {
                            void navigator.clipboard.writeText(samlAcsUrl)
                            setSamlStatus({ type: 'success', message: 'ACS URL copied to clipboard!' })
                          }}
                        >
                          <Icon name="content_copy" /> Copy
                        </Button>
                      </div>
                      <div className="flex items-center justify-between gap-2 border-t border-border/50 pt-2">
                        <div>
                          <p className="font-medium text-text-main">SP XML Metadata</p>
                          <code className="block break-all font-mono text-xs">{samlMetadataUrl}</code>
                        </div>
                        <a
                          href={samlMetadataUrl}
                          target="_blank"
                          rel="noopener noreferrer"
                          download="kinerouter-sp-metadata.xml"
                          className="inline-flex items-center gap-1 text-xs font-medium text-primary hover:underline"
                        >
                          <Icon name="download" className="text-[16px]" /> Download XML
                        </a>
                      </div>
                    </div>

                    <div className="flex flex-col gap-2 border-t border-border/50 pt-2 sm:flex-row">
                      <Button type="button" disabled={samlLoading} onClick={() => void saveSaml()} className="w-full sm:w-auto">
                        Save SAML settings
                      </Button>
                      <Button type="button" variant="outline" disabled={samlTestLoading} onClick={() => void testSaml()} className="w-full sm:w-auto">
                        Test SAML settings
                      </Button>
                    </div>
                    <Notice status={samlTestStatus} />
                    <Notice status={samlStatus} />
                  </div>
                ) : (
                  <div className="flex flex-col gap-4 border-t border-border/50 pt-2">
                    <div className="grid grid-cols-1 gap-4">
                      <div className="flex flex-col gap-2">
                        <label className="text-sm font-medium sm:text-base">Issuer URL</label>
                        <Input
                          placeholder="https://auth.example.com/application/o/kinerouter/"
                          value={oidcForm.issuerUrl}
                          onChange={(e) => setOidcForm((p) => ({ ...p, issuerUrl: e.target.value }))}
                          disabled={oidcLoading}
                        />
                      </div>
                      <div className="flex flex-col gap-2">
                        <label className="text-sm font-medium sm:text-base">Client ID</label>
                        <Input
                          placeholder="kinerouter-dashboard"
                          value={oidcForm.clientId}
                          onChange={(e) => setOidcForm((p) => ({ ...p, clientId: e.target.value }))}
                          disabled={oidcLoading}
                        />
                      </div>
                      <div className="flex flex-col gap-2">
                        <label className="text-sm font-medium sm:text-base">Client Secret</label>
                        <Input
                          type="password"
                          placeholder="Leave blank to keep existing secret"
                          value={oidcSecret}
                          onChange={(e) => setOidcSecret(e.target.value)}
                          disabled={oidcLoading}
                        />
                        <p className="text-xs text-text-muted sm:text-sm">This value is write-only after saving.</p>
                      </div>
                      <div className="flex flex-col gap-2">
                        <label className="text-sm font-medium sm:text-base">Scopes</label>
                        <Input
                          placeholder="openid profile email"
                          value={oidcForm.scopes}
                          onChange={(e) => setOidcForm((p) => ({ ...p, scopes: e.target.value }))}
                          disabled={oidcLoading}
                        />
                      </div>
                      <div className="flex flex-col gap-2">
                        <label className="text-sm font-medium sm:text-base">Login Button Label</label>
                        <Input
                          placeholder="Sign in with OIDC"
                          value={oidcForm.loginLabel}
                          onChange={(e) => setOidcForm((p) => ({ ...p, loginLabel: e.target.value }))}
                          disabled={oidcLoading}
                        />
                      </div>
                    </div>

                    <div className="rounded-lg border border-border bg-bg p-3 text-xs text-text-muted sm:text-sm">
                      <p className="mb-1 font-medium text-text-main">Redirect URI</p>
                      <code className="block break-all font-mono">{oidcRedirectUri}</code>
                    </div>

                    <div className="flex flex-col gap-2 border-t border-border/50 pt-2 sm:flex-row">
                      <Button type="button" disabled={oidcLoading} onClick={() => void saveOidc()} className="w-full sm:w-auto">
                        Save OIDC settings
                      </Button>
                      <Button type="button" variant="outline" disabled={oidcTestLoading} onClick={() => void testOidc()} className="w-full sm:w-auto">
                        Test connection
                      </Button>
                    </div>
                    <Notice status={oidcTestStatus} />
                    <Notice status={oidcStatus} />
                  </div>
                )}

                {ssoActive && (
                  <p className="text-xs text-warning sm:text-sm">
                    SSO login ({settings.ssoType === 'saml' ? 'SAML 2.0' : 'OIDC'}) is currently active. Password login is disabled until you switch back.
                  </p>
                )}
                {settings.authMode === 'both' && (
                  <p className="text-xs text-warning sm:text-sm">
                    Password and SSO login ({settings.ssoType === 'saml' ? 'SAML 2.0' : 'OIDC'}) are both active.
                  </p>
                )}
              </div>
            )}
          </div>

          {/* Routing strategy */}
          <Section icon="route" iconClass="bg-info/10 text-info" title="Routing Strategy">
            <div className="flex flex-col gap-4">
              <div className="flex items-start justify-between gap-4 sm:items-center">
                <div className="min-w-0 flex-1">
                  <p className="text-sm font-medium text-text-main sm:text-base">Round Robin</p>
                  <p className="text-xs text-text-muted sm:text-sm">Cycle through accounts to distribute load</p>
                </div>
                <Switch
                  checked={fallbackRR}
                  onCheckedChange={(v) => void patch({ fallbackStrategy: v ? 'round-robin' : 'fill-first' })}
                />
              </div>

              {fallbackRR && (
                <div className="flex items-start justify-between gap-4 border-t border-border/50 pt-2 sm:items-center">
                  <div className="min-w-0 flex-1">
                    <p className="text-sm font-medium text-text-main sm:text-base">Sticky Limit</p>
                    <p className="text-xs text-text-muted sm:text-sm">Calls per account before switching</p>
                  </div>
                  <Input
                    type="number"
                    min={1}
                    max={10}
                    defaultValue={sticky}
                    className="w-16 shrink-0 text-center sm:w-20"
                    onChange={(e) => {
                      const n = parseInt(e.target.value, 10)
                      if (!Number.isNaN(n) && n >= 1) void patch({ stickyRoundRobinLimit: n })
                    }}
                  />
                </div>
              )}

              <div className="flex items-start justify-between gap-4 border-t border-border/50 pt-4 sm:items-center">
                <div className="min-w-0 flex-1">
                  <p className="text-sm font-medium text-text-main sm:text-base">Combo Round Robin</p>
                  <p className="text-xs text-text-muted sm:text-sm">
                    Cycle through providers in combos instead of always starting with first
                  </p>
                </div>
                <Switch
                  checked={comboRR}
                  onCheckedChange={(v) => void patch({ comboStrategy: v ? 'round-robin' : 'fallback' })}
                />
              </div>

              {comboRR && (
                <div className="flex items-center justify-between border-t border-border/50 pt-2">
                  <div>
                    <p className="font-medium">Combo Sticky Limit</p>
                    <p className="text-sm text-text-muted">Calls per combo model before switching</p>
                  </div>
                  <Input
                    type="number"
                    min={1}
                    max={100}
                    defaultValue={comboSticky}
                    className="w-20 text-center"
                    onChange={(e) => {
                      const n = parseInt(e.target.value, 10)
                      if (!Number.isNaN(n) && n >= 1) void patch({ comboStickyRoundRobinLimit: n })
                    }}
                  />
                </div>
              )}

              <p className="border-t border-border/50 pt-2 text-xs italic text-text-muted">
                {fallbackRR
                  ? `Currently distributing requests across all available accounts with ${sticky} calls per account.`
                  : 'Currently using accounts in priority order (Fill First).'}
                {comboRR
                  ? ` Combos rotate after ${comboSticky} call${comboSticky === 1 ? '' : 's'} per model.`
                  : ' Combos always start with their first model.'}
              </p>
            </div>
          </Section>

          {/* Network */}
          <Section icon="wifi" iconClass="bg-purple-500/10 text-purple-500" title="Network">
            <div className="flex flex-col gap-4">
              <div className="flex items-start justify-between gap-4 sm:items-center">
                <div className="min-w-0 flex-1">
                  <p className="text-sm font-medium text-text-main sm:text-base">Outbound Proxy</p>
                  <p className="text-xs text-text-muted sm:text-sm">Enable proxy for OAuth + provider outbound requests.</p>
                </div>
                <Switch
                  checked={settings.outboundProxyEnabled === true}
                  disabled={proxyLoading}
                  onCheckedChange={(v) => void toggleProxy(v)}
                />
              </div>

              {settings.outboundProxyEnabled === true && (
                <form onSubmit={applyProxy} className="flex flex-col gap-4 border-t border-border/50 pt-2">
                  <div className="flex flex-col gap-2">
                    <label className="text-sm font-medium sm:text-base">Proxy URL</label>
                    <Input
                      placeholder="http://127.0.0.1:7897"
                      value={proxyForm.url}
                      onChange={(e) => setProxyForm((p) => ({ ...p, url: e.target.value }))}
                      disabled={proxyLoading}
                    />
                    <p className="text-xs text-text-muted sm:text-sm">Leave empty to inherit existing env proxy (if any).</p>
                  </div>

                  <div className="flex flex-col gap-2 border-t border-border/50 pt-2">
                    <label className="text-sm font-medium sm:text-base">No Proxy</label>
                    <Input
                      placeholder="localhost,127.0.0.1"
                      value={proxyForm.noProxy}
                      onChange={(e) => setProxyForm((p) => ({ ...p, noProxy: e.target.value }))}
                      disabled={proxyLoading}
                    />
                    <p className="text-xs text-text-muted sm:text-sm">Comma-separated hostnames/domains to bypass the proxy.</p>
                  </div>

                  <div className="flex flex-col items-stretch gap-2 border-t border-border/50 pt-2 sm:flex-row sm:items-center">
                    <Button type="button" variant="secondary" disabled={proxyTestLoading || proxyLoading} onClick={() => void testProxy()} className="w-full sm:w-auto">
                      Test proxy URL
                    </Button>
                    <Button type="submit" disabled={proxyLoading} className="w-full sm:w-auto">Apply</Button>
                  </div>
                </form>
              )}

              {proxyStatus.message && (
                <div className="border-t border-border/50 pt-2">
                  <Notice status={proxyStatus} />
                </div>
              )}
            </div>
          </Section>

          {/* Observability */}
          <Section icon="monitoring" iconClass="bg-orange-500/10 text-orange-500" title="Observability">
            <div className="flex items-start justify-between gap-4 sm:items-center">
              <div className="min-w-0 flex-1">
                <p className="text-sm font-medium text-text-main sm:text-base">Enable Observability</p>
                <p className="text-xs text-text-muted sm:text-sm">Record request details for inspection in the logs view</p>
              </div>
              <Switch
                checked={settings.enableObservability === true}
                onCheckedChange={(v) => void patch({ enableObservability: v })}
              />
            </div>
          </Section>

          <div className="flex flex-col gap-2 sm:flex-row">
            <Button
              variant="outline"
              onClick={() => setShutdownOpen(true)}
              className="w-full border-danger/30 text-danger hover:bg-danger/10 hover:border-danger/50"
            >
              <Icon name="power_settings_new" /> Shutdown
            </Button>
            <Button variant="outline" onClick={() => void handleLogout()} className="w-full">
              <Icon name="logout" /> Logout
            </Button>
          </div>

          <div className="py-4 text-center text-xs text-text-muted sm:text-sm">
            <p>KineRouter{version ? ` v${version}` : ''}</p>
            <p className="mt-1">
              {['localhost', '127.0.0.1', '::1'].includes(window.location.hostname)
                ? 'Local Mode - All data stored on your machine'
                : 'Remote Mode'}
            </p>
          </div>
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
                    ? 'bg-primary/15 text-primary ring-2 ring-primary'
                    : 'text-text-main hover:bg-black/5 dark:hover:bg-white/5',
                )}
              >
                <span className="text-2xl">{item.flag}</span>
                <span className="flex h-8 items-center text-center leading-tight">{item.name}</span>
                {active && <Icon name="check" className="text-sm" />}
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
