# Plan: A1-D Runtime Convergence (2026-08-24)

按《AIOps_全面代码修改报告》§27.22 任务固定拆分 `A0 -> A1 -> A2 -> B1 -> B2 -> C -> D` 实施。A0 已在上一 plan 完成（见 2026-08-24-a0-runtime-convergence.md）。

## Implementation Status (2026-08-24)

### A1：现有 Control Plane 增加并发权威（COMPLETE，代码 + 真实 MySQL 集成测试）
- **A1-01**：`0004_runtime_convergence.sql` 一次成型（ai_runs lease/runtime_wait 列 + ai_runtime_commits + ai_run_claims + ai_context_replay_guard + control_commands + tool_runs + outbox fencing）。已在 A0 应用到生产 MySQL。
- **A1-02 Lease DAO + claim/renew + claim history**：
  - `store/ai_run_lease.go`：`RuntimeLeaseDAO.Claim/Renew/Release/LeaseExpired/GetRuntimeMetadataTx/ScanRecoveryCandidates` + `LeaseFencingTx`。
  - Claim 原子抢占（FOR UPDATE）+ epoch 递增 + 随机 token（只存 SHA256 hash）+ DB-time 过期判定 + retry backoff 拒绝 + claim 历史写 `ai_run_claims`。
  - 端点：`/internal/v1/control-plane/runs/{id}/claim|renew|release`（capability=control_plane.runs.mutate，run-scoped，fencing）。
- **A1-03 Runtime Commit + AppendTx + commit-result retention**：
  - `store/ai_runtime_commit.go`：`RuntimeCommitDAO.Get/CreateTx`（幂等，同 (run_id,commit_id) → ErrCommitDuplicate）。
  - `store/ai_run_events.go`：`AppendTx`（owner-lock 顺序 + event_id 幂等去重，供 commit 同事务原子追加）。
  - 端点：`/internal/v1/control-plane/runs/{id}/commit`（幂等命中返回首次 response_json；事务内 Lease fencing + Run CAS + 事件 AppendTx + commit 记录原子）。
- **A1-04 Recovery snapshot/runtime metadata**：recovery snapshot 增加 `lease` 元数据（owner/epoch/claim/token_hash/wait_kind/retry）。
- **真实 MySQL 集成测试**：`store/ai_lease_commit_integration_test.go`（`//go:build integration`）对真实 MySQL 验证：双 executor claim → 单 owner、同 owner 幂等、renew 正确/错误 token、commit 幂等 CreateTx → ErrCommitDuplicate。**PASS**。

### A2：Replay 与恢复（COMPLETE）
- **A2-01 Shared TrustedRequest replay guard + /internal/v1/security/replay/consume**：
  - `api/mysql_replay_cache.go`：`MySQLReplayCache`（MySQL `ai_context_replay_guard` 表，PK issuer+audience+nonce，INSERT IGNORE 原子消费，跨 Pod 共享）实现 `trustedauth.ReplayCache`。
  - `cmd/api/main.go`：生产 verifier 用 `NewMySQLReplayCache` 替代 `InMemoryReplayCache(4096)`。
  - `api/security_replay.go`：`SecurityReplayConsume`（system principal + capability=control_plane.replay.consume），路由 `/internal/v1/security/replay/consume`。
- **A2-02 Recovery Scanner + fencing/response-loss tests**：
  - `ScanRecoveryCandidates`：只返回无活跃 Lease / 不在 retry backoff 的非终态 Run（有活跃 Lease 的 Run 由当前 owner 继续，避免双 executor 抢）。
  - `/runs/unfinished` 改用 `ScanRecoveryCandidates` + 返回 `recovery_candidates` 标记。
  - 测试更新：`TestControlPlaneRunUnfinishedGlobalCapability` 匹配新查询。

### B1：internal query 增强 ToolRun（COMPLETE）
- **B1-01 ai_tool_runs data-quality/time-window/result-limit expansion**：
  - `store/ai_tool_runs.go`：`AIToolRun` 加 14 个 B1 字段 + `CreateWithQuality/UpdateQuality/MarkEvidenceConsumed/IsEvidenceConsumed/GetByIdemKey`。
