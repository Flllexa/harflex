import { readFileSync } from 'node:fs'
import { execFileSync } from 'node:child_process'
import { test } from 'node:test'
import assert from 'node:assert/strict'

const read = path => readFileSync(new URL('../' + path, import.meta.url), 'utf8')
test('design scan uses actual CSS tokens and portable component snippets', () => {
  const design = read('DESIGN.md')
  const sidecar = JSON.parse(read('.impeccable/design.json'))
  const css = read('frontend/src/styles/tokens.css')
  const globalCSS = read('frontend/src/styles/global.css')
  assert.ok(!design.includes('<!-- SEED:'))
  assert.equal(sidecar.schemaVersion, 2)
  assert.deepEqual([...design.matchAll(/^## (.+)$/gm)].map(m => m[1]), [
    'Overview', 'Colors', 'Typography', 'Layout', 'Elevation & Depth', 'Shapes', 'Components', "Do's and Don'ts",
  ])
  for (const [name, meta] of Object.entries(sidecar.extensions.colorMeta)) {
    assert.ok(css.includes('--' + name + ': ' + meta.canonical), name)
    assert.ok(design.includes(name + ': "' + meta.canonical + '"'), name)
    assert.equal(meta.tonalRamp.length, 8)
  }
  assert.ok(sidecar.components.length >= 5 && sidecar.components.length <= 10)
  for (const component of sidecar.components) {
    assert.ok(design.includes('  ' + component.refersTo + ':'), component.refersTo)
    assert.match(component.html, /class="ds-/)
    assert.doesNotMatch(component.html + component.css, /<img|https?:|@import|url\(/)
    for (const match of component.css.matchAll(/var\((--[^,)]+)/g)) assert.ok(css.includes(match[1] + ':'), match[1])
  }
  for (const text of [sidecar.narrative.overview, ...sidecar.narrative.keyCharacteristics, ...sidecar.narrative.dos, ...sidecar.narrative.donts]) {
    assert.ok(design.includes(text), 'narrative must be verbatim: ' + text)
  }
  assert.ok(design.includes(sidecar.narrative.northStar))
  for (const rule of sidecar.narrative.rules) assert.ok(design.includes('**' + rule.name + '.** ' + rule.body))
  for (const breakpoint of sidecar.extensions.breakpoints) assert.ok(globalCSS.includes('(min-width: ' + breakpoint.value + ')'))
  for (const shadow of sidecar.extensions.shadows) assert.ok(globalCSS.includes(shadow.value))
  for (const match of css.matchAll(/--font-(?:sans|mono): ([^;]+);/g)) assert.ok(design.includes('fontFamily: "' + match[1] + '"'))
  const componentTokens = design.split('\ncomponents:\n')[1].split('\n---')[0]
  const allowed = ['backgroundColor', 'textColor', 'typography', 'rounded', 'padding', 'size', 'height', 'width']
  for (const match of componentTokens.matchAll(/^    (\w+):/gm)) assert.ok(allowed.includes(match[1]), match[1])
})
test('release checks pin tooling and expose synthetic browser tests', () => {
  assert.match(read('go.mod'), /toolchain go1\.26\.8/)
  assert.equal(JSON.parse(read('frontend/package.json')).scripts['test:e2e'], 'playwright test')
  const taskfile = read('Taskfile.yml')
  for (const task of ['test:go:', 'test:frontend:', 'test:e2e:', 'check:tidy:', 'check:diff:']) assert.ok(taskfile.includes(task))
  const workflow = read('.github/workflows/ci.yml')
  for (const marker of ['contents: read', '1.26.8', 'node-version: 22', 'v3.0.0-beta.26', 'unsigned-smoke-', 'libgtk-4-dev', 'libwebkitgtk-6.0-dev']) assert.ok(workflow.includes(marker), marker)
  assert.doesNotMatch(workflow, /secrets\.|wails3 (package|sign)|npm publish|gh release/)
})

test('whitespace gate checks committed ranges and normalizes source line endings', () => {
  const attrs = read('.gitattributes')
  for (const extension of ['bat', 'sh', 'go', 'ts', 'tsx', 'css', 'yml', 'yaml', 'xml', 'plist', 'nsi', 'nsh', 'txt', 'java', 'mjs', 'js', 'html', 'json', 'sql', 'gradle', 'properties', 'md', 'toml', 'mod', 'sum', 'svg', 'storyboard']) {
    assert.match(attrs, new RegExp('^\\*\\.' + extension + ' text eol=' + (extension === 'bat' ? 'crlf' : 'lf') + '$', 'm'))
  }
  for (const pattern of ['**/gradlew', 'Dockerfile*', '**/Dockerfile*', '*.m', '*.pbxproj', '**/desktop', '*.manifest', '*.pro', '.gitattributes', '.gitignore', 'frontend/.npmrc', '*.xcassets']) {
    assert.ok(attrs.includes(pattern + ' text eol=lf\n'), pattern)
  }
  assert.ok(attrs.includes('build/ios/icon.png binary\n'))
  const paths = ['build/android/gradlew', 'build/docker/Dockerfile.cross', 'build/ios/main.m', 'build/ios/project.pbxproj', 'build/linux/desktop', 'build/windows/wails.exe.manifest', 'build/android/app/proguard-rules.pro', 'build/ios/Assets.xcassets', 'frontend/.npmrc']
  const actual = execFileSync('git', ['check-attr', 'eol', '--', ...paths], { cwd: new URL('..', import.meta.url), encoding: 'utf8' })
  for (const path of paths) assert.ok(actual.includes(path + ': eol: lf\n'), path)
  assert.match(execFileSync('git', ['check-attr', 'text', '--', 'build/ios/icon.png'], { cwd: new URL('..', import.meta.url), encoding: 'utf8' }), /build\/ios\/icon\.png: text: unset/)
  const taskfile = read('Taskfile.yml')
  assert.match(taskfile, /git diff --check "?\$\(git merge-base HEAD origin\/main\)"?\.\.HEAD/)
  const workflow = read('.github/workflows/ci.yml')
  assert.match(workflow, /fetch-depth: 0/)
  assert.match(workflow, /git diff --check "\$PR_BASE_SHA"\.\.HEAD/)
  assert.match(workflow, /git diff --check "\$PUSH_BEFORE_SHA"\.\.HEAD/)
  assert.match(workflow, /DEFAULT_BRANCH: \$\{\{ github\.event\.repository\.default_branch \}\}/)
  assert.match(workflow, /git merge-base HEAD "\$default_ref"/)
  assert.match(workflow, /git hash-object -t tree \/dev\/null/)
  assert.match(workflow, /git diff --check "\$empty" HEAD/)
  assert.doesNotMatch(workflow, /git rev-list --max-parents=0 HEAD|git show --check --format= "\$base"/)
  assert.doesNotMatch(workflow, /git diff --check HEAD\^\.\.HEAD/)
})

const declarations = (source, selector) => {
  const index = source.indexOf(selector)
  assert.ok(index >= 0, 'missing CSS selector: ' + selector)
  return source.slice(index + selector.length).match(/^[^{]*{([^}]+)}/)[1]
}
const property = (source, name) => {
  const value = source.split(';').map(entry => entry.split(':').map(part => part.trim())).find(([key]) => key === name)?.[1]
  assert.ok(value, 'missing CSS property: ' + name)
  return value
}

test('sidecar input focus matches the implemented offset', () => {
  const input = JSON.parse(read('.impeccable/design.json')).components.find(component => component.refersTo === 'input')
  const nativeFocus = declarations(read('frontend/src/styles/global.css'), 'input:focus-visible, textarea:focus-visible')
  assert.equal(property(declarations(input.css, '.ds-input:focus-visible'), 'outline-offset'), property(nativeFocus, 'outline-offset'))
})

test('sidecar navigation retains selected colors on hover', () => {
  const nav = JSON.parse(read('.impeccable/design.json')).components.find(component => component.refersTo === 'nav-current')
  assert.match(nav.html, /aria-current="page"/)
  const nativeSelected = declarations(read('frontend/src/styles/global.css'), '.nav-item[aria-current]')
  const selectedHover = declarations(nav.css, '.ds-nav[aria-current]:hover')
  for (const name of ['background', 'color']) {
    const normalize = value => value.replace(/,[^)]*/g, '')
    assert.equal(normalize(property(selectedHover, name)), property(nativeSelected, name), name)
  }
})
