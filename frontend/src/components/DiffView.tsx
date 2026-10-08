export function diffLineClass(line: string) {
  if (line.startsWith('+++') || line.startsWith('---')) return 'diff-meta'
  if (line.startsWith('+')) return 'diff-add'
  if (line.startsWith('-')) return 'diff-del'
  if (line.startsWith('@@')) return 'diff-hunk'
  return undefined
}

/** Unified diff with the product's add/remove/hunk colors. */
export function DiffView({ diff, label }: { diff: string; label?: string }) {
  return <pre className="mono diff-body" aria-label={label} tabIndex={0}>{diff.split('\n').map((line, index) => <span key={index} className={diffLineClass(line)}>{line}{'\n'}</span>)}</pre>
}

/** Shows the first lines of long text and says how much was left out. */
export function clip(text: string, maxLines = 16, maxChars = 2400): { text: string; hiddenLines: number } {
  const lines = text.split('\n')
  let kept = lines.slice(0, maxLines).join('\n')
  if (kept.length > maxChars) kept = kept.slice(0, maxChars)
  const hiddenLines = Math.max(0, lines.length - kept.split('\n').length)
  return { text: kept, hiddenLines }
}
