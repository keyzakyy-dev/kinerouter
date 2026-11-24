import { useEffect } from 'react'
import { Icon } from '@/components/icon'
import { cn } from '@/lib/utils'

/**
 * Modal shell matching KineRouter's src/shared/components/Modal.js: traffic lights
 * on desktop, a title row, and a scrollable body.
 */
export function Modal({
  isOpen,
  onClose,
  title,
  children,
  footer,
  size = 'md',
  closeOnOverlay = true,
  className,
}: {
  isOpen: boolean
  onClose: () => void
  title?: string
  children: React.ReactNode
  footer?: React.ReactNode
  size?: 'sm' | 'md' | 'lg' | 'xl' | 'full'
  closeOnOverlay?: boolean
  className?: string
}) {
  const sizes = {
    sm: 'max-w-sm',
    md: 'max-w-md',
    lg: 'max-w-lg',
    xl: 'max-w-xl',
    full: 'max-w-4xl',
  }

  useEffect(() => {
    if (!isOpen) return
    const prev = document.body.style.overflow
    document.body.style.overflow = 'hidden'
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose()
    }
    document.addEventListener('keydown', onKey)
    return () => {
      document.body.style.overflow = prev
      document.removeEventListener('keydown', onKey)
    }
  }, [isOpen, onClose])

  if (!isOpen) return null

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center p-4">
      <div
        className="fade-in absolute inset-0 bg-black/50 backdrop-blur-[2px]"
        onClick={closeOnOverlay ? onClose : undefined}
      />
      <div
        role="dialog"
        aria-modal="true"
        className={cn(
          'fade-in relative flex w-full max-h-[calc(100dvh-2rem)] flex-col rounded-[14px] border border-border-subtle bg-surface shadow-[var(--shadow-elev)]',
          sizes[size],
          className,
        )}
      >
        <div className="flex shrink-0 items-center justify-between gap-3 px-4 py-3">
          {title ? (
            <h3 className="truncate text-sm font-semibold text-text-main">{title}</h3>
          ) : (
            <span />
          )}
          <button
            onClick={onClose}
            aria-label="Close"
            className="-mr-1 cursor-pointer rounded-md p-1 text-text-muted transition-colors hover:bg-surface-2 hover:text-text-main"
          >
            <Icon name="close" className="text-[18px]" />
          </button>
        </div>
        <div className="custom-scrollbar min-h-0 flex-1 overflow-y-auto p-4">{children}</div>
        {footer && <div className="shrink-0 border-t border-border-subtle p-3">{footer}</div>}
      </div>
    </div>
  )
}

/** Confirmation dialog used for destructive actions. */
export function ConfirmModal({
  isOpen,
  onClose,
  onConfirm,
  title = 'Confirm',
  message,
  confirmLabel = 'Confirm',
  variant = 'primary',
  loading,
}: {
  isOpen: boolean
  onClose: () => void
  onConfirm?: () => void | Promise<void>
  title?: string
  message?: React.ReactNode
  confirmLabel?: string
  variant?: 'primary' | 'danger'
  loading?: boolean
}) {
  return (
    <Modal isOpen={isOpen} onClose={onClose} title={title} size="sm">
      <p className="whitespace-pre-line text-sm text-text-muted">{message}</p>
      <div className="mt-5 grid grid-cols-2 gap-2">
        <button
          onClick={() => onConfirm?.()}
          disabled={loading}
          className={cn(
            'inline-flex h-9 cursor-pointer items-center justify-center rounded-md px-4 text-sm font-medium transition-colors disabled:opacity-50',
            variant === 'danger'
              ? 'bg-danger text-white hover:bg-danger/90'
              : 'bg-primary text-primary-foreground hover:bg-primary/90',
          )}
        >
          {loading ? 'Working…' : confirmLabel}
        </button>
        <button
          onClick={onClose}
          disabled={loading}
          className="inline-flex h-9 cursor-pointer items-center justify-center rounded-md border border-border px-4 text-sm font-medium text-text-main transition-colors hover:bg-surface-2 disabled:opacity-50"
        >
          Cancel
        </button>
      </div>
    </Modal>
  )
}
