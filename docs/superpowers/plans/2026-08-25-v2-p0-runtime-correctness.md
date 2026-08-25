# Plan: V2 复审 8 个 P0 修复（RUNTIME_CORRECTNESS_CANDIDATE）

依据《AIOps_全面代码修改报告_V2.md》：DESIGN=APPROVED, CODE=SUBSTANTIALLY_IMPROVED_WITH_RESIDUAL_P0, PRODUCTION=NOT_READY。
目标：修复 8 个 P0，达到 RUNTIME_CORRECTNESS_CANDIDATE。

## Implementation Status（2026-08-25，全部完成 + 真实环境验证）

### P0#1/#2/#10：Public Cancel 统一 + Lease fence + Commit hash/legal-transition（COMPLETE）
- **RunControlService.CancelTx**（`run_control_service.go`）：Public/Internal/Admin Cancel 唯一权威事务——
  SELECT Run FOR UPDATE → 校验 expected_version + 非终态 → set cancelled + state_version++ +
  **lease_epoch++ / clear owner/claim/token/expiry**（Cancel 原子失效 Lease，旧 executor 被 Fence）+
  append RUN_CANCELLED event + command 幂等响应。不再走 runDAO.Cancel 非原子路径。
- **Runtime Commit**（`control_plane_lease.go`）：fast-path + in-tx recheck commit_id——
  same key + same hash → 首次结果；same key + diff hash → 409 IDEMPOTENCY_KEY_REUSED；
  `TransitionTxValidated`（store 层 ValidateRunTransition：终态不可复活 + legal transition）。
- store 增加 `ValidateRunTransition`/`runTransitions`/`IllegalTransitionError`/`IsTerminalStatus`。

### P0#3/#11：Lease 全 DB-time + expired renew + caller claim/token exact retry（COMPLETE）
- **Claim** 支持 caller `ClaimRequest{claim_id, lease_token, claim_source}`（P0-LEASE-03）：
  同 claim_id+token exact retry → 返回既有权（epoch 不变）；新 claim_id → ErrClaimIDReused；
  过期且同 claim_id → ErrClaimIDExpired。expires_at/remaining 全 DB time（DATE_ADD + TIMESTAMPDIFF）。
- **Renew**：过期 Lease 不得复活（`lease_expires_at >= CURRENT_TIMESTAMP(3)`），
  owner/epoch/token 匹配但过期 → ErrLeaseLost（RUN_LEASE_LOST）。

### P0#4/#12：Orchestrator Lease 三态 + 稳定 commit_id（COMPLETE）
- `_LeaseState` ACTIVE/UNCERTAIN/LOST（P0#4）：renew 连续失败 → UNCERTAIN → LOST；
  `check_active()` 抛 LeaseLostError（进入数据面访问前停止）。
- `commit_id` 稳定（P0#12）：同一次执行的重试复用同一 commit_id（不再每次生成新 UUID）。
- caller 生成稳定 claim_id + lease_token（P0-LEASE-03 编排侧）。

### P0#5/#13：ToolRun 真实 run_id + pre-I/O fencing + finish fencing（COMPLETE）
- `internalQueryRequest` 加 `run_id` + `lease_token`；`newToolRunFromRequest` 校验真实 UUID，
  缺失 → 不包装（拒绝写 run_id='' 孤儿 ToolRun）。
- **FenceToolExecutionTx**（P0-TOOL-02）：任何 datasource I/O 前校验 Run 非终态 +
  owner/epoch/token 匹配 + Lease 未过期（DB time）；失败 → 409 TOOL_LEASE_LOST。
- **FinishToolRunWithFencing**（P0-TOOL-03）：统一锁序，Run 终态或 epoch 不匹配 →
  late=true/eligible=0 + TOOL_RESULT_LATE event。

### P0#6/#14：Tool exact replay + args_hash（COMPLETE）
- `toolArgsHash`（SHA256 操作语义参数）；幂等判定 (run_id, idempotency_key, args_hash)——
  同 key 同 args → exact replay；同 key 不同 args → 409 IDEMPOTENCY_KEY_REUSED。

### P0#7/#15/#16：Evidence 授权修复 + server-owned policy（COMPLETE）
- `internalControlPlaneToolEvidenceConsume`：先读 body 得真实 run_id，再对 run_id 授权
  （原实现把 tool_run_id 当 run_id 授权，正常无法通过）；allowed_statuses 服务端拥有
  （`success,partial,no_data`），不接受调用方传入。

### P0#8：Stage D 保持 disabled + 错误码合同（COMPLETE）
- ai-action-executor 默认 EXECUTION_MODE=disabled；`approved` 模式仅记录 TOCTOU-clean，
  **不执行真实 mutation**（消息明确 "real mutation requires adapter"）；helm 默认 disabled。
- contract 错误码新增：RUN_LEASE_LOST/CLAIM_ID_REUSED/CLAIM_ID_EXPIRED/TOOL_LEASE_LOST/TOOL_RESULT_STALE。

### 0006 migration（COMPLETE + 应用到生产）
- `0006_lease_cancel_toolrun.sql`：`ai_run_claims.claim_source` + `ai_tool_runs.args_hash` +
  `chk_ai_tool_runs_run_id_nonempty`（CHECK run_id 非空）。用 `-- statement-breakpoint` 分隔。
- 已应用到生产 MySQL（8 migrations），RequireCurrent 通过。

## 验证（真实环境 + 全量）
- **真实环境（orbstack）**：新 query-api v2-p0 部署 Ready；P0-LEASE-03 caller claim+token
  （200, epoch=1, token 保留）、exact retry 同 epoch、新 claim_id → 409 CLAIM_ID_REUSED、
  **P0#1 Cancel 原子失效 Lease**（cancelled + lease_epoch 1→2 + owner/token clear +
  旧 executor renew → 409 RUN_LEASE_FENCING）、claim 不存在 run → 404。
- **真实 MySQL 集成**：TestA1LeaseCommit（caller claim + exact retry + reused）、
  TestP0CommitHashConflictReal（diff hash → ErrCommitDuplicate）、
  TestP0ToolPreIOFencingReal（正确通过 / 错误 token 拒绝）、TestEvidenceConsumeReal、
  TestToolReconcilerReal、TestToolLateFencingReal 全 PASS。
- **全量**：query-go 10 包、ingest 6 包、orchestrator 1127、action-executor、frontend tsc exit 0。

## 边界 / 诚实
- P0-LEASE-01（claim 响应含 server_now/lease_remaining_ms）已实现（DB time 返回）。
- 多节点 failover / MySQL 真 HA / PVC 跨节点 标记 BLOCKED_BY_ENV（单节点）。
- Execution Production Execution=NOT YET APPROVED；红线 F1-F5 保持；GIT_ACTION=NONE。
