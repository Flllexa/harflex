// A small, dependency-free Markdown reader for assistant messages.
//
// It produces a plain AST that React renders as elements: there is no raw HTML
// pass-through, images are never fetched, and only http(s)/mailto links survive.
// Input is often a half-written stream, so every construct tolerates truncation:
// an unclosed fence is code until the end and an unmatched delimiter stays text.

export type Inline =
  | { t: 'text'; v: string }
  | { t: 'code'; v: string }
  | { t: 'em' | 'strong' | 'del'; c: Inline[] }
  | { t: 'link'; href: string; c: Inline[] }
  | { t: 'br' }

export type Align = 'left' | 'right' | 'center' | null

export type Block =
  | { t: 'p'; c: Inline[] }
  | { t: 'h'; level: 1 | 2 | 3 | 4 | 5 | 6; c: Inline[] }
  | { t: 'code'; lang: string; v: string; closed: boolean }
  | { t: 'quote'; c: Block[] }
  | { t: 'list'; ordered: boolean; start: number; items: Block[][] }
  | { t: 'hr' }
  | { t: 'table'; head: Inline[][]; align: Align[]; rows: Inline[][][] }

const MAX_DEPTH = 8
const safeProtocols = new Set(['http:', 'https:', 'mailto:'])

/** Returns a normalized URL only for protocols a desktop shell may open; otherwise ''. */
export function safeHref(raw: string): string {
  const value = raw.trim()
  if (!value || /[\u0000-\u001f\u007f\s]/.test(value)) return ''
  try {
    const url = new URL(value)
    return safeProtocols.has(url.protocol) ? url.href : ''
  } catch { return '' }
}