- **B1-02 InternalQuery ToolRun wrapper + ToolResultEnvelope**：
  - `api/toolrun_wrapper.go`：`ToolResultEnvelope`（quality complete/partial/failed + truncated + digest + absolute window + source_errors）+ `MaxToolResultBytes` 服务端硬上限 + `execToolQuery` 统一包装器。
  - `internalQueryRequest` 加 tool_run_id/idempotency_key/executor_id/lease_epoch/query_window_start/end。
  - **全部 8 个 handler**（metrics/logs/traces/alerts/topology/kubernetes/changes/knowledge）已接入 `beginToolRun/endToolRun`/`execToolQuery`（ToolRun 审计/幂等/Lease 边界）。K8s 保留 partial/all-fail 语义。
  - 测试更新：`TestInternalQueryChangesSuccess`/`TestInternalQueryKnowledgeSuccess` 断言 ToolResultEnvelope（quality+data.total）。
- **B1-03 ToolResult → Evidence atomic consume**：DAO 提供 `MarkEvidenceConsumed/IsEvidenceConsumed`（一次消费，防重复转 Evidence）。

### B2：Lease-aware main loop + Chat/Investigation 拆分（COMPLETE）
- **B2-01 Lease-aware main loop（COMPLETE）**：`lease_aware_execution.py` — `LeaseAwareExecutor`（Claim→execute→renew loop→Commit 包装；epoch+token fencing；Lease 不可达 fail-closed 不执行无保护 Run）；`ControlPlaneClient` 加 `claim_lease/renew_lease/release_lease/commit` 方法。测试：`test_lease_aware_execution.py` 3 passed。
- **B2-02 移除推断 tool event**：orchestrator 源码确认无"基于 agent 输出推断 ToolRun"逻辑（已满足）。
- **B2-03 Chat 拆分（COMPLETE，F-07 核心）**：`node_chat_classify` + `_needs_investigation`（调查词→investigation_required CTA 不采集；纯对话→chat_pure 跳过 heavy collect）。

### Stage C：Trace SoT + 角色拆分 + Alert Leader + LLM egress + TLS（COMPLETE）
- **C-01 ClickHouseSpanSink（COMPLETE）**：`ai-apm-ingest-go/internal/tracesink/` — 用 CH HTTP 接口写 `trace_spans`（零新增依赖，P14 删除 CH 后重建）；`span_dedup_key`（SHA256 tenant|cluster|trace|span|start_ns）幂等去重；readiness fail-closed（sink 不可用 → /health 503）；helm migration `0002_trace_span_dedup.sql` 加列。测试：3 passed（dedup 确定性/HTTP 写/fail-closed）。
- **C-02 query-api 三角色拆分（COMPLETE）**：`cmd/api/main.go` 加 `--role`（api/run-dispatch/alert-eval）；`api`=HTTP+dispatch+alert；`run-dispatch`=outbox 循环；`alert-eval`=alert 循环。
- **C-03 Alert 单 Leader + MySQL cooldown（COMPLETE + 真实 MySQL）**：`store/alert_runtime_state.go` — `AlertEvalLeaderDAO`（MySQL lease，单 Leader）+ `AlertRuleRuntimeStateDAO`（cooldown/dampening 持久化，进程内 map 仅缓存）；0005 migration；alert_engine 集成。**真实环境验证**：单 Leader 生效（holder=query-api-*，epoch=2）、真实告警评估 firing→resolved。
- **C-04 LLM egress proxy（COMPLETE 代码 + helm）**：新增 `ai-llm-egress-proxy/` 独立服务（allowlist deepseek/openai + PROXY_TOKEN auth + provider key 只存 proxy）；helm deployment + NetworkPolicy（orchestrator→proxy 入站 + proxy→LLM 出站）+ values。测试：3 passed。
- **C-05 K8s TLS（COMPLETE）**：`K8S_INSECURE_SKIP_VERIFY` 默认 false（production fail-closed）+ startup WARN；helm `k8sInsecureSkipVerify: "false"`。

### Stage D：ai-action-executor（COMPLETE 代码 + helm + 真实边界确认）
- **D-01 独立 ai-action-executor**：新增 `ai-action-executor/` 独立服务（唯一真实 mutation 边界）；`EXECUTION_MODE=disabled|manual|approved`（默认 disabled，不做真实 mutation）；Orchestrator `grantK8sWrite=false`（无生产写凭据，F-22）；helm deployment + NetworkPolicy（query-api→executor 入站）。
- **D-02 Approval/action 幂等 + TOCTOU + scoped credential**：`readCurrentState` TOCTOU（执行前重读 UID/resourceVersion 防漂移）；action_hash 绑定 immutable action；CredentialRef 经 Credential Broker。
- **D-03 execution_unknown reconcile-before-retry**：`handleReconcile` 先读目标实际状态再决定，禁止盲 retry。测试：4 passed（disabled 拒绝/缺 action_hash/TOCTOU clean/reconcile-before-retry）。

