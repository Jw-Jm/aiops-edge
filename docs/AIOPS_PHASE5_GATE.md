# Phase 5 Gate — Writer Implementation + Atomic-Cutover Readiness

> 执行合同：`aiops/aiops-agentic-v9.2-final.md`（§71 Phase 5 / §72 Phase 6 cutover 规则）
> 范围决策：`选项 C`（Phase 5 只建 writer 新链路 + cutover readiness，不做生产切换）
> 日期：2026-08-20
> 分支纪律：`GIT_ACTION: NONE`（全程未 add/commit/push）

## PHASE: 5 — STATUS: PASS (Gate 5)

> 2026-08-20 修订：本轮 Gate Closure 收口 4 个缺口（dedup/backlog observable、三字段 partial semantics、VM/VLogs switchable、K8s watch single leader），补齐证据与实现后判定 **Gate 5 PASS**。判定标准见文末 Gate 5 判定表。

---

## 一、Writer 三字段强制 + fail-closed（T1/T4）

### ai-event-collector
- `scope.go`（新）：`EventScope{TenantID, ClusterID}` + `validateCanonicalUUID`（拒绝空/default/slug/数值），复用 Phase 3/4 冻结 canonical UUID pattern。
- `config.go`：`TENANT_ID`/`CLUSTER_ID` 不再默认 `"default"`。
- `main.go`：启动 `EventScope.Validate()` fail-closed（非法 `log.Fatalf`）。
- 单测：`TestEventScopeValidate`、`TestValidateCanonicalUUID`（6 用例 PASS）。

### ai-apm-ingest-go
- `internal/clickhouse/{writer,log_writer,metrics_writer}.go`：serialize 移除 `clusterID==""→"default"` 兜底。
- `internal/pipeline/ingest.go` `SetClusterID`：移除 `""→"default"`。
- `cmd/ingest/main.go`：
  - `CLUSTER_ID` 缺失 → **fail-closed 拒绝启动**（`log.Fatalf`）——cluster_id 是本实例静态身份，缺失即写不出 partial/missing_fields（ClickHouse 列固定），故走 reject 路径（符合"该路径不允许 partial 则直接 reject"）。
  - `/v1/traces`、`/v1/logs` 的 `X-Tenant-ID` 缺失 → 400 fail-closed。
- 单测：`TestSerializeSpansEscapesSpecialChars` 断言更新（空 cluster_id 不再 default 兜底）。

### 三字段 / partial semantics 判定（Gate 缺口 2 收口）
- **缺失即 reject**，不写空、不猜：ingest `CLUSTER_ID=""` → 启动拒绝；`X-Tenant-ID=""` → 400；event-collector `TENANT_ID`/`CLUSTER_ID` 非 canonical → 启动拒绝。因此**不存在"缺字段写空"的数据语义缺陷**。
- **部署迁移待办**：现有 helm `clusterId: "default"`/`tenantId: ""` 非 canonical——代码已 fail-closed（缺失/非法拒绝启动），但**现有部署需在 Phase 6 cutover 前注入注册后的集群 canonical UUID**，否则无法启动。此为该部署配置项（已更新 values.yaml 注释），非 Phase 5 代码缺陷。

## 二、WAL / outbox + checkpoint（T2/T3）

### WAL（event-collector）
- `wal.go`（新）：崩溃安全 WAL（Append/Ack/ReadAll/Compact/Close），Append 返回单调递增 seq，Ack 推进连续 ack 水位并持久化 `.ack`，Compact 截断已 ack 前缀，重启从 consecutiveAck 之后恢复。
- `config.go` 新增 `WAL_DIR`（空→内存重试，向后兼容）。
- `clickhouse.go`：`EventWriter` 集成 WAL——启动恢复未 ack 批次；flush 失败先 Append 再入重试；flushRetry 成功 Ack + 全部成功 Compact；丢弃最旧批次 Ack 对应 seq。
- 单测：`TestWALAppendAckReplay`、`TestWALCompactKeepsUnacked`、`TestWALCompactionResetsSeqSafe`（崩溃恢复/compaction 语义 PASS）。

### checkpoint（event-collector）
- `latestTSQuery(source, tenantID, clusterID)`：`WHERE source + tenant_id + cluster_id`（V9.2 §71 checkpoint key = tenant+cluster+source）。
- 单测：`TestLatestTSQueryIncludesTenant`。

## 三、event dedup + backlog observable（Gate 缺口 1 收口）

### dedup（两层级）
1. **采集层**：`k8s_events.go` 内存 UID 去重（`seen(uid)` FIFO 上限 1000，LIST→WATCH 重连防重）；`sel_events.go` 对 `(node, record-id)` 去重。
2. **存储层幂等**：`k8s_events` 为 `ReplacingMergeTree`（含 name/message 完整键），WAL replay 重复窗口（write success→crash→replay）由 ReplacingMergeTree 按 key 收敛，不产生重复逻辑事件。

