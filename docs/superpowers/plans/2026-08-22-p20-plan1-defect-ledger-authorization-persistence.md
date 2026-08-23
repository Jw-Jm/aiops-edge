# P20 Plan 1：Defect Ledger + Authorization 权威接入 + 模型/持久化整改

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 产出可去重可审计的 Defect Ledger（P20.1/P20.2），完成 Authorization 权威接入（包2）与模型/持久化代码整改（包3），将代码缺陷收敛至 P0=0/P1=0。

**Architecture:** 围绕 ai-orchestrator（Python）做三块独立收敛：(a) 新建台账文档并逐条建立唯一 ledger ID/关闭条件；(b) 确认并加固 Authorization Matrix 已接 query-api 权威身份的调用路径 + ManualBoundary 唯一 Run 入口 + Approval 绑定；(c) 收敛 `investigation_state.Hypothesis` 到权威 `contracts.Hypothesis`，并将 `run_persistence.py`/`sse_stream.py`/`phase11_execution.py` 的 In-memory 实现接线到 query-api 既有持久化端点。

**Tech Stack:** Python 3.9+（ai-orchestrator）、pytest、ruff；Go（query-api 仅作已存在端点参考，不新增）。

## Global Constraints

- GIT_ACTION = NONE：所有任务只记录变更清单（变更到主 Evidence 文档 Phase 20 章节），不执行 `git commit`/`git push`。
- 红线 F1-F5 保持（除 Gate 6 cutover 外）；真实业务执行变更仍 NOT APPROVED。
- Agent≠Execution 隔离：不得新增 Agent 侧执行类依赖。
- 本计划只做代码/文档整改 + 本地测试验证（In-memory + 隔离），不 rollout 生产（Helm 属 Plan 2，Gate 6 属 Plan 3）。
- Python 运行环境：本机 Python 3.9.6；全量 pytest 收集 flow_engine 报既有 error（记忆 37249409），本计划测试按文件运行，避免全量 collection。
- 测试必须为负向断言（证明缺陷不再触发），不得用"偶现/重跑通过"关闭。

---

## Task 1: 新建可去重可审计 Defect Ledger

**Files:**
- Create: `docs/P20_DEFECT_LEDGER.md`（在 aiops/ 下）

**Interfaces:**
- Produces: 台账文档，含唯一 ledger ID（`P20-<来源>-<序号>`）、双源去重映射、关闭条件模板。后续所有缺陷修复任务以此台账为状态源。

- [ ] **Step 1: 建立台账骨架（合并两清单）**

创建 `docs/P20_DEFECT_LEDGER.md`，内容含：

```markdown
# V9.3 Phase 20 Defect Ledger

> 唯一 ledger ID 规则：`P20-<来源>-<序号>`；来源 ∈ {CONTRACT, BUGBOT}。
> 关闭条件：repro + root cause + fix + 负向测试 + 回归 exit code + Evidence 链接 六项齐备。
> 禁止用"偶现/重跑通过"关闭安全 flaky。

## 一、合同 P0 清单（§八十六，15 项）
| ledger_id | severity | 缺陷 | source | component | repro | root cause | fix | 负向测试 | 回归 | evidence_link | status |
|-----------|----------|------|--------|-----------|-------|------------|-----|----------|------|---------------|--------|
| P20-CONTRACT-P0-01 | P0 | fabricated fact | CONTRACT | rca_engine | ... | ... | ... | ... | ... | ... | OPEN |
...（P0-02..P0-15 同表）

## 二、合同 P1 清单（§八十六，7 项）
（P20-CONTRACT-P1-01..P1-07）

## 三、Bugbot 清单（第七部分 6 P0 + 6 P1）
| ledger_id | severity | 缺陷 | source | 映射到合同 | 状态 |
|-----------|----------|------|--------|-----------|------|
| P20-BUGBOT-P0-01 | P0 | P0-1 身份伪造 | BUGBOT | 已由 R0-R13 修复 | CLOSED |
...（逐条）
```

- [ ] **Step 2: 逐条填写两清单**

合同 15 P0 + 7 P1 逐条建 ledger 条目（severity/component/status=OPEN），Bugbot 6 P0 + 6 P1 逐条标注映射（合并到合同条目或独立），已修复条目标注 CLOSED + Evidence 链接。

