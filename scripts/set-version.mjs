#!/usr/bin/env node
// Writes one version (X.Y.Z, from a release tag vX.Y.Z) into every place the app and its installers carry it.
// Usage: node scripts/set-version.mjs 1.2.3
import { readFileSync, writeFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const version = (process.argv[2] ?? '').replace(/^v/, '')
if (!/^\d+\.\d+\.\d+$/.test(version)) {
  console.error('Uso: node scripts/set-version.mjs X.Y.Z (ex.: 1.2.3)')
  process.exit(1)
}
const root = join(dirname(fileURLToPath(import.meta.url)), '..')

// Each file names the pattern that holds its version; the first group is kept and the version replaces the rest.
const targets = [
  ['build/config.yml', /(^\s*version:\s*")\d+\.\d+\.\d+(")/m],
  ['build/darwin/Info.plist', /(<key>CFBundleShortVersionString<\/key>\s*<string>)[^<]*(<\/string>)/],
  ['build/darwin/Info.plist', /(<key>CFBundleVersion<\/key>\s*<string>)[^<]*(<\/string>)/],
  ['build/darwin/Info.dev.plist', /(<key>CFBundleShortVersionString<\/key>\s*<string>)[^<]*(<\/string>)/],
  ['build/darwin/Info.dev.plist', /(<key>CFBundleVersion<\/key>\s*<string>)[^<]*(<\/string>)/],
  ['build/windows/info.json', /("file_version":\s*")[^"]*(")/],
  ['build/windows/info.json', /("ProductVersion":\s*")[^"]*(")/],
  ['build/windows/nsis/wails_tools.nsh', /(!define INFO_PRODUCTVERSION ")[^"]*(")/],
  ['build/windows/wails.exe.manifest', /(<assemblyIdentity[^>]*\sversion=")[^"]*(")/],
  ['build/linux/nfpm/nfpm.yaml', /(^version:\s*")[^"]*(")/m],
  ['frontend/package.json', /(^\s*"version":\s*")[^"]*(")/m],
  ['internal/externalagent/codex_catalog.go', /("title": "Harflex", "version": ")[^"]*(")/],
  ['internal/mcp/client.go', /(Name: "Harflex", Version: ")[^"]*(")/],
]
// MSIX wants four numbers.
const msix = [['build/windows/msix/app_manifest.xml', /(<Identity[\s\S]*?\sVersion=")[^"]*(")/], ['build/windows/msix/template.xml', /(\sVersion=")\d+\.\d+\.\d+\.\d+(")/]]

let failed = false
const write = (file, pattern, value) => {
  const path = join(root, file)
  const text = readFileSync(path, 'utf8')
  if (!pattern.test(text)) { console.error(`versão não encontrada em ${file}`); failed = true; return }
  writeFileSync(path, text.replace(pattern, `$1${value}$2`))
}
for (const [file, pattern] of targets) write(file, pattern, version)
for (const [file, pattern] of msix) write(file, pattern, `${version}.0`)
if (failed) process.exit(1)
console.log(`Versão ${version} aplicada.`)
