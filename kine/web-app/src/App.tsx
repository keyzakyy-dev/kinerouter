import { lazy, Suspense } from 'react'
import { Navigate, Route, Routes } from 'react-router-dom'
import { AppShell } from '@/components/layout'
import { RequireAuth, FullPageSpinner } from '@/lib/auth'

// Route-level splitting keeps recharts out of the first paint.
const LoginPage = lazy(() => import('@/pages/login'))
const CallbackPage = lazy(() => import('@/pages/callback'))
const EndpointPage = lazy(() => import('@/pages/endpoint'))
const ProvidersPage = lazy(() => import('@/pages/providers').then((m) => ({ default: m.ProvidersPage })))
const ProviderDetailPage = lazy(() =>
  import('@/pages/provider-detail').then((m) => ({ default: m.ProviderDetailPage })),
)
const CombosPage = lazy(() => import('@/pages/combos'))
const UsagePage = lazy(() => import('@/pages/usage'))
const SkillsPage = lazy(() => import('@/pages/skills'))
const ConsoleLogPage = lazy(() => import('@/pages/console-log'))
const ProxyPoolsPage = lazy(() => import('@/pages/proxy-pools'))
const PxpipePage = lazy(() => import('@/pages/pxpipe'))
const TokenSaverPage = lazy(() => import('@/pages/token-saver'))
const TranslatorPage = lazy(() => import('@/pages/translator'))
const QuotaPage = lazy(() => import('@/pages/quota'))
const CliToolsPage = lazy(() => import('@/pages/cli-tools'))
const CliToolDetailPage = lazy(() => import('@/pages/cli-tool-detail'))
const MitmPage = lazy(() => import('@/pages/mitm'))
const MediaProvidersPage = lazy(() =>
  import('@/pages/media-providers').then((m) => ({ default: m.MediaProvidersPage })),
)
const MediaProviderDetailPage = lazy(() =>
  import('@/pages/media-providers').then((m) => ({ default: m.MediaProviderDetailPage })),
)
const WebProvidersPage = lazy(() =>
  import('@/pages/media-providers').then((m) => ({ default: m.WebProvidersPage })),
)
const MediaComboDetailPage = lazy(() =>
  import('@/pages/media-providers').then((m) => ({ default: m.MediaComboDetailPage })),
)
const SettingsPage = lazy(() => import('@/pages/settings'))

export default function App() {
  return (
    <Suspense fallback={<FullPageSpinner />}>
      <Routes>
        <Route path="/login" element={<LoginPage />} />
        <Route path="/callback" element={<CallbackPage />} />
        <Route
          element={
            <RequireAuth>
              <AppShell />
            </RequireAuth>
          }
        >
          <Route path="/dashboard" element={<Navigate to="/dashboard/endpoint" replace />} />
          <Route path="/dashboard/endpoint" element={<EndpointPage />} />
          <Route path="/dashboard/providers" element={<ProvidersPage />} />
          <Route path="/dashboard/providers/:id" element={<ProviderDetailPage />} />
          <Route path="/dashboard/combos" element={<CombosPage />} />
          <Route path="/dashboard/usage" element={<UsagePage />} />
          <Route path="/dashboard/quota" element={<QuotaPage />} />
          <Route path="/dashboard/token-saver" element={<TokenSaverPage />} />
          <Route path="/dashboard/cli-tools" element={<CliToolsPage />} />
          <Route path="/dashboard/cli-tools/:toolId" element={<CliToolDetailPage />} />
          <Route path="/dashboard/mitm" element={<MitmPage />} />
          <Route path="/dashboard/proxy-pools" element={<ProxyPoolsPage />} />
          <Route path="/dashboard/pxpipe" element={<PxpipePage />} />
          <Route path="/dashboard/skills" element={<SkillsPage />} />
          <Route path="/dashboard/console-log" element={<ConsoleLogPage />} />
          <Route path="/dashboard/translator" element={<TranslatorPage />} />
          <Route path="/dashboard/media-providers/web" element={<WebProvidersPage />} />
          {/* combo/:id must precede :kind/:providerId — `combo` would otherwise
              match the :kind segment and the pair would 404 as a provider. */}
          <Route path="/dashboard/media-providers/combo/:id" element={<MediaComboDetailPage />} />
          <Route path="/dashboard/media-providers/:kind" element={<MediaProvidersPage />} />
          <Route path="/dashboard/media-providers/:kind/:providerId" element={<MediaProviderDetailPage />} />
          <Route path="/dashboard/settings" element={<SettingsPage />} />
          {/* KineRouter serves this page at /dashboard/profile; keep the alias so
              bookmarks and CLI links to either path land here. */}
          <Route path="/dashboard/profile" element={<SettingsPage />} />
        </Route>
        <Route path="/" element={<Navigate to="/dashboard" replace />} />
        <Route path="*" element={<Navigate to="/dashboard" replace />} />
      </Routes>
    </Suspense>
  )
}
