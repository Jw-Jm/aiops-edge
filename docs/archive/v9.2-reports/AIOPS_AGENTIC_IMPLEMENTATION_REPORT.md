# AIOps V9.2 — Agentic Implementation Report

Status: **Phase 3 PASS** (P3.1–P3.10c COMPLETE, P3.10c-final Cluster Credential Identity Binding COMPLETE incl. registration-time identity discovery + real two-cluster negative E2E, P3.11 PASS, P3.12 COMPLETE). P0 Phase 3 defects: 0. LEGACY_ONLY_TEST_FAILURES: 2 (Phase 14). **Phase 4 PASS (Gate 4)** (see below; Object Store real bootstrap was the final blocker, now RESOLVED via real MinIO endpoint). **Phase 5 PASS (Gate 5)** (writer implementation + atomic-cutover readiness; Gate Closure 2026-08-20 收口 4 缺口后判定 PASS：dedup/backlog observable、三字段 partial/reject semantics、VM/VLogs switchable、K8s watch single leader via Lease。PRODUCTION_ACTIVE=false，未切生产；见 `docs/AIOPS_PHASE5_GATE.md`). Phase 6 allowed (atomic cutover window).

> P3.10c-final (2026-08-20): P3.10c originally recorded the identity mismatch as "detected at the test layer" (routed to the wrong node). Under review that was NOT a closed boundary, so a P0 hardening was performed: the Kubernetes Access Boundary now **actively fails closed** on `credential → cluster` identity mismatch using the authoritative kube-system Namespace UID binding. Two follow-up P0 items were closed on review: (1) registration-time identity discovery is now a production `ClusterRegistrar` path (caller cannot forge the UID); (2) a real two-cluster negative E2E proves the production `SecretResolver` + `kubectlIdentityReader` fail closed on mismatch. See P3.10c-final below.

## Phase 3 (P3.1–P3.12)

### P3.1 Contract errata
- EdDSA amended as the official V9.2 context signing algorithm (INTERNAL-AUTH-P0-011): JWS `typ=AIOPS-CONTEXT`, per-direction independent Ed25519 keypairs; verifier holds only opposite public key.
- `RoleScopeAssignment` logical entity → physical table `scope_assignments` (evolved in place, no parallel table).
- `tenant_clusters` added to express Tenant 1:N Cluster ownership.
- `token_version`: single authoritative source (`user_sessions.token_version`), not a duplicated authority.

### P3.2 MySQL control-plane schema migration
- Added `tenant_clusters(tenant_id, cluster_id, created_at)` with `UNIQUE(cluster_id)` and `UNIQUE(tenant_id, cluster_id)`.
- `clusters` gained V9.2 §9 minimum fields: `type`, `capabilities`, `labels`, `deleted_at`.
- Added `EnsureTenantCluster` / `TenantClustersForCluster` / `RegisterCluster` (canonical write path).

### P3.3 JWT / Auth
- JWT carries and verifies `token_version` (authoritative source `user_sessions.token_version`). Mismatch → session invalidated.
- `resolveMySQLAuthorizationContext` and `AuthorizationDAO.Authorize` read `s.token_version` consistently.
- JWT stays minimal (sub/sid/iat/exp/iss/aud/token_version); no role/scope/permissions.

### P3.4 Three-context production primitives
- Go and Python both implement sign/verify for `RunInvocationContext` / `RunControlContext` / `TrustedRequestContext` (EdDSA, `typ=AIOPS-CONTEXT`, kid, issuer/audience, time, nonce replay, strict decode, type-probe to return `ErrWrongContextType`).
- Cross-language verified dynamically: Go sign → Python verify PASS; Python sign → Go verify PASS.

### P3.5 Service Identity + Helm
- Directional service credentials and directional Ed25519 keypairs provisioned via `aiops-secrets` (6 keys). Private keys mount only signer side; public only verifier side.
- Legacy single `RequestContext` protocol retained only as `PHASE3_TRANSITION_ONLY` until P3.9.

### P3.6 Cluster Registry
- `RegisterCluster` writes canonical UUID/slug/name/tenant ownership/credential_ref/type/capabilities/labels and records `tenant_clusters`.
- `ResolveRef` rejects non-UUID/slug/all/default; no numeric/id=1 fallback.

