# AIOps V9.2 Phase 6 — P6.4.3 Cutover Rollback / Abort Matrix

> 状态：FROZEN（production cutover 之前必须遵守）
> 来源：`aiops-agentic-v9.2-final.md` Gate 6 原子切换规则
>
> 本矩阵在 production cutover 前**写死**。所有 checkpoint 遵循"**fresh-data verification PASS 之前不得 stop old writer/reader**"铁律。
> 本文件是架构 contract，配套可执行状态机见 `internal/cutover/state_machine.go`（query-go）。

## 铁律

1. **fresh-data verification PASS** 之前，绝不允许 `stop old writer` / `stop old reader`。
2. 任何 `ABORT` / `HARD ABORT` 都必须**保持旧链完整**（旧 writer/reader 不停、adapter 不删、fallback 不摘）。
3. `rollback` = 撤销已执行的 cutover 步骤，恢复到上一个 checkpoint 的旧链状态；绝不回滚到"半 cutover"状态。
4. WAL local per instance；Lease 解决 leader ownership，**不解决 WAL handoff**，不得声称 WAL 自动迁移。

## 状态机（可执行）

`internal/cutover/state_machine.go`（query-go）定义状态机：

```
Precheck → ActivateNewWriter → ActivateNewReader → FreshDataVerify → ScopeVerify → SemanticVerify → StopOldWriter → StopOldReader → RemoveAdapters → RemoveFallback → FinalVerify → Done
```

每个 checkpoint 只能按序推进；任何失败都落到对应的 ABORT / ROLLBACK 行为，不得跳过 checkpoint 前进。

## Checkpoint → Failure → Required Behavior

| # | Cutover checkpoint | Failure | Required behavior |
|---|---|---|---|
| 1 | Precheck | backend unhealthy | **ABORT**；旧链保持 |
| 2 | Activate new writer | activation fail | **ABORT**；旧 writer/reader 不动 |
| 3 | Activate new reader | startup/readiness fail | **ROLLBACK**：回退 new writer activation，旧链继续 |
| 4 | Fresh data verify | invisible | **ABORT**：不停 old writer/reader |
| 5 | Scope verify | A/B 串数据 | **HARD ABORT** |
| 6 | Semantic verify | unavailable→no_data 折叠等 | **HARD ABORT** |
| 7 | Stop old writer | stop fail | **不进入 adapter removal** |
| 8 | Stop old reader | stop fail | **不进入 adapter removal** |
| 9 | Remove adapters | regression | **Gate FAIL**；按预定义恢复方案处理 |
| 10 | Remove fallback | tests fail | **Gate FAIL** |
| 11 | Final verify | 任一不满足 | **Gate FAIL**；旧物理历史数据必须 PRESENT BUT UNREACHABLE |

## Cutover 顺序（原子窗口内，锁死）

```
PRECHECK
→ switch new writer ACTIVE
→ switch new reader ACTIVE
→ generate fresh telemetry
→ verify fresh data visible
→ verify tenant/cluster/resource scope
→ verify semantic mapping
→ stop old writer
→ stop old reader
→ remove old writer adapter
→ remove old reader adapter
→ remove fallback / transition router
→ final verification
```

## Gate 6 完成判据（必须全部满足）

```
new writer ACTIVE
new reader ACTIVE
old writer ABSENT
old reader ABSENT
old active adapter ABSENT
production fallback ABSENT
old physical historical data PRESENT BUT UNREACHABLE
```

## 例外

除非发现真实 architecture conflict、destructive authorization requirement，或无法满足 Gate 的环境 blocker，否则不得偏离上述 checkpoint 顺序或跳过 ABORT/ROLLBACK 行为。
