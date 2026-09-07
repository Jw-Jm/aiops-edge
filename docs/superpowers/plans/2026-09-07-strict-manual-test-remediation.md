# Strict Full-Product Test Remediation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将 `AIOps_平台全量产品功能测试执行手册_最终版_2026-09-05.md` 的 87 项严格测试从当前 45 PASS / 14 FAIL / 8 PARTIAL / 20 BLOCKED 修复到 87 PASS，并保证测试结论可重复、运行态镜像与 Git 提交完全一致。

**Architecture:** 修复分为六条收敛链：先修复测试清单和错误判据，再通过真实 OTLP 入口补齐新鲜观测数据；修正历史 Run 的可选 Graph Context 契约与改密错误体验；把现有但未接线的 canonical ClusterRegistrar 接入产品 API 并纳管 `kind-aiops-kind-02`；最后以真实 LLM Egress Proxy、单 Worker 和串行共享证据链完成 AI/报告/知识测试。所有测试数据都从正式产品入口产生，不使用 Mock、Fixture 或直接写库冒充闭环。

**Tech Stack:** Go 1.26、React 18/TypeScript/Vite/Vitest、Node.js/Playwright、Python/FastAPI/Pytest、Helm/Kubernetes/OrbStack、MySQL、ClickHouse、VictoriaMetrics、VictoriaLogs、HugeGraph。

**Spec:** `AIOps_平台全量产品功能测试执行手册_最终版_2026-09-05.md`；基线证据为 `test-results/strict-20260907T20260907071045/strict-summary.json`。

## Global Constraints

- 测试范围严格等于手册 87 项；最终门禁只能从逐项结果计算，任何 FAIL、PARTIAL、BLOCKED、ERROR、缺项或重复项都必须使 `strict_go=false`。
- `FAIL` 只用于“必需前置条件已经满足，但产品行为违反手册”的情形；手册要求的真实观测对象不存在时，相关完整用例必须记为 `BLOCKED`，并记录 `blocker_code=TELEMETRY_PRECONDITION_UNMET`，不得记为产品 FAIL，也不得用 PARTIAL 掩盖阻塞。
- Empty 只能在手册明确把“无数据 Empty 正常”列为验收条目、且该用例其余必测项都已执行时判 PASS。PF-PAGE-003 和 PF-PAGE-006 仍要求真实趋势图、Trace 详情和 Span 瀑布，因此最近 24 小时无数据时，即使页面空态显示正确，这两个完整用例也必须保持 BLOCKED。
- 本轮真实环境仅限本机 OrbStack；`kind-aiops-kind-02` 专用于 AIOps 多集群纳管验证，不在其中部署整套 AIOps 控制面。
- 不得用 Mock、Fixture、前端假数据、直接 INSERT/UPDATE 数据库或修改结果 JSON 代替产品链路；允许通过正式 OTLP、页面和公开 API 创建带唯一测试标记的真实测试数据。
- K8s 写操作仅允许 `aiops-action-test` 和手册指定的测试命名空间；每个动作测试必须记录变更前状态，并在结束后恢复。
- 资源受限：所有浏览器、AI、镜像构建和数据准备步骤串行运行；自研中心服务保持单副本；DeepFlow、KubeVirt、IPMI 不为本轮结论额外启动。
- 外部 LLM 凭据只存在 `aiops-secrets`/LLM Egress Proxy 中，不写入仓库、测试结果、命令日志或浏览器；无法通过真实 Provider 连通性预检时必须保持 BLOCKED。
- 所有新增 API 都必须沿用 HttpOnly Session、canonical tenant/cluster scope、MySQL 权威角色和 `credential_ref -> Secret -> kube-system UID` 边界。
- 任何源码修复后都必须先提交，再使用 `git-$(git rev-parse --short=12 HEAD)` 构建全部自研镜像；不得用脏工作区构建候选镜像。
- 最终按用户要求在本地 `main` 直接提交并通过已配置的 HTTP remote 执行普通 `git push origin main`；禁止 force push。若 GitHub 分支保护拒绝直接推送，必须如实报告，不能声称已更新 GitHub。

---

## Root-Cause Findings

### 1. 当前统计文件自身不一致

- `strict-summary.json.counts` 写为 `PASS=44, PARTIAL=9`，但逐项 `categories[].items[]` 重新统计为 `PASS=45, PARTIAL=8`；FAIL=14、BLOCKED=20 一致。
- 这说明本轮汇总不是由唯一明细源原子计算，当前总数虽然都是 87，但分类计数不可作为发布证据。

### 2. 六项 FAIL 与两项 PARTIAL 被错误归因给产品，实际是“最近 24 小时无真实观测数据”的环境前置条件阻塞

- ClickHouse 现有 `trace_spans` 最新时间为 2026-09-05，而测试执行时间为 2026-09-07；`trace_spans` 虽有 220 行，但最近 24 小时为 0，`trace_summary_state`、`trace_summary_index` 和日志最近 24 小时同样为 0。
- `Overview/index.tsx` 只有在 `stats.trend.length > 0` 时创建 ECharts canvas；`Trace.tsx` 默认请求 24 小时数据；Query API 的 `HourlyTrend`、`ServiceREDStats`、`ListServices` 和 `FindTraces` 都按当前时间窗查询。因此这是测试前置数据陈旧，不是 ECharts 渲染故障。
- 产品层应继续显示真实 Empty，不能伪造数据、自动扩大时间窗或静默回退到七天历史；测试层必须把“空态渲染是否正确”和“手册完整功能是否可验收”分开。空态正确只证明页面没有白屏/报错，不足以证明 PF-PAGE-003 的趋势图或 PF-PAGE-006 的 Trace Drawer、Span 瀑布已经通过。
- 因此这八项在该次运行中的规范化诊断状态都应为 `BLOCKED/TELEMETRY_PRECONDITION_UNMET`，而不是 FAIL 或 PARTIAL。若只修正分类、不重新执行，明细诊断计数应为 45 PASS / 8 FAIL / 6 PARTIAL / 28 BLOCKED；这不是新的正式测试结果，`strict_go` 仍为 false。
- 受影响：PF-PAGE-003、PF-PAGE-006、PF-LOGIC-003、PF-DATA-001、PF-DATA-002、PF-DATA-003、PF-BE-004、PF-FLOW-001。

