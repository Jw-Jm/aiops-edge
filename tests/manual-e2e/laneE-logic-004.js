// PF-LOGIC-004 告警规则→告警事件逻辑。
// 策略：UI 新建必然触发的测试规则（test-laneE 前缀，threshold 类型，metric=pod_restarts、
// threshold=-1、condition='>'——K8s 指标评估返回值恒 ≥0，恒 > -1，必然触发）。
// 已知缺陷（实测确认，如实记录）：
//  a) 告警评估 Leader（query-alert-eval）仅在进程启动时从 MySQL 加载规则，API/UI 新建
//     规则不被评估——需 rollout restart 才能生效（测试干预，记录于 notes）；
//  b) /api/v1/alerts/rules/{id} 未纳入 AuthMiddleware canonical 路由白名单 →
//     规则 PUT/DELETE 一律 403 permission_denied（产品级缺陷：规则编辑/删除不可用）。
// 状态流转验证：MySQL 将阈值改为 999（不再触发）+ 重启 eval → 自动 firing→resolved。
// 清理：UI 删除（预期 403 失败，留证）→ MySQL/CH 直接清理 + eval 重启。
// Standalone: TEST_RUN_ID=nonllm-20260905 NODE_PATH=/opt/homebrew/lib/node_modules node tests/manual-e2e/laneE-logic-004.js
const { ENV, spaNav, makeCollector, shot, withSession } = require('./lib/laneD-runner')
const { writeResult, apiRetry, sh } = require('./lib/laneE')

const RULE_NAME = 'test-laneE-恒触发告警'
const SVC = 'kubernetes'
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

