import { memo, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { Check, Copy } from 'lucide-react'
import { parseMarkdown, type Block, type Inline } from '../lib/markdown'
import { copyText, openExternal } from '../lib/desktop'

function Inlines({ nodes }: { nodes: Inline[] }): ReactNode {
  return <>{nodes.map((node, index) => {
    switch (node.t) {
      case 'text': return node.v
      case 'br': return <br key={index} />
      case 'code': return <code key={index} className="md-inline-code">{node.v}</code>
      case 'em': return <em key={index}><Inlines nodes={node.c} /></em>
      case 'strong': return <strong key={index}><Inlines nodes={node.c} /></strong>
      case 'del': return <del key={index}><Inlines nodes={node.c} /></del>
      case 'link': return <a key={index} className="md-link" href={node.href} title={node.href} rel="noopener noreferrer" onClick={event => { event.preventDefault(); void openExternal(node.href) }}><Inlines nodes={node.c} /></a>
    }
  })}</>
}

export function CopyButton({ text, label, className = 'md-copy' }: { text: string; label: string; className?: string }) {
  const [copied, setCopied] = useState(false)
  const timer = useRef<ReturnType<typeof setTimeout>>()
  useEffect(() => () => clearTimeout(timer.current), [])
  async function copy() {
    if (!await copyText(text)) return
    setCopied(true)
    clearTimeout(timer.current)
    timer.current = setTimeout(() => setCopied(false), 1600)
  }
  return <button type="button" className={className} aria-label={copied ? 'Copiado' : label} onClick={() => void copy()}>
    {copied ? <Check aria-hidden="true" /> : <Copy aria-hidden="true" />}<span aria-hidden="true">{copied ? 'Copiado' : 'Copiar'}</span>
  </button>
}

function CodeBlock({ lang, value }: { lang: string; value: string }) {
  return <figure className="md-code">
    <figcaption><span className="mono">{lang || 'texto'}</span><CopyButton text={value} label={`Copiar código${lang ? ` ${lang}` : ''}`} /></figcaption>
    <pre tabIndex={0} aria-label={`Código${lang ? ` ${lang}` : ''}`}><code>{value}</code></pre>
  </figure>
}

function BlockView({ block }: { block: Block }): ReactNode {
  switch (block.t) {
    case 'p': return <p><Inlines nodes={block.c} /></p>
    case 'h': {
      // Message headings sit below the page's own h1/h2, so they start at h3.
      const Tag = (['h3', 'h4', 'h5', 'h6', 'h6', 'h6'] as const)[block.level - 1]
      return <Tag className={`md-heading md-h${block.level}`}><Inlines nodes={block.c} /></Tag>
    }
    case 'code': return <CodeBlock lang={block.lang} value={block.v} />
    case 'quote': return <blockquote><Blocks blocks={block.c} /></blockquote>
    case 'hr': return <hr />
    case 'list': {
      const Tag = block.ordered ? 'ol' : 'ul'
      return <Tag start={block.ordered && block.start !== 1 ? block.start : undefined}>{block.items.map((item, index) => <li key={index}><Blocks blocks={item} /></li>)}</Tag>
    }
    case 'table': return <div className="md-table" role="region" aria-label="Tabela" tabIndex={0}><table>
      <thead><tr>{block.head.map((cell, index) => <th key={index} scope="col" style={block.align[index] ? { textAlign: block.align[index]! } : undefined}><Inlines nodes={cell} /></th>)}</tr></thead>
      <tbody>{block.rows.map((row, index) => <tr key={index}>{row.map((cell, column) => <td key={column} style={block.align[column] ? { textAlign: block.align[column]! } : undefined}><Inlines nodes={cell} /></td>)}</tr>)}</tbody>
    </table></div>
  }
}

function Blocks({ blocks }: { blocks: Block[] }): ReactNode {
  return <>{blocks.map((block, index) => <BlockView key={index} block={block} />)}</>
}

/** Renders assistant Markdown as React elements; nothing is injected as HTML. */
export const Markdown = memo(function Markdown({ text }: { text: string }) {
  const blocks = useMemo(() => parseMarkdown(text), [text])
  return <div className="markdown"><Blocks blocks={blocks} /></div>
})