### 3. 七项 FAIL 与一项 PARTIAL 是测试判据/接口契约漂移

- PF-UI-003：截图中三条通知真实可见；脚本已把 `panel` 定位到 `.ant-dropdown`，随后又在该节点内部查询另一个 `.ant-dropdown`，所以 `itemCount` 恒为 0。
- PF-LOGIC-012：GET/POST `/ops/changes` 已返回 200，页面登记也成功；脚本仍把“必须 404”写成通过条件。
- PF-LOGIC-015：无显式 `cluster_id` 的 typed graph search 已借助 active session scope 返回 200；脚本仍把预期 503 的旧缺陷作为通过条件，说明和检查名也已过时。
- PF-DATA-008：产品容量接口只接受 `cpu|memory|disk|network`，失败探针使用旧指标 `node_cpu_usage`。手册允许无历史时展示 Empty，因此正确条件是合法参数返回 200 且页面/API 空态一致。
- PF-DATA-011、PF-BE-001：审批中心已使用 canonical `/ai/actions`，探针仍请求 legacy `/ops/tasks`。
- PF-DATA-012、PF-BE-005：Graph 产品代码使用 `/ai/kg/health`、typed entity/search/neighbors/path 和 `/services/map`；探针仍请求已淘汰的聚合路由 `/ai/kg/graph`。

### 4. PF-PAGE-013 是可选 Graph Context 被错误建模为 404

- 历史 Run 存在，但没有 `ai_run_graph_contexts` 行；`RunGraphContext` 将 `sql.ErrNoRows` 映射为 `ENTITY_NOT_FOUND`/404。
- 调查详情页已经把 Graph Context 当可选投影并捕获异常，但浏览器仍记录未解释 404。对“Run 存在、投影尚未生成”应返回 200 的显式 partial/empty 状态；只有 Run 本身不存在才返回 404。

### 5. 三项 PARTIAL 的多集群阻塞来自产品接线缺口，不是机器上没有第二集群

- 本机已有 `kind-aiops-kind-02`，但产品 `/clusters` 只暴露一个 canonical cluster。
- `k8sboundary.ClusterRegistrar` 已实现真实 UID 探测和重复物理集群防护，却没有接入 HTTP Handler。
- `/api/v1/clusters` 精确路由当前固定调用 `ClusterList`，所以前端 POST `/clusters` 不会进入 `clusterCreate`；前端仍要求浏览器提交原始 kubeconfig，和 `credential_ref` 安全边界不一致。
- Query API 的 Secret RBAC 只允许一个 `localCredentialSecretName`，无法读取第二个纳管集群凭据 Secret。
- 受影响：PF-LOGIC-002、PF-BE-010、PF-FLOW-007；PF-DATA-011 的 Cluster 核对也必须在该任务后复验。

### 6. 二十项 BLOCKED 与知识后端 PARTIAL 的共同前置是“真实 AI 链路未开启”

- 当前 `ai-orchestrator` 为 `LLM_MOCK=true, INVESTIGATION_RUNTIME_ENABLED=0`；独立 Worker 虽启用 Investigation runtime，但同样是 `LLM_MOCK=true`。
- Executor 实际为 `EXECUTION_MODE=approved`，PF-LOGIC-010 已完成 Proposal、职责分离审批、真实扩容和恢复，因此 Executor 本身不是本轮 AI 阻塞根因。
- 需要真实 LLM Provider、真实 telemetry/alert、durable Run/Tool/Evidence、报告和知识写入形成一次共享但可追溯的产品闭环。
- 受影响：PF-LOGIC-005～009、PF-LOGIC-011、PF-AI-001～010、PF-BE-006、PF-FLOW-002～004、PF-FLOW-008。

### 7. PF-LOGIC-001 是错误文案判据与产品体验问题

- 后端错误当前密码已正确返回 400 `invalid_current_password`，前端也没有被强制登出；失败仅因脚本不识别下划线错误码。
- 页面直接显示内部错误码，不符合用户可读体验。应由前端稳定映射为“当前密码不正确”，测试再验证错误可见且会话保持。

---

## Task 1: Make the 87-Case Result Gate Trustworthy

**Files:**

- Create: `tests/manual-e2e/strict/manifest.js`
- Create: `tests/manual-e2e/strict/result-policy.js`
- Create: `tests/manual-e2e/strict/result-policy.test.js`
- Create: `tests/manual-e2e/strict/summarize.js`
- Create: `tests/manual-e2e/strict/summarize.test.js`
- Create: `tests/manual-e2e/strict/run.js`
- Modify: `tests/manual-e2e/lib/harness.js`
- Modify: `tests/manual-e2e/lib/laneA.js`

- [ ] **Step 1: Write a failing summary invariant test**

  Build a fixture with the current mismatch (`counts.PASS=44` while item list has 45) and assert that the summarizer discards supplied counts, recomputes from items, rejects duplicate/missing IDs, and returns exactly 87 unique cases.

  Add result-policy fixtures proving these rules:

  - a satisfied prerequisite plus a violated assertion is FAIL;
  - `telemetry.ready=false` maps every telemetry-dependent case to `status=BLOCKED` and `blocker_code=TELEMETRY_PRECONDITION_UNMET`;
  - a correctly rendered Empty component is retained as evidence but cannot turn PF-PAGE-003 or PF-PAGE-006 into PASS;
  - a case that explicitly permits Empty may PASS only after all its remaining assertions pass;
  - applying the policy to the eight stale-data outcomes yields 45 PASS / 8 FAIL / 6 PARTIAL / 28 BLOCKED without changing the total of 87.

  Run: `node --test tests/manual-e2e/strict/summarize.test.js tests/manual-e2e/strict/result-policy.test.js`

  Expected: FAIL because the source-controlled strict summarizer does not exist yet.