const punctuation = /[!-/:-@[-`{-~]/
const isSpace = (char: string | undefined) => char === undefined || /\s/.test(char)
const isWordChar = (char: string | undefined) => char !== undefined && /[\p{L}\p{N}]/u.test(char)

function pushText(out: Inline[], value: string) {
  if (!value) return
  const last = out[out.length - 1]
  if (last?.t === 'text') last.v += value
  else out.push({ t: 'text', v: value })
}

/** Index of the next backtick run that is exactly `run` long, or -1. */
function findCodeClose(src: string, from: number, run: number): number {
  let at = src.indexOf('`', from)
  while (at >= 0) {
    let length = 1
    while (src[at + length] === '`') length++
    if (length === run) return at
    at = src.indexOf('`', at + length)
  }
  return -1
}

const backtickRun = (src: string, at: number) => { let length = 0; while (src[at + length] === '`') length++; return length }

function findClosingBracket(src: string, open: number): number {
  let depth = 0
  for (let i = open; i < src.length; i++) {
    const char = src[i]
    if (char === '\\') { i++; continue }
    if (char === '`') {
      const run = backtickRun(src, i)
      const end = findCodeClose(src, i + run, run)
      i = end >= 0 ? end + run - 1 : i + run - 1
      continue
    }
    if (char === '[') depth++
    else if (char === ']' && --depth === 0) return i
  }
  return -1
}

function readDestination(src: string, open: number): { href: string; end: number } | undefined {
  let i = open + 1
  while (src[i] === ' ' || src[i] === '\n') i++
  let href: string
  if (src[i] === '<') {
    const close = src.indexOf('>', i)
    if (close < 0) return undefined
    href = src.slice(i + 1, close)
    i = close + 1
  } else {
    let depth = 0
    const start = i
    for (; i < src.length; i++) {
      const char = src[i]
      if (char === '\\') { i++; continue }
      if (char === ' ' || char === '\n') break
      if (char === '(') depth++
      else if (char === ')') { if (depth === 0) break; depth-- }
    }
    href = src.slice(start, i)
  }
  while (src[i] === ' ' || src[i] === '\n') i++
  if (src[i] === '"' || src[i] === "'") {
    const close = src.indexOf(src[i], i + 1)
    if (close < 0) return undefined
    i = close + 1
    while (src[i] === ' ' || src[i] === '\n') i++
  }
  return src[i] === ')' ? { href, end: i + 1 } : undefined
}

function readBareURL(src: string, start: number): number {
  let end = start
  while (end < src.length && !/[\s<>]/.test(src[end])) end++
  while (end > start) {
    const char = src[end - 1]
    if (/[.,;:!?'"]/.test(char)) { end--; continue }
    if (char === ')' || char === ']' || char === '}') {
      const open = char === ')' ? '(' : char === ']' ? '[' : '{'
      const body = src.slice(start, end)
      if (body.split(open).length >= body.split(char).length) break
      end--
      continue
    }
    break
  }
  return end
}

/** Parses inline spans. `depth` bounds recursion; `inLink` stops nested links. */
export function parseInline(src: string, depth = 0, inLink = false): Inline[] {
  const out: Inline[] = []
  let i = 0
  let buffer = ''
  const flush = () => { pushText(out, buffer); buffer = '' }
  while (i < src.length) {
    const char = src[i]
    if (char === '\\' && i + 1 < src.length && punctuation.test(src[i + 1])) { buffer += src[i + 1]; i += 2; continue }
    if (char === '\n') { flush(); out.push({ t: 'br' }); i++; continue }
    if (char === '`') {
      const run = backtickRun(src, i)
      const close = findCodeClose(src, i + run, run)
      if (close >= 0) {
        flush()
        let value = src.slice(i + run, close).replace(/\n/g, ' ')
        if (value.length > 1 && value.startsWith(' ') && value.endsWith(' ') && value.trim() !== '') value = value.slice(1, -1)
        out.push({ t: 'code', v: value })
        i = close + run
      } else { buffer += '`'.repeat(run); i += run }
      continue
    }
    if ((char === '[' || (char === '!' && src[i + 1] === '[')) && depth < MAX_DEPTH) {
      const image = char === '!'
      const open = image ? i + 1 : i
      const close = findClosingBracket(src, open)
      const destination = close > 0 && src[close + 1] === '(' ? readDestination(src, close + 1) : undefined
      if (destination) {
        const label = src.slice(open + 1, close)
        flush()
        if (image) {
          // Remote images are never fetched; the alternative text stays visible.
          pushText(out, label ? `[imagem: ${label}]` : '[imagem]')
        } else {
          const href = safeHref(destination.href)
          const children = parseInline(label, depth + 1, true)
          if (href && !inLink) out.push({ t: 'link', href, c: children.length ? children : [{ t: 'text', v: href }] })
          else out.push(...children)
        }
        i = destination.end
        continue
      }
    }
    if (char === '<' && !inLink) {
      const close = src.indexOf('>', i + 1)
      const inner = close > 0 ? src.slice(i + 1, close) : ''
      const href = /^(https?:\/\/|mailto:)\S+$/i.test(inner) ? safeHref(inner) : ''
      if (href) {
        flush()
        out.push({ t: 'link', href, c: [{ t: 'text', v: inner }] })
        i = close + 1
        continue
      }
    }
    if ((char === 'h' || char === 'H') && !inLink && /^https?:\/\//i.test(src.slice(i, i + 8)) && !isWordChar(src[i - 1]) && src[i - 1] !== '/') {
      const end = readBareURL(src, i)
      const href = end - i > 8 ? safeHref(src.slice(i, end)) : ''
      if (href) {
        flush()
        out.push({ t: 'link', href, c: [{ t: 'text', v: src.slice(i, end) }] })
        i = end
        continue
      }
    }
    if ((char === '*' || char === '_' || char === '~') && depth < MAX_DEPTH) {
      const double = src[i + 1] === char
      if (char === '~' && !double) { buffer += char; i++; continue }
      const delimiter = double ? char + char : char
      const after = src[i + delimiter.length]
      const opens = !isSpace(after) && (char !== '_' || !isWordChar(src[i - 1]))
      let close = -1
      for (let at = i + delimiter.length; opens && at < src.length; at++) {
        if (src[at] === '\\') { at++; continue }
        if (src[at] === '`') {
          const run = backtickRun(src, at)
          const end = findCodeClose(src, at + run, run)
          at = end >= 0 ? end + run - 1 : at + run - 1
          continue
        }
        if (!src.startsWith(delimiter, at) || at === i + delimiter.length || isSpace(src[at - 1])) continue
        // A single delimiter must not be half of a doubled run, and `_` must end at a word edge.
        if (!double && src[at + 1] === char) { at++; continue }
        if (char === '_' && isWordChar(src[at + delimiter.length])) continue
        close = at
        break
      }
      if (close > 0) {
        flush()
        out.push({ t: char === '~' ? 'del' : double ? 'strong' : 'em', c: parseInline(src.slice(i + delimiter.length, close), depth + 1, inLink) })
        i = close + delimiter.length
      } else { buffer += delimiter; i += delimiter.length }
      continue
    }
    buffer += char
    i++
  }
  flush()
  return out
}

const fenceOpen = /^( {0,3})(`{3,}|~{3,})\s*([^`\s]*)[^`]*$/
const heading = /^ {0,3}(#{1,6})(?:\s+(.*?))?(?:\s+#+)?\s*$/
const rule = /^ {0,3}([-*_])(?:\s*\1){2,}\s*$/
const quoteLine = /^ {0,3}>/
const bullet = /^( *)([-*+])( +|$)/
const ordered = /^( *)(\d{1,9})([.)])( +|$)/
const tableSeparator = /^\s*\|?\s*:?-+:?\s*(?:\|\s*:?-+:?\s*)*\|?\s*$/
const indentOf = (line: string) => /^ */.exec(line)![0].length
const isBlank = (line: string) => line.trim() === ''

type Marker = { indent: number; ordered: boolean; number: number; width: number; hasContent: boolean }
function listMarker(line: string): Marker | null {
  const unordered = bullet.exec(line)
  if (unordered) return { indent: unordered[1].length, ordered: false, number: 1, width: unordered[0].length, hasContent: unordered[3] !== '' }
  const numbered = ordered.exec(line)
  return numbered ? { indent: numbered[1].length, ordered: true, number: Number(numbered[2]), width: numbered[0].length, hasContent: numbered[4] !== '' } : null
}

function splitRow(line: string): string[] {
  let text = line.trim()
  if (text.startsWith('|')) text = text.slice(1)
  if (text.endsWith('|') && !text.endsWith('\\|')) text = text.slice(0, -1)
  const cells: string[] = []
  let current = ''
  for (let i = 0; i < text.length; i++) {
    if (text[i] === '\\' && text[i + 1] === '|') { current += '|'; i++; continue }
    if (text[i] === '`') {
      const run = backtickRun(text, i)
      const end = findCodeClose(text, i + run, run)
      if (end >= 0) { current += text.slice(i, end + run); i = end + run - 1; continue }
    }
    if (text[i] === '|') { cells.push(current.trim()); current = ''; continue }
    current += text[i]
  }
  cells.push(current.trim())
  return cells
}

