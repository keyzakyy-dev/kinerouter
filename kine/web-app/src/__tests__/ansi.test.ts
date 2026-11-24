import { describe, expect, it } from 'vitest'
import { ansiSpans } from '@/lib/ansi'

describe('ansiSpans', () => {
  it('splits SGR coloured text and resets state', () => {
    const parts = ansiSpans('\x1b[31mred\x1b[0m plain')
    expect(parts).toHaveLength(2)
    expect(parts[0]).toMatchObject({ text: 'red', fg: '#e06c75', bold: false })
    expect(parts[1]).toMatchObject({ text: ' plain', fg: undefined })
  })

  it('carries bold across colour changes', () => {
    const parts = ansiSpans('\x1b[1;34mbin')
    expect(parts).toEqual([{ text: 'bin', fg: '#61afef', bold: true, dim: false }])
  })
})