- [ ] **Step 2: Add the immutable 87-case manifest**

  Define every documented ID, category, source runner and whether it requires telemetry, two clusters, real LLM, Action execution or knowledge persistence. For Empty-capable cases, record the exact manual clause that permits Empty; do not infer permission from the current UI. Do not derive the expected set from files discovered at runtime.

- [ ] **Step 3: Implement single-source summary calculation**

  `summarize.js` must:

  - load one final result per manifest ID;
  - accept only `PASS|FAIL|PARTIAL|BLOCKED|ERROR`; every BLOCKED result must carry a stable `blocker_code` and evidence;
  - reject unknown, duplicate and missing IDs;
  - compute counts only from final items;
  - require `PASS` for all 87 items before `strict_go=true`;
  - write the summary atomically through a temporary file and rename;
  - include Git SHA, image tag, execution start/end, manual document digest and environment preflight digest.

- [ ] **Step 4: Make artifact paths consistent**

  Remove the dual `test-results/` versus `tests/test-results/` behavior and the fixed `/tmp/laneA-summary-backup.jsonl` fallback. Every lane must write only under `test-results/<TEST_RUN_ID>/`, with the orchestrator creating the directory once before serial execution.

- [ ] **Step 5: Implement the serial strict runner**

  `run.js` runs environment preflight, telemetry preparation, non-AI lanes, one shared AI chain, backend cross-checks and final summarization in that order. If telemetry preparation fails, it still captures the visible Empty behavior and runs cases that do not depend on telemetry, but emits exactly one final BLOCKED result for each telemetry-dependent manifest ID with the shared prerequisite evidence. A lane process exit, malformed JSON or absent result is `ERROR`, never an implicit PASS.

- [ ] **Step 6: Verify the gate unit tests**

  Run: `node --test tests/manual-e2e/strict/summarize.test.js tests/manual-e2e/strict/result-policy.test.js`

  Expected: PASS, including raw-count reconstruction at 45 PASS / 14 FAIL / 8 PARTIAL / 20 BLOCKED and explicit stale-data policy normalization at 45 PASS / 8 FAIL / 6 PARTIAL / 28 BLOCKED.

- [ ] **Step 7: Commit the test-gate foundation**

  ```bash
  git add tests/manual-e2e/strict tests/manual-e2e/lib/harness.js tests/manual-e2e/lib/laneA.js
  git commit -m "test: make strict 87-case gate deterministic"
  ```

---

## Task 2: Correct Stale Test Oracles and Retired API Probes

**Files:**

- Modify: `observability-frontend/src/App.tsx`
- Modify: `observability-frontend/src/App.test.tsx`
- Modify: `observability-frontend/src/api/client.ts`
- Modify: `observability-frontend/src/api/client.test.ts`
- Modify: `observability-frontend/src/api/knowledgeGraph.test.ts`
- Modify: `tests/manual-e2e/laneD-003-alerts.js`
- Modify: `tests/manual-e2e/laneE-logic-012.js`
- Modify: `tests/manual-e2e/laneE-logic-015.js`
- Create: `tests/manual-e2e/strict/page-data.js`
- Create: `tests/manual-e2e/strict/backend.js`

- [ ] **Step 1: Add failing frontend contract tests**

  Assert that notification entries expose a stable `data-testid="notification-alert-item"`; canonical approval reads use `/ai/actions`; Graph client exports contain no `/ai/kg/graph`; capacity test requests use `metric=cpu`.

  Run: `cd observability-frontend && npm run test:run -- src/App.test.tsx src/api/client.test.ts src/api/knowledgeGraph.test.ts`

  Expected: FAIL before implementation.

- [ ] **Step 2: Give notification entries a stable semantic locator**

  Add `data-testid="notification-alert-item"` to each rendered notification entry. Update PF-UI-003 to count that locator directly and assert the rendered entries equal `min(activeEvents.length, 6)`, while the Badge continues to compare expanded object rows.

- [ ] **Step 3: Rewrite PF-LOGIC-012 around the current product contract**

  Use a unique `strict-<TEST_RUN_ID>` service/content marker; require POST `/ops/changes` 200, GET list contains the same ID/marker, page shows the record, timestamp is within the execution window, and MySQL `change_events` matches the public API. Remove every 404 expectation and stale “route not implemented” note.

- [ ] **Step 4: Rewrite PF-LOGIC-015 around typed Graph behavior**

  Require graph health 200, scoped and active-session entity search 200, identity labels equal the active tenant/cluster, Graph Ops counts equal backend values, and service panorama warnings accurately reflect only real degradation. Remove the hard-coded `graph_search_without_cluster_id_503_defect` false check.

- [ ] **Step 5: Persist page-data and backend probes in source control**

  `page-data.js` must implement PF-DATA-001～012 and `backend.js` PF-BE-001～010 using the manual's public contracts. For the currently wrong checks:

  - PF-DATA-008: `/capacity/forecast?metric=cpu`; a 200 empty history passes only when the Capacity page shows Empty and response arrays are internally consistent.
  - PF-DATA-011/PF-BE-001: `/ai/actions`, plus `ai_actions`/approval tables for three-way comparison; never `/ops/tasks`.
  - PF-DATA-012/PF-BE-005: `/ai/kg/health`, typed search, neighbors/path when entities exist, `/services/map`, and Graph Ops APIs; never `/ai/kg/graph`.

