// Reads the Code evidence (a unified diff) into files and lines for the Code bench.

export type DiffLine = { kind: 'add' | 'remove' | 'context' | 'hunk'; text: string; oldNumber?: number; newNumber?: number }
export type DiffFile = { path: string; status: 'added' | 'removed' | 'modified'; added: number; removed: number; lines: DiffLine[] }

export function parseUnifiedDiff(content: string): DiffFile[] {
  const files: DiffFile[] = []
  let current: DiffFile | undefined
  let oldLine = 0, newLine = 0
  for (const raw of content.split('\n')) {
    if (raw.startsWith('diff --git ')) {
      const match = / b\/(.+)$/.exec(raw)
      current = { path: match ? match[1] : raw.slice(11), status: 'modified', added: 0, removed: 0, lines: [] }
      files.push(current)
      continue
    }
    if (!current) {
      // Evidence without git headers: one file named by the first +++ line, or a single block.
      if (raw.startsWith('+++ ')) { current = { path: raw.slice(4).replace(/^b\//, ''), status: 'modified', added: 0, removed: 0, lines: [] }; files.push(current) }
      continue
    }
    if (raw.startsWith('new file mode')) { current.status = 'added'; continue }
    if (raw.startsWith('deleted file mode')) { current.status = 'removed'; continue }
    if (raw.startsWith('index ') || raw.startsWith('--- ') || raw.startsWith('+++ ') || raw.startsWith('similarity ') || raw.startsWith('rename ') || raw.startsWith('old mode') || raw.startsWith('new mode') || raw.startsWith('Binary files')) continue
    const hunk = /^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@(.*)$/.exec(raw)
    if (hunk) {
      oldLine = Number(hunk[1]); newLine = Number(hunk[2])
      current.lines.push({ kind: 'hunk', text: hunk[3].trim() })
      continue
    }
    if (raw.startsWith('+')) { current.lines.push({ kind: 'add', text: raw.slice(1), newNumber: newLine++ }); current.added++ }
    else if (raw.startsWith('-')) { current.lines.push({ kind: 'remove', text: raw.slice(1), oldNumber: oldLine++ }); current.removed++ }
    else if (raw.startsWith(' ') || raw === '') { if (raw === '' && current.lines.length === 0) continue; current.lines.push({ kind: 'context', text: raw.slice(1), oldNumber: oldLine++, newNumber: newLine++ }) }
  }
  return files.map(file => ({ ...file, lines: trimTrailingEmpty(file.lines) }))
}

function trimTrailingEmpty(lines: DiffLine[]) {
  let end = lines.length
  while (end > 0 && lines[end - 1].kind === 'context' && lines[end - 1].text === '') end--
  return lines.slice(0, end)
}
