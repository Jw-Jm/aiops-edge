# Phase 3 交付记录：架构收敛（2026-09-03）

> 分支 `phase3/architecture-convergence`。依据最终报告 §24 Phase 3（§.1431）。

## 1. P1-R1：ApprovalStore 从 production path 退出 / fail-closed ✅

- `db_approval.ApprovalStore` 移除内存降级：MySQL 不可用/写失败 → `ApprovalStoreError`（raise，fail-closed），审批状态绝不落内存充当授权。
- `store._TaskStore.persist()` 不再吞错（异常传播 → 调用方 5xx）。
- 测试：`test_approval_store` fail-closed 语义；legacy 域测试（k8s_endpoints/emergency）改用显式内存 double。
- canonical Action 审批/执行链 = query-api + MySQL（`workflow_contract_mysql_test.go` 真 MySQL 9/9 已证，不经 orchestrator ApprovalStore）。
- 验证：orchestrator pytest 1331 passed（当时）。

## 2. P1-A1：legacy runtime 调用矩阵 + 物理删除 ✅

详见 `docs/remediation/2026-09-03/phase3-p1a1/LEGACY_USAGE_MATRIX.md`（§5 最终删除状态）。

核心：
- **canonical RcaEngine 提升**：`rca_engine_legacy.py`（命名残留，内容为 Phase 9 canonical）→ `rca_engine/phase9_engine.py`；`rca_engine/__init__` 删 feature-flag 桥，静态导出。
- **legacy investigator/flow 主链删除**：main B6（maybe_investigate）/A3（alert→flow dispatch）删除；`investigator.py` 物理删除；flow_api `/run`、`/resume` 端点删除。
- **direct mutation 退役**：`_direct_mutation_enabled()` 恒 False（flag 不再读取）。
- **Helm/values**：ai-orchestrator + investigation-worker 的 legacy env 全部移除；values keys 删除。
- **前端**：`runFlowAsync`/`resumeFlowRun` 删除（无调用方）。
- 关闭条件 grep：`rca_engine_legacy|LEGACY_FLOW_RUNTIME_ENABLED|LEGACY_DIRECT_MUTATIONS_ENABLED|INVESTIGATOR_ENABLED` 在 runtime/Helm/tests = **0 命中**。
- ARCH-601..607 canonical-only 装配契约入 `test-production-architecture-contracts.sh`。

## 3. 域拆包（第 4 项）

现状：main.py 仍为 4435 行 / 85 条路由，但域模块化已存在并持续增长：
`apps/investigation.py`（worker）、`ai_runs_api.py`、`ops_action_api.py`、`data_cleanup_api.py`、`flow_api.py`、`kg_api.py`、`production_surface.py`（生产路由 allowlist 过滤）、`internal_ingress.py`、`db_agents.py` 等。

> 状态：本迭代评估了"完整物理拆分 main.py"（将剩余 chat/gateway/legacy-tasks 域逐条搬出）的改造成本与回归风险，确认其为**无行为变化的结构重构**且会触碰大量共享 main 全局状态。选择以「production_surface 路由清单 + ARCH 模块边界契约」作为拆包约束的收敛形态（见 §5 决策），未在本次做高风险搬移。

## 4. GOV1 ruleset 变更备注（补记）

2026-09-03 merge PR #1 时发现：仓库为单账号（Jw-Jm），GitHub 拒绝 author self-approve（`Review Can not approve your own pull request`），而 ruleset `main-release-gate-protection`（id 22195118）设 `required_approving_review_count: 1` → 任何 PR 作者（唯一开发者）均无法合入。经授权将 approval 数调整为 **0**，保留：
- PR-only merge（禁直接 push main）；
- required status check `release-gate`；
- `non_fast_forward`（禁 force push）、`deletion`（禁删分支）；
- bypass_actors 空。

恢复人工 review 门禁：多人协作时可把 `required_approving_review_count` 调回 1。

## 5. 决策与边界

- P1-A1 用户边界已遵守：① 兼容桥仅符号兼容（RcaEngine 静态同一实现）；② 未误删 V2 Investigation Runtime / 当前 RCA API / Query API→Orchestrator 正式链；③ legacy 测试删除但安全约束由 ARCH 契约承接；④ 本轮未混入域拆包/HA/接口美化。
- **域拆包（P3-4）收敛形态**：production route 过滤（`production_surface`）+ 既有域 router + ARCH 模块边界断言已构成"按域装配"约束；对 main.py 剩余单文件的搬移列为独立重构任务（涉及 chat/gateway 大域，需回归预算），未在本轮混入（与用户 P1-A1 边界 4 一致）。
- **HA（P3-5 / P2-HA1）**：报告标注"需要时"。生产现为单副本（orchestratorReplicas=1, PVC RWO）。是否投入多副本全套（checkpoint 外部化、worker lease、2+ replicas、故障恢复测试）或按报告接受单副本并写 `runtime-slo.md`（明确 RTO/失效行为、删除"生产高可用"模糊表述）——需要正式 SLO 输入，未擅断。

## 6. 验证汇总

- orchestrator pytest：1321 passed（legacy 删除后）。
- gate `AIOPS_GATE_STAGES=helm,contracts`：exit 0。
- `test-production-architecture-contracts.sh`（test secrets 模式）：pass（含 ARCH-601..607）。
- frontend `tsc --noEmit`：0。
- 关闭条件 grep：0 命中。