- [ ] **Step 6: Remove unused retired frontend API exports**

  Delete unused `getKgGraph`/`KgGraph` aggregate DTOs and legacy `listApprovalTasks`/`approveTask`/`rejectTask` exports so future tests cannot accidentally rediscover retired routes.

- [ ] **Step 7: Run focused frontend and static contract tests**

  ```bash
  cd observability-frontend
  npm run test:run -- src/App.test.tsx src/api/client.test.ts src/api/knowledgeGraph.test.ts src/pages/admin/Approvals.test.tsx
  npm run build
  ```

  Expected: PASS and repository search finds no production consumer of `/ai/kg/graph` or `/ops/tasks`.

- [ ] **Step 8: Commit oracle repairs**

  ```bash
  git add observability-frontend/src tests/manual-e2e
  git commit -m "test: align strict probes with product contracts"
  ```

---

## Task 3: Create Fresh Real Telemetry Through the Production Ingest Path

**Files:**

- Modify: `ai-apm-ingest-go/tools/loadgen/main.go`
- Create: `ai-apm-ingest-go/tools/loadgen/main_test.go`
- Modify: `ai-apm-ingest-go/Dockerfile`
- Create: `deploy/scripts/prepare-strict-manual-test.sh`
- Create: `deploy/scripts/test-strict-telemetry-contract.sh`
- Modify: `tests/manual-e2e/strict/run.js`
- Create: `tests/manual-e2e/strict/telemetry-cases.js`
- Create: `tests/manual-e2e/strict/telemetry-cases.test.js`
- Modify: `tests/manual-e2e/laneA-page-003.js`
- Modify: `tests/manual-e2e/laneA-page-006.js`
- Modify: `tests/manual-e2e/laneE-logic-003.js`
- Modify: `tests/manual-e2e/laneG-flow-001.js`

- [ ] **Step 1: Write failing load-generator tests**

  Assert one deterministic `payments -> orders` trace contains unique span IDs, one root, one real child whose `parentSpanId` equals the root span ID, both `service.name` resources, valid timestamps, mixed success/error spans and correlated logs. Add an mTLS client-construction test using CA/cert/key files.

  Run: `cd ai-apm-ingest-go && go test ./tools/loadgen -run 'TestStrict|TestMTLS' -count=1`

  Expected: FAIL because the current generator emits one unrelated SERVER span per trace and has no mTLS client.

- [ ] **Step 2: Extend loadgen without adding a permanent workload**

  Add explicit strict mode that emits low-volume parent/child traces for `payments` and `orders`, a unique run marker on spans/logs, `--rounds`, and standard mTLS env/flags. Read the API key from `LOADGEN_API_KEY`, falling back to the existing in-pod `INGEST_API_KEY`; never print the key.

- [ ] **Step 3: Package the helper in the existing ingest image**

  Build `/loadgen` in the existing multi-stage Dockerfile and copy it beside `/ingest`. Do not deploy a second long-running generator or image.

- [ ] **Step 4: Implement a low-resource preparation script**

  `prepare-strict-manual-test.sh` first records whether the current 24-hour window is empty, then executes `/loadgen` once inside the single existing ingest Pod, using its mounted mTLS files and Secret-provided API key. It must then poll only public Query APIs until all of these are true for the active canonical cluster:

  - dashboard trend has at least one point;
  - `/services` contains `payments` and `orders`;
  - `/traces?service=payments&hours=24` returns the generated trace;
  - trace detail contains a valid parent/child relation;
  - logs filtered by `service_name=payments` contain the marker;
  - `/services/map` contains the `payments -> orders` dependency.

  Timeout after 120 seconds with a non-zero exit and a diagnostic count. The runner converts that outcome into `BLOCKED/TELEMETRY_PRECONDITION_UNMET` for the eight dependent cases; it must not emit cascading FAIL/PARTIAL results. Do not backdate records or write ClickHouse/Victoria databases directly.

- [ ] **Step 5: Add a deployment contract test**

  Verify the image contains `/loadgen`, strict mode is opt-in, no Helm Deployment/CronJob enables it by default, and the preparation script never reads or logs provider secrets.

- [ ] **Step 6: Update affected test assertions**

  Use `telemetry-cases.js` as the single shared evidence reader and apply these exact rules:

  - PF-PAGE-003: when the initial API response is empty, verify the intentional Empty component and absence of browser errors, retain that as diagnostic evidence, then mark the complete case BLOCKED because the required live trend chart and cluster-driven data change cannot be exercised. After preparation succeeds, require non-empty API trend points, an ECharts canvas and matching card/trend values.
  - PF-PAGE-006: when the initial 24-hour query is empty, verify the intentional Empty component and working filters, retain diagnostic evidence, then mark the complete case BLOCKED because Trace Drawer, Span waterfall and context cannot be exercised. After preparation succeeds, require the generated trace and execute every documented filter/detail interaction.
  - PF-LOGIC-003、PF-DATA-001、PF-DATA-002 和 PF-FLOW-001: require the same fresh `payments` identity and active canonical cluster from page through public API; an absent marker is BLOCKED, while mismatched identity after the marker exists is FAIL.
  - PF-DATA-003 和 PF-BE-004: validate the same generated trace and parent/child span relation against page, public API and ClickHouse; do not use a seven-day fallback record.
  - All eight cases consume one immutable telemetry-preparation evidence object. Missing preparation produces eight BLOCKED results referencing that object, never eight independent product failures.

- [ ] **Step 7: Run focused tests**

  ```bash
  cd ai-apm-ingest-go
  go test ./... -count=1
  cd ..
  node --test tests/manual-e2e/strict/telemetry-cases.test.js
  bash deploy/scripts/test-strict-telemetry-contract.sh
  ```

  Expected: PASS.

