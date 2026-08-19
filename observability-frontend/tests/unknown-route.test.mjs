import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'

const app = await readFile(new URL('../src/App.tsx', import.meta.url), 'utf8')

assert.match(
  app,
  /<Route path="\*" element=\{<NotFound \/>\} \/>/,
  'unknown routes must render the dedicated NotFound page',
)
assert.doesNotMatch(
  app,
  /<Route path="\*" element=\{<Overview \/>\} \/>/,
  'unknown routes must not silently fall back to the overview page',
)

console.log('unknown-route regression: PASS')
