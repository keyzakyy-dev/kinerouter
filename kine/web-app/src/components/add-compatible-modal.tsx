import { useEffect, useState } from 'react'
import { api, type ProviderNode } from '@/lib/api'
import { Modal } from '@/components/modal'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input, Label } from '@/components/ui/input'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'

/**
 * Ported from src/app/(dashboard)/dashboard/providers/components/AddCompatibleModal.js.
 * Creates an OpenAI-/Anthropic-compatible provider node; the node owns its
 * prefix and base URL so the dashboard never sends a URL per request.
 */
const VARIANTS = {
  openai: {
    title: 'Add OpenAI Compatible',
    type: 'openai-compatible',
    defaultBaseUrl: 'https://api.openai.com/v1',
    namePlaceholder: 'OpenAI Compatible (Prod)',
    prefixPlaceholder: 'oc-prod',
    baseUrlHint: 'Use the base URL (ending in /v1) for your OpenAI-compatible API.',
    modelIdPlaceholder: 'e.g. gpt-4, claude-3-opus',
    hasApiType: true,
  },
  anthropic: {
    title: 'Add Anthropic Compatible',
    type: 'anthropic-compatible',
    defaultBaseUrl: 'https://api.anthropic.com/v1',
    namePlaceholder: 'Anthropic Compatible (Prod)',
    prefixPlaceholder: 'ac-prod',
    baseUrlHint:
      'Use the base URL (ending in /v1) for your Anthropic-compatible API. The system will append /messages.',
    modelIdPlaceholder: 'e.g. claude-3-opus',
    hasApiType: false,
  },
} as const

export function AddCompatibleModal({
  variant,
  isOpen,
  onClose,
  onCreated,
}: {
  variant: keyof typeof VARIANTS
  isOpen: boolean
  onClose: () => void
  onCreated: (node: ProviderNode) => void
}) {
  const config = VARIANTS[variant]
  const [name, setName] = useState('')
  const [prefix, setPrefix] = useState('')
  const [apiType, setApiType] = useState('chat')
  const [baseUrl, setBaseUrl] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [checkKey, setCheckKey] = useState('')
  const [checkModelId, setCheckModelId] = useState('')
  const [validating, setValidating] = useState(false)
  const [validation, setValidation] = useState<{ valid: boolean; error?: string; method?: string } | null>(null)
  const [error, setError] = useState('')

  // Base URL starts empty; the default is ghost text in the placeholder so the
  // field never submits a value the user did not choose.
  useEffect(() => {
    setBaseUrl('')
  }, [config.hasApiType, config.defaultBaseUrl, apiType])

  useEffect(() => {
    if (!isOpen || config.hasApiType) return
    setValidation(null)
    setCheckKey('')
    setCheckModelId('')
  }, [isOpen, config.hasApiType])

  const reset = () => {
    setName('')
    setPrefix('')
    setApiType('chat')
    setBaseUrl('')
    setCheckKey('')
    setCheckModelId('')
    setValidation(null)
    setError('')
  }

  const close = () => {
    reset()
    onClose()
  }

  const submit = async () => {
    if (!name.trim() || !prefix.trim() || !baseUrl.trim() || submitting) return
    setSubmitting(true)
    setError('')
    try {
      const body: Record<string, unknown> = {
        name: name.trim(),
        prefix: prefix.trim(),
        baseUrl: baseUrl.trim(),
        type: config.type,
      }
      if (config.hasApiType) body.apiType = apiType
      const r = await api.createProviderNode(body)
      onCreated(r.node)
      reset()
    } catch (e) {
      // Surface the server's reason (invalid prefix/baseUrl) instead of failing
      // silently like the reference's console.log.
      setError(e instanceof Error ? e.message : 'Failed to create provider')
    } finally {
      setSubmitting(false)
    }
  }

  const check = async () => {
    setValidating(true)
    try {
      setValidation(
        await api.validateProviderNode({
          baseUrl: baseUrl.trim(),
          apiKey: checkKey,
          type: config.type,
          modelId: checkModelId.trim() || undefined,
        }),
      )
    } catch {
      setValidation({ valid: false, error: 'Network error' })
    } finally {
      setValidating(false)
    }
  }

  return (
    <Modal isOpen={isOpen} onClose={close} title={config.title}>
      <div className="flex flex-col gap-4">
        <div className="flex flex-col gap-1.5">
          <Label>Name</Label>
          <Input value={name} onChange={(e) => setName(e.target.value)} placeholder={config.namePlaceholder} />
          <span className="text-[11px] text-text-muted">Required. A friendly label for this node.</span>
        </div>
        <div className="flex flex-col gap-1.5">
          <Label>Prefix</Label>
          <Input value={prefix} onChange={(e) => setPrefix(e.target.value)} placeholder={config.prefixPlaceholder} />
          <span className="text-[11px] text-text-muted">Required. Used as the provider prefix for model IDs.</span>
        </div>
        {config.hasApiType && (
          <div className="flex flex-col gap-1.5">
            <Label>API Type</Label>
            <Select value={apiType} onValueChange={setApiType}>
              <SelectTrigger>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="chat">Chat Completions</SelectItem>
                <SelectItem value="responses">Responses API</SelectItem>
              </SelectContent>
            </Select>
          </div>
        )}
        <div className="flex flex-col gap-1.5">
          <Label>Base URL</Label>
          <Input value={baseUrl} onChange={(e) => setBaseUrl(e.target.value)} placeholder={config.defaultBaseUrl} />
          <span className="text-[11px] text-text-muted">{config.baseUrlHint}</span>
        </div>
        <div className="flex flex-col gap-1.5">
          <Label>API Key (for Check)</Label>
          <Input type="password" value={checkKey} onChange={(e) => setCheckKey(e.target.value)} />
        </div>
        <div className="flex flex-col gap-1.5">
          <Label>Model ID (optional)</Label>
          <Input
            value={checkModelId}
            onChange={(e) => setCheckModelId(e.target.value)}
            placeholder={config.modelIdPlaceholder}
          />
          <span className="text-[11px] text-text-muted">
            If provider lacks /models endpoint, enter a model ID to validate via chat/completions instead.
          </span>
        </div>
        <div className="flex flex-col gap-3 sm:flex-row sm:items-center">
          <Button onClick={check} disabled={!checkKey || validating || !baseUrl.trim()} variant="secondary">
            {validating ? 'Checking...' : 'Check'}
          </Button>
          {validation &&
            (validation.valid ? (
              <div className="flex items-center gap-2">
                <Badge variant="success">Valid</Badge>
                {validation.method === 'chat' && (
                  <span className="text-sm text-text-muted">(via inference test)</span>
                )}
              </div>
            ) : (
              <div className="flex flex-col gap-1">
                <Badge variant="error">Invalid</Badge>
                {validation.error && <span className="text-sm text-danger">{validation.error}</span>}
              </div>
            ))}
        </div>
        {error && <p className="text-sm text-danger">{error}</p>}
        <div className="flex flex-col gap-2 sm:flex-row">
          <Button
            onClick={submit}
            className="w-full"
            disabled={!name.trim() || !prefix.trim() || !baseUrl.trim() || submitting}
          >
            {submitting ? 'Creating...' : 'Create'}
          </Button>
          <Button onClick={close} variant="ghost" className="w-full">
            Cancel
          </Button>
        </div>
      </div>
    </Modal>
  )
}