- [ ] **Step 8: Commit the telemetry precondition**

  ```bash
  git add ai-apm-ingest-go deploy/scripts tests/manual-e2e
  git commit -m "test: generate fresh scoped telemetry for strict runs"
  ```

---

## Task 4: Return an Explicit Empty Graph Context for Existing Historical Runs

**Files:**

- Modify: `ai-apm-query-go/internal/api/run_graph_context.go`
- Create: `ai-apm-query-go/internal/api/run_graph_context_test.go`
- Modify: `observability-frontend/src/pages/investigation/IntelligentInvestigation.tsx`
- Create: `observability-frontend/src/pages/investigation/IntelligentInvestigation.test.tsx`
- Modify: `tests/manual-e2e/laneA-page-013.js`

- [ ] **Step 1: Write failing Query API contract tests**

  Cover three distinct cases:

  - nonexistent Run -> 404 `ENTITY_NOT_FOUND`;
  - Run outside tenant -> 403 `GRAPH_SCOPE_DENIED`;
  - existing Run with no graph context row -> 200 with `run_id`, `context_version: 0`, `vertices: []`, `edges: []`, `partial: true`, and `warning_codes: ["GRAPH_CONTEXT_NOT_AVAILABLE"]`.

  Run: `cd ai-apm-query-go && go test ./internal/api -run TestRunGraphContext -count=1`

  Expected: FAIL on the third case.

- [ ] **Step 2: Implement the empty projection contract**

  Change only `sql.ErrNoRows` from the graph-context DAO to the 200 partial response. Keep malformed stored JSON and backend failures as 503; keep missing Run as 404.

- [ ] **Step 3: Render the partial state explicitly in the frontend**

  Treat the 200 empty response as “该历史调查未生成图上下文” rather than a network failure. Continue rendering the rest of the durable Run detail.

- [ ] **Step 4: Strengthen PF-PAGE-013**

  After entering a historical Run, require all investigation APIs to be explained 2xx responses, assert either a real graph or the explicit partial warning, and reject unexplained browser network errors.

- [ ] **Step 5: Run focused tests**

  ```bash
  cd ai-apm-query-go && go test ./internal/api -run TestRunGraphContext -count=1
  cd ../observability-frontend && npm run test:run -- src/pages/investigation/IntelligentInvestigation.test.tsx
  ```

  Expected: PASS.

- [ ] **Step 6: Commit the contract repair**

  ```bash
  git add ai-apm-query-go observability-frontend tests/manual-e2e/laneA-page-013.js
  git commit -m "fix: expose missing run graph context as partial data"
  ```

---

## Task 5: Make Wrong-Current-Password Feedback User-Readable

**Files:**

- Modify: `observability-frontend/src/pages/ChangePassword/index.tsx`
- Modify: `observability-frontend/src/pages/ChangePassword/ChangePassword.test.tsx`
- Modify: `tests/manual-e2e/laneE-logic-001.js`

- [ ] **Step 1: Write a failing frontend test**

  Mock a 400 `{error: "invalid_current_password"}` and require “当前密码不正确” to be shown while navigation and logout are not called.

  Run: `cd observability-frontend && npm run test:run -- src/pages/ChangePassword/ChangePassword.test.tsx`

  Expected: FAIL because the raw error code is currently shown.

- [ ] **Step 2: Add a bounded error-code mapping**

  Map known password errors to Chinese messages; keep an opaque generic fallback. Do not expose backend internals and do not change the correct backend status 400.

- [ ] **Step 3: Correct the E2E oracle and stale notes**

  PF-LOGIC-001 must require the mapped message, URL still `/change-password`, authenticated `/me` still succeeds, and original password remains valid. Remove obsolete claims about refresh token loss, 401 forced logout and missing `/auth/logout`, because current checks already disprove them.

- [ ] **Step 4: Run tests and commit**

  ```bash
  cd observability-frontend
  npm run test:run -- src/pages/ChangePassword/ChangePassword.test.tsx
  cd ..
  git add observability-frontend/src/pages/ChangePassword tests/manual-e2e/laneE-logic-001.js
  git commit -m "fix: present change-password validation errors clearly"
  ```

---

## Task 6: Wire Canonical Multi-Cluster Registration and Register kind-aiops-kind-02

**Files:**

- Modify: `ai-apm-query-go/internal/api/handler.go`
- Modify: `ai-apm-query-go/internal/api/clusters.go`
- Create: `ai-apm-query-go/internal/api/clusters_registration_test.go`
- Modify: `ai-apm-query-go/internal/bootstrap/http.go`
- Modify: `ai-apm-query-go/internal/store/clusters.go`
- Modify: `ai-apm-query-go/internal/store/clusters_registry_test.go`
- Modify: `observability-frontend/src/api/client.ts`
- Modify: `observability-frontend/src/pages/admin/AdminSettings.tsx`
- Modify: `observability-frontend/src/pages/admin/AdminSettings.test.tsx`
- Modify: `deploy/helm/aiops/values.yaml`
- Modify: `deploy/helm/aiops/values-local-validation.yaml`
- Modify: `deploy/helm/aiops/templates/query-api/rbac.yaml`
- Create: `deploy/scripts/register-local-managed-cluster.sh`
- Create: `deploy/scripts/test-managed-cluster-registration-contract.sh`
- Modify: `tests/manual-e2e/laneE-logic-002.js`
- Modify: `tests/manual-e2e/laneG-flow-007.js`
- Modify: `tests/manual-e2e/strict/backend.js`