function startsTable(lines: string[], at: number): boolean {
  return at + 1 < lines.length && lines[at].includes('|') && lines[at + 1].includes('-') && tableSeparator.test(lines[at + 1])
    && splitRow(lines[at]).length === splitRow(lines[at + 1]).length
}

/** Whether `lines[at]` starts a block that may interrupt a paragraph. */
function interrupts(lines: string[], at: number): boolean {
  const line = lines[at]
  if (fenceOpen.test(line) || heading.test(line) || rule.test(line) || quoteLine.test(line) || startsTable(lines, at)) return true
  const marker = listMarker(line)
  // Only a non-empty bullet or an ordered list starting at 1 may interrupt a paragraph.
  return !!marker && marker.hasContent && (!marker.ordered || marker.number === 1)
}

function closesFence(line: string, marker: string): boolean {
  const trimmed = line.trim()
  return trimmed.length >= marker.length && [...trimmed].every(char => char === marker[0])
}

function parseList(lines: string[], start: number, depth: number): { block: Block; next: number } {
  const first = listMarker(lines[start])!
  const base = first.indent
  const items: Block[][] = []
  let i = start
  const sibling = (line: string | undefined) => {
    const marker = line === undefined ? null : listMarker(line)
    return marker && marker.ordered === first.ordered && marker.indent >= base && marker.indent <= base + 1 ? marker : null
  }
  while (i < lines.length) {
    const marker = sibling(lines[i])
    if (!marker) break
    const itemLines = [lines[i].slice(marker.width)]
    const contentIndent = Math.max(marker.width, base + 2)
    i++
    while (i < lines.length) {
      const line = lines[i]
      if (isBlank(line)) {
        // Blank lines stay in the item only when more indented content follows.
        let look = i + 1
        while (look < lines.length && isBlank(lines[look])) look++
        if (look < lines.length && indentOf(lines[look]) > base + 1) { while (i < look) { itemLines.push(''); i++ } continue }
        break
      }
      const indent = indentOf(line)
      if (indent > base + 1) { itemLines.push(line.slice(Math.min(indent, contentIndent))); i++; continue }
      if (listMarker(line) || interrupts(lines, i)) break
      if (isBlank(itemLines[itemLines.length - 1])) break
      itemLines.push(line.trim()) // lazy paragraph continuation
      i++
    }
    items.push(parseBlocks(itemLines, depth + 1))
    // A loose list keeps going across blank lines when another sibling follows.
    let look = i
    while (look < lines.length && isBlank(lines[look])) look++
    if (look > i && sibling(lines[look])) i = look
  }
  return { block: { t: 'list', ordered: first.ordered, start: first.number, items }, next: i }
}

