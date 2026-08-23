# P20 Plan 4：Gate 9-13 重做 + Fresh Final Cycle

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在 Plan 1-3 完成后，尽可能真实环境重做 Gate 9/10/11/12/13，并执行 P20.4 Fresh Final Cycle + P20.5 + Gate 20（禁止复用 P19 证据）。双集群为显式外部依赖（仅当 P19 B v0.2 通过 + 单独授权才纳入）。

**Architecture:** 逐 Gate 对照合同 Entry Criteria 复验（本轮窗口 + 本轮 exit code），用真实栈（真实 MySQL/query-api+orchestrator 进程重启/真实 K8s 只读）验证关键链路；随后 P20.4 全链路 fresh cycle（full tests→version→5镜像→deploy→fresh telemetry→real LLM→browser→smoke），P20.5 记录 identity evidence，Gate 20 汇总后停止。

**Tech Stack:** Go（query-go/ingest/collector）、Python（orchestrator）、React/Vite（frontend）、pytest/ruff、helm/kubectl、orbstack acceptance、deepseek LLM。

## Global Constraints

- GIT_ACTION = NONE：只记录变更，不 commit/push。
- 红线 F1-F5 保持（除 Plan 3 Gate 6 cutover 已完成）；本计划**不触发真实业务执行变更**。
- **禁止复用 Phase 19 evidence**：所有 final evidence 必须来自 Phase 20 本轮窗口 + 本轮 image digest。
- **双集群为显式外部依赖**：two-cluster isolation 仅当 P19 B v0.2 通过 + 单独授权后纳入 Gate 20 输入；否则本轮默认 orbstack 单集群。
- 确定性验收语义（设计 v0.2 §7）：精确命令/超时/exit code；未授权一律 403、授权空才 no_data；红线用语义测试（静态调用路径 + 运行时策略 + 负向测试），不依赖关键词 grep。
- Python 环境：本机 3.9.6；orchestrator 测试按文件/子集运行，避免全量 collection 触发 flow_engine 既有 error。

---

## Task 1: Gate 9（Hypothesis RCA）重做

**Files:**
- 无新增（验证）
- Read: `ai-orchestrator/rca_engine.py`、`rca_production.py`、`tests/test_p9_*.py`

**Interfaces:**
- Produces: Gate 9 重做证据（本轮测试 + 真实链路 + Entry Criteria 逐条复验）。

- [ ] **Step 1: 跑 P9 测试（本轮窗口）**

Run: `cd ai-orchestrator && python -m pytest tests/test_p9_rca_engine.py tests/test_p91_rca_snapshot.py tests/test_p92_hypothesis.py tests/test_p97_scoring.py tests/test_p9_production_adapter.py -v`
Expected: 全 PASS。记录 exit code。

- [ ] **Step 2: 对照合同 §七十五 Entry Criteria 逐条复验**

逐条核对 P9.1-P9.10 + Gate 9 断言（可复现评分分量 / 矛盾降低或阻断确认 / missing critical 阻断自动补救 / prompt-only RCA 不存在）。在 Evidence 记录每条 PASS/理由。

- [ ] **Step 3: 真实链路验证（真实 query-api 数据构造 Evidence）**

用真实栈（真实 VLogs/CH alerts，P19 已验证可读）构造 Evidence → RcaEngine → 确认 root_cause/confidence_state/unknown-safe 语义正确。

- [ ] **Step 4: 红线验证（语义测试）**

确认 RcaEngine 无执行类依赖（静态调用路径断言）+ RCA 停在 Root Cause/Unknowns（不触发执行）。

- [ ] **Step 5: 记录变更（不 commit）**

追加主 Evidence 文档 Phase 20 章节（Gate 9 重做结果 + exit code + Entry Criteria 复验表）。

---

## Task 2: Gate 10（Run Persistence/SSE/Recovery）重做

**Files:**
- 无新增（验证）
- Read: `ai-orchestrator/run_persistence.py`、`sse_stream.py`（已接线，见 Plan 1）、query-api `control_plane_runs.go`

**Interfaces:**
- Produces: Gate 10 重做证据（真实 MySQL 持久化 + 进程重启恢复 + SSE tenant 校验）。