- [ ] **Step 1: Write failing HTTP registration tests**

  Require:

  - GET `/api/v1/clusters` remains available to authorized readers and is tenant filtered;
  - POST is admin-only and accepts `slug`, `name`, `environment`, `region`, `credential_ref`, `type`, `capabilities`, `labels`;
  - raw `kubeconfig` in a browser request is rejected;
  - the handler resolves the Secret, probes the target `kube-system` UID, persists a generated canonical UUID and tenant ownership, and rejects duplicate active physical cluster identities;
  - cluster node/namespace/event reads use the identity-validated boundary rather than the legacy raw `kubeconfig` column.

  Run: `cd ai-apm-query-go && go test ./internal/api ./internal/k8sboundary ./internal/store -run 'TestCluster|TestRegistration' -count=1`

  Expected: FAIL because the registrar is not HTTP-wired and exact POST `/clusters` currently calls `ClusterList`.

- [ ] **Step 2: Wire one collection route for GET and POST**

  Route exact `/api/v1/clusters` through `RequireRoleForWrite("admin", ClusterRouter)`. Store a production `ClusterRegistrar` and `ClusterClientManager` on `Handler`; use the same manager for detail reads. Keep response DTOs free of raw kubeconfig and provider credentials.

- [ ] **Step 3: Make tenant ownership authoritative**

  Add a tenant-scoped list query based on `tenant_clusters`; stop filtering by display name. Use canonical `cluster_id` for detail APIs while preserving a temporary numeric read compatibility path only if existing unit tests require it.

- [ ] **Step 4: Change the admin UI to credential references**

  Replace the raw kubeconfig textarea with a `credential_ref` field/help text. After successful registration, reload clusters and expose the returned canonical ID. Update client types and unit tests.

- [ ] **Step 5: Allow an explicit list of credential Secrets**

  Introduce `queryApi.credentialSecretNames` and render an RBAC `resourceNames` list. The local validation profile must include both `aiops-local-cluster-kubeconfig` and `aiops-managed-aiops-kind-02-kubeconfig`; the default remains empty/fail-closed.

- [ ] **Step 6: Implement safe local registration automation**

  `register-local-managed-cluster.sh` must:

  - verify the exact context `kind-aiops-kind-02` exists;
  - obtain that real kubeconfig from kind;
  - rewrite only the API server host for reachability from the OrbStack management cluster while retaining certificate verification through an explicit TLS server name;
  - prove the rewritten kubeconfig can read the target `kube-system` UID before creating the Secret;
  - create/update only `observability/aiops-managed-aiops-kind-02-kubeconfig`;
  - log in through the public product API, POST a `credential_ref`, and read back the canonical cluster/tenant binding;
  - remain idempotent and never print kubeconfig, tokens, certificates or passwords.

- [ ] **Step 7: Rewrite multi-cluster tests to require two real clusters**

  PF-LOGIC-002, PF-BE-010 and PF-FLOW-007 must select cluster A and B via UI, verify `/me.active_scope`, nodes/namespaces/events against each real Kubernetes API, and prove service/Trace/alert/investigation/K8s requests never leak A data into B. Empty observability data on B is acceptable only when API, UI and backend all agree and no A identity appears.

- [ ] **Step 8: Run focused tests**

  ```bash
  cd ai-apm-query-go
  go test ./internal/api ./internal/k8sboundary ./internal/store -run 'TestCluster|TestRegistration' -count=1
  cd ../observability-frontend
  npm run test:run -- src/pages/admin/AdminSettings.test.tsx src/api/client.test.ts
  cd ..
  bash deploy/scripts/test-managed-cluster-registration-contract.sh
  ```

  Expected: PASS.

- [ ] **Step 9: Commit canonical cluster registration**

  ```bash
  git add ai-apm-query-go observability-frontend deploy tests/manual-e2e
  git commit -m "feat: wire canonical managed-cluster registration"
  ```

---

## Task 7: Enable a Resource-Bounded Real AI Chain and Persist Every Missing Test

**Files:**

- Create: `deploy/helm/aiops/values-local-strict-test.yaml`
- Create: `deploy/scripts/preflight-strict-manual-test.sh`
- Create: `deploy/scripts/test-local-strict-profile.sh`
- Create: `tests/manual-e2e/lib/strict-chain.js`
- Create: `tests/manual-e2e/laneE-logic-005.js`
- Create: `tests/manual-e2e/laneE-logic-006.js`
- Create: `tests/manual-e2e/laneE-logic-007.js`
- Create: `tests/manual-e2e/laneE-logic-008.js`
- Create: `tests/manual-e2e/laneE-logic-009.js`
- Create: `tests/manual-e2e/laneE-logic-011.js`
- Create: `tests/manual-e2e/laneF-ai-001.js`
- Create: `tests/manual-e2e/laneF-ai-002.js`
- Create: `tests/manual-e2e/laneF-ai-003.js`
- Create: `tests/manual-e2e/laneF-ai-004.js`
- Create: `tests/manual-e2e/laneF-ai-005.js`
- Create: `tests/manual-e2e/laneF-ai-006.js`
- Create: `tests/manual-e2e/laneF-ai-007.js`
- Create: `tests/manual-e2e/laneF-ai-008.js`
- Create: `tests/manual-e2e/laneF-ai-009.js`
- Create: `tests/manual-e2e/laneF-ai-010.js`
- Create: `tests/manual-e2e/laneG-flow-002.js`
- Create: `tests/manual-e2e/laneG-flow-003.js`
- Create: `tests/manual-e2e/laneG-flow-004.js`
- Create: `tests/manual-e2e/laneG-flow-008.js`
- Modify: `tests/manual-e2e/strict/backend.js`
- Modify: `tests/manual-e2e/strict/run.js`

- [ ] **Step 1: Write a failing Helm profile contract test**

  Require the strict overlay to render:

  - `ai-orchestrator`: `LLM_MOCK=false`, gateway Investigation runtime remains 0;
  - one `ai-investigation-worker`: `INVESTIGATION_RUNTIME_ENABLED=1`, `LLM_MOCK=false`;
  - one LLM Egress Proxy with provider key only on the proxy;
  - one approved local Action Executor limited to the test namespaces;
  - DeepFlow disabled;
  - no extra long-running load generator;
  - functional-test resource limits suitable for a single-node OrbStack run.

  Run: `bash deploy/scripts/test-local-strict-profile.sh`

  Expected: FAIL because the overlay does not exist.

