// Checks the translations: every t('…') key exists in English and Spanish, placeholders match, and no
// accented Portuguese is left outside t(). Run: node scripts/check-i18n.mjs
import { readFileSync, readdirSync, statSync } from 'node:fs'
import { dirname, join, relative } from 'node:path'
import { fileURLToPath } from 'node:url'

const root = join(dirname(fileURLToPath(import.meta.url)), '..')
const src = join(root, 'src')
const files = (dir) => readdirSync(dir).flatMap(name => {
  const path = join(dir, name)
  if (statSync(path).isDirectory()) return files(path)
  return /\.(tsx?|mjs)$/.test(name) && !/\.test\./.test(name) && !path.includes('/i18n/') && !path.includes('/src/test/') ? [path] : []
})

// The dictionaries are plain object literals; read the braces of one literal, skipping quoted text.
function literal(text, label) {
  const start = text.indexOf(`export const ${label}: Record<string, string> = {`)
  if (start < 0) return {}
  let i = text.indexOf('{', start), depth = 0, quote = ''
  const from = i
  for (; i < text.length; i++) {
    const c = text[i]
    if (quote) { if (c === '\\') i++; else if (c === quote) quote = ''; continue }
    if (c === "'" || c === '"') quote = c
    else if (c === '{') depth++
    else if (c === '}' && --depth === 0) break
  }
  return Function(`return (${text.slice(from, i + 1)})`)()
}
async function dictionary(name) {
  const text = readFileSync(join(src, 'i18n/dictionaries', `${name}.ts`), 'utf8')
  return { en: literal(text, 'en'), es: literal(text, 'es') }
}

const names = readdirSync(join(src, 'i18n/dictionaries')).filter(f => f.endsWith('.ts') && f !== 'index.ts').map(f => f.slice(0, -3))
const en = {}, es = {}
for (const name of names) {
  const d = await dictionary(name)
  Object.assign(en, d.en); Object.assign(es, d.es)
}

const problems = []
const used = new Set()
for (const file of files(src)) {
  const code = readFileSync(file, 'utf8')
  // Keys: t('…'), t("…") and t(`…`) without interpolation; the first argument only.
  for (const match of code.matchAll(/\bt\(\s*(['"`])((?:\\.|(?!\1).)*?)\1/g)) {
    const key = match[2].replace(/\\'/g, "'").replace(/\\"/g, '"')
    used.add(key)
    if (!(key in en)) problems.push(`sem en: ${JSON.stringify(key)} (${relative(root, file)})`)
    if (!(key in es)) problems.push(`sem es: ${JSON.stringify(key)} (${relative(root, file)})`)
  }
}
for (const key of Object.keys(en)) {
  const placeholders = (text) => [...text.matchAll(/\{(\w+)\}/g)].map(m => m[1]).sort().join(',')
  if (!(key in es)) problems.push(`es ausente no dicionário: ${JSON.stringify(key)}`)
  else if (placeholders(key) !== placeholders(en[key]) || placeholders(key) !== placeholders(es[key])) problems.push(`placeholders diferentes: ${JSON.stringify(key)}`)
}

// Accented Portuguese left in code (outside the dictionaries and tests): a rough signal, reviewed by hand.
const stray = []
for (const file of files(src)) {
  readFileSync(file, 'utf8').split('\n').forEach((line, index) => {
    // Comments are not UI text: drop block/JSDoc lines and trailing // comments before looking for accents.
    const code = line.replace(/\s\/\/\s.*$/, '').replace(/\/\*.*\*\//g, '')
    if (/^\s*(\/\/|\*|\/\*)/.test(line)) return
    if (/[áéíóúãõçâêôÁÉÍÓÚÃÕÇ]/.test(code) && !/\bt\(/.test(code)) stray.push(`${relative(root, file)}:${index + 1}`)
  })
}

console.log(`chaves usadas: ${used.size} · en: ${Object.keys(en).length} · es: ${Object.keys(es).length}`)
console.log(`problemas: ${problems.length}`)
for (const p of problems.slice(0, 60)) console.log('  -', p)
console.log(`linhas com acento fora de t() (revisar): ${stray.length}`)
for (const s of stray) console.log('  ·', s)
process.exitCode = problems.length ? 1 : 0

// Coverage: every Portuguese string literal in the code must be a dictionary key, because the screen shows it through t().
// Literals used only as code (identifiers, protocol values, paths) are listed too, and reviewed by hand.
const uncovered = new Map()
for (const file of files(src)) {
  readFileSync(file, 'utf8').split('\n').forEach((line, index) => {
    const code = line.replace(/\s\/\/\s.*$/, '')
    if (/^\s*(\/\/|\*|\/\*)/.test(line)) return
    for (const match of code.matchAll(/(['"])((?:\\.|(?!\1).)*?)\1/g)) {
      const text = match[2]
      if (!/[áéíóúãõçâêôÁÉÍÓÚÃÕÇ]/.test(text) || text.includes('${') || text.includes('{')) continue
      if (text in en) continue
      const where = `${relative(root, file)}:${index + 1}`
      uncovered.set(text, [...(uncovered.get(text) ?? []), where])
    }
  })
}
console.log(`textos em português sem tradução: ${uncovered.size}`)
for (const [text, where] of [...uncovered].slice(0, 200)) console.log(`  ✗ ${JSON.stringify(text).slice(0, 110)}  (${where[0]}${where.length > 1 ? ` +${where.length - 1}` : ''})`)
