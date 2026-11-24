/** Standard ANSI foreground colours (SGR 30-37, 90-97). */
const ANSI_FG: Record<number, string> = {
  30: '#4b5263',
  31: '#e06c75',
  32: '#98c379',
  33: '#e5c07b',
  34: '#61afef',
  35: '#c678dd',
  36: '#56b6c2',
  37: 'inherit',
  90: 'var(--color-text-muted)',
  91: '#ef7a85',
  92: '#a5d6a7',
  93: '#f0d089',
  94: '#7cc0ff',
  95: '#d99bf0',
  96: '#6fd3de',
  97: 'inherit',
}

/**
 * Splits command output on SGR escapes so `ls --color=always`, `git -c
 * color.ui=always status` and friends render the way they would in a shell.
 */
export function ansiSpans(text: string) {
  const parts: { text: string; fg?: string; bold?: boolean; dim?: boolean }[] = []
  let fg: string | undefined
  let bold = false
  let dim = false
  const push = (slice: string) => {
    if (slice) parts.push({ text: slice, fg, bold, dim })
  }
  const escapes = /\x1b\[([0-9;]*)m/g
  let last = 0
  for (let match = escapes.exec(text); match; match = escapes.exec(text)) {
    push(text.slice(last, match.index))
    last = escapes.lastIndex
    for (const code of match[1].split(';')) {
      const n = Number(code || 0)
      if (n === 0) {
        fg = undefined
        bold = false
        dim = false
      } else if (n === 1) bold = true
      else if (n === 2) dim = true
      else if (n === 22) bold = dim = false
      else if (n === 39) fg = undefined
      else if (ANSI_FG[n]) fg = ANSI_FG[n]
    }
  }
  push(text.slice(last))
  return parts
}