## 验证（本机真实环境 + 全量测试）
- **单元/全量**：query-go 10 包 PASS、ingest-go 6 包 PASS、ai-action-executor 1 包、ai-llm-egress-proxy 1 包、frontend tsc exit 0、helm lint 0 failed、orchestrator lease test 3 passed。
- **真实 MySQL 集成**：`TestA1LeaseCommit`（Lease claim/renew/fencing + commit 幂等）PASS、`TestC3AlertLeader`（单 Leader + state 持久化）PASS。
- **真实环境部署验证（orbstack）**：0005 migration 应用生产库；新 query-api（a1d-runtime）部署 Ready；A1 claim/renew/commit 真实验证（claim 404/200、renew 正确 200/错误 409 fencing、commit 200 状态推进+事件追加+幂等、claim 双 owner 409 HELD）；A2 replay consume（首次 200/重放 409 CONTEXT_REPLAYED）；B1 ToolRun wrapper（ai_tool_runs 写入 quality/lease、幂等重放 200）；C-03 alert 单 Leader + 真实告警 firing→resolved。
- **红线隔离**：新增核心文件 grep execute/credential/kubeconfig 0 match（ai-action-executor 的 credential 是合法 Credential Broker 边界）。

## 8 项遗漏补齐（2026-08-24 第三轮，全部完成）

**T1 27.13 Tool Reconciler（COMPLETE + 真实 MySQL）**：`ai_tool_runs.go` `ScanExpiredRunning`（deadline<DB_NOW 无反向锁）+ `ConvergeToolRun`（统一锁序 Run→ToolRun，recheck 仍 running 才置 timeout/failed_unknown，eligible=0）；`api/tool_reconciler.go` `RunToolReconcilerLoop`（30s 周期，收敛 append tool_run.timeout event）；main.go 在 run-dispatch/api role 启动。真实 MySQL 集成测试 PASS。

**T2 27.14 Evidence 一次消费（COMPLETE + 真实 MySQL）**：`store/ai_evidence.go` `EvidenceDAO.ConsumeToolRunAsEvidence`（同事务锁 ToolRun + recheck 终态成功/eligible/未消费/同 run/tenant/cluster → 创建 ai_evidence + 同事务标记 evidence_consumed_at；不跨 epoch/终态进入 Evidence）；端点 `/internal/v1/control-plane/tools/{id}/evidence/consume`（capability=control_plane.evidence.consume）。真实 MySQL 集成测试 PASS（已消费拒绝/跨 cluster 拒绝）。

**T3 27.18 Trace 主链接线 + SpanSink=nil 门禁（COMPLETE）**：ingest main.go 已接 ClickHouseSpanSinkAuth（CLICKHOUSE_USER/PASSWORD）；`TRACE_SOT_MODE=required` 时 SpanSink=nil → 拒绝启动（fail-closed）；sink 写入失败 readiness 503。真实 CH 写链已验。

**T4 27.16/27.17 Orchestrator main loop + Chat/Investigation 闭环（COMPLETE）**：run-invocations 在 QUERY_API_URL（production）下用 `LeaseAwareExecutor` 包裹执行（claim→renew→commit，LeaseAcquireError→409 RUN_LEASE_UNAVAILABLE），本地/单测不引入；前端 AiChat 识别 `__investigation_required__` CTA → "创建结构化调查"按钮跳转 /investigation/new（createRun 接线）。测试 26 passed。

**T5 27.19/F-14 LLM key 迁移 proxy（COMPLETE）**：orchestrator `_fetch_saved_llm_config` 用 `signed_query_api_request`（capability=llm.config.read）拉取；配 LLM_PROXY_URL 时返回 proxy 基址且**不持有外部 key**（key 由 proxy 独占）；query-api `/settings/llm/internal` 升级为 TrustedRequestContext + `llm.config.read` capability（不再仅凭共享 token）。测试 18 passed。

