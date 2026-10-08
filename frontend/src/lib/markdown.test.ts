import { describe, expect, it } from 'vitest'
import { markdownText, parseInline, parseMarkdown, safeHref, type Block, type Inline } from './markdown'

const text = (value: string): Inline => ({ t: 'text', v: value })

describe('safeHref', () => {
  it('keeps web and mail links and refuses everything else', () => {
    expect(safeHref('https://example.com/a?b=1')).toBe('https://example.com/a?b=1')
    expect(safeHref('mailto:ana@example.com')).toBe('mailto:ana@example.com')
    for (const value of ['javascript:alert(1)', 'JaVaScRiPt:alert(1)', 'data:text/html,hi', 'file:///etc/passwd', 'wails://wails/x', '/relative', '', ' ', 'https://a b.com', 'java\nscript:alert(1)']) {
      expect(safeHref(value)).toBe('')
    }
  })
})

describe('inline Markdown', () => {
  it('reads code, emphasis, strong and strike-through', () => {
    expect(parseInline('use `go test` and **bold** with *italic* or ~~gone~~')).toEqual([
      text('use '), { t: 'code', v: 'go test' }, text(' and '), { t: 'strong', c: [text('bold')] }, text(' with '),
      { t: 'em', c: [text('italic')] }, text(' or '), { t: 'del', c: [text('gone')] },
    ])
  })

  it('keeps code literal and honors longer backtick runs', () => {
    expect(parseInline('``a ` b``')).toEqual([{ t: 'code', v: 'a ` b' }])
    expect(parseInline('`**not bold**`')).toEqual([{ t: 'code', v: '**not bold**' }])
    expect(parseInline('`` ` ``')).toEqual([{ t: 'code', v: '`' }])
  })

  it('leaves unmatched delimiters and arithmetic as text', () => {
    expect(parseInline('2 * 3 * 4')).toEqual([text('2 * 3 * 4')])
    expect(parseInline('snake_case_name and file_name.txt')).toEqual([text('snake_case_name and file_name.txt')])
    expect(parseInline('**sem fim')).toEqual([text('**sem fim')])
    expect(parseInline('um ~til e `crase')).toEqual([text('um ~til e `crase')])
  })

  it('nests emphasis and ignores delimiters inside code', () => {
    expect(parseInline('**negrito com *itálico* dentro**')).toEqual([{ t: 'strong', c: [text('negrito com '), { t: 'em', c: [text('itálico')] }, text(' dentro')] }])
    expect(parseInline('_a `b_` c_')).toEqual([{ t: 'em', c: [text('a '), { t: 'code', v: 'b_' }, text(' c')] }])
  })

  it('creates links only for safe protocols and never nests them', () => {
    expect(parseInline('[docs](https://example.com/x)')).toEqual([{ t: 'link', href: 'https://example.com/x', c: [text('docs')] }])
    expect(parseInline('[ruim](javascript:alert(1))')).toEqual([text('ruim')])
    expect(parseInline('[a](https://x.com/(b))')).toEqual([{ t: 'link', href: 'https://x.com/(b)', c: [text('a')] }])
    expect(parseInline('[https://a.com](https://b.com)')).toEqual([{ t: 'link', href: 'https://b.com/', c: [text('https://a.com')] }])
    expect(parseInline('<https://example.com>')).toEqual([{ t: 'link', href: 'https://example.com/', c: [text('https://example.com')] }])
    expect(parseInline('[sem destino] e (parênteses)')).toEqual([text('[sem destino] e (parênteses)')])
  })

  it('links bare URLs without swallowing trailing punctuation', () => {
    expect(parseInline('veja https://example.com/a, depois')).toEqual([text('veja '), { t: 'link', href: 'https://example.com/a', c: [text('https://example.com/a')] }, text(', depois')])
    expect(parseInline('(https://example.com/a)')).toEqual([text('('), { t: 'link', href: 'https://example.com/a', c: [text('https://example.com/a')] }, text(')')])
    expect(parseInline('xhttps://example.com')).toEqual([text('xhttps://example.com')])
    expect(parseInline('http://')).toEqual([text('http://')])
  })

  it('never fetches images and never emits raw HTML', () => {
    expect(parseInline('![logo](https://example.com/a.png)')).toEqual([text('[imagem: logo]')])
    expect(parseInline('<img src=x onerror=alert(1)>')).toEqual([text('<img src=x onerror=alert(1)>')])
    expect(parseInline('<script>alert(1)</script>')).toEqual([text('<script>alert(1)</script>')])
  })

  it('turns newlines into line breaks and honors escapes', () => {
    expect(parseInline('a\nb')).toEqual([text('a'), { t: 'br' }, text('b')])
    expect(parseInline('\\*literal\\*')).toEqual([text('*literal*')])
  })

  it('bounds recursion on adversarial nesting', () => {
    const nested = '*'.repeat(200) + 'x' + '*'.repeat(200)
    expect(() => parseInline(nested)).not.toThrow()
    expect(() => parseInline('['.repeat(500) + ']'.repeat(500))).not.toThrow()
  })
})

