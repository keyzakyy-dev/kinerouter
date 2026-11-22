/**
 * Port of src/shared/utils/modelSearchFilter.js. Supports substring terms,
 * `!term` / `-term` negation, and `/regex/flags` (a leading `/` is live while
 * the user is still typing).
 */
export type ParsedQuery = {
  raw: string
  isEmpty: boolean
  isRegex: boolean
  regex: RegExp | null
  positiveTerms: string[]
  negativeTerms: string[]
}

const EMPTY: ParsedQuery = {
  raw: '',
  isEmpty: true,
  isRegex: false,
  regex: null,
  positiveTerms: [],
  negativeTerms: [],
}

export function parseSearchQuery(rawQuery?: string | null): ParsedQuery {
  const trimmed = rawQuery?.trim() ?? ''
  if (!trimmed) return EMPTY

  if (trimmed.startsWith('/')) {
    const full = /^\/(.+)\/([a-z]*)$/i.exec(trimmed)
    if (full) {
      try {
        return {
          raw: trimmed,
          isEmpty: false,
          isRegex: true,
          regex: new RegExp(full[1], full[2] || 'i'),
          positiveTerms: [],
          negativeTerms: [],
        }
      } catch { /* invalid while typing */ }
    } else if (trimmed.length > 1) {
      try {
        return {
          raw: trimmed,
          isEmpty: false,
          isRegex: true,
          regex: new RegExp(trimmed.slice(1), 'i'),
          positiveTerms: [],
          negativeTerms: [],
        }
      } catch { /* unclosed group while typing */ }
    }
  }

  const positiveTerms: string[] = []
  const negativeTerms: string[] = []
  for (const token of trimmed.split(/\s+/)) {
    if (!token) continue
    if ((token.startsWith('!') || token.startsWith('-')) && token.length > 1) {
      negativeTerms.push(token.slice(1).toLowerCase())
    } else {
      positiveTerms.push(token.toLowerCase())
    }
  }
  return {
    raw: trimmed,
    isEmpty: positiveTerms.length === 0 && negativeTerms.length === 0,
    isRegex: false,
    regex: null,
    positiveTerms,
    negativeTerms,
  }
}

export function matchesSearch(
  target: string | string[] | Record<string, unknown> | null | undefined,
  queryOrParsed: string | ParsedQuery | null | undefined,
): boolean {
  const parsed =
    typeof queryOrParsed === 'string' || !queryOrParsed ? parseSearchQuery(queryOrParsed as string) : queryOrParsed
  if (parsed.isEmpty) return true

  let candidates: string[] = []
  if (typeof target === 'string') candidates = [target]
  else if (Array.isArray(target)) candidates = target.filter((t): t is string => typeof t === 'string')
  else if (target && typeof target === 'object') {
    candidates = ['name', 'id', 'value', 'alias', 'provider']
      .map((k) => target[k])
      .filter((t): t is string => typeof t === 'string')
  }
  if (candidates.length === 0) return false

  if (parsed.isRegex && parsed.regex) {
    parsed.regex.lastIndex = 0
    const fullText = candidates.join(' ')
    if (/\(\?!|\(\?</.test(parsed.raw)) return parsed.regex.test(fullText)
    return candidates.some((t) => parsed.regex!.test(t)) || parsed.regex.test(fullText)
  }

  const lower = candidates.map((c) => c.toLowerCase())
  for (const neg of parsed.negativeTerms) if (lower.some((c) => c.includes(neg))) return false
  for (const pos of parsed.positiveTerms) if (!lower.some((c) => c.includes(pos))) return false
  return true
}
