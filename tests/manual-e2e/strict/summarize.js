const fs = require('fs')
const path = require('path')
const { assertResultStatus, VALID_STATUSES } = require('./result-policy')

function summarizeItems({ manifest, items, metadata = {} }) {
  const expectedIds = new Set(manifest.map((item) => item.id))
  const seen = new Set()
  for (const item of items) {
    if (seen.has(item.id)) throw new Error(`duplicate result id: ${item.id}`)
    if (!expectedIds.has(item.id)) throw new Error(`unknown result id: ${item.id}`)
    if (!VALID_STATUSES.has(item.status)) throw new Error(`invalid result status for ${item.id}: ${item.status}`)
    assertResultStatus(item)
    seen.add(item.id)
  }
  const missing = manifest.map((item) => item.id).filter((id) => !seen.has(id))
  if (missing.length > 0) throw new Error(`missing result ids: ${missing.join(', ')}`)

  const counts = { PASS: 0, FAIL: 0, PARTIAL: 0, BLOCKED: 0, ERROR: 0 }
  for (const item of items) counts[item.status] += 1
  return {
    ...metadata,
    counts,
    total_expected: manifest.length,
    total_observed: items.length,
    strict_go: manifest.length === items.length && items.every((item) => item.status === 'PASS'),
    items,
  }
}

function writeSummaryAtomic(filePath, summary) {
  const dir = path.dirname(filePath)
  fs.mkdirSync(dir, { recursive: true })
  const temporary = `${filePath}.tmp-${process.pid}-${Date.now()}`
  fs.writeFileSync(temporary, `${JSON.stringify(summary, null, 2)}\n`)
  fs.renameSync(temporary, filePath)
}

module.exports = { summarizeItems, writeSummaryAtomic }
