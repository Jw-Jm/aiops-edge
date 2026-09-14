const VALID_STATUSES = new Set(['PASS', 'FAIL', 'PARTIAL', 'BLOCKED', 'ERROR'])

function failedChecks(checks = []) {
  return checks.filter((check) => !check.pass).map((check) => check.name)
}

function blockedResult({ id, checks, evidence }) {
  return {
    id,
    status: 'BLOCKED',
    failedChecks: [],
    blocker_code: 'TELEMETRY_PRECONDITION_UNMET',
    evidence,
    checks,
  }
}

function classifyCase({ id, checks = [], spec = {}, preflight = {} }) {
  const failures = failedChecks(checks)
  if (spec.requires?.telemetry && preflight.telemetryReady === false && !spec.allowEmpty) {
    return blockedResult({ id, checks, evidence: preflight.telemetryEvidence || { reason: 'telemetry precondition is not ready' } })
  }
  return {
    id,
    status: failures.length > 0 ? 'FAIL' : 'PASS',
    failedChecks: failures,
    checks,
  }
}

function applyTelemetryPrecondition(results, preflight) {
  if (preflight.telemetryReady !== false) return results
  const evidence = preflight.telemetryEvidence || { reason: 'telemetry precondition is not ready' }
  return results.map((result) => blockedResult({
    id: result.id,
    checks: result.checks || [],
    evidence,
  }))
}

function assertResultStatus(result) {
  if (!result || !VALID_STATUSES.has(result.status)) {
    throw new Error(`invalid result status for ${result?.id || 'unknown'}: ${result?.status || 'missing'}`)
  }
  if (result.status === 'BLOCKED' && (!result.blocker_code || !result.evidence)) {
    throw new Error(`blocked result ${result.id} must include blocker_code and evidence`)
  }
}

module.exports = {
  VALID_STATUSES,
  applyTelemetryPrecondition,
  assertResultStatus,
  classifyCase,
}