describe('block Markdown', () => {
  it('reads headings, paragraphs and rules', () => {
    const blocks = parseMarkdown('# Título\n\nPrimeira linha\nsegunda linha\n\n---\n\n### Fim ###')
    expect(blocks).toEqual([
      { t: 'h', level: 1, c: [text('Título')] },
      { t: 'p', c: [text('Primeira linha'), { t: 'br' }, text('segunda linha')] },
      { t: 'hr' },
      { t: 'h', level: 3, c: [text('Fim')] },
    ])
  })

  it('reads fenced code, including unclosed streams and longer fences', () => {
    expect(parseMarkdown('```go\nfmt.Println("a")\n```\ndepois')).toEqual([
      { t: 'code', lang: 'go', v: 'fmt.Println("a")', closed: true }, { t: 'p', c: [text('depois')] },
    ])
    expect(parseMarkdown('```sh\necho um\necho dois')).toEqual([{ t: 'code', lang: 'sh', v: 'echo um\necho dois', closed: false }])
    expect(parseMarkdown('````md\n```\ninterno\n```\n````')).toEqual([{ t: 'code', lang: 'md', v: '```\ninterno\n```', closed: true }])
    expect(parseMarkdown('~~~\n# não é título\n~~~')).toEqual([{ t: 'code', lang: '', v: '# não é título', closed: true }])
  })

  it('reads bullet, ordered and nested lists', () => {
    const blocks = parseMarkdown('- um\n- dois\n  - filho a\n  - filho b\n- três\n\n1. primeiro\n2. segundo\n   - aninhado')
    const [bullets, numbers] = blocks as Extract<Block, { t: 'list' }>[]
    expect(bullets.ordered).toBe(false)
    expect(bullets.items).toHaveLength(3)
    expect(bullets.items[1]).toEqual([
      { t: 'p', c: [text('dois')] },
      { t: 'list', ordered: false, start: 1, items: [[{ t: 'p', c: [text('filho a')] }], [{ t: 'p', c: [text('filho b')] }]] },
    ])
    expect(numbers.ordered).toBe(true)
    expect(numbers.items).toHaveLength(2)
    expect(numbers.items[1][1]).toMatchObject({ t: 'list', ordered: false })
  })

  it('keeps the first number of an ordered list and tolerates loose items', () => {
    const [list] = parseMarkdown('3. três\n\n4. quatro') as Extract<Block, { t: 'list' }>[]
    expect(list.start).toBe(3)
    expect(list.items).toHaveLength(2)
  })

  it('lets a list interrupt a paragraph only when it is unambiguous', () => {
    expect(parseMarkdown('Itens:\n- a\n- b').map(block => block.t)).toEqual(['p', 'list'])
    expect(parseMarkdown('Em 2024.\n2. não lista').map(block => block.t)).toEqual(['p'])
    expect(parseMarkdown('Passos:\n1. primeiro').map(block => block.t)).toEqual(['p', 'list'])
  })

  it('reads blockquotes with nested blocks and lazy lines', () => {
    expect(parseMarkdown('> **nota**\n> - a\n> - b\n\nfora')).toEqual([
      { t: 'quote', c: [{ t: 'p', c: [{ t: 'strong', c: [text('nota')] }] }, { t: 'list', ordered: false, start: 1, items: [[{ t: 'p', c: [text('a')] }], [{ t: 'p', c: [text('b')] }]] }] },
      { t: 'p', c: [text('fora')] },
    ])
  })

  it('reads GFM tables with alignment and escaped pipes', () => {
    const [table] = parseMarkdown('| Nome | Valor |\n| :--- | ---: |\n| `a|b` | 1 |\n| x \\| y | 2 |') as Extract<Block, { t: 'table' }>[]
    expect(table.t).toBe('table')
    expect(table.align).toEqual(['left', 'right'])
    expect(table.head).toEqual([[text('Nome')], [text('Valor')]])
    expect(table.rows).toEqual([[[{ t: 'code', v: 'a|b' }], [text('1')]], [[text('x | y')], [text('2')]]])
  })

  it('does not mistake a lone pipe line for a table', () => {
    expect(parseMarkdown('a | b\nsem separador').map(block => block.t)).toEqual(['p'])
  })

  it('renders the final text of a complete answer', () => {
    const answer = '## Resumo\n\n- **Arquivo:** `main.go`\n- Rodar: `go test ./...`\n\n```sh\ngo test\n```'
    expect(markdownText(parseMarkdown(answer))).toBe('Resumo\nArquivo: main.go\nRodar: go test ./...\ngo test')
  })

  it('survives hostile and truncated input', () => {
    for (const source of ['', '\n\n', '```', '> ', '- ', '1.', '| a |\n|', '#', '[', '![', '**', '~~', '`', '<', '\u0000', '- '.repeat(5000), '> '.repeat(300) + 'x', '  - '.repeat(100) + 'x']) {
      expect(() => parseMarkdown(source)).not.toThrow()
    }
  })

  it('does not create script-capable nodes from HTML-looking input', () => {
    const blocks = parseMarkdown('<div onclick="x()">oi</div>\n\n<script>alert(1)</script>')
    expect(JSON.stringify(blocks)).not.toContain('"t":"html"')
    expect(markdownText(blocks)).toContain('<script>alert(1)</script>')
  })
})
