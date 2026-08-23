# AIOps V9.2 — Multi-Cluster Architecture (frozen target)

Status: **FROZEN TARGET / NOT_YET_IMPLEMENTED** (Phase 2 freezes; implementation lands Phase 3+).

## Cluster identity (V9.2 §9)

- `cluster_id` = immutable canonical UUID (all DB FK / labels / resource / evidence / run / action / audit).
- `slug` = globally unique human-readable reference, regex `^[a-z][a-z0-9-]{1,62}[a-z0-9]$`.
- `name` = mutable display name.
- Never use slug/name/K8s UID/kube-context/endpoint/array index/`id=1` as canonical id.
- Cluster delete+re-register → new UUID even if slug reused.

`clusters` fields: cluster_id, slug, name, type, environment, region, status, version, capabilities, labels, credential_ref, created_at, updated_at, deleted_at.

## Tenant ↔ cluster ownership (V9.2 §6.3)

User N:M Tenant; Tenant 1:N Cluster. `tenant_clusters.cluster_id` unique; a canonical cluster belongs to at most one tenant at a time. Future shared clusters → `cluster_access_grants`, not data duplication.

## Multi-cluster Run (V9.2 §23)

- single_cluster: exactly 1 cluster, primary_cluster_id = that cluster.
- multi_cluster: ≥2 clusters, primary_cluster_id NULL. Used for comparison/investigation only.
- Write actions only from a derived single-cluster remediation run (never inside multi_cluster run).

## Strong isolation (V9.2 §24)

- ai_tool_runs / ai_evidence / ai_hypotheses / ai_actions / ai_verifications carry cluster_id NOT NULL.
- No single Tool queries A+B; no Evidence belongs to multiple clusters; no Hypothesis mixes A/B evidence.
- Cross-cluster comparison = Cluster A investigation + Cluster B investigation + authorized server-side comparison.
- TrustedRequestContext is per-cluster (one context, one cluster). No `cluster=*` / `allowed_clusters=[...]` internal wildcard.

## Kubernetes Access Boundary (V9.2 §18-19)

- Read: Agent → Tool Registry → InternalQueryClient → query-api → Kubernetes Read Adapter → ClusterClientManager → K8s API.
- Write: OpsAction → Execution Policy Engine → MySQL Authz → Confirmation/Approval → Execution Adapter → ClusterClientManager → K8s API.
- Agent/Planner/Tool never create K8s client, load kubeconfig, kubectl, read Secrets, or hit cluster API directly.
- ClusterClientManager cache key = canonical cluster_id; invalidate on credential_ref change / rotation / disabled / deleted; no cross-cluster client reuse.
- credential_ref: per-cluster Secret, MySQL stores `k8s-secret://<ns>/<secret-name>`, preferred key `kubeconfig`; only Kubernetes Access Boundary resolves.

## K8sGPT (V9.2 §20)

K8sGPT is a Tool of Kubernetes Agent. Temp kubeconfig created only by Kubernetes Access Boundary, 0600, single canonical cluster, deleted after, no logging, no propagation to orchestrator; ToolResult returns structured diagnostics. Offline artifact in `ai-orchestrator/bin/k8sgpt`.

## Second cluster (V9.2 §69)

Local acceptance runs the existing local cluster + a kind cluster `aiops-kind-02` created in Phase 3.

## Implementation status

```text
Cluster registry (UUID/slug/name): PLANNED (Phase 3)
Kind aiops-kind-02:                PLANNED (Phase 3)
credential_ref / Secret:           PLANNED (Phase 3)
ResourceResolver:                  PLANNED (Phase 3, current prod code = PHASE3_PENDING_MIGRATION)
ClusterClientManager:              PLANNED (Phase 3/5)
```

---

# 更新：V9.3 当前实现状态（Phase 21 P21.1，2026-08-23）

## 已实现（与真实运行代码一致）
- **Cluster Registry**：canonical cluster `91771a6e-9c2d-11f1-8271-bea176fe9f9f`（主）+ kind-02 `84f7e5a3-f54c-58ae-a292-3f70e935ea4a`（identity_uid `ea994341-a547-488b-9d4a-1973b102f766`）注册 ready。clusters/tenant_clusters 权威表。
- **P19 双集群接入**（真实，orbstack + kind-02）：
  - 中心数据面（VM/VLogs/CH 共享），kind-02 不建第二套 SoT。
  - 中央凭据：kubeconfig-orbstack/kubeconfig-kind-02 Secret + ADMIN_KUBECONFIG env；k8sboundary SA 永不过期 token（Secret 型）。
  - 采集收缩：kind-02 经中央受控 OTLP generator（ingest-kind02，CLUSTER_ID=84f7e5a3）注入中心 VLogs。
  - **隔离 Gate**：`internalScopeAuthorized`（query-api `/internal/v1/query/*`）校验 cluster 属 tenant；错误 tenant/cluster/capability → 403；已授权空 → no_data。
  - 跨 cluster RCA：EvidenceScopeMismatch 阻断（RcaEngine）。
- **Kubernetes Access Boundary**：Agent → Tool Registry → InternalQueryClient → query-api `/internal/v1/query/kubernetes` → k8sboundary（KubePods/KubeNodeDetails 只读）；orchestrator 走内部边界，不直接持 kubeconfig。
- **K8sGPT**：P19.7 安全注入（tmpfs 0600 临时配置，无命令行实参/无持久化）。

## 边界
- 跨集群写（kind-02 本地采集→中心 CH）不可行（ClusterIP only），收缩为中央 OTLP generator；kind-02 本地采集列为后续。
- two-cluster isolation 为外部依赖（P19 B v0.2 已验证）。
- 红线 F1-F5 保持；Execution Production Execution NOT APPROVED；GIT_ACTION=NONE。