function parseBlocks(lines: string[], depth: number): Block[] {
  const blocks: Block[] = []
  let i = 0
  while (i < lines.length) {
    const line = lines[i]
    if (isBlank(line)) { i++; continue }
    const fence = fenceOpen.exec(line)
    if (fence) {
      const body: string[] = []
      let closed = false
      for (i++; i < lines.length; i++) {
        if (closesFence(lines[i], fence[2])) { closed = true; i++; break }
        body.push(lines[i].slice(Math.min(fence[1].length, indentOf(lines[i]))))
      }
      blocks.push({ t: 'code', lang: fence[3], v: body.join('\n'), closed })
      continue
    }
    const head = heading.exec(line)
    if (head) {
      blocks.push({ t: 'h', level: head[1].length as 1 | 2 | 3 | 4 | 5 | 6, c: parseInline((head[2] ?? '').trim()) })
      i++
      continue
    }
    if (rule.test(line)) { blocks.push({ t: 'hr' }); i++; continue }
    if (quoteLine.test(line) && depth < MAX_DEPTH) {
      const inner: string[] = []
      for (; i < lines.length; i++) {
        if (quoteLine.test(lines[i])) inner.push(lines[i].replace(/^ {0,3}> ?/, ''))
        else if (!isBlank(lines[i]) && !interrupts(lines, i) && inner.length > 0 && !isBlank(inner[inner.length - 1])) inner.push(lines[i]) // lazy continuation
        else break
      }
      blocks.push({ t: 'quote', c: parseBlocks(inner, depth + 1) })
      continue
    }
    if (startsTable(lines, i)) {
      const head = splitRow(lines[i])
      const align = splitRow(lines[i + 1]).map((cell): Align => cell.startsWith(':') && cell.endsWith(':') ? 'center' : cell.endsWith(':') ? 'right' : cell.startsWith(':') ? 'left' : null)
      const rows: Inline[][][] = []
      for (i += 2; i < lines.length && !isBlank(lines[i]) && lines[i].includes('|'); i++) {
        const cells = splitRow(lines[i])
        rows.push(head.map((_, column) => parseInline(cells[column] ?? '')))
      }
      blocks.push({ t: 'table', head: head.map(cell => parseInline(cell)), align, rows })
      continue
    }
    if (listMarker(line) && depth < MAX_DEPTH) {
      const list = parseList(lines, i, depth)
      blocks.push(list.block)
      i = list.next
      continue
    }
    const paragraph = [line.trimEnd()]
    for (i++; i < lines.length && !isBlank(lines[i]) && !interrupts(lines, i); i++) paragraph.push(lines[i].trim())
    blocks.push({ t: 'p', c: parseInline(paragraph.join('\n')) })
  }
  return blocks
}

export function parseMarkdown(source: string): Block[] {
  return parseBlocks(source.replace(/\r\n?/g, '\n').split('\n'), 0)
}

/** Plain text of a parsed document, used for copy actions and tests. */
export function markdownText(blocks: Block[]): string {
  const inline = (nodes: Inline[]): string => nodes.map(node => node.t === 'text' || node.t === 'code' ? node.v : node.t === 'br' ? '\n' : inline(node.c)).join('')
  return blocks.map(block => {
    switch (block.t) {
      case 'p': case 'h': return inline(block.c)
      case 'code': return block.v
      case 'quote': return markdownText(block.c)
      case 'list': return block.items.map(item => markdownText(item)).join('\n')
      case 'table': return [block.head, ...block.rows].map(row => row.map(inline).join('\t')).join('\n')
      case 'hr': return ''
    }
  }).join('\n')
}
