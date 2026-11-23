import { useState } from 'react'
import { Icon } from '@/components/icon'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { useCopyToClipboard } from '@/lib/use-copy-to-clipboard'
import { cn } from '@/lib/utils'

/**
 * Translator debugger, ported from
 * src/app/(dashboard)/dashboard/translator/page.js: the same 7 replay steps,
 * the same artifact files, the same Load / Format / Copy / Send actions.
 *
 * ponytail: the body editor is a monospace textarea instead of Monaco. Monaco
 * pulls ~5 MB from a CDN, which breaks an offline install; swap in
 * @monaco-editor/react if syntax highlighting is worth that dependency.
 */

const STEPS = [
  { id: 1, label: 'Client Request', file: '1_req_client.json', lang: 'json', desc: 'Raw request from client' },
  { id: 2, label: 'Source Body', file: '2_req_source.json', lang: 'json', desc: 'After initial conversion' },
  { id: 3, label: '[OI] Intermediate', file: '3_req_openai.json', lang: 'json', desc: 'source → openai' },
  { id: 4, label: 'Target Request', file: '4_req_target.json', lang: 'json', desc: 'openai → target + URL + headers' },
  { id: 5, label: 'Provider Response', file: '5_res_provider.txt', lang: 'text', desc: 'Raw SSE from provider' },
  { id: 6, label: '[OI] Response', file: '6_res_openai.txt', lang: 'text', desc: 'target → openai (response)' },
  { id: 7, label: 'Client Response', file: '7_res_client.txt', lang: 'text', desc: 'Final response to client' },
] as const

type Meta = { provider: string; model: string; sourceFormat: string; targetFormat: string }

function MetaBadge({ label, value, color }: { label: string; value?: string; color: string }) {
  const colors: Record<string, string> = {
    blue: 'bg-foreground/5 text-muted-foreground',
    orange: 'bg-foreground/5 text-muted-foreground',
    green: 'bg-foreground/5 text-muted-foreground',
    purple: 'bg-foreground/5 text-muted-foreground',
  }
  return (
    <span
      className={cn(
        'inline-flex items-center gap-1 rounded px-2 py-0.5 font-mono text-xs',
        colors[color],
      )}
    >
      <span className="font-sans text-[10px] text-text-muted/70">{label}:</span>
      {value}
    </span>
  )
}