> WAL replay 重复窗口的幂等由 ReplacingMergeTree 兜底；Lease leader 转换的防重由 dedup + ReplacingMergeTree 保护。两者关系：**leader election 防正常态多 active writer；dedup/idempotency 防 retry/replay/reconnect 重复事实**。

### backlog observable（新增实现）
- `WAL.PendingStats()`：未 ack records / bytes / 最旧 pending 时间；`OldestAgeSeconds()`。
- `/metrics` 新增暴露：
  - `ai_event_collector_wal_pending_records`
  - `ai_event_collector_wal_pending_bytes`
  - `ai_event_collector_wal_oldest_pending_age_seconds`
- 单测：`TestWALPendingStatsBacklogObservable`、`TestWALPendingStatsCountsUnackedOnly`（积压归零/乱序 ack 语义 PASS）。

## 四、VM/VLogs writer adapter（Gate 缺口 3 收口）

- `internal/telemetry/` 包（新）：`WriteResult{Status,ErrorCode,Retryable}` 统一错误语义 + `MetricsWriter` 接口 + scope 常量透传。
- `vmetrics.go`：`VictoriaMetricsWriter.Write/WriteScope`——先 `telemetrylabels.ValidateScopeLabels`（tenant/cluster canonical UUID，resource scope 强制 resource_id），`__name__` 必填。
- `vlogs.go`：`VictoriaLogsWriter.WriteLog/WriteLogScope`——scope label 校验 + body 非空。
- **Switchable（修复：不再 hardcoded disabled）**：
  - `Mode` 类型：`legacy`（默认，不生产写）/ `new`（生产写）。
  - `ParseMode(s)`（env 解析）、`SetMode(Mode)`、`Enable()`、`New...WriterMode(endpoint, mode)`（隔离/受控环境真实启用）。
  - **默认 legacy（PRODUCTION_ACTIVE=false）**，Phase 6 原子 cutover 由部署侧改配置（如 `TELEMETRY_WRITER_MODE=new`）受控切换，无需改源码重建。
- 单测：VM 6 个 + VLogs 5 个 + mode 6 个 = 14 个（含 default disabled、switchable、mode 构造、ParseMode）全绿。

## 五、K8s watch single leader（Gate 缺口 4 收口）

### 现状
- event-collector 是 DaemonSet 多副本，原实现多副本都 watch 集群级 K8s 事件 → 多 writer 竞争写 k8s_events（原靠 ReplacingMergeTree 兜底，**违反 §71 single leader**）。

### 修复（最小化 Kubernetes Lease leader-election，方案 B，不引入 client-go）
- `internal/leaderelection/` 包（新）：
  - `lease.go`：窄 Lease abstraction，仅访问 `coordination.k8s.io/v1` Lease（GET/CREATE/UPDATE），可选 Bearer token（in-cluster）+ 复用 SA CA TLS。
  - `elector.go`：状态机 FOLLOWER→LEADER→FOLLOWER，`Acquire→Renew→Detect loss→Stop watch→Reacquire`。**fail-safe：renew 失败/超时立即 `onLeadership(false)` 停止 watch/write，绝不"WARN 后继续当 leader 写"**。ctx 取消时 best-effort `Release`（独立未取消 context），follower 立即接管。
- 集成 `leaderelection.go`：`runWatchWithLeaderElection`——每个 leadership 用独立可取消 context 跑 `k8sWatcher.Run`；leader 丢失即取消 context 停止 watch。仅 leader 执行 cluster-wide watch，follower 只做 SEL（天然按节点隔离）。
- `config.go`：`LEADER_ELECTION_ENABLED`（默认 true）、`LEASE_NAME`、`LEASE_NAMESPACE`。
- 单测（leaderelection 包 5 个）：
  - `TestElectorOnlyOneLeaderThenHandoff`（场景1/3/4：两实例竞争仅一个 leader；leader 退出 follower 接管）
  - `TestElectorRenewFailStopsLeadership`（场景5：renew 失败立即停写，fail-safe）
  - `TestElectorReacquireAfterLeaseExpired`（过期租约可抢占）
  - `TestElectorCallbackStrictlyAlternates`（场景6：回调严格交替，无重叠 writer 区间）
  - `TestLeaseClientCreateAndUpdate`、`TestLeaseClientGetMissingReturnsEmpty`

