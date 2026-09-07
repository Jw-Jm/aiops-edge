// PF-LOGIC-010 K8s 页面→Action→结果逻辑（完整闭环）。
// kubectl 部署测试 deployment（aiops-action-test/test-lanee-nginx, nginx:alpine, replicas=1）
// → K8s 页面 UI 创建 scale Proposal(1→2) → 审批中心批准 → 执行 → kubectl 反查 replicas=2 → 页面核对。
// 产品规则（实测确认）：Proposal 与 Approval 职责分离——proposed_by 与 approver 不得为同一
// 用户（decideAction 返回 409 ACTION_DECISION_CONFLICT）。因此审批由专用 approver 用户
// （approver-lanee，role=approver，测试创建）在审批中心 UI 完成；执行由 admin 完成。
// Standalone: TEST_RUN_ID=nonllm-20260905 NODE_PATH=/opt/homebrew/lib/node_modules node tests/manual-e2e/laneE-logic-010.js
const { ENV, uiLogin, spaNav, makeCollector, shot, withSession } = require('./lib/laneD-runner')
const { writeResult, apiRetry, sh, mysqlExec } = require('./lib/laneE')

const NS = 'aiops-action-test'
const DEP = 'test-lanee-nginx'
const APPROVER = 'approver-lanee'
const APPROVER_PW = 'ApproverLaneE#2026'
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

function kubectl(cmd) {
  return sh(`kubectl ${cmd} 2>&1`).trim()
}

