const crypto = require('crypto')
const { execFileSync } = require('child_process')
const { request: playwrightRequest } = require('playwright')
const { getJson, postJson, writeLedger } = require('../lib/strict-chain')

const NAMESPACE = 'aiops-action-test'
const TARGET = 'aiops-action-test'
const APPROVER_PASSWORD = 'StrictActionApprover#2026'

function actionExecutionSucceeded(response) {
  const status = response?.status || 0
  const body = response?.body || {}
  const executionStatus = String(body.execution_status || body.executionStatus || '').toLowerCase()
  const state = String(body.status || '').toLowerCase()
  return status >= 200 && status < 300 && (executionStatus === 'success' || executionStatus === 'succeeded' || executionStatus === 'completed' || state === 'success' || state === 'succeeded' || state === 'completed')
}

function actionStateMatches(value, expected) {
  return value !== '' && Number(value) === Number(expected)
}

function kubectl(args) {
  return execFileSync('kubectl', ['--context', process.env.AIOPS_KUBE_CONTEXT || 'orbstack', ...args], {
    encoding: 'utf8', timeout: 30_000, stdio: ['ignore', 'pipe', 'pipe'],
  }).trim()
}

function replicas() {
  return kubectl(['get', 'deployment', TARGET, '-n', NAMESPACE, '-o', 'jsonpath={.spec.replicas}'])
}

function setReplicas(value) {
  kubectl(['scale', 'deployment', TARGET, '-n', NAMESPACE, `--replicas=${value}`])
}

async function waitForReplicas(expected, timeoutMs = 60_000) {
  const deadline = Date.now() + timeoutMs
  let observed = ''
  while (Date.now() < deadline) {
    try {
      observed = replicas()
      if (actionStateMatches(observed, expected)) return observed
    } catch {}
    await new Promise((resolve) => setTimeout(resolve, 2_000))
  }
  return observed
}

function artifact(ledger, name, response, identity = {}) {
  const body = response?.body || {}
  const id = body.run_id || body.action_id || body.case_id || body.report_id || body.id
  ledger.artifacts[name] = {
    status: response?.status || 0,
    id,
    action_id: body.action_id,
    run_id: body.run_id,
    ...identity,
  }
  ledger.checks.push({ name, pass: response?.status >= 200 && response?.status < 300, status: response?.status || 0, id })
  writeLedger(ledger)
  return response
}

function runTag(runId) {
  return String(runId || Date.now()).replace(/[^a-zA-Z0-9]/g, '').slice(-20)
}

async function ensureApprover(admin, tenantId, runId) {
  const username = `strict-approver-${runTag(runId)}`
  const users = await getJson(admin, '/users?limit=100')
  const rows = Array.isArray(users.body?.users) ? users.body.users : []
  let user = rows.find((row) => row.username === username)
  let created = false
  if (!user) {
    const createdResponse = await postJson(admin, '/users', {
      username,
      password: APPROVER_PASSWORD,
      display_name: 'Strict manual test approver',
      role: 'approver',
      email: `${username}@example.invalid`,
      tenant_id: tenantId,
      is_approver: true,
    })
    if (!(createdResponse.status >= 200 && createdResponse.status < 300)) {
      return { username, user: null, created: false, error: `approver create status=${createdResponse.status}` }
    }
    const refreshed = await getJson(admin, '/users?limit=100')
    const refreshedRows = Array.isArray(refreshed.body?.users) ? refreshed.body.users : []
    user = refreshedRows.find((row) => row.username === username)
    created = true
  }
  if (!user?.id || user.role !== 'approver') return { username, user: null, created, error: 'approver role or id missing' }
  return { username, user, created, error: '' }
}

async function loginApprover(username, tenantId, clusterId) {
  const context = await playwrightRequest.newContext()
  const login = await context.post(`${process.env.AIOPS_API_BASE || 'http://localhost:30253/api/v1'}/auth/login`, {
    data: { username, password: APPROVER_PASSWORD },
  })
  if (!login.ok()) {
    await context.dispose()
    return { context: null, error: `approver login status=${login.status()}` }
  }
  const scope = await context.post(`${process.env.AIOPS_API_BASE || 'http://localhost:30253/api/v1'}/me/scope`, {
    data: { tenant_id: tenantId, cluster_id: clusterId },
  })
  if (!scope.ok()) {
    await context.dispose()
    return { context: null, error: `approver scope status=${scope.status()}` }
  }
  return { context, error: '' }
}