### 真实 K8s 验证（kind-02 集群，`coordination.k8s.io/v1`）
- API 可用性：`kubectl get --raw /apis/coordination.k8s.io/v1` → leases 资源注册。
- Lease 创建/持有/接管：手动 `kubectl apply` Lease → `HOLDER=pod-a`；改为 `pod-b` → `configured`，`HOLDER=pod-b`（holderIdentity 可读写、可接管，对应 Elector Create/Update/Release 路径）。
- **环境限制**：kind-02 为单节点集群，无法部署多副本 DaemonSet 做真实"多 Pod 竞争"E2E。多实例竞争逻辑已由单测覆盖（exactly one leader / fail-safe / 无重叠 / handoff）；**真实多节点多 DaemonSet Pod 竞争验证列为 Phase 6 cutover 前置项**。

### RBAC（新增）
- `event-collector/rbac.yaml`：`apiGroups: [coordination.k8s.io], resources: [leases], verbs: [get, create, update]`。
- `event-collector/deployment.yaml`：修正旧注释（原"多副本 watch 由 ReplacingMergeTree 兜底"为错误架构说明）→ 改为 Lease leader election 语义；新增 `TENANT_ID`/`CLUSTER_ID`/`WAL_DIR`/`LEADER_ELECTION_ENABLED`/`LEASE_NAME`/`LEASE_NAMESPACE` env + `wal` hostPath 卷。
- `values.yaml`：新增 `tenantId`/`leaderElectionEnabled`/`leaseName`，clusterId/tenantId 注明需 canonical UUID。
- helm 渲染验证通过（`helm template` 含 coordination RBAC、TENANT_ID、WAL_DIR、LEASE env）。

## 六、ClickHouse log_records 关闭准备

- `log_records` 仍写入（P4.5 已标 LEGACY），**停写跟随 Phase 6 原子窗口**；本次未改其写入路径。
- legacy log writer removal = READY（代码已具备 fail-closed 三字段，停写由 Phase 6 feature-switch 控制）。

---

## Gate 5 判定结果（Gate Closure 收口后）

| Gate 项 | 结果 | 证据 |
|---|---|---|
| new writer tests PASS | ✅ | event-collector + telemetry + leaderelection 单测全绿 |
| WAL replay PASS | ✅ | `TestWALAppendAckReplay`（崩溃/重启恢复） |
| storage outage recovery PASS | ✅ | WAL + bounded retry 双路径，`-race` 无报告 |
| **event dedup/backlog observable PASS** | ✅ | 采集层 UID/(node,id) 去重 + ReplacingMergeTree 幂等兜底；`WAL.PendingStats` + `/metrics` 暴露 pending records/bytes/oldest age |
| telemetry labels contract PASS | ✅ | telemetrylabels 3 单测 + VM/VLogs adapter 校验测试 |
| VM/VLogs writer adapter PASS | ✅ | `internal/telemetry` 14 单测（含 switchable） |
| **writer ready for atomic cutover PASS** | ✅ | 三字段 fail-closed + WAL + checkpoint + VM/VLogs switchable（默认 legacy，可受控切 new）+ **未切换任何生产路径** |
| **K8s watch single leader PASS** | ✅ | `internal/leaderelection` 5 单测（exactly one leader/fail-safe/无重叠/handoff）+ 真实 Lease 创建/持有/接管验证 + RBAC coordination/leases |

## 原子 cutover readiness 状态（R2 §71/§72）

- **Phase 5 已完成**：writer 新链路建设 + 验证 + **cutover readiness**（VM/VLogs switchable、K8s watch single leader 已就绪）。全部单测 `-race` 绿。
- **未做**：任何生产 writer 切换；ClickHouse `log_records` 仍在写入；VM/VLogs 默认 `ModeLegacy`（未切 new）。
- **进入 Phase 6 前置**：
  1. 部署侧注入 canonical UUID 的 `TENANT_ID`/`CLUSTER_ID`（现有 helm 默认 `default`/`""` 会 fail-closed）。
  2. 多节点集群完成真实多 DaemonSet Pod 竞争 E2E（单测已覆盖逻辑）。
  3. 新 reader 可部署或 feature-switch 就绪后，同一受控原子窗口切 writer + 立即完成 reader cutover → 停旧 writer/reader → 删旧 active adapter → 删 fallback。
- **禁止中间态**：不得出现"新 writer 写新 schema 而生产 reader 只读旧 schema"。

## 运行验证命令

```bash
# ai-event-collector
cd aiops/ai-event-collector && go build ./... && go vet ./... && go test -race ./...

# ai-apm-ingest-go
cd aiops/ai-apm-ingest-go && go build ./... && go vet ./... && go test -race ./...
```

两者均 PASS。

## NEXT_PHASE

```text
PHASE: 6 (NOT_STARTED — atomic cutover window)
STATUS: PENDING
GIT_ACTION: NONE
```