- [ ] **Step 3: 逐条执行去重比对**

对每条 Bugbot 缺陷与合同缺陷比对：同一缺陷双源 → 合并；仅单源 → 保留。在台账"映射到合同"列标注。

- [ ] **Step 4: 验证台账完整性**

逐条核对 15+7+12=34 条目齐全、唯一 ledger ID、无空 repro/status。人工核对清单数量。

- [ ] **Step 5: 记录变更（不 commit）**

将本任务产出追加到主 Evidence 文档 Phase 20 章节（P20.1/P20.2 完成 + ledger 路径）。

---

## Task 2: Authorization 权威接入加固（包2）

**Files:**
- Modify: `ai-orchestrator/authorization_matrix.py`
- Modify: `ai-orchestrator/main.py`（run-invocations 接入点）
- Test: `ai-orchestrator/tests/test_p20_authz_wiring.py`（新建）

**Interfaces:**
- Consumes: `contracts`（既有）、query-api `authorizeUserChatCapability`/`UserDAO.GetByUUID` 语义（Go 侧已存在，orchestrator 经签名上下文信任）。
- Produces: 验证 Authorization Matrix 在所有资源入口 fail-closed；ManualBoundary 唯一 Run 创建；Approval 绑定 identity。

- [ ] **Step 1: 写负向测试（证明 authz 全入口 fail-closed）**

```python
# tests/test_p20_authz_wiring.py
import pytest
from authorization_matrix import AuthorizationMatrix, AuthzError


def test_unknown_resource_fail_closed():
    m = AuthorizationMatrix(service_account_roles={})
    with pytest.raises(AuthzError):
        m.authorize_request(
            principal="user", role="viewer",
            tenant_id="7ed01afc-cc79-4ecd-8767-a2befa6168ad",
            cluster_id="91771a6e-9c2d-11f1-8271-bea176fe9f9f",
            resource="unknown.resource", capability="x", action="read",
        )
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd ai-orchestrator && python -m pytest tests/test_p20_authz_wiring.py::test_unknown_resource_fail_closed -v`
Expected: FAIL（若未知 resource 未抛错）或 PASS（若已 fail-closed——则记录该负向已满足）。

- [ ] **Step 3: 核对 main.py run-invocations 已接入 authz**

读取 `ai-orchestrator/main.py` L555-612（run-invocations），确认：
- L570-580 ManualBoundary 拒 system/自动来源（唯一 Run 创建入口）。
- L581-593 `_authz_matrix.authorize(capability="ai.investigate", action="create")`。
若存在未覆盖的 Run 创建/资源入口，补挂 authz 调用。写测试覆盖每个入口。

- [ ] **Step 4: 核对 Approval 绑定 identity**

读取 `ai-orchestrator/phase11_execution.py` ApprovalService，确认 requester!=approver、绑定 action hash/version/target/risk/resourceVersion、跨 cluster 拒绝。写负向测试补全缺失断言。

- [ ] **Step 5: 跑新增 + 相关回归测试**

Run: `cd ai-orchestrator && python -m pytest tests/test_p20_authz_wiring.py tests/test_p13_security.py tests/test_p13_wiring.py -v`
Expected: 全 PASS。

- [ ] **Step 6: 记录变更（不 commit）**

追加主 Evidence 文档 Phase 20 章节（包2 完成 + 负向测试数 + 回归 exit code）。

---

## Task 3: Hypothesis 模型收敛（包3-1）

**Files:**
- Modify: `ai-orchestrator/investigation_state.py`
- Test: `ai-orchestrator/tests/test_p77_investigation_state.py`
- Test: `ai-orchestrator/tests/test_r2_t5_gate.py`（若引用平行 Hypothesis）

**Interfaces:**
- Consumes: `contracts.Hypothesis`（权威，含 tenant_id/cluster_id/resource_id 强隔离）。
- Produces: `investigation_state.Hypothesis` 删除，所有消费方改用 `contracts.Hypothesis`（组合权威封装）。

- [ ] **Step 1: 确认消费方**

Run: `cd ai-orchestrator && grep -rn "from investigation_state import.*Hypothesis\|Hypothesis(" --include=*.py tests/ | grep -v test_p77`
列出所有消费 `investigation_state.Hypothesis` 的测试/文件。

