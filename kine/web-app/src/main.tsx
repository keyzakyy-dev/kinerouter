import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { BrowserRouter } from 'react-router-dom'
import { AuthProvider } from '@/lib/auth'
import { ToastProvider } from '@/lib/toast'
import App from './App'
import 'material-symbols/outlined.css'
import './index.css'

// Icon ligature text stays hidden until the font is ready (mirrors the
// KineRouter dashboard: .material-symbols-outlined starts at opacity 0).
if (document.fonts?.ready) {
  document.fonts.ready.then(() => document.documentElement.classList.add('fonts-loaded'))
} else {
  document.documentElement.classList.add('fonts-loaded')
}

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <BrowserRouter>
      <AuthProvider>
        <ToastProvider>
          <App />
        </ToastProvider>
      </AuthProvider>
    </BrowserRouter>
  </StrictMode>,
)