- [ ] **Step 2: Add the strict local overlay**

  Layer on `values-local-validation.yaml`; set only the real-AI switches and functional-test resource limits. Do not change production defaults and do not replace provider failures with deterministic responses.

- [ ] **Step 3: Add a fail-closed preflight**

  `preflight-strict-manual-test.sh` must verify, without exposing secret values:

  - clean Git tree and one expected `git-<sha>` image tag across all self-owned Pods;
  - all required Pods Ready and no recent CrashLoop/OOMKill;
  - two canonical product clusters including `kind-aiops-kind-02`;
  - fresh telemetry marker and typed Graph health;
  - LLM settings `configured=true`, proxy `ready=true`, both AI processes `LLM_MOCK=false`, Worker runtime enabled;
  - one real provider connection test succeeds;
  - `aiops-action-test` target exists and Executor is `approved`;
  - knowledge backend read/write health is available.

  Any failure exits non-zero and records a BLOCKED reason; it must not modify result statuses.

- [ ] **Step 4: Implement one shared real evidence ledger**

  `strict-chain.js` creates one unique run marker and records only IDs/timestamps returned by real product operations: tenant, cluster, service, alert rule/event, chat session/turns, Run, ToolRun, Evidence, Graph Context, Action, Approval, Verification, Report and Knowledge entry. It may cache these identifiers under the current test-results directory to avoid repeating expensive LLM work, but every consumer must re-read the corresponding public API/backend fact before passing.

- [ ] **Step 5: Execute the minimum real AI work serially**

  Use the fresh `payments -> orders` telemetry and one controlled test alert to perform:

  1. a two-turn Chat that invokes real metrics/logs/trace/K8s/Graph tools;
  2. one durable Investigation that reaches terminal and produces ToolRun, Evidence, RCA and Graph Context;
  3. one bounded remediation proposal against a real deployment in `aiops-action-test`, with separate approver decision, execution, kubectl verification and restoration;
  4. one report generated from that Run, previewed/downloaded, added to knowledge and found by a later AI query.

  Reuse this chain as evidence, but never mark a test PASS merely because a ledger ID exists.

- [ ] **Step 6: Implement all currently absent logic/AI/flow runners**

  Each new file maps exactly to the corresponding manual section and emits detailed per-check evidence. The RCA test must accept `insufficient_evidence` only when missing/contradicting evidence is explicitly identified and the LLM does not overstate confidence; it must not require a fabricated confirmed root cause.

- [ ] **Step 7: Complete knowledge backend verification**

  PF-BE-006 must compare the created knowledge entry through the product API and the authoritative knowledge backend, verify tenant/cluster/source/report identity, and ensure the follow-up Chat cites that exact item. This closes PF-LOGIC-011, PF-AI-010 and PF-FLOW-008 with the same real artifact.

- [ ] **Step 8: Verify profile and non-network AI tests**

  ```bash
  bash deploy/scripts/test-local-strict-profile.sh
  cd ai-orchestrator
  python3 -m pytest tests -q
  ```

  Expected: PASS. Real provider calls are reserved for the deployed E2E stage, not unit tests.

- [ ] **Step 9: Commit the strict AI chain**

  ```bash
  git add deploy tests/manual-e2e
  git commit -m "test: cover the real AI report and knowledge chain"
  ```

---

## Task 8: Run the Full Source Test Matrix Before Building Images

**Files:**

- Modify if needed: `Makefile`
- Modify if needed: `deploy/scripts/verify-aiops-workflow-gates.sh`

- [ ] **Step 1: Run all Go suites serially**

  ```bash
  cd ai-apm-query-go && go test ./... -count=1
  cd ../ai-apm-ingest-go && go test ./... -count=1
  cd ../ai-action-executor && go test ./... -count=1
  cd ../ai-credential-broker && go test ./... -count=1
  ```

  Expected: all PASS.

- [ ] **Step 2: Run Python and workflow suites**

  ```bash
  cd /Users/mssc/Documents/Code/agent/aiops
  make test-workflow-contract
  make test-orchestrator
  ```

  Expected: all PASS.

- [ ] **Step 3: Run frontend tests and production build**

  ```bash
  cd /Users/mssc/Documents/Code/agent/aiops/observability-frontend
  npm run test:run
  npm run build
  ```

  Expected: all tests PASS and TypeScript/Vite build succeeds.

- [ ] **Step 4: Run Helm and architecture contracts**

  ```bash
  cd /Users/mssc/Documents/Code/agent/aiops
  helm lint deploy/helm/aiops
  bash deploy/scripts/verify-aiops-workflow-gates.sh
  bash deploy/scripts/test-deployment-contracts.sh
  bash deploy/scripts/test-production-architecture-contracts.sh
  bash deploy/scripts/test-strict-telemetry-contract.sh
  bash deploy/scripts/test-managed-cluster-registration-contract.sh
  bash deploy/scripts/test-local-strict-profile.sh
  ```

  Expected: all PASS.

- [ ] **Step 5: Confirm no unresolved implementation markers or retired probes**

  ```bash
  rg -n 'T[B]D|F[I]XME|implement[[:space:]]+later|GRAPH_FEATURE_UNAVAILABLE_LEGACY|ops_changes_get_404|graph_search_without_cluster_id_503_defect' tests/manual-e2e deploy/scripts
  rg -n "'/ai/kg/graph'|\"/ai/kg/graph\"|'/ops/tasks'|\"/ops/tasks\"" observability-frontend/src tests/manual-e2e
  ```

  Expected: no matches except deliberately named negative contract fixtures.