### P3.7 ResourceResolver
- Canonical resource ID no longer contains tenant: `service:<cluster_uuid>:<ns>:<svc>` etc. Tests updated; negative tests reject tenant-in-id and slug.

### P3.8 Cluster fallback removal
- Removed `id=1` / `kubernetes-cluster` / `current-context` automatic fallback in `clusterKubeconfig` / `clusterNodes` / dashboard resources. Old pages must pass explicit cluster or fail closed.

### P3.9 Production trust cutover
- **P3.9-A (orchestrator → query-api):** `internal_query.py` signs `TrustedRequestContext` (V2); query-api `internalRequestAuthorizationContext` verifies only `TrustedRequestContext` (V2). Bidirectional V2 trust closed.
- **P3.9-B1 (orchestrator RunInvocation ingress):** `POST /internal/v1/run-invocations` verifies service credential + EdDSA JWS + replay + `context_type=run_invocation` + body/scope consistency, builds `InvocationScope` (never legacy RequestContext), reuses the AI Chat business function. Multi-cluster → 422 (no first-cluster fallback).
- **P3.9-B2/B3 (query-api issuer):** `RunInvocationIssuer` signs RunInvocationContext (EdDSA, canonical cluster UUID); `ProxyAI` on `/api/v1/ai/chat` performs JWT+MySQL auth, canonical cluster resolution + tenant ownership, signs and forwards to orchestrator; other proxy routes stay fail-closed.
- **P3.9-C (RunControl):** `POST /internal/v1/run-controls/{cancel|stream|action_decision}` verifies RunControlContext with endpoint-bound operation. Contract defined/verified; full run state machine business active in Phase 10.
- **P3.9-D (legacy removal):** Deleted legacy `SignTrustedRequestContext` / `VerifyTrustedRequestContext` (Go) and `sign_trusted_request_context` (Python) plus their tests. V2 verifier rejects `typ=JWT` (verified inline). Production callers migrated to `ScopeView`.

### P3.10 Second cluster (aiops-kind-02)
- Pulled pinned `kindest/node:v1.35.0` (matching primary K8s v1.35.6) from the official registry (digest sha256:4613778f...269661), after the domestic mirror failed with cloudfront EOF.
- Created `aiops-kind-02` (v1.35.0, control-plane Ready).
- **Dual-cluster isolation verified at infrastructure level:**
  - Cluster A (orbstack): node `orbstack`, API `https://127.0.0.1:26443`, kube-context `orbstack`.
  - Cluster B (aiops-kind-02): node `aiops-kind-02-control-plane`, API `https://127.0.0.1:50943`, kube-context `kind-aiops-kind-02`.
  - Independent node identities + independent API endpoints + independent kube-contexts → two distinct canonical clusters.
- Canonical UUID registration into the Cluster Registry and per-cluster Secret provisioning occur at deployment (Phase 18); the infrastructure-level dual-cluster isolation evidence satisfies the P3.10 cluster-creation gate.

### P3.10b Application-Level Multi-Cluster Gate Closure
- Ran the real `store`/`biz` code paths against the running MySQL (port-forwarded) to prove the canonical multi-cluster chain. All 16 checks PASS:
  - Formal `RegisterCluster` registered `orbstack` (UUID_A) and `aiops-kind-02` (UUID_B); UUID_A != UUID_B.
  - `ResolveRef` returns the canonical UUID for each slug.
  - `tenant_clusters` ownership established for both clusters.
  - Independent `credential_ref` per cluster (reference only, no secret content).
  - Same-name resource isolation: `service:<UUID_A>:default:orders` != `service:<UUID_B>:default:orders` via `ResourceResolver`.
  - Wrong scope fails closed: unknown / `all` / numeric / `default` / unregistered cluster all rejected (no orbstack/current-context/first-cluster/kubernetes-cluster fallback).
- `ResolveRef` hardened to reject `default` explicitly (V9.2 §9).
- Test data cleaned up after verification; no residual test clusters in the acceptance DB.