async function readActionUntilTerminal(admin, actionId, timeoutMs = 90_000) {
  const deadline = Date.now() + timeoutMs
  let latest = { status: 0, body: {} }
  while (Date.now() < deadline) {
    latest = await getJson(admin, `/ai/actions/${encodeURIComponent(actionId)}`)
    const state = String(latest.body?.execution_status || latest.body?.status || '').toLowerCase()
    if (['success', 'succeeded', 'completed', 'rejected', 'failed', 'cancelled'].includes(state)) return latest
    await new Promise((resolve) => setTimeout(resolve, 2_000))
  }
  return latest
}

async function createApproveExecute({ admin, approver, ledger, clusterId, action, name, expectedReplicas, check, selfDecision = false }) {
  const proposal = await postJson(admin, '/ai/actions', {
    idempotency_key: `${name}-${ledger.run_id}-${crypto.randomUUID()}`,
    cluster_id: clusterId,
    resource_type: 'deployment',
    namespace: NAMESPACE,
    target_name: TARGET,
    operation: 'scale',
    params: { replicas: expectedReplicas },
  })
  artifact(ledger, `${name}_proposal`, proposal)
  const actionId = proposal.body?.action_id
  check(`${name}_proposal_created`, (proposal.status === 200 || proposal.status === 201) && Boolean(actionId), `status=${proposal.status}`)
  if (!actionId) return null

  if (selfDecision) {
    const self = await postJson(admin, `/ai/actions/${encodeURIComponent(actionId)}/decision`, {
      decision: 'approved', action_version: action.action_version || 1, idempotency_key: `${name}-self-${ledger.run_id}`,
    })
    check('approval_separation_of_duties', self.status === 409, `self-decision status=${self.status}`)
  }

  const approval = await postJson(approver, `/ai/actions/${encodeURIComponent(actionId)}/decision`, {
    decision: 'approved', action_version: action.action_version || 1, idempotency_key: `${name}-approval-${ledger.run_id}`,
  })
  artifact(ledger, `${name}_approval`, approval, { action_id: actionId })
  check(`${name}_approved_by_independent_approver`, approval.status >= 200 && approval.status < 300, `status=${approval.status}`)

  const approvedReadback = await getJson(admin, `/ai/actions/${encodeURIComponent(actionId)}`)
  artifact(ledger, `${name}_approved_readback`, approvedReadback, { action_id: actionId })
  check(`${name}_approval_readback`, approvedReadback.status === 200 && String(approvedReadback.body?.status || '').toLowerCase() === 'approved', `status=${approvedReadback.status}`)

  const execution = await postJson(admin, `/ai/actions/${encodeURIComponent(actionId)}/execute`, {})
  artifact(ledger, `${name}_execute`, execution, { action_id: actionId })
  const final = await readActionUntilTerminal(admin, actionId)
  artifact(ledger, `${name}_readback`, final, { action_id: actionId })
  check(`${name}_execution_succeeded`, actionExecutionSucceeded(final), `status=${final.status} execution_status=${final.body?.execution_status || final.body?.status || ''}`)
  const observed = await waitForReplicas(expectedReplicas)
  check(`${name}_target_state_verified`, actionStateMatches(observed, expectedReplicas), `replicas=${observed} expected=${expectedReplicas}`)
  return actionId
}

