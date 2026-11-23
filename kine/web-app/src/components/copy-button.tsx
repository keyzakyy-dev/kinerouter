import { useState } from 'react'
import { Check, Copy } from 'lucide-react'
import { Button } from './ui/button'

export function CopyButton({
  value,
  label,
  size = 'icon',
  className,
}: {
  value: string
  label?: string
  size?: 'icon' | 'sm'
  className?: string
}) {
  const [copied, setCopied] = useState(false)

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(value)
    } catch {
      // clipboard API blocked (insecure origin / permissions) — fall back to select+execCommand
      const el = document.createElement('textarea')
      el.value = value
      document.body.appendChild(el)
      el.select()
      document.execCommand('copy')
      el.remove()
    }
    setCopied(true)
    setTimeout(() => setCopied(false), 1500)
  }

  if (size === 'sm') {
    return (
      <Button variant="outline" size="sm" onClick={copy}>
        {copied ? <Check className="size-3.5" /> : <Copy className="size-3.5" />}
        {label ?? 'Copy'}
      </Button>
    )
  }
  return (
    <Button variant="ghost" size="icon" onClick={copy} title={label ?? 'Copy'} className={className}>
      {copied ? <Check className="size-3.5 text-success" /> : <Copy className="size-3.5" />}
    </Button>
  )
}