- [ ] **Step 1: 跑 P10 测试**

Run: `cd ai-orchestrator && python -m pytest tests/test_p10_run_persistence.py -v`
Expected: 全 PASS。

- [ ] **Step 2: 真实 MySQL 集成验证（integration tag）**

Run: `cd ai-apm-query-go && TEST_MYSQL_DSN=... go test -tags integration ./internal/api/ -run "TestGate10Full|TestProcessRestartRecoveryIntegration"`
Expected: 全 PASS（真实 MySQL 持久化 + 进程重启恢复）。记录 DSN 来源（测试库非生产）。

- [ ] **Step 3: SSE tenant 校验验证**

确认 sse_stream.py 订阅时校验 Run tenant（fail-closed），负向测试证明跨 tenant 订阅被拒。

- [ ] **Step 4: 对照合同 §七十六 Entry Criteria 复验**

P10.1-P10.8 + Gate 10 逐条核对（CAS/单调 sequence/幂等/recovery/cancel 语义）。记录。

- [ ] **Step 5: 记录变更（不 commit）**

追加主 Evidence 文档 Phase 20 章节（Gate 10 重做结果 + integration exit code）。

---

## Task 3: Gate 11（Remediation/Approval/Execution）重做

**Files:**
- 无新增（验证）
- Read: `ai-orchestrator/phase11_execution.py`（已接 query-api SoT，见 Plan 1）

**Interfaces:**
- Produces: Gate 11 重做证据（审批链 + 权威 SoT + 不触发真实执行）。

- [ ] **Step 1: 跑 P11 测试**

Run: `cd ai-orchestrator && python -m pytest tests/test_p11_execution.py -v`
Expected: 全 PASS。

- [ ] **Step 2: 对照合同 §七十七 Entry Criteria 复验**

P11.1-P11.10 + Gate 11（R2/R3/R4 human gates 不可绕过 / 无 L0-L4 Autonomy / action mutation 失效 / verification 用 SLI 非 exit code）。记录。

- [ ] **Step 3: 审批链权威 SoT 验证**

确认 ApprovalService 身份来源为 query-api 权威 SoT（Plan 1 已接线），不可达 fail-closed。

- [ ] **Step 4: 确认不触发真实执行**

确认本 Gate 只验证审批/执行链路（dry-run/In-memory 模拟），不落地真实 K8s/OpenStack 变更。语义断言。

- [ ] **Step 5: 记录变更（不 commit）**

追加主 Evidence 文档 Phase 20 章节（Gate 11 重做结果）。

---

## Task 4: Gate 12（前端产品收敛）重做

**Files:**
- 无新增（验证，前端已接线见 Plan 1）
- Read: `observability-frontend/src/pages/investigation/*.tsx`

**Interfaces:**
- Produces: Gate 12 重做证据（前端编译 + 真实数据/SSE 接线）。

- [ ] **Step 1: 前端编译验证**

Run: `cd observability-frontend && npx tsc --noEmit && npx vite build`
Expected: exit 0 + build ok。记录 exit code。

- [ ] **Step 2: 前端真实接线验证**

确认调查中心页从真实 `/api/v1/ai/runs` 拉取 + SSE 订阅（非 DEMO 占位）。浏览器 smoke（真实环境）验证页面可加载主旅程。

- [ ] **Step 3: 对照合同 §七十八 Entry Criteria 复验**

P12.1-P12.8 逐条核对（六大导航收敛 / 调查中心 Run 对象 / 智能调查页 / 发起调查显式按钮 / Logs 拆分 / SSE UX）。记录。

- [ ] **Step 4: 记录变更（不 commit）**

追加主 Evidence 文档 Phase 20 章节（Gate 12 重做结果 + tsc/build exit code）。

---

## Task 5: Gate 13（服务端安全加固）重做

**Files:**
- 无新增（验证）
- Read: `ai-orchestrator/authorization_matrix.py`（已接权威身份，见 Plan 1）

**Interfaces:**
- Produces: Gate 13 重做证据（P13.1-P13.8 + 权威 SoT）。

- [ ] **Step 1: 跑 P13 测试**

Run: `cd ai-orchestrator && python -m pytest tests/test_p13_security.py tests/test_p13_wiring.py -v`
Expected: 全 PASS。