**T6 平台自观测 metrics + correlation IDs（COMPLETE）**：`api/control_plane_metrics.go`（Lease/Commit/Outbox/Tool/Replay/Recovery/SSE/LLM/Alert/correlation 原子计数器）+ PrometheusMetrics 追加输出；各 handler 接入 cp.inc。不泄露 secret（correlation 只计数不透传值）。

**T7 27.20 RWO/Checkpointer + 27.12 Tool late/fencing（COMPLETE）**：27.20 确认 Run correctness/recovery 不依赖 AsyncSqliteSaver（PersistentRunRepository 满足，无需删除）；27.12 `FinishToolRunWithFencing`（统一锁序，Run 终态或 epoch 不匹配 → late/eligible=0 + TOOL_RESULT_LATE，否则 eligible=1）。真实 MySQL 集成测试 PASS。

**T8 部署验证（PARTIAL，诚实）**：SBOM 脚本 `deploy/scripts/sbom.sh`（镜像 digest + 依赖摘要，归档 docs/AIOPS_IMAGE_SBOM.md）；prod values 已有 `llmMock: "false"`；MySQL backup cronjob 存在；egress default-deny 灰度中（event-collector），最终 `egressDefaultDeny: true` 注释目标（集群不稳定暂缓）；internal TLS/mTLS 未配置（需证书基建）；**MySQL 真实 failover RPO=0 标记 BLOCKED_BY_ENV**（单节点）。

## 边界处理（2026-08-24 第二轮，全部解决）

**orchestrator 测试环境冲突 → 解决**：安装 Python 3.14.3（install_binary），创建 `.venv314`（清华源装 langgraph>=1.0 + fastapi/pydantic/cryptography/pymysql/prometheus_client 等）。`from langgraph.store.base` 冲突完全消除。**orchestrator 全量测试 1126 passed, 1 skipped**。B2-03 修复：`node_chat_classify` 改为"仅明确结构化调查（发起调查/完整根因分析）→ CTA；纯闲聊 → chat_pure；普通诊断 → 正常 Chat（保留 exec_context）"，修复 `test_stream_sync_initial_injects_exec_context` 回归。

**C-04 ai-llm-egress-proxy 部署 → 完成**：交叉编译 + 基于运行镜像构建镜像，部署 Ready。真实验证：无 token 403（PROXY_TOKEN 鉴权）、未配置 provider 404、带 token deepseek 转发到真实 API（401 Authentication Fails=占位 key，证明转发链 + key 注入工作）。Provider key 只存 proxy（掩码 ****lder），符合 F-14。

**D-01 ai-action-executor 部署 → 完成**：交叉编译 + 部署 Ready，`EXECUTION_MODE=disabled`。真实验证：healthz ok、execute 403 rejected（"real mutation not permitted"）、token 鉴权生效（无 token 403）。Orchestrator `grantK8sWrite=false`（无生产写凭据，F-22）。

**C-01 ClickHouseSpanSink 真实 CH 写链 → 完成**：修复 CH `start_time` 格式 bug（DateTime64(9) 不接受 Z 后缀 → 改 UTC 无后缀带纳秒格式）；sink 支持 CH basic auth（`NewClickHouseSpanSinkAuth`）；应用 `0002_trace_span_dedup.sql`（span_dedup_key + date_bucket）到生产 CH；真实 CH 集成测试 `TestCHSpanSinkReal` PASS（写入真实 trace_spans，幂等重写不失败，测试行已清理）。

**C-02 三角色拆分部署 → 完成**：部署独立 `--role=alert-eval` 角色（无 HTTP 服务，只跑告警循环）；与 api 角色争 MySQL Lease 单 Leader，验证只有 api 角色（query-api-*）持有 Leader，alert-eval 正确等待（无重复评估）。验证后删除独立角色，恢复生产单 api 角色（仍持有 alert Leader）。

## 验证（最终全量）
- query-go 10 包 PASS、ingest-go 6 包 PASS、orchestrator **1126 passed**、frontend tsc exit 0、helm lint 0 failed。
- 真实环境：C-01 真实 CH 写链、C-02 三角色单 Leader、C-04 proxy 转发+鉴权、D-01 executor disabled 拒绝、A1 lease/commit/replay 全部验证通过。
- 生产部署：query-api / ai-action-executor / ai-llm-egress-proxy 全 Ready。
- 红线隔离：新增核心文件 grep execute/credential/kubeconfig 0 match；红线 F1-F5 保持；Execution Production Execution=NOT YET APPROVED；GIT_ACTION=NONE（未 commit）。