function chQuery(q) {
  const esc = q.replace(/"/g, '').replace(/\$/g, '\\$')
  return sh(`kubectl exec -n observability clickhouse-0 -c clickhouse -- sh -c "clickhouse-client --password \\"\\$CH_PROBE_PASSWORD\\" -q \\"${esc}\\"" 2>/dev/null`).trim()
}
function mysqlQuery(q) {
  const esc = q.replace(/"/g, '').replace(/\$/g, '\\$').replace(/`/g, '')
  return sh(`kubectl exec -n observability mysql-0 -- sh -c "mysql -uroot -p\\"\\$MYSQL_ROOT_PASSWORD\\" aiops -e \\"${esc}\\"" 2>/dev/null`).trim()
}

async function pollChEvent(maxMs) {
  const deadline = Date.now() + maxMs
  let last = ''
  while (Date.now() < deadline) {
    try {
      last = chQuery(`SELECT id,rule_id,rule_name,service,severity,value,threshold,status,tenant_id,cluster_id,timestamp FROM observability.alert_events WHERE rule_name = '${RULE_NAME}' ORDER BY timestamp DESC LIMIT 1 FORMAT TSV`)
      if (last.includes(RULE_NAME)) return last
    } catch (e) { last = String(e).slice(0, 120) }
    await sleep(20000)
  }
  return last
}

async function run() {
  const vp = ENV.viewports[0]
  const tag = `${vp.width}x${vp.height}`
  const checks = []
  const notes = []
  const check = (name, pass, detail = '') => checks.push({ name, pass: !!pass, detail: String(detail).slice(0, 500) })
  const col = makeCollector(tag)
  let ruleId = ''

  await withSession({ viewport: vp, col, seedScope: true }, async (page) => {
    // 1. UI 新建必然触发的测试规则（threshold=-1 恒触发）
    await spaNav(page, '/alerts/rules')
    await page.waitForTimeout(2500)
    await page.getByRole('button', { name: '新建规则' }).click()
    await page.waitForTimeout(1000)
    await page.getByPlaceholder('如：order-svc 错误率过高').fill(RULE_NAME)
    // exact: true —— 否则会子串匹配到规则名输入框（placeholder "如：order-svc 错误率过高"）
    await page.getByPlaceholder('如：order-svc', { exact: true }).fill(SVC)
    await page.getByPlaceholder('如：error_rate').fill('pod_restarts')
    // modal 内 InputNumber 顺序：[持续时间(默认5), 阈值] → 阈值取 nth(1)，填 -1 → 恒触发
    await page.locator('.ant-modal .ant-input-number input').nth(1).fill('-1')
    // 启用开关默认开启（ant-switch-checked），不点击，仅断言
    const swCls = await page.locator('.ant-modal .ant-switch').first().getAttribute('class').catch(() => '')
    check('rule_enabled_switch_on', /ant-switch-checked/.test(swCls || ''), `启用开关 class=${swCls}`)
    await page.locator('.ant-modal').getByRole('button', { name: /确\s*定|OK/ }).first().click()
    await page.waitForTimeout(2500)
    const rulesText = ((await page.locator('body').textContent().catch(() => '')) || '')
    check('rule_created_via_ui', rulesText.includes(RULE_NAME), `告警规则页面包含 "${RULE_NAME}"=${rulesText.includes(RULE_NAME)}`)

    // API + MySQL 反查
    const rulesResp = await apiRetry(page, 'get', '/alerts/rules')
    let rule = null
    try { rule = (JSON.parse(rulesResp.body)?.data || []).find((r) => r.name === RULE_NAME) } catch {}
    ruleId = rule?.id || ''
    check('rule_persisted_api', !!rule, `GET /alerts/rules 命中：${JSON.stringify(rule || {}).slice(0, 220)}`)
    const mysqlRow = mysqlQuery(`SELECT id,name,metric,threshold,enabled FROM alert_rules WHERE name LIKE 'test-laneE%'`)
    check('rule_persisted_mysql', /test-laneE/.test(mysqlRow), `MySQL alert_rules: ${mysqlRow.replace(/\t/g, ' | ').replace(/\n/g, ' ; ').slice(0, 220)}`)

    // 2. 等待 2 个评估周期（60s/周期）→ 预期因 Leader 不重载规则而无事件
    notes.push('等待 130s（2 个告警评估周期）观察事件产生……')
    await sleep(130000)
    let chCount = '-1'
    try { chCount = chQuery(`SELECT count() FROM observability.alert_events WHERE rule_name = '${RULE_NAME}'`) } catch {}
    let evalLog = ''
    try { evalLog = sh(`kubectl logs -n observability deploy/query-alert-eval --tail=200 2>/dev/null | grep -c "test-laneE" || true`).trim() } catch {}
    check('rule_triggers_event_without_restart', Number(chCount) > 0,
      `等待 130s 后 CH alert_events 命中=${chCount}；eval pod 日志 test-laneE 命中=${evalLog}。缺陷(a)：告警评估 Leader 仅在启动时加载规则，新建规则不被评估（如实记录 FAIL）`)

    // 3. 测试干预：重启 query-alert-eval 使其重载 MySQL 规则（记录于 notes）
    notes.push('测试干预：kubectl rollout restart deploy/query-alert-eval（使 Leader 重载 MySQL 规则；非产品代码改动）')
    try { sh('kubectl rollout restart deploy/query-alert-eval -n observability') } catch (e) { notes.push(`restart 失败: ${String(e).slice(0, 120)}`) }
    try { sh('kubectl rollout status deploy/query-alert-eval -n observability --timeout=180s') } catch {}
    // 轮询等待事件产生（Leader 租约 120s + 评估周期 60s，最长等 6 分钟）
    const chEvent = await pollChEvent(360000)
    const evFields = chEvent.split('\t')
    const evOk = chEvent.includes(RULE_NAME)
    check('real_event_generated_after_reload', evOk,
      evOk ? `重启 eval 后 CH 事件: ${chEvent.replace(/\t/g, ' | ').slice(0, 300)}` : `重启 eval 后 CH 仍无事件: ${chEvent.slice(0, 200)}`)

    // 字段一致性（CH 后端事实）
    if (evOk) {
      const [evId, evRuleId, evRuleName, evSvc, evSev, evVal, evThr, evStatus, evTenant, evCluster] = evFields
      check('event_fields_match_rule', evRuleName === RULE_NAME && evSvc === SVC && evSev === 'warning' && Number(evThr) === -1 && evStatus === 'firing',
        `rule_name=${evRuleName} service=${evSvc} severity=${evSev} value=${evVal} threshold=${evThr} status=${evStatus}（与规则配置一致）`)
      check('event_tenant_cluster_labels', evTenant === ENV.tenantId && evCluster === ENV.clusterId,
        `已知缺陷：事件 tenant_id="${evTenant}" cluster_id="${evCluster}" 均为空（CH 全部历史事件同样为空）→ 前端 /alerts/events 按 scope 过滤返回 0，事件在页面不可见（如实记录 FAIL）`)
      // API 可见性（前端视角）
      const evApi = await apiRetry(page, 'get', `/alerts/events?rule=${ruleId}&limit=50`)
      let apiTotal = -1
      try { apiTotal = JSON.parse(evApi.body)?.total } catch {}
      check('event_visible_in_alerts_page_api', apiTotal > 0,
        `GET /alerts/events?rule=${ruleId} total=${apiTotal}（CH 有记录但 API 返回 0——tenant/cluster 标签为空缺陷）`)
    }

    // 4. 状态流转：阈值改为 999（不再触发）→ 重启 eval → 自动 firing→resolved
    if (evOk && ruleId) {
      // 已知缺陷(b)：PUT /alerts/rules/{id} 被 AuthMiddleware canonical 白名单拦截 → 403
      const upd = await apiRetry(page, 'put', `/alerts/rules/${ruleId}`, {
        name: RULE_NAME, service: SVC, type: 'threshold', metric: 'pod_restarts',
        condition: '>', threshold: 999, duration: 5, severity: 'warning', enabled: true,
      })
      check('rule_update_api_available', upd.status === 200,
        `PUT /alerts/rules/${ruleId} -> ${upd.status} ${upd.body.slice(0, 140)}；缺陷(b)：/alerts/rules/{id} 未纳入 canonical 路由白名单，规则更新/删除一律 403（产品级缺陷，如实记录）`)
      // 测试干预：经 MySQL 修改阈值 + 重启 eval，验证 firing→resolved 自动流转
      notes.push('测试干预：MySQL UPDATE alert_rules SET threshold=999 + 重启 query-alert-eval（API 更新因缺陷(b)不可用），验证 firing→resolved 自动流转')
      mysqlQuery(`UPDATE alert_rules SET threshold=999 WHERE name LIKE 'test-laneE%'`)
      try { sh('kubectl rollout restart deploy/query-alert-eval -n observability') } catch {}
      try { sh('kubectl rollout status deploy/query-alert-eval -n observability --timeout=180s') } catch {}
      const deadline2 = Date.now() + 360000
      let chAfter = ''
      while (Date.now() < deadline2) {
        try {
          chAfter = chQuery(`SELECT status, resolved_at, timeline FROM observability.alert_events WHERE rule_name = '${RULE_NAME}' ORDER BY timestamp DESC LIMIT 1 FORMAT TSV`)
        } catch (e) { chAfter = String(e).slice(0, 200) }
        if (/resolved/.test(chAfter)) break
        await sleep(20000)
      }
      const resolvedOk = /resolved/.test(chAfter)
      check('event_status_transition_firing_to_resolved', resolvedOk,
        `阈值改为 999（不再触发）后：CH status/resolved_at/timeline=${chAfter.replace(/\t/g, ' | ').slice(0, 300)}；期望自动 resolved（firing→resolved 流转）`)
      const hist = chQuery(`SELECT count() FROM observability.alert_events WHERE rule_name = '${RULE_NAME}'`)
      check('event_history_queryable', Number(hist) >= 1, `CH 历史事件条数=${hist}（timeline 含状态变更历史）`)
    }

    await shot(page, 'PF-LOGIC-004-rules')

    // 5. 清理：UI 删除（预期因缺陷(b)失败，留证）→ MySQL/CH 直接清理
    if (ruleId) {
      const del = await apiRetry(page, 'delete', `/alerts/rules/${ruleId}`)
      check('rule_delete_api_available', del.status === 200 || del.status === 204,
        `DELETE /alerts/rules/${ruleId} -> ${del.status} ${del.body.slice(0, 120)}（缺陷(b) 同源证据：删除规则 API 403）`)
    }
    // UI 删除按钮路径（与 API 同一 403）
    await spaNav(page, '/alerts/rules')
    await page.waitForTimeout(2000)
    const row = page.locator('tr').filter({ hasText: RULE_NAME }).first()
    if (await row.isVisible().catch(() => false)) {
      await row.getByRole('button', { name: /删除/ }).first().click().catch(() => {})
      await page.waitForTimeout(800)
      await page.locator('.ant-popconfirm').getByRole('button', { name: /确\s*定|确认/ }).first().click().catch(() => {})
      await page.waitForTimeout(2000)
      const afterText = ((await page.locator('body').textContent().catch(() => '')) || '')
      check('rule_delete_via_ui_fails_defect', afterText.includes(RULE_NAME),
        `UI 删除后规则仍在列表（删除失败提示）=${afterText.includes(RULE_NAME)}——缺陷(b) 用户可见影响：规则无法通过产品删除`)
    }
    // 直接清理（测试数据清理要求）：MySQL 规则行 + CH 事件 + eval 重启
    mysqlQuery(`DELETE FROM alert_rules WHERE name LIKE 'test-laneE%'`)
    try { chQuery(`ALTER TABLE observability.alert_events DELETE WHERE rule_name LIKE 'test-laneE%'`) } catch {}
    try { sh('kubectl rollout restart deploy/query-alert-eval -n observability') } catch {}
    try { sh('kubectl rollout status deploy/query-alert-eval -n observability --timeout=180s') } catch {}
    await sleep(15000)
    const mysqlAfter = mysqlQuery(`SELECT count(*) FROM alert_rules WHERE name LIKE 'test-laneE%'`).split('\n').pop().trim()
    const chAfterDel = chQuery(`SELECT count() FROM observability.alert_events WHERE rule_name LIKE 'test-laneE%'`)
    check('test_rule_cleanup_verified', mysqlAfter === '0' && chAfterDel === '0',
      `清理后 MySQL alert_rules test-laneE 行数=${mysqlAfter}，CH 事件条数=${chAfterDel}（清理路径：MySQL+CH 直接删除 + eval 重启，因缺陷(b) API/UI 删除不可用）`)
  })

  const pass = checks.filter((c) => c.pass).length
  const status = pass === checks.length ? 'PASS' : (pass >= 5 ? 'PARTIAL' : 'FAIL')
  writeResult('PF-LOGIC-004', checks, notes, status)
}

run().catch((e) => { console.error(e); process.exit(1) })