- [ ] **Step 6: Commit any test-matrix wiring**

  ```bash
  git add Makefile deploy/scripts/verify-aiops-workflow-gates.sh
  git diff --cached --quiet || git commit -m "test: include strict contracts in verification matrix"
  ```

---

## Task 9: Build From a Clean Commit, Deploy, Register Cluster B, and Re-run 87/87

**Files:**

- Evidence output only: `test-results/strict-<timestamp>/`
- Runtime values generated outside Git from existing Kubernetes Secrets

- [ ] **Step 1: Freeze the candidate commit**

  ```bash
  cd /Users/mssc/Documents/Code/agent/aiops
  git status --short
  git branch --show-current
  git rev-parse HEAD
  ```

  Expected: clean output from `git status --short`, branch `main`, and one immutable SHA.

- [ ] **Step 2: Build every self-owned image sequentially from that commit**

  ```bash
  AIOPS_IMAGE_TAG="git-$(git rev-parse --short=12 HEAD)"
  IMAGE_TAG="$AIOPS_IMAGE_TAG" ./deploy/scripts/build-images.sh all
  ```

  Expected: every image exists locally and carries `org.opencontainers.image.revision=$(git rev-parse HEAD)`.

- [ ] **Step 3: Deploy the strict local profile without destroying persistent evidence**

  Reuse the Secret values already maintained by `local-validation.sh`, apply `values-local-validation.yaml` followed by `values-local-strict-test.yaml`, keep DeepFlow disabled, and wait for all workloads. Do not use `--destroy` for this remediation run.

- [ ] **Step 4: Prove code/image/runtime identity**

  Compare Git SHA, every local image revision label, Helm `global.imageTag`, and every self-owned Pod image. Any component on another revision blocks testing.

- [ ] **Step 5: Register the existing managed test cluster**

  Run: `./deploy/scripts/register-local-managed-cluster.sh`

  Expected: product API returns two canonical clusters, one named `kind-aiops-kind-02`, and its nodes/namespaces are readable through the product boundary.

- [ ] **Step 6: Prepare fresh telemetry and run fail-closed preflight**

  ```bash
  ./deploy/scripts/prepare-strict-manual-test.sh
  ./deploy/scripts/preflight-strict-manual-test.sh
  ```

  Expected: PASS with no secret values in output.

- [ ] **Step 7: Execute the complete manual suite serially**

  ```bash
  TEST_RUN_ID="strict-$(date -u +%Y%m%dT%H%M%SZ)" node tests/manual-e2e/strict/run.js
  ```

  Expected final summary:

  ```text
  total_expected=87
  total_observed=87
  PASS=87
  FAIL=0
  PARTIAL=0
  BLOCKED=0
  ERROR=0
  strict_go=true
  ```

- [ ] **Step 8: Verify environment restoration**

  Confirm every Action target is restored to its recorded pre-test state, all test namespaces are healthy, there are no OOMKilled/CrashLoopBackOff Pods, and no non-test namespace was modified.

- [ ] **Step 9: Review the final diff and evidence**

  ```bash
  git status --short
  git log --oneline --decorate -8
  git diff HEAD~8 --check
  ```

  Expected: only intentionally ignored `test-results` artifacts are new; tracked tree is clean; no whitespace errors.

- [ ] **Step 10: Push main through the configured HTTP remote and verify readback**

  ```bash
  git push origin main
  LOCAL_MAIN="$(git rev-parse main)"
  REMOTE_MAIN="$(git ls-remote origin refs/heads/main | awk '{print $1}')"
  test "$LOCAL_MAIN" = "$REMOTE_MAIN"
  ```

  Expected: direct push succeeds and remote `main` equals the tested/deployed local commit. If GitHub rejects the push, stop here and report the exact protection/authentication blocker.

---

## Non-PASS Case Closure Map

- Task 2 closes PF-UI-003, PF-LOGIC-012, PF-LOGIC-015, PF-DATA-008, the approval part of PF-DATA-011, PF-DATA-012, PF-BE-001 and PF-BE-005.
- Task 3 makes PF-PAGE-003, PF-PAGE-006, PF-LOGIC-003, PF-DATA-001, PF-DATA-002, PF-DATA-003, PF-BE-004 and PF-FLOW-001 executable with fresh production-path test telemetry. Successful preparation and full assertions close them as PASS; failed preparation leaves them BLOCKED with one shared prerequisite reason and does not create product FAILs.
- Task 4 closes PF-PAGE-013.
- Task 5 closes PF-LOGIC-001.
- Task 6 closes PF-LOGIC-002, PF-BE-010 and PF-FLOW-007, and completes the Cluster assertion in PF-DATA-011.
- Task 7 closes PF-LOGIC-005, PF-LOGIC-006, PF-LOGIC-007, PF-LOGIC-008, PF-LOGIC-009, PF-LOGIC-011, PF-AI-001 through PF-AI-010, PF-BE-006, PF-FLOW-002, PF-FLOW-003, PF-FLOW-004 and PF-FLOW-008.
- Task 1 prevents the current 44/9 versus 45/8 summary discrepancy from hiding or inventing any result.

## Final Acceptance Criteria

- 87 manifest IDs each have exactly one current result and all are PASS.
- Browser evidence contains no unexplained Console, page error or 4xx/5xx network response.
- Fresh `payments -> orders` trace/log/RED/topology identity matches UI, public API and backend.
- Both canonical clusters are visible to the same tenant; switching active scope never leaks cluster A data into `kind-aiops-kind-02`.
- Real Provider metrics prove non-Mock LLM calls; Run/Tool/Evidence/RCA/Action/Approval/Verification/Report/Knowledge IDs are mutually consistent.
- Action target is restored, no unrelated namespace is changed, and the resource-constrained OrbStack environment remains healthy.
- Git working tree is clean; every self-owned image and Pod is built from the exact tested commit; remote HTTP `main` reads back the same SHA.