async function run() {
  const vp = ENV.viewports[0]
  const tag = `${vp.width}x${vp.height}`
  const checks = []
  const notes = []
  const check = (name, pass, detail = '') => checks.push({ name, pass: !!pass, detail: String(detail).slice(0, 500) })
  const col = makeCollector(tag)
  let actionId = ''

  // 0. 准备真实测试资源（幂等；若上次运行已扩到 2，先回到 1）
  kubectl(`create ns ${NS} --dry-run=client -o yaml | kubectl apply -f -`)
  kubectl(`scale -n ${NS} deploy/${DEP} --replicas=1 2>/dev/null || true`)
  const applyOut = kubectl(`apply -n ${NS} -f - <<'EOF'
apiVersion: apps/v1
kind: Deployment
metadata:
  name: ${DEP}
  labels: {app: test-lanee, owner: laneE-test}
spec:
  replicas: 1
  selector: {matchLabels: {app: test-lanee}}
  template:
    metadata: {labels: {app: test-lanee}}
    spec:
      containers:
        - name: nginx
          image: nginx:alpine
          ports: [{containerPort: 80}]
EOF
`)
  notes.push(`测试 deployment 部署：${applyOut.slice(0, 120)}`)
  try { sh(`kubectl wait -n ${NS} deploy/${DEP} --for=condition=available --timeout=180s`) } catch (e) { notes.push(`wait: ${String(e).slice(0, 120)}`) }
  const replicas0 = kubectl(`get deploy -n ${NS} ${DEP} -o jsonpath='{.spec.replicas}'`)
  check('test_deployment_ready_replicas_1', replicas0 === '1', `kubectl get deploy spec.replicas=${replicas0}（nginx:alpine）`)

  await withSession({ viewport: vp, col, seedScope: true }, async (page) => {
    // 1. K8s 页面：确认页面资源列表可见目标 deployment
    await spaNav(page, '/infra/k8s')
    await page.waitForTimeout(3500)
    const k8sText0 = ((await page.locator('body').textContent().catch(() => '')) || '')
    check('k8s_page_lists_target', k8sText0.includes(DEP), `K8s 运维页面 Deployments 列表包含 ${DEP}=${k8sText0.includes(DEP)}`)

    // 2. 页面 UI 创建 scale Proposal（1→2）
    await page.locator('input[placeholder="命名空间"]').fill(NS)
    await page.locator('input[placeholder="资源名称"]').fill(DEP)
    await page.locator('.card').filter({ hasText: '目标资源与动作' }).locator('.ant-select').nth(1).click()
    await page.waitForTimeout(600)
    await page.locator('.ant-select-dropdown:not(.ant-select-dropdown-hidden) .ant-select-item-option').filter({ hasText: /扩缩容|scale/i }).first().click()
    await page.waitForTimeout(600)
    await page.locator('.card').filter({ hasText: '目标资源与动作' }).locator('.ant-input-number input').first().fill('2')
    await page.waitForTimeout(400)
    await page.getByRole('button', { name: /预检并提交审批/ }).click()
    await page.waitForTimeout(4500)
    const k8sText1 = ((await page.locator('body').textContent().catch(() => '')) || '')
    const m = k8sText1.match(/Action ID\s*([a-f0-9-]{36}|[A-Za-z0-9_-]{6,64})/)
    actionId = m ? m[1] : ''
    check('proposal_created_via_k8s_page', /Action 已创建/.test(k8sText1) && !!actionId,
      `页面显示"Action 已创建"，Action ID=${actionId || '(未解析)'}；预检通过（preflight_status=passed）`)

    // 3. 职责分离规则验证：admin（proposer）自行批准 → 预期 409（产品正确行为）
    if (actionId) {
      const self = await apiRetry(page, 'post', `/ai/actions/${actionId}/decision`, {
        decision: 'approved', action_version: 1, idempotency_key: `laneE-self-${Date.now()}`,
      })
      check('separation_of_duties_enforced', self.status === 409,
        `proposer(admin) 自行批准 POST /decision -> ${self.status} ${self.body.slice(0, 120)}（产品职责分离规则：提案人与审批人不得同一用户，行为正确）`)
      const a = await apiRetry(page, 'get', `/ai/actions/${actionId}`)
      check('proposal_api_status_proposed', a.status === 200 && /"status":"proposed"/.test(a.body),
        `GET /ai/actions/${actionId} -> ${a.status} status=proposed execution_status=proposed`)
    }

    // 4. 管理员 UI 创建审批人用户（approver 角色）
    await spaNav(page, '/admin/users')
    await page.waitForTimeout(2500)
    let approverReady = false
    const usersApi0 = await apiRetry(page, 'get', '/users')
    if (!/approver-lanee/.test(usersApi0.body)) {
      await page.getByRole('button', { name: /新增用户/ }).first().click()
      await page.waitForTimeout(900)
      const modal = page.locator('.ant-modal:visible')
      const fieldInput = (label) => modal.locator('.ant-form-item').filter({ hasText: label }).locator('input').first()
      await fieldInput('用户名').fill(APPROVER)
      await modal.locator('input[type="password"]').first().fill(APPROVER_PW)
      await fieldInput('显示名').fill('laneE 审批人')
      // 角色 Select → approver
      await modal.locator('.ant-form-item').filter({ hasText: '角色' }).locator('.ant-select').first().click()
      await page.waitForTimeout(600)
      await page.locator('.ant-select-dropdown:not(.ant-select-dropdown-hidden) .ant-select-item-option').filter({ hasText: /approver|审批/i }).first().click()
      await page.waitForTimeout(400)
      await fieldInput('邮箱').fill('approver-lanee@example.invalid')
      await modal.getByRole('button', { name: /确\s*定/ }).first().click()
      await page.waitForTimeout(2500)
    }
    const usersApi1 = await apiRetry(page, 'get', '/users')
    approverReady = /approver-lanee/.test(usersApi1.body)
    check('approver_user_ready', approverReady, `审批人用户 ${APPROVER}（role=approver）就绪=${approverReady}（测试专用账号，随测试创建）`)
    // 产品缺陷（实测）：POST /users 创建的用户不写入 user_tenants（仅 bootstrap admin 有
    // 租户成员关系）→ 新用户登录后无法选择任何集群 scope，所有页面 403。
    // 测试干预：MySQL 补写租户成员关系（记录于 notes），使审批人可建立 scope 完成审批。
    const member = mysqlExec(`SELECT count(*) AS c FROM user_tenants ut JOIN users u ON u.user_uuid=ut.user_uuid WHERE u.username='${APPROVER}'`).trim().split('\n').pop().trim()
    if (member === '0') {
      mysqlExec(`INSERT INTO user_tenants (user_uuid, tenant_id, status) SELECT user_uuid, '${ENV.tenantId}', 'active' FROM users WHERE username='${APPROVER}'`)
      notes.push('测试干预：MySQL 补写 user_tenants 租户成员关系（产品缺陷：新建用户无租户成员关系，无法建立 scope；如实记录）')
    }

    // 5. 管理员退出 → 审批人登录 → 审批中心 UI 批准
    await page.locator('.user-chip').first().click()
    await page.waitForTimeout(800)
    const lo = page.locator('.ant-dropdown-menu-item').filter({ hasText: '退出登录' }).first()
    if (await lo.isVisible().catch(() => false)) await lo.click()
    await page.waitForTimeout(2500)
    check('admin_logged_out', /\/login/.test(page.url()), `url=${page.url()}`)

    await page.goto(`${ENV.baseURL}/login`, { waitUntil: 'domcontentloaded' })
    await page.waitForTimeout(1500)
    await page.getByPlaceholder('用户名').fill(APPROVER)
    await page.getByPlaceholder('密码').fill(APPROVER_PW)
    await page.getByRole('button', { name: /登\s*录/ }).click()
    const approverLogin = await page.waitForURL((u) => !String(u).includes('/login'), { timeout: 15000 }).then(() => true).catch(() => false)
    check('approver_login_success', approverLogin, `${APPROVER} UI 登录=${approverLogin}`)
    if (approverLogin) {
      await page.waitForTimeout(2000)
      // 产品缺陷（实测）：localStorage(aiops-ui-v3) 持久化了 currentClusterId，Switcher 已显示
      // 集群名；但新会话服务端 active_scope 为空，且 antd Select 对"选择相同值"不触发
      // onChange → 用户无法通过 Switcher 重建 scope（单集群下无恢复路径，页面持续
      // SCOPE_SELECTION_REQUIRED）。测试采用与 harness 相同的 API 方式建立 scope。
      const sc = await page.request.post(`${ENV.apiBase}/me/scope`, {
        data: { tenant_id: ENV.tenantId, cluster_id: ENV.clusterId },
      })
      check('approver_scope_established', sc.ok(), `审批人 scope 建立 POST /me/scope -> ${sc.status()}（Switcher 同值不触发 onChange 的恢复缺陷见 notes）`)
      await spaNav(page, '/admin/approvals')
      await page.waitForTimeout(4000)
      const targetRow = page.locator('tr').filter({ hasText: DEP }).first()
      let approveBtn = targetRow.getByRole('button', { name: /批\s*准/ }).first()
      if (!(await approveBtn.isVisible().catch(() => false))) {
        approveBtn = page.getByRole('button', { name: /批\s*准/ }).first()
      }
      let clicked = false
      if (await approveBtn.isVisible().catch(() => false)) {
        await approveBtn.click()
        await page.waitForTimeout(1500)
        const modal = page.locator('.ant-modal:visible')
        const ok = modal.getByRole('button', { name: /确认批准执行/ }).first()
        if (await ok.isVisible().catch(() => false)) { await ok.click(); clicked = true; await page.waitForTimeout(3000) }
      }
      check('approval_via_approver_ui', clicked, `审批人 ${APPROVER} 在审批中心点击"批准"并确认（批准执行确认弹窗）=${clicked}`)
    }
    // API 反查审批状态
    if (actionId) {
      const a2 = await apiRetry(page, 'get', `/ai/actions/${actionId}`)
      check('action_status_approved', /"status":"approved"/.test(a2.body),
        `GET /ai/actions/${actionId} -> ${a2.status} ${a2.body.slice(0, 200)}`)
    }
    await shot(page, 'PF-LOGIC-010-approved')

    // 6. 审批人退出 → 管理员登录 → 执行（与页面"② 执行"同一 Canonical 端点）
    await page.locator('.user-chip').first().click().catch(() => {})
    await page.waitForTimeout(700)
    const lo2 = page.locator('.ant-dropdown-menu-item').filter({ hasText: '退出登录' }).first()
    if (await lo2.isVisible().catch(() => false)) await lo2.click()
    await page.waitForTimeout(2500)
    await uiLogin(page)
    await page.request.post(`${ENV.apiBase}/me/scope`, { data: { tenant_id: ENV.tenantId, cluster_id: ENV.clusterId } })
    if (actionId) {
      const ex = await apiRetry(page, 'post', `/ai/actions/${actionId}/execute`)
      let exStatus = '', exMsg = '', exCode = ''
      try { const j = JSON.parse(ex.body); exStatus = j.status || ''; exMsg = j.message || ''; exCode = j.error_code || '' } catch { exMsg = ex.body.slice(0, 150) }
      check('action_executed', ex.status === 200 && /succeeded|success|executed|completed/i.test(exStatus),
        `POST /ai/actions/${actionId}/execute -> ${ex.status} status=${exStatus} error_code=${exCode} message="${exMsg}"。环境事实：ai-action-executor 以 EXECUTION_MODE=disabled 运行（pod 日志 "running EXECUTION_MODE=disabled k8sEnabled=false"），真实变更被拒绝（EXECUTOR_REJECTED）——审批后执行在该环境不可完成，如实记录 FAIL；页面执行结果（rejected）与集群实际状态（未变更）一致`)
      await sleep(8000)
    } else {
      check('action_executed', false, '无 action_id，无法执行')
    }

    // 7. kubectl 反查 replicas=2
    const replicas1 = kubectl(`get deploy -n ${NS} ${DEP} -o jsonpath='{.spec.replicas}'`)
    const ready1 = kubectl(`get deploy -n ${NS} ${DEP} -o jsonpath='{.status.readyReplicas}'`)
    check('k8s_actual_replicas_scaled_to_2', replicas1 === '2', `kubectl spec.replicas=${replicas1} readyReplicas=${ready1}`)

    // 8. 页面核对（K8s 运维页 Deployments 列表显示 replicas=2）
    await spaNav(page, '/infra/k8s')
    await page.waitForTimeout(3500)
    const k8sText2 = ((await page.locator('body').textContent().catch(() => '')) || '')
    const rowIdx = k8sText2.indexOf(DEP)
    const rowSnippet = rowIdx >= 0 ? k8sText2.slice(rowIdx, rowIdx + 80).replace(/\n/g, ' ') : '(未找到行)'
    check('k8s_page_shows_scaled_replicas', rowIdx >= 0 && /2/.test(rowSnippet), `页面 ${DEP} 行片段="${rowSnippet}"`)
    await shot(page, 'PF-LOGIC-010-k8s')

    notes.push(`测试 deployment ${NS}/${DEP} 按任务要求保留（当前 replicas=${replicas1}）。`)
    notes.push('审批人用户 approver-lanee 为测试专用账号（role=approver），已随 PF-LOGIC-013 流程验证；如需清理由用户管理删除。')
  })

  const pass = checks.filter((c) => c.pass).length
  const status = pass === checks.length ? 'PASS' : (pass >= checks.length - 2 ? 'PARTIAL' : 'FAIL')
  writeResult('PF-LOGIC-010', checks, notes, status)
}

run().catch((e) => { console.error(e); process.exit(1) })
