import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { ModelSelectModal } from '@/components/model-select-modal'

// A compatible node is a connection whose provider is a providerNodes id, and
// /api/providers only lists registry entries — so the picker must not require
// a registry row for it or every custom provider silently disappears.
const NODE = 'openai-compatible-chat-bdca53ac'

const data: Record<string, unknown> = {
  '/api/providers': {
    providers: [
      {
        id: 'openai', alias: 'openai', category: 'apikey', authType: 'apikey',
        display: { name: 'OpenAI', color: '#000' }, models: [{ id: 'gpt-5.5', name: 'GPT-5.5' }],
        serviceKinds: ['llm'], noAuth: false, priority: 1,
      },
    ],
    connections: [
      { id: 'c1', provider: 'openai', isActive: true },
      { id: 'c2', provider: NODE, isActive: true },
    ],
  },
  '/api/provider-nodes': {
    nodes: [{ id: NODE, type: 'openai-compatible', name: 'Local vLLM', prefix: 'local', apiType: 'chat' }],
  },
  '/api/models/custom': { models: [{ providerAlias: NODE, id: 'glm-4.7', type: 'llm' }] },
  '/api/combos': { combos: [] },
  '/api/models/disabled': { disabled: {} },
  '/api/models': { object: 'list', data: [] },
}

beforeEach(() => {
  vi.stubGlobal('fetch', vi.fn(async (url: string) => {
    const key = Object.keys(data).find((k) => String(url).startsWith(k))
    return new Response(JSON.stringify(key ? data[key] : {}), { status: 200 })
  }))
})

describe('ModelSelectModal', () => {
  it('lists a compatible provider node and its registered models', async () => {
    render(
      <ModelSelectModal
        isOpen
        onClose={() => {}}
        onSelect={() => {}}
        modelAliases={{ 'qwen3-32b': `${NODE}/qwen3-32b` }}
      />,
    )

    await waitFor(() => expect(screen.getByText('Local vLLM')).toBeTruthy())
    // A registered custom model and an alias both surface under the node.
    expect(screen.getByText('glm-4.7')).toBeTruthy()
    expect(screen.getByText('qwen3-32b')).toBeTruthy()
  })
})