- [ ] **Step 2: 对照合同 §七十九 Entry Criteria 复验**

P13.1-P13.8（授权矩阵 / role tamper 服务端忽略 / 未知 cluster fail-closed / SystemPrincipal 不能建 Run / self-approval 拒 / Agent 无 kubeconfig / Evidence 不落 Secret）。记录。

- [ ] **Step 3: 权威身份验证**

确认 Authorization Matrix 从 query-api 权威身份取值（Plan 1 已接入 run-invocations），前端 role 参数被忽略（服务端权威）。负向测试。

- [ ] **Step 4: 记录变更（不 commit）**

追加主 Evidence 文档 Phase 20 章节（Gate 13 重做结果）。

---

## Task 6: P20.4 Fresh Final Cycle（禁止复用 P19）

**Files:**
- 无新增（全链路运行）

**Interfaces:**
- Consumes: Gate 9-13 重做通过 + Plan 2/3 完成。
- Produces: 本轮窗口全链路 evidence（不得复用 P19）。

- [ ] **Step 1: full tests（5 仓库，本轮 exit code）**

按设计 §7.1 命令逐仓库跑：query-go/ingest/collector `go test ./...`+`go vet ./...`；orchestrator pytest 子集 + ruff；frontend tsc+vite build。记录每仓库 exit code。

- [ ] **Step 2: bump version + source hash**

bump 版本（如 `v1.2.0-p20-<sha8>`），重算 source_tree_hash（对齐 P18 方法）。

- [ ] **Step 3: 重建 5 镜像**

构建 query-api/ingest/event-collector/orchestrator/frontend 5 镜像，记录 digest。

- [ ] **Step 4: deploy 到 orbstack（含包1 收紧后 Helm）**

rollout 全部镜像 + 包1 Helm。验证各 pod Running 1/1。

- [ ] **Step 5: fresh telemetry**

受控注入 canonical 数据，验证 VM/VLogs/CH 三写闭环（对齐 P6.5 三写闭环）。

- [ ] **Step 6: real LLM smoke**

deepseek 真实推理（models 200 + chat 真实输出）。

- [ ] **Step 7: browser smoke**

前端主旅程（login + 服务/调查中心/日志等主页面可加载）。

- [ ] **Step 8: platform-self + registered-external source smoke**

平台自身数据 + 已注册外部数据源（如 kind-02/OTLP）smoke。

- [ ] **Step 9: two-cluster isolation（外部依赖）**

**仅当 P19 B v0.2 通过 + 单独授权**才纳入：orbstack + kind-02 复验 403/no_data/跨集群 RCA。否则记为外部依赖挂起，不计入本轮 Gate 20。

- [ ] **Step 10: 记录变更（不 commit）**

追加主 Evidence 文档 Phase 20 章节（P20.4 全链路结果 + 每步 exit code/digest）。

---

## Task 7: P20.5 Final Identity Evidence + Gate 20

**Files:**
- 无新增（记录汇总）

**Interfaces:**
- Consumes: P20.4 全部完成。
- Produces: P20.5 identity evidence + Gate 20 汇总后停止（不自动进入 Phase 21）。

- [ ] **Step 1: 记录 identity evidence**

记录 `source_tree_hash / build_id / version / image digests / deployed version / smoke run IDs`。

- [ ] **Step 2: Gate 20 汇总**

核对所有 final evidence 均来自 Phase 20 本轮窗口 + 本轮 image digest（逐项标注时间窗口与 digest）。

- [ ] **Step 3: Gate 后停止**

Gate 20 通过后**停止**，不自动进入 Phase 21（Phase 21 由用户另行授权）。

- [ ] **Step 4: 记录变更（不 commit）**

追加主 Evidence 文档 Phase 20 章节（P20.5 + Gate 20 汇总）。

**Plan 4 完成标准：** Gate 9/10/11/12/13 全部重做（本轮测试 exit code + Entry Criteria 逐条复验 + 真实栈验证）；P20.4 全链路本轮窗口完成（禁止复用 P19）；P20.5 identity evidence 记录；Gate 20 通过后停止；双集群显式外部依赖（未授权则不纳入）。
