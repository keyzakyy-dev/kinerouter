/**
 * Browser-local preset stores for the CLI tool page, ported from
 * src/app/(dashboard)/dashboard/cli-tools/components/cliEndpointPresets.js.
 *
 * localStorage only: these are per-browser conveniences, never server state.
 * The keys keep the reference's `kinerouter.` prefix on purpose — a user moving
 * between the two dashboards keeps the presets they saved.
 */

export type EndpointPreset = { name: string; baseUrl: string }
export type KeyPreset = { name: string; key: string }

const ENDPOINT_KEY = 'kinerouter.cliToolEndpointPresets'
const KEY_KEY = 'kinerouter.cliToolApiKeyPresets'

export const stripSlash = (url: string) => (url || '').replace(/\/+$/, '')

function read<T>(storageKey: string, field: 'baseUrl' | 'key'): T[] {
  try {
    const raw = JSON.parse(window.localStorage.getItem(storageKey) || '[]')
    if (!Array.isArray(raw)) return []
    return raw.filter((p) => p?.name && p?.[field])
  } catch {
    return []
  }
}

function write(storageKey: string, items: unknown[]) {
  window.localStorage.setItem(storageKey, JSON.stringify(items))
}

export const readEndpointPresets = () => read<EndpointPreset>(ENDPOINT_KEY, 'baseUrl')
export const readKeyPresets = () => read<KeyPreset>(KEY_KEY, 'key')

/** Adds or replaces by exact value; returns the stored name (or null). */
export function upsertEndpointPreset(value: string, name?: string): string | null {
  const v = stripSlash(value)
  if (!v) return null
  const items = readEndpointPresets()
  const existing = items.find((p) => stripSlash(p.baseUrl) === v)
  if (existing && !name) return existing.name
  let finalName = (name || '').trim()
  if (!finalName) {
    try {
      finalName = new URL(v).host
    } catch {
      finalName = v
    }
  }
  const next = [...items.filter((p) => p.name !== finalName && stripSlash(p.baseUrl) !== v), { name: finalName, baseUrl: v }]
    .sort((a, b) => a.name.localeCompare(b.name))
  write(ENDPOINT_KEY, next)
  return finalName
}

export function upsertKeyPreset(value: string, name?: string): string | null {
  if (!value) return null
  const items = readKeyPresets()
  const existing = items.find((p) => p.key === value)
  if (existing && !name) return existing.name
  const finalName = (name || `${value.slice(0, 10)}…`).trim()
  if (!finalName) return null
  const next = [...items.filter((p) => p.name !== finalName && p.key !== value), { name: finalName, key: value }]
    .sort((a, b) => a.name.localeCompare(b.name))
  write(KEY_KEY, next)
  return finalName
}

export const deleteEndpointPreset = (name: string) =>
  write(ENDPOINT_KEY, readEndpointPresets().filter((p) => p.name !== name))
export const deleteKeyPreset = (name: string) =>
  write(KEY_KEY, readKeyPresets().filter((p) => p.name !== name))

/**
 * Saves an applied endpoint unless it exactly matches a built-in dropdown
 * option (the reference's rememberEndpoint). Call after a successful Apply.
 */
export function rememberEndpoint(url: string, builtIns: string[]) {
  const v = stripSlash(url)
  if (!v) return null
  const all = builtIns.filter(Boolean).flatMap((u) => [stripSlash(u), `${stripSlash(u)}/v1`])
  if (all.includes(v)) return null
  return upsertEndpointPreset(v)
}
