import { createContext, useCallback, useContext, useMemo, useState } from 'react'
import { Icon } from '@/components/icon'
import { cn } from '@/lib/utils'

/**
 * Toast notifications, ported from src/store/notificationStore.js so ported
 * pages can keep calling notify.success / error / warning / info.
 */
type ToastType = 'success' | 'error' | 'warning' | 'info'
type Toast = { id: number; type: ToastType; message: string; title?: string }

type Notifier = {
  success: (message: string, title?: string) => void
  error: (message: string, title?: string) => void
  warning: (message: string, title?: string) => void
  info: (message: string, title?: string) => void
}
const noop = () => {}
const ToastContext = createContext<Notifier>({
  success: noop,
  error: noop,
  warning: noop,
  info: noop,
})

let nextId = 0

const STYLES: Record<ToastType, { icon: string; className: string }> = {
  success: { icon: 'check_circle', className: 'text-success' },
  error: { icon: 'error', className: 'text-danger' },
  warning: { icon: 'warning', className: 'text-warning' },
  info: { icon: 'info', className: 'text-info' },
}

export function ToastProvider({ children }: { children: React.ReactNode }) {
  const [toasts, setToasts] = useState<Toast[]>([])

  const remove = useCallback((id: number) => {
    setToasts((prev) => prev.filter((t) => t.id !== id))
  }, [])

  const add = useCallback(
    (type: ToastType, message: unknown, title?: string) => {
      const id = ++nextId
      // A caller passing an API error object must not crash the renderer.
      const text = typeof message === 'string' ? message : String(message)
      setToasts((prev) => [...prev, { id, type, message: text, title }])
      setTimeout(() => remove(id), type === 'error' ? 8000 : 5000)
    },
    [remove],
  )

  const value = useMemo<Notifier>(
    () => ({
      success: (m, t) => add('success', m, t),
      error: (m, t) => add('error', m, t),
      warning: (m, t) => add('warning', m, t),
      info: (m, t) => add('info', m, t),
    }),
    [add],
  )

  return (
    <ToastContext.Provider value={value}>
      {children}
      <div className="pointer-events-none fixed bottom-4 right-4 z-[100] flex w-full max-w-[22rem] flex-col gap-2">
        {toasts.map((toast) => (
          <div
            key={toast.id}
            className="slide-in-right pointer-events-auto flex items-start gap-2.5 rounded-lg bg-surface p-3 shadow-[var(--shadow-elevated)]"
          >
            <Icon
              name={STYLES[toast.type].icon}
              className={cn('mt-px shrink-0 text-[16px]', STYLES[toast.type].className)}
            />
            <div className="min-w-0 flex-1">
              {toast.title && (
                <p className="mb-0.5 text-xs font-medium text-text-main">{toast.title}</p>
              )}
              <p
                className={cn(
                  'whitespace-pre-line break-words text-xs leading-relaxed',
                  toast.title ? 'text-text-muted' : 'text-text-main',
                )}
              >
                {toast.message}
              </p>
            </div>
            <button
              onClick={() => remove(toast.id)}
              aria-label="Dismiss"
              className="-mr-1 -mt-1 shrink-0 cursor-pointer rounded p-1 text-text-muted transition-colors hover:text-text-main"
            >
              <Icon name="close" className="text-[14px]" />
            </button>
          </div>
        ))}
      </div>
    </ToastContext.Provider>
  )
}

/** The KineRouter store exposes notify.success/error/warning/info directly. */
export function useNotify(): Notifier {
  return useContext(ToastContext)
}