- [ ] **Step 2: 迁移测试到权威 Hypothesis**

在消费处将 `investigation_state.Hypothesis(...)` 改为 `contracts.Hypothesis(...)`（权威字段：hypothesis_id=UUID、title、description、supporting_evidence=[UUID]、status=HypothesisStatus、tenant_id/cluster_id/resource_id）。写一个迁移测试证明行为等价。

- [ ] **Step 3: 删除平行 dataclass**

从 `investigation_state.py` 删除 `@dataclass class Hypothesis`（L37-45），保留 InvestigationState/StepState/StateStore。

- [ ] **Step 4: 跑受影响测试**

Run: `cd ai-orchestrator && python -m pytest tests/test_p77_investigation_state.py tests/test_r2_t5_gate.py -v`
Expected: 全 PASS（消费方已迁移）。

- [ ] **Step 5: 记录变更（不 commit）**

追加主 Evidence 文档 Phase 20 章节（包3-1 完成 + 平行模型删除 + 回归）。

---

## Task 4: P10 Run 持久化/SSE 接线到 query-api（包3-2）

**Files:**
- Modify: `ai-orchestrator/run_persistence.py`（In-memory → 经 query-api 持久化端点）
- Modify: `ai-orchestrator/sse_stream.py`（SSE 校验 Run tenant）
- Test: `ai-orchestrator/tests/test_p10_run_persistence.py`（既有，适配）
- Test: `ai-orchestrator/tests/test_p20_run_persistence_wiring.py`（新建）

**Interfaces:**
- Consumes: query-api 既有持久化端点：`/internal/v1/ai/runs`（control_plane_runs.go）、`/internal/v1/ai/runs/events`（ai_run_events.go）、SSE proxy（sse_proxy.go）。orchestrator 经签名上下文（TrustedContextIssuer）调用。
- Produces: `RunStateStore` 持久化后端切换（DB 不可达 fail-closed）；`SSEStream` 校验 Run tenant。

- [ ] **Step 1: 确认 query-api 持久化端点契约**

读取 `ai-apm-query-go/internal/api/control_plane_runs.go`、`ai_run_events.go`、`sse_proxy.go` 的方法签名（POST body 字段 / 响应结构），记录到任务内注释。

- [ ] **Step 2: 写接线测试（DB 不可达 fail-closed）**

```python
# tests/test_p20_run_persistence_wiring.py
import pytest
from run_persistence import RunStateStore, RunPersistenceError


def test_backend_unavailable_fail_closed():
    # 后端配置为不可达 URL → create 抛错，不静默内存回退
    store = RunStateStore(query_api_url="http://127.0.0.1:1", signing_key="dummy")
    with pytest.raises(RunPersistenceError):
        store.create_run(run_id="x", tenant_id="7ed01afc-cc79-4ecd-8767-a2befa6168ad")
```

- [ ] **Step 3: 实现 RunStateStore 持久化后端**

将 `run_persistence.py` 的内存操作改为经 query-api 持久化端点（POST /internal/v1/ai/runs + control 端点），保留 CAS/状态机语义；后端不可达 → `RunPersistenceError` fail-closed（不静默回退内存）。

- [ ] **Step 4: SSE 校验 Run tenant**

`sse_stream.py` 订阅时校验调用方 tenant == Run.tenant_id，不匹配拒绝（fail-closed）。

- [ ] **Step 5: 跑接线 + 既有测试**

Run: `cd ai-orchestrator && python -m pytest tests/test_p20_run_persistence_wiring.py tests/test_p10_run_persistence.py -v`
Expected: 全 PASS。

- [ ] **Step 6: 记录变更（不 commit）**

追加主 Evidence 文档 Phase 20 章节（包3-2 完成 + fail-closed 断言 + 回归）。

---

## Task 5: P11 Approval/Execution 接 query-api 权威 SoT（包3-3）

**Files:**
- Modify: `ai-orchestrator/phase11_execution.py`
- Test: `ai-orchestrator/tests/test_p11_execution.py`（既有）
- Test: `ai-orchestrator/tests/test_p20_p11_sot_wiring.py`（新建）