### P3.10c CredentialRef Runtime Resolution Gate
- Implemented the **Kubernetes Access Boundary** (`internal/k8sboundary`): `SecretResolver` resolves `credential_ref` (`k8s-secret://<ns>/<name>`) → Kubernetes Secret `kubeconfig` key; `ClusterClientManager` resolves and caches a kubeconfig per canonical `cluster_id` (no cross-cluster reuse). Added `ClusterDAO.GetByClusterID`.
- Created two independent Secrets (`orbstack`, `aiops-kind-02`) in a test namespace, each containing only its own cluster's kubeconfig.
- Proved the real chain keyed only by canonical UUID: `cluster_id → ClusterRegistry → credential_ref → SecretResolver → Secret → kubeconfig → kubectl → node`:
  - `UUID_A` → `orbstack`
  - `UUID_B` → `aiops-kind-02-control-plane`
  - routing is disjoint (A never reaches the kind node and vice-versa)
  - missing Secret → fails closed (no current-context fallback)
  - wrong credential_ref (A pointed at B's Secret) → credential/cluster identity mismatch detected (routed to kind node, not orbstack)
- No Secret content printed; evidence only records existence, namespace/name, resolved node, PASS/FAIL.
- Test Secrets, namespace, MySQL rows and the temporary program were cleaned up after verification.
- **Honest limitation (superseded by P3.10c-final):** the mismatch above was observed by the *temporary test program* comparing the resolved node to the expected one; the Access Boundary itself did not yet reject it. That was a real P0 gap and is closed below.

### P3.10c-final Cluster Credential Identity Binding (P0 hardening)
- **Authoritative identity = kube-system Namespace `metadata.uid`** (independent of node name, kube-context, API Server address, slug). `APIServer` kept for diagnostics only, never as the identity authority.
- **Registry:** `clusters.kubernetes_identity_uid` column (schema `ensureClusterAuthorityMetadata`); `Cluster.KubernetesIdentityUID`; `GetByClusterID` returns it; `RegisterCluster` persists it and rejects duplicate ACTIVE registration of the same physical cluster (`FindActiveByKubeSystemUID` → `ErrClusterIdentityDuplicate`).
- **Boundary (`internal/k8sboundary`):** interface-injected `KubeconfigReader` / `IdentityReader` / `ClusterStore`; public `ClusterClientManager.GetClient(clusterID)` only (raw `GetKubeconfig` is now a private `getValidatedKubeconfig`). Flow:
  `cluster_id → Registry expected UID → credential_ref → Secret → kubeconfig → probe kube-system UID → expected == observed ? cache+return : CLUSTER_IDENTITY_MISMATCH → invalidate → FAIL CLOSED`.
  Missing `kubernetes_identity_uid` or non-canonical ref → fail closed (`ErrClusterIdentityMissing` / `ErrInvalidClusterRef`); no default/current-context fallback.
- **Cache:** keyed by canonical UUID; entry records credential_ref; on credential change the stale client is invalidated and re-validated before reuse. Mismatch is never cached.
- **Contract:** added `CLUSTER_IDENTITY_MISMATCH` to Python `ErrorCode`, Go `contract.ErrorCodeClusterIdentityMismatch` (+ `HTTPStatusCode` → 409), TS `ErrorCode` union; shared `contract-fixtures.json` + TS fixture carry a structured mismatch error; cross-language fixture assertion added.
- **Tests (TDD):** `k8sboundary_test.go` 8 tests prove, with injected fakes, that the *boundary* actively fails closed: `UUID_A + Secret_B → err == ErrClusterIdentityMismatch && client == nil`, mismatch not cached, cache invalidated on credential change, recovery after fix, missing binding fails closed, non-canonical refs rejected. `store` tests cover `GetByClusterID` identity read, `FindActiveByKubeSystemUID` nil/existing, and duplicate-active registration rejection.
- **Registration-time identity discovery (production path, P0):** `ClusterRegistrar` (`internal/k8sboundary/registration.go`) encapsulates `credential_ref → SecretResolver → kubeconfig → target Kubernetes API → kube-system UID → persist`. `ClusterRegistration` has NO identity field, so a caller cannot forge the UID; the UID is discovered from the real cluster and a fresh canonical UUID is generated. Unresolvable credential or failed probe → no DB write (no partial registration); duplicate ACTIVE physical cluster rejected. `registration_test.go` covers discovery, no-forge, fail-before-write, and duplicate rejection.
- **Real two-cluster negative E2E (P0):** `k8sboundary_integration_test.go` (`//go:build integration`) runs the REAL `SecretResolver` + REAL `kubectlIdentityReader` against the two live clusters (orbstack + kind-aiops-kind-02), passing the real kube-system UIDs through Secrets. Proves: `UUID_A+Secret_A → orbstack UID → PASS`; `UUID_B+Secret_B → kind-02 UID → PASS`; `UUID_A+Secret_B → observed kind-02 UID ≠ orbstack binding → CLUSTER_IDENTITY_MISMATCH → client == nil → cache unchanged`. Run: `go test -tags integration ./internal/k8sboundary/ -run Integration -v` (cleans the temp namespace/Secrets afterward).
- **Boundary surface tightened:** package-level `KubeNodes(kubeconfig)` is now private `kubeNodes`; raw kubeconfig does not cross the boundary as a public API (`Client.KubeNodes()` is the validated path).
- **Honest scope note:** the boundary currently has no in-process production caller yet (by design, per V9.2 Phase plan). Its identity-verification and registration-discovery paths are proven fail-closed by the injectable suite AND the real-cluster integration E2E; `query_k8s` and Execution migration happen in Phase 6 / Phase 11. No Kubernetes access path is added outside the boundary.

### P3.11 Full tests
- query-api: all packages `ok` (cmd/api, internal/api, auth, biz, contract, store, **k8sboundary**).
- k8sboundary: 13 unit tests (8 boundary + 5 registrar) via injected fakes PASS; **real-cluster integration E2E PASS** (`go test -tags integration` — orbstack + kind-02 mismatch fail-closed proven with real readers).
- orchestrator: **403 passed, 2 failed** — the 2 failures are the frozen Phase 14 legacy tests (`test_checkpointer`, `test_loop_iterations`, both `LEGACY_PATH_PENDING_REMOVAL`).
- frontend: `npm run build` PASS (4.42s); `tsc --noEmit` PASS.
- contract: shared `contract-fixtures.json` (incl. `CLUSTER_IDENTITY_MISMATCH`) decodes in Go + Python; `HTTPStatusCode(CLUSTER_IDENTITY_MISMATCH) == 409`.
- helm: `helm lint` 0 failed.
- Cross-language: Go sign RunInvocation → Python verify PASS; Python sign TrustedRequest → Go verify PASS.
- secret scan: PASS (no keys/credentials in changed files).

## Phase 3 deviations
- **Service identity algorithm:** V9.2 §13-14 amended HS256 → EdDSA/Ed25519 (INTERNAL-AUTH-P0-011) per decision. `typ=AIOPS-CONTEXT`.
- **P3.10 image source:** domestic mirror failed (cloudfront EOF); pulled the same pinned `kindest/node:v1.35.0` from the official registry (single attempt, fixed digest). No unknown-version fallback.

## Phase 3 Gate status
- All code/security gates P3.1–P3.9 PASS.
- **P3.10 PASS:** `aiops-kind-02` created (pinned image) and dual-cluster isolation verified (independent nodes/APIs/contexts).
- **P3.10b PASS:** application-level canonical multi-cluster chain proven (two canonical UUIDs, tenant ownership, independent credential_ref, same-name resource isolation, fallback-absent fail-closed).
- **P3.10c PASS:** `credential_ref` runtime resolution proven through the real Kubernetes Access Boundary — `canonical UUID → credential_ref → Secret → kubeconfig → kubectl → node`, keyed only by canonical UUID, with missing-Secret fail-closed.
- **P3.10c-final PASS:** the Access Boundary **actively** fails closed on credential/cluster identity mismatch via `kubernetes_identity_uid` (kube-system Namespace UID) binding. Tests assert `UUID_A + credential_B → err == CLUSTER_IDENTITY_MISMATCH && client == nil`; no mismatch client is returned or cached; credential change invalidates cache; missing binding fails closed; non-canonical refs rejected. This supersedes the earlier test-layer-only observation.
- The single-cluster trust boundary (browser → query-api → orchestrator via RunInvocation/TrustedRequest) is closed and tested.
- Remaining at deployment (Phase 18): final image build/Helm release and multi-cluster smoke on real images; no Phase 3 contract item is deferred.

---

## Phase 4 (P4.1–P4.10) — 统一 Schema Ownership 与初始化体系

**Status: Phase 4 PASS (Gate 4).** 目标 = schema/ownership/init；不做 dual-write、不切 reader/writer（Phase 5/6）。详见 `docs/superpowers/plans/phase4-gate-result.md`。Object Store 真实 bootstrap 曾是最后 BLOCKER（BLOCKED_OBJECT_STORE_RUNTIME_MISSING），已通过真实 MinIO S3-compatible endpoint 补齐（create/validate bucket + 幂等 + readiness + Evidence key 契约），重跑 `phase4-gate.sh` → **GATE 4 全项 PASS**。

### P4.1 Runtime DDL Inventory
- `docs/superpowers/plans/phase4-inventory.md`：多模式扫描（非单一 grep 计数）归档所有 schema-creation callsite（query-api EnsureSchema、orchestrator db.migrate、event-collector ClickHouse DDL、Chroma get_or_create、SQLite 本地态）；orchestrator MySQL 用途 DDL/DML read/DML write 三类清单。

### P4.2 Authoritative Ownership Matrix
- `docs/SCHEMA_OWNERSHIP.md` 修正 V9.2 冻结命名（`auth_sessions`、`ai_audit_events`、`platform_audit_events`；移除 `resource_scopes`/`cluster_nodes` 作新授权表）+ 追加 Phase 4 落地列。
- `docs/superpowers/plans/phase4-ownership.md` 机器可读矩阵（AI Runtime 冻结 12 表 + 控制面 + audit）。

### P4.3 Unified MySQL Versioned Migrator + schema-migrator
- `internal/store/migrations/`：权威元数据表 **`aiops_schema_migrations`**（不复用/不 ALTER 旧 `schema_migrations`）；`migration_id` namespaced（`mysql/0001-...`）+ `checksum CHAR(64)` + `applied_at`；GET_LOCK 迁移锁；`-- statement-breakpoint` 行首解析（非 Split(";")）；幂等执行（成功后才 INSERT，捕获已存在类错误幂等恢复）。
- `cmd/schema-migrator`：独立迁移执行器（`aiops_migrator` 账号）。
- **P4.3 cutover DEVIATION（用户拍板 A）：** query-api runtime cutover 推迟到 P4.4（EnsureSchema 完整接管后），非缺陷。

### P4.4 Frozen MySQL Schema + EnsureSchema 接管 + cutover
- 版本 SQL：`0001`（控制面 ~25 表 + RBAC 8 表 + LEGACY 3 表 + ALTER + backfill DML）、`0002`（AI Runtime 12 表）、`0003`（platform_audit_events）。
- **`TestMigratedSchemaCoversLegacyEnsureSchema` PASS（真实 MySQL 8.4）**：权威 migrations 覆盖 legacy EnsureSchema（A covers B）+ 二次幂等。
- **`TestAIRuntimeSchemaManifest` PASS**：ai_runs 无 cluster_id、ai_run_clusters PK、明细表 cluster_id NOT NULL、ai_plan_steps NULL、ai_run_events PK。
- `store.EnsureBootstrapData()`（DML-only seed）；`cmd/api/main.go` cutover：`EnsureSchema()` → `migrations.RequireCurrent` + `EnsureBootstrapData`（runtime 零 DDL）。

### P4.5 Versioned ClickHouse Bootstrap
- `deploy/helm/aiops/files/clickhouse/migrations/0001_observability_baseline.sql` + `deploy/tools/clickhouse-migrator/`（真实 migration state machine：原始字节 SHA256、先 checksum 后执行、>1 行损坏 fail-closed）。
- **三态真实验证（真实 ClickHouse 24.8）**：first APPLIED（checksum == 文件 SHA256）、second SKIPPED、modified CHECKSUM_MISMATCH（abort-before-SQL，metadata 未改写，schema 未变，exit 1）。
- `log_records` 标 LEGACY（Raw Logs SoT = VictoriaLogs）；`k8s_events` 迁入迁移器；event-collector 改只读 `EnsureSchemaCompatible`（无 CREATE/ALTER/DROP、无 log_records INSERT）。

### P4.6 VictoriaMetrics / VictoriaLogs Scope Label Contracts
- `docs/contracts/vmlogs-label-contract.md` + `vmetrics-label-contract.md`。
- `ai-apm-ingest-go/internal/telemetrylabels/`：`ValidateScopeLabels`（tenant/cluster canonical UUID，对齐 Phase 3 pattern；resource scope 强制 resource_id）+ `NormalizeScopeLabels`；3 单测（拒 orbstack/1/default）。

### P4.7 Object Store + Chroma Bootstrap Contracts
- `docs/contracts/object-store-contract.md`（bucket/prefix/key/raw_digest/retention + tenant isolation）+ `chroma-collection-contract.md`。
- `rag.py` 改 get-only（不再 get_or_create）+ 常量；`rag_bootstrap.py`（bootstrap create + check-only readiness）。
- `deploy/tools/object-store-bootstrap`：真实 S3-compatible bootstrap（create/validate bucket `aiops-evidence`/`aiops-knowledge`，幂等 + readiness check + Evidence object key 契约）。
- **Object Store runtime acceptance（原 BLOCKED_OBJECT_STORE_RUNTIME_MISSING）：** 已通过真实 MinIO S3-compatible endpoint（本地镜像）补齐验证——first create + second idempotent + readiness + `<tenant_id>/<cluster_id>/<run_id>/<evidence_id>` key 契约。改 SoT 仍须正式 Erratum。

### P4.8 Remove Runtime Schema Creation
- MySQL 受限账号（`users-init-job.yaml`）：`aiops_app`（DML only，无 CREATE/ALTER/DROP/INDEX）+ `aiops_migrator`（DDL）；密码来自 Secret env/mount（非明文 values）。
- query-api/orchestrator deployment 改 `MYSQL_USER=aiops_app` + `MYSQL_APP_PASSWORD`。
- `init-job.yaml` 改 schema-migrator（`aiops_migrator`，hook weight 20，顺序 users-init→schema-migrator→runtime）。
- orchestrator `main.py` 移除 `db.migrate()`。
- **受限账号权限真实验证：** `aiops_app` CREATE TABLE → denied、SELECT → allowed；`aiops_migrator` CREATE/DROP → allowed。

### P4.9 Gate Tests
- `deploy/scripts/phase4-gate.sh`：A empty init / B second idempotent / C existing upgrade / D restricted accounts / E runtime no-DDL / F checksum / **G Object Store bootstrap+readiness**。
- **GATE 4 RESULT: PASS**（真实 MySQL + ClickHouse + MinIO 验证；G 为补齐 Object Store 后新增并 PASS）。

### P4.10 Gate Report
- `docs/superpowers/plans/phase4-gate-result.md`。

## Phase 4 Gate status
- **Gate 4 PASS**（A-F 六类 + **G Object Store** 全项真实环境验证）。
- **Object Store（原 BLOCKED_OBJECT_STORE_RUNTIME_MISSING）已 RESOLVED：** 启动受控 MinIO S3-compatible endpoint（本地镜像），用 `deploy/tools/object-store-bootstrap` 真实执行 create/validate bucket（first create + second idempotent + readiness + `<tenant_id>/<cluster_id>/<run_id>/<evidence_id>` key 契约），并纳入 `phase4-gate.sh` 项 G。重跑 → GATE 4 全项 PASS。
- **最终状态：**
  ```text
  PHASE: 4
  STATUS: PASS (Gate 4)
  P4.1-P4.10: COMPLETE
  P4.7 Object Store runtime acceptance: PASS（真实 MinIO endpoint）
  GIT_ACTION: NONE
  NEXT_PHASE: 5 (NOT_STARTED — Phase Gate 后停止，不自动进入)
  ```
- LEGACY 项：`log_records`、`audit_logs`、旧 `schema_migrations`、orchestrator 直连 legacy 表、`get_or_create_collection`。
- **Phase 4 = schema/ownership/init**，未做 dual-write；writer cutover = Phase 5；reader cutover = Phase 6。

---

## Phase 5 (P5) — Writer Implementation + Atomic-Cutover Readiness

**Status: Phase 5 PASS (Gate 5).** 范围决策 = 选项 C：只建 writer 新链路 + cutover readiness，**不做生产切换**（VM/VLogs `Enabled()` 恒 false，ClickHouse `log_records` 仍在写入）。详见 `docs/AIOPS_PHASE5_GATE.md`。

### P5.1 ai-event-collector 三字段强制 + fail-closed
- `scope.go`（新）：`EventScope{TenantID, ClusterID}` + `validateCanonicalUUID`（拒绝空/default/slug/数值），复用 Phase 3/4 冻结 canonical UUID pattern。
- `config.go`：`TENANT_ID`/`CLUSTER_ID` 不再默认 `"default"`。
- `main.go`：启动 `EventScope.Validate()` fail-closed（非法 `log.Fatalf`）。
- 单测：`TestEventScopeValidate`、`TestValidateCanonicalUUID`（6 用例 PASS）。

### P5.2 ai-event-collector WAL 持久化
- `wal.go`（新）：崩溃安全 WAL（Append/Ack/ReadAll/Compact/Close），Append 返回单调递增 seq，Ack 推进连续 ack 水位并持久化 `.ack`，Compact 截断已 ack 前缀，重启从 consecutiveAck 之后恢复。
- `config.go` 新增 `WAL_DIR`（空→内存重试，向后兼容）。
- `clickhouse.go`：`EventWriter` 集成 WAL——启动恢复未 ack 批次；flush 失败先 Append 再入重试；flushRetry 成功 Ack + 全部成功 Compact；丢弃最旧批次 Ack 对应 seq。
- 单测：`TestWALAppendAckReplay`、`TestWALCompactKeepsUnacked`、`TestWALCompactionResetsSeqSafe`（崩溃恢复/compaction 语义 PASS）。

### P5.3 ai-event-collector checkpoint key=tenant+cluster+source
- `clickhouse.go`：纯函数 `latestTSQuery(source, tenantID, clusterID)`，`QueryLatestTS` 改用 `WHERE source + tenant_id + cluster_id`（V9.2 §71）。
- 单测：`TestLatestTSQueryIncludesTenant`。

### P5.4 ai-apm-ingest-go 消除 default 兜底
- `internal/clickhouse/{writer,log_writer,metrics_writer}.go`：serialize 移除 `clusterID==""→"default"` 兜底（fail-closed，禁止猜测）。
- `internal/pipeline/ingest.go` `SetClusterID`：移除 `""→"default"`。
- `cmd/ingest/main.go`（Gate Closure 修正）：`CLUSTER_ID` 缺失 → **fail-closed 拒绝启动**（`log.Fatalf`，非"空+WARN"）——cluster_id 是静态身份，缺失写不出 partial/missing_fields，故走 reject 路径；`/v1/traces`、`/v1/logs` 的 `X-Tenant-ID` 缺失 → 400 fail-closed。
- 单测：`TestSerializeSpansEscapesSpecialChars` 断言更新（空 cluster_id 不再 default 兜底）。

### P5.5 VictoriaMetrics writer adapter（default disabled but switchable）
- `internal/telemetry/{writer,vmetrics}.go`（新包）：`WriteResult{Status,ErrorCode,Retryable}` 统一错误语义 + `MetricsWriter` 接口；`VictoriaMetricsWriter.Write/WriteScope` 先 `telemetrylabels.ValidateScopeLabels`（tenant/cluster canonical UUID，resource scope 强制 resource_id），`__name__` 必填。
- **Switchable（Gate Closure 修正，非 hardcoded disabled）**：`Mode` 类型 legacy/new + `ParseMode`/`SetMode`/`Enable`/`New...WriterMode`；默认 `ModeLegacy`（PRODUCTION_ACTIVE=false），Phase 6 由部署配置受控切 `new`。
- 单测：VM 6 + mode 3 个 PASS。

### P5.6 VictoriaLogs writer adapter（default disabled but switchable）
- `internal/telemetry/vlogs.go`（新）：`VictoriaLogsWriter.WriteLog/WriteLogScope` 先 scope label 校验 + body 非空；`serializeJSONLine` 用 `NormalizeScopeLabels` + `_msg`/`_time`。
- **Switchable**：同上 `Mode`/`ParseMode`/`SetMode`/`Enable`/`New...WriterMode`，默认 `ModeLegacy`。
- 单测：VLogs 5 + mode 3 个 PASS。telemetry 包合计 14 单测全绿。

### Phase 5 最终状态
  ```text
  PHASE: 5
  STATUS: PASS (Gate 5)  — Gate Closure 2026-08-20 收口 4 缺口后判定
  P5.1-P5.6 + P5.7: COMPLETE
  WAL replay / storage recovery: PASS（-race 全绿）
  event dedup / backlog observable: PASS（采集层去重 + ReplacingMergeTree 幂等 + WAL.PendingStats/metrics）
  telemetry labels contract: PASS（telemetrylabels 3 单测 + VM/VLogs adapter 14 单测）
  三字段 / partial semantics: PASS（缺失即 reject，不写空不猜；部署 clusterId/tenantId 需 canonical UUID）
  VM/VLogs writer adapter: PASS（default legacy disabled but switchable，未切生产）
  K8s watch single leader: PASS（internal/leaderelection 5 单测 + 真实 Lease 创建/持有/接管验证）
  ClickHouse log_records legacy removal: READY（停写跟随 Phase 6 原子窗口）
  GIT_ACTION: NONE
  NEXT_PHASE: 6 (NOT_STARTED — atomic cutover window; writer+reader cutover 同一受控窗口)
  ```

---

## P5.7 Phase 5 Gate Closure（2026-08-20）

Gate 复核发现 3 个实质缺口 + 1 个非 Gate 字段项，本轮全部收口后判定 **Gate 5 PASS**。详见 `docs/AIOPS_PHASE5_GATE.md`。

### P5.7.1 event dedup / backlog observable（缺口 1）
- dedup：采集层 UID/(node,record-id) 去重 + 存储层 `ReplacingMergeTree` 幂等兜底（WAL replay 重复窗口由 key 收敛）。
- backlog：`WAL.PendingStats()`（records/bytes/oldest）+ `OldestAgeSeconds()`；`/metrics` 新增 `ai_event_collector_wal_pending_records`/`_bytes`/`_oldest_pending_age_seconds`。
- 单测：`TestWALPendingStatsBacklogObservable`、`TestWALPendingStatsCountsUnackedOnly`。

### P5.7.2 三字段 / partial semantics（缺口 2）
- `cmd/ingest/main.go`：`CLUSTER_ID` 缺失从"空+WARN"改为 **fail-closed 拒绝启动**（reject 路径，不写空、不猜，ClickHouse 列固定无法表达 partial）。
- `X-Tenant-ID` 缺失 → 400；event-collector `TENANT_ID`/`CLUSTER_ID` 非 canonical → 启动拒绝。
- 部署迁移待办：现有 helm `clusterId: "default"`/`tenantId: ""` 非 canonical，代码已 fail-closed；Phase 6 前需注入注册后集群 UUID。

### P5.7.3 VM/VLogs switchable（缺口 3）
- `internal/telemetry`：从 `Enabled() bool { return false }`（hardcoded）改为 `Mode` 类型 + `ParseMode`/`SetMode`/`Enable`/`New...WriterMode`。默认 `ModeLegacy`（disabled），Phase 6 由部署配置（如 `TELEMETRY_WRITER_MODE=new`）受控切 new，无需改源码重建。

### P5.7.4 K8s watch single leader（缺口 4）
- 新增 `internal/leaderelection/` 包：窄 `coordination.k8s.io/v1` Lease abstraction（get/create/update）+ `Elector` 状态机（FOLLOWER→LEADER→FOLLOWER，Acquire/Renew/Detect loss/Stop/Reacquire，fail-safe）。
- 集成 `leaderelection.go`：仅 Lease holder 启动 cluster-wide K8s watch（per-leadership 可取消 context），follower 只做 SEL；Lease 丢失即停 watch/write。
- RBAC：`coordination.k8s.io/leases` get/create/update。
- 单测：5 个（exactly one leader / handoff / renew fail 停写 / reacquire / 严格交替无重叠）。
- 真实 K8s 验证（kind-02）：coordination API 可用；Lease 创建/持有/接管（pod-a→pod-b）holderIdentity 可观测。多节点多 DaemonSet Pod 竞争 E2E 受限于单节点环境，列为 Phase 6 前置项。