async function runStrictActionLoop({ admin, ledger, tenantId, clusterId }) {
  const checks = []
  const check = (name, pass, detail = '') => checks.push({ name, pass: Boolean(pass), detail: String(detail).slice(0, 500) })
  if (!ledger?.telemetry_ready) {
    check('action_telemetry_precondition', false, 'fresh strict telemetry is unavailable')
    return { checks }
  }

  let approverContext = null
  let approverId = null
  let approverUsername = ''
  try {
    const baseline = replicas()
    if (!actionStateMatches(baseline, 1)) setReplicas(1)
    const baselineObserved = await waitForReplicas(1)
    check('action_target_precondition', actionStateMatches(baselineObserved, 1), `replicas=${baselineObserved} expected=1`)

    const approver = await ensureApprover(admin, tenantId, ledger.run_id)
    approverUsername = approver.username
    approverId = approver.user?.id || null
    check('independent_approver_ready', Boolean(approver.user?.id) && approver.user.role === 'approver', approver.error || approver.username)
    if (!approver.user?.id) return { checks }

    const loggedIn = await loginApprover(approver.username, tenantId, clusterId)
    approverContext = loggedIn.context
    check('independent_approver_login', Boolean(approverContext), loggedIn.error || approver.username)
    if (!approverContext) return { checks }

    const action = await postJson(admin, '/ai/actions', {
      idempotency_key: `strict-action-${ledger.run_id}-${crypto.randomUUID()}`,
      cluster_id: clusterId,
      resource_type: 'deployment', namespace: NAMESPACE, target_name: TARGET,
      operation: 'scale', params: { replicas: 2 },
    })
    artifact(ledger, 'strict_action_proposal', action)
    const actionId = action.body?.action_id
    check('action_proposal_created', (action.status === 200 || action.status === 201) && Boolean(actionId), `status=${action.status}`)
    if (!actionId) return { checks }
    const actionRecord = action.body
    if (actionId) {
      const selfDecision = await postJson(admin, `/ai/actions/${encodeURIComponent(actionId)}/decision`, {
        decision: 'approved', action_version: actionRecord.action_version || 1, idempotency_key: `strict-self-${ledger.run_id}`,
      })
      check('approval_separation_of_duties', selfDecision.status === 409, `self-decision status=${selfDecision.status}`)
      const approval = await postJson(approverContext, `/ai/actions/${encodeURIComponent(actionId)}/decision`, {
        decision: 'approved', action_version: actionRecord.action_version || 1, idempotency_key: `strict-approval-${ledger.run_id}`,
      })
      artifact(ledger, 'strict_action_approval', approval, { action_id: actionId })
      check('action_approved_by_independent_approver', approval.status >= 200 && approval.status < 300, `status=${approval.status}`)
      const approvalReadback = await getJson(admin, `/ai/actions/${encodeURIComponent(actionId)}`)
      artifact(ledger, 'strict_action_approval_readback', approvalReadback, { action_id: actionId })
      check('action_approval_readback', approvalReadback.status === 200 && String(approvalReadback.body?.status || '').toLowerCase() === 'approved', `status=${approvalReadback.status}`)
      const execution = await postJson(admin, `/ai/actions/${encodeURIComponent(actionId)}/execute`, {})
      artifact(ledger, 'strict_action_execute', execution, { action_id: actionId })
      const final = await readActionUntilTerminal(admin, actionId)
      artifact(ledger, 'strict_action_readback', final, { action_id: actionId })
      check('action_execution_succeeded', actionExecutionSucceeded(final), `status=${final.status} execution_status=${final.body?.execution_status || final.body?.status || ''}`)
      const observed = await waitForReplicas(2)
      check('action_target_scaled_to_2', actionStateMatches(observed, 2), `replicas=${observed} expected=2`)

      const rollbackId = await createApproveExecute({
        admin, approver: approverContext, ledger, clusterId, action: actionRecord,
        name: 'strict_action_rollback', expectedReplicas: 1, check,
      })
      check('action_rollback_created', Boolean(rollbackId), `action_id=${rollbackId || ''}`)
    }
  } catch (error) {
    check('action_loop_exception', false, 'action loop failed before completion')
  } finally {
    if (approverContext) await approverContext.dispose().catch(() => {})
    if (approverId) {
      const deleted = await admin.delete(`/users/${encodeURIComponent(approverId)}`)
      check('independent_approver_cleaned', deleted.status() === 200 || deleted.status() === 204, `status=${deleted.status()}`)
    } else if (approverUsername) {
      check('independent_approver_cleaned', true, 'approver was not created')
    }
    try {
      const restored = await waitForReplicas(1)
      check('action_final_state_restored', actionStateMatches(restored, 1), `replicas=${restored} expected=1`)
    } catch {
      check('action_final_state_restored', false, 'target state could not be read')
    }
  }
  return { checks }
}

module.exports = { actionExecutionSucceeded, actionStateMatches, runStrictActionLoop }