**Interfaces:**
- Consumes: query-api 权威 SoT（authorization.go 的 AuthorizationDAO、users.go GetByUUID）。orchestrator 经签名上下文获权威身份。
- Produces: ApprovalService/SecurityGate 从 query-api 权威身份取值，非本地配置；fail-closed。

- [ ] **Step 1: 写负向测试（权威 SoT fail-closed）**

证明 ApprovalService 在权威身份不可达时拒绝，而非本地降级。

- [ ] **Step 2: 接线 ApprovalService**

`phase11_execution.py` 的 ApprovalService 身份来源改为 query-api 权威 SoT（经签名上下文），不可达/非 active → fail-closed。

- [ ] **Step 3: 跑新增 + 既有测试**

Run: `cd ai-orchestrator && python -m pytest tests/test_p20_p11_sot_wiring.py tests/test_p11_execution.py -v`
Expected: 全 PASS。

- [ ] **Step 4: 记录变更（不 commit）**

追加主 Evidence 文档 Phase 20 章节（包3-3 完成 + 回归）。

---

## Task 6: P12 Run API/SSE 页面真实接线（包3-4）

**Files:**
- Modify: `observability-frontend/src/pages/investigation/*.tsx`（移除 DEMO_RUNS/DEMO_DETAIL/setTimeout 占位）
- Test: `npx tsc --noEmit` + `npx vite build`

**Interfaces:**
- Consumes: 前端 `api/client.ts` 的 `/api/v1/ai/runs` 端点 + query-api 已部署的 Run API/SSE。
- Produces: 调查中心页从真实 Run API 拉取数据，SSE 订阅真实事件；移除 DEMO 占位。

- [ ] **Step 1: 确认前端 API 客户端端点**

读取 `observability-frontend/src/api/client.ts`，确认已有 `/api/v1/ai/runs`（R12 已打通）。记录到注释。

- [ ] **Step 2: 移除 DEMO 占位**

将 `pages/investigation/InvestigationCenter.tsx` / `IntelligentInvestigation.tsx` / `NewInvestigation.tsx` 的 DEMO_RUNS/DEMO_DETAIL/setTimeout 改为真实 fetch `/api/v1/ai/runs` + SSE 订阅（按现有 API 客户端模式）。

- [ ] **Step 3: 编译验证**

Run: `cd observability-frontend && npx tsc --noEmit && npx vite build`
Expected: exit 0 + build ok。

- [ ] **Step 4: 记录变更（不 commit）**

追加主 Evidence 文档 Phase 20 章节（包3-4 完成 + tsc/build exit code）。

---

## Task 7: P20.3 零缺陷收口回归

**Files:**
- 无新增（回归运行）

**Interfaces:**
- Consumes: Task 1-6 全部完成。
- Produces: P0=0/P1=0 的代码级回归证据（对应合同 P0/P1 清单中可代码验证项）。

- [ ] **Step 1: 更新台账状态**

对 Task 1-6 已修复缺陷，将台账 status 从 OPEN → CLOSED，补全负向测试/回归/Evidence 链接。

- [ ] **Step 2: 跑 orchestrator 相关回归**

Run: `cd ai-orchestrator && python -m pytest tests/test_p20_*.py tests/test_p13_*.py tests/test_p77_investigation_state.py tests/test_p10_run_persistence.py tests/test_p11_execution.py -v`
Expected: 全 PASS。记录 exit code。

- [ ] **Step 3: 红线隔离验证（语义测试取代 grep）**

对 Agent/Planner 代码做静态调用路径断言（无执行类依赖）+ 运行时策略 fail-closed 负向测试。不依赖关键词 grep 作为通过标准。

- [ ] **Step 4: 记录变更（不 commit）**

追加主 Evidence 文档 Phase 20 章节（P20.3 零缺陷代码级收口 + ledger 状态快照 + 回归 exit code）。

**Plan 1 完成标准：** Defect Ledger 34 条目齐全且状态可审计；Authorization 权威接入全入口 fail-closed 有负向测试；`investigation_state.Hypothesis` 收敛删除；P10/P11 接线 fail-closed；P12 前端真实接线编译通过；orchestrator 回归全 PASS；台账 P0/P1 代码可验证项 CLOSED。