export default function TranslatorPage() {
  const [contents, setContents] = useState<Record<number, string>>({})
  const [expanded, setExpanded] = useState<Record<number, boolean>>({ 1: true })
  const [loading, setLoading] = useState<Record<string, boolean>>({})
  // Detected from step 1: { provider, model, sourceFormat, targetFormat }
  const [meta, setMeta] = useState<Meta | null>(null)
  const { copied, copy } = useCopyToClipboard()

  const setLoad = (key: string, val: boolean) => setLoading((prev) => ({ ...prev, [key]: val }))
  const setContent = (id: number, val: string) => setContents((prev) => ({ ...prev, [id]: val }))
  const toggle = (id: number) => setExpanded((prev) => ({ ...prev, [id]: !prev[id] }))

  const openNext = (nextId: number) => {
    const next: Record<number, boolean> = {}
    STEPS.forEach((s) => {
      next[s.id] = false
    })
    next[nextId] = true
    setExpanded(next)
  }

  // Load a saved artifact from logs/translator/.
  const handleLoad = async (stepId: number) => {
    const step = STEPS.find((s) => s.id === stepId)!
    setLoad(`load-${stepId}`, true)
    try {
      const res = await fetch(`/api/translator/load?file=${step.file}`)
      const data = await res.json()
      if (data.success) {
        setContent(stepId, data.content)
        if (stepId === 1) await detectMeta(data.content)
      } else {
        alert(data.error || 'File not found')
      }
    } catch (e) {
      alert((e as Error).message)
    }
    setLoad(`load-${stepId}`, false)
  }

  // Step 1: detect provider/format from the model field.
  const detectMeta = async (rawContent: string) => {
    try {
      const body = typeof rawContent === 'string' ? JSON.parse(rawContent) : rawContent
      const res = await fetch('/api/translator/translate', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ step: 1, body }),
      })
      const data = await res.json()
      if (data.success) setMeta(data.result)
    } catch {
      // not valid JSON yet — nothing to detect
    }
  }

  const save = (file: string, content: string) =>
    fetch('/api/translator/save', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ file, content }),
    }).catch(() => {})

  // Step 1 → Step 3: source → [OI] intermediate.
  const handleToOI = async () => {
    setLoad('toOI', true)
    try {
      const raw = contents[1]
      const body = JSON.parse(raw)
      // Save input: 1_req_client.json + 2_req_source.json (body only).
      save('1_req_client.json', raw)
      save(
        '2_req_source.json',
        JSON.stringify({ timestamp: new Date().toISOString(), headers: {}, body: body.body || body }, null, 2),
      )

      const res = await fetch('/api/translator/translate', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ step: 2, body }),
      })
      const data = await res.json()
      if (!data.success) {
        alert(data.error)
        return
      }
      setContent(3, JSON.stringify(data.result.body, null, 2))
      openNext(3)
    } catch (e) {
      alert((e as Error).message)
    }
    setLoad('toOI', false)
  }

  // Step 3 → Step 4: [OI] → target + build URL/headers.
  const handleToTarget = async () => {
    setLoad('toTarget', true)
    try {
      const raw = contents[3]
      const openaiBody = JSON.parse(raw)
      save('3_req_openai.json', raw)

      const res = await fetch('/api/translator/translate', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          step: 3,
          body: { ...openaiBody, provider: meta?.provider, model: meta?.model },
        }),
      })
      const data = await res.json()
      if (!data.success) {
        alert(data.error)
        return
      }
      // Embed provider + model so Send works even without meta.
      const step4Content = { ...data.result, provider: meta?.provider, model: meta?.model }
      setContent(4, JSON.stringify(step4Content, null, 2))
      openNext(4)
    } catch (e) {
      alert((e as Error).message)
    }
    setLoad('toTarget', false)
  }

  // Step 4 → Step 5: send to the provider through the executor.
  const handleSend = async () => {
    setLoad('send', true)
    try {
      const raw = contents[4]
      const step4 = JSON.parse(raw)
      save('4_req_target.json', raw)

      // Read provider/model from the step-4 content, falling back to meta.
      const provider = step4.provider || meta?.provider
      const model = step4.model || meta?.model

      if (!provider || !model) {
        alert('Missing provider or model. Please run step 1 first to detect them.')
        return
      }

      const res = await fetch('/api/translator/send', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ provider, model, body: step4.body || step4 }),
      })

      if (!res.ok) {
        const err = await res.json().catch(() => ({ error: res.statusText }))
        alert(err.error || 'Send failed')
        return
      }

      // Accumulate the streaming response.
      const reader = res.body!.getReader()
      const decoder = new TextDecoder()
      let full = ''
      for (;;) {
        const { done, value } = await reader.read()
        if (done) break
        full += decoder.decode(value, { stream: true })
      }

      setContent(5, full)
      openNext(5)

      await fetch('/api/translator/save', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ file: '5_res_provider.txt', content: full }),
      })
    } catch (e) {
      alert((e as Error).message)
    } finally {
      setLoad('send', false)
    }
  }

  const handleCopy = (id: number) => {
    if (!contents[id]) return
    copy(contents[id], `translator-step-${id}`)
  }

  const handleFormat = (id: number) => {
    try {
      setContent(id, JSON.stringify(JSON.parse(contents[id]), null, 2))
    } catch {
      // not JSON, skip
    }
  }

  const getAction = (stepId: number) => {
    if (stepId === 1)
      return (
        <Button size="sm" onClick={handleToOI} disabled={loading.toOI}>
          <Icon name="arrow_forward" className="text-[16px]" />→ [OI]
        </Button>
      )
    if (stepId === 3)
      return (
        <Button size="sm" onClick={handleToTarget} disabled={loading.toTarget}>
          <Icon name="arrow_forward" className="text-[16px]" />→ Target
        </Button>
      )
    if (stepId === 4)
      return (
        <Button size="sm" onClick={handleSend} disabled={loading.send}>
          <Icon name="send" className="text-[16px]" />Send
        </Button>
      )
    return null
  }

  return (
    <div className="space-y-3 p-8">
      <div className="mb-2 flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold text-text-main">Translator Debug</h1>
          <p className="mt-1 text-sm text-text-muted">Replay request flow — matches log files</p>
        </div>
        {meta && (
          <div className="flex flex-wrap items-center justify-end gap-2">
            <MetaBadge label="src" value={meta.sourceFormat} color="blue" />
            <Icon name="arrow_forward" className="text-[14px] text-text-muted" />
            <MetaBadge label="dst" value={meta.targetFormat} color="orange" />
            <MetaBadge label="provider" value={meta.provider} color="green" />
            <MetaBadge label="model" value={meta.model} color="purple" />
          </div>
        )}
      </div>

      {STEPS.map((step) => {
        const action = getAction(step.id)
        const isExpanded = !!expanded[step.id]
        const content = contents[step.id] || ''

        return (
          <Card key={step.id}>
            <div className="space-y-3 p-4">
              <div className="flex items-center justify-between">
                <button
                  onClick={() => toggle(step.id)}
                  className="group flex flex-1 cursor-pointer items-center gap-2 text-left"
                >
                  <Icon
                    name={isExpanded ? 'expand_more' : 'chevron_right'}
                    className="text-[20px] text-text-muted transition-colors group-hover:text-primary"
                  />
                  <span className="w-4 font-mono text-xs text-text-muted/60">{step.id}</span>
                  <h3 className="text-sm font-semibold text-text-main">{step.label}</h3>
                  <span className="font-mono text-xs text-text-muted/60">{step.file}</span>
                  {content && <span className="text-xs text-muted-foreground">({content.length} chars)</span>}
                </button>
                {!isExpanded && (
                  <div className="flex shrink-0 gap-1">
                    <Button
                      size="sm"
                      variant="ghost"
                      onClick={() => handleLoad(step.id)}
                      disabled={loading[`load-${step.id}`]}
                      title="Load"
                    >
                      <Icon name="folder_open" className="text-[16px]" />
                    </Button>
                    {action}
                  </div>
                )}
              </div>

              {isExpanded && (
                <>
                  <textarea
                    value={content}
                    onChange={(e) => {
                      setContent(step.id, e.target.value)
                      if (step.id === 1) detectMeta(e.target.value)
                    }}
                    spellCheck={false}
                    aria-label={step.label}
                    placeholder={step.desc}
                    className="h-[400px] w-full resize-y rounded-lg border border-border bg-[#1e1e1e] p-3 font-mono text-xs leading-relaxed text-[#d4d4d4] focus:outline-none"
                  />
                  <div className="flex flex-wrap gap-2">
                    <Button
                      size="sm"
                      variant="outline"
                      onClick={() => handleLoad(step.id)}
                      disabled={loading[`load-${step.id}`]}
                    >
                      <Icon name="folder_open" className="text-[16px]" />
                      Load
                    </Button>
                    <Button size="sm" variant="outline" onClick={() => handleFormat(step.id)}>
                      <Icon name="data_object" className="text-[16px]" />
                      Format
                    </Button>
                    <Button size="sm" variant="outline" onClick={() => handleCopy(step.id)}>
                      <Icon name="content_copy" className="text-[16px]" />
                      {copied === `translator-step-${step.id}` ? 'Copied' : 'Copy'}
                    </Button>
                    {action}
                  </div>
                </>
              )}
            </div>
          </Card>
        )
      })}
    </div>
  )
}
