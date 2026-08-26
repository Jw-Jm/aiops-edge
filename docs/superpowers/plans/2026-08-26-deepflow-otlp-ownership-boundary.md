# DeepFlow OTLP Ownership Boundary Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将 DeepFlow 原始数据与 AIOps 平台数据彻底解耦，并以真实官方 OTLP/gRPC 链路填充平台 `observability.trace_spans`。

**Architecture:** DeepFlow 保留自己的 ClickHouse 和原始表；DeepFlow Server 通过官方 `ingester.exporters` 将 `flow_log.l7_flow_log` 发送到 AIOps Ingest 4317；Ingest 统一转换为内部 Span 后写平台 ClickHouse。迁移先双运行验证，随后移除 Ingest 的 DeepFlow ClickHouse 直连、同步代码和网络放行。

**Tech Stack:** Go 1.23, `opentelemetry-proto`, gRPC, ClickHouse HTTP sink, Helm 3, Kubernetes NetworkPolicy, pytest/shell contract tests.

**Spec:** `docs/superpowers/specs/2026-08-26-deepflow-otlp-ownership-boundary-design.md`

## Global Constraints

- DeepFlow 源码和 DeepFlow ClickHouse schema 不修改；只修改自研 Go、Helm、测试和部署配置。
- DeepFlow ClickHouse 不得出现在 AIOps Ingest 运行时环境变量、代码连接路径或 SQL 写路径中。
- 平台 Trace SoT 固定为 `observability.trace_spans`；平台日志 SoT 固定为 VictoriaLogs。
- 不把 DeepFlow 原始 flow log 写入 `observability.log_records`。
- 每个行为变更先写失败测试，再写最小实现；每次修复都构建同一 Git SHA 的镜像并部署验证。
- 不修改用户未跟踪的 `AIOps前端全功能及真实性验收测试方案_细化排查版_最终版.md` 和 `部署验证.md`。
- 不执行真实 AI Chat/DeepSeek 调用，不向外部模型发送 telemetry、日志、Trace 或 K8s 数据。

---

### Task 1: Add OTLP/gRPC receiver contract tests

**Files:**
- Create: `ai-apm-ingest-go/internal/otlpgrpc/receiver_test.go`
- Create: `ai-apm-ingest-go/internal/otlpgrpc/convert_test.go`
- Modify: `ai-apm-ingest-go/internal/pipeline/ingest_test.go`

**Interfaces:**
- Produces: `Receiver.Export(context.Context, *coltrace.ExportTraceServiceRequest) (*coltrace.ExportTraceServiceResponse, error)` behavior and a conversion contract consumed by the server wiring task.
- Consumes: the existing `model.Span`, `Pipeline`, and platform Span sink interfaces.

- [ ] **Step 1: Write the failing tests**

  Cover these exact cases:

  ```go
  func TestExportRejectsMissingTenantMetadata(t *testing.T) {}
  func TestExportRejectsUnexpectedTenantMetadata(t *testing.T) {}
  func TestExportConvertsResourceAndSpanAttributes(t *testing.T) {}
  func TestExportPreservesTraceSpanAndParentIDs(t *testing.T) {}
  func TestExportReturnsUnavailableWhenSpanSinkFails(t *testing.T) {}
  func TestExportRejectsMalformedTimestampsAndIDs(t *testing.T) {}
  ```

  The conversion fixture must contain `service.name`, `service.instance.id`, `k8s.namespace.name`, `k8s.pod.name`, `http.method`, `http.url`, and an error status; assertions must inspect the internal `model.Span`, not only the gRPC response.

- [ ] **Step 2: Run the focused tests and verify they fail**

  Run: `cd ai-apm-ingest-go && go test ./internal/otlpgrpc ./internal/pipeline -run 'TestExport|TestOTLPGRPC' -count=1`

  Expected: FAIL because the receiver package and protobuf dependencies do not yet exist.

- [ ] **Step 3: Commit the red tests**

  Run: `git add ai-apm-ingest-go/internal/otlpgrpc ai-apm-ingest-go/internal/pipeline/ingest_test.go && git commit -m "test: define DeepFlow OTLP grpc receiver contract"`

---

### Task 2: Implement shared OTLP conversion and gRPC receiver

**Files:**
- Modify: `ai-apm-ingest-go/go.mod`
- Modify: `ai-apm-ingest-go/go.sum`
- Create: `ai-apm-ingest-go/internal/otlpgrpc/receiver.go`
- Create: `ai-apm-ingest-go/internal/otlpgrpc/convert.go`
- Modify: `ai-apm-ingest-go/internal/pipeline/ingest.go`
- Modify: `ai-apm-ingest-go/internal/model/span.go`

**Interfaces:**
- Consumes: `coltrace.ExportTraceServiceRequest`, metadata `x-tenant-id`, configured tenant, and `pipeline.SpanSink`.
- Produces: `NewReceiver(p *pipeline.Pipeline, tenantID string) *Receiver`, `Receiver.Export`, and `ConvertRequest(tenantID string, clusterID string, req *coltrace.ExportTraceServiceRequest) ([]*model.Span, error)`.

- [ ] **Step 1: Add the OTLP protobuf and gRPC modules**

  Add the official `go.opentelemetry.io/proto/otlp` and `google.golang.org/grpc` modules with `go mod tidy`; do not hand-edit checksums or use a local fork.

- [ ] **Step 2: Implement conversion using the existing model rules**

  Convert `ResourceSpans` and `ScopeSpans` into `model.Span`, preserving IDs and mapping resource/span attributes. Reuse the existing status, duration, HTTP, DB, RPC, Kubernetes and slow/error semantics. Reject empty trace/span IDs and timestamps that cannot be represented.

- [ ] **Step 3: Implement tenant metadata and sink failure semantics**

  Read lowercase `x-tenant-id` from incoming metadata. Return `codes.Unauthenticated` for missing or mismatched values. Call the same durable platform sink path used by JSON; return `codes.Unavailable` when the required sink refuses the batch.

- [ ] **Step 4: Refactor JSON ingestion to use the shared conversion path**

  Keep `/v1/traces` behavior and `X-Tenant-ID` header compatibility, but ensure JSON and gRPC produce equivalent `model.Span` fields and RED aggregation. Do not add a second persistence implementation.

- [ ] **Step 5: Run focused and package tests**

  Run: `cd ai-apm-ingest-go && go test ./internal/otlpgrpc ./internal/pipeline -count=1` and `go test ./... -count=1`.

- [ ] **Step 6: Commit**

  Run: `git add ai-apm-ingest-go && git commit -m "feat: receive DeepFlow spans over OTLP grpc"`

---

### Task 3: Wire the receiver, metrics, ports, and NetworkPolicy

**Files:**
- Modify: `ai-apm-ingest-go/cmd/ingest/main.go`
- Modify: `ai-apm-ingest-go/internal/metrics/metrics.go`
- Modify: `deploy/helm/aiops/templates/ingest/deployment.yaml`
- Modify: `deploy/helm/aiops/templates/networkpolicy.yaml`
- Modify: `deploy/helm/aiops/values.yaml`
- Test: `ai-apm-ingest-go/cmd/ingest/main_test.go`

**Interfaces:**
- Consumes: `otlpgrpc.NewReceiver`, `OTLP_GRPC_PORT`, `DEEPFLOW_TENANT_ID`, and existing readiness state.
- Produces: one HTTP server on 8080 and one gRPC server on `OTLP_GRPC_PORT` (default 4317), plus counters for received/accepted/rejected/failed gRPC batches.

- [ ] **Step 1: Write failing wiring and render assertions**

  Assert that `OTLP_GRPC_PORT=4317` starts a gRPC listener, shutdown closes it, the `/health` response remains unchanged for HTTP clients, and the rendered Ingest Service contains TCP 4317.

- [ ] **Step 2: Add gRPC server lifecycle and metrics**

  Start the gRPC server from `main`, register the receiver, include it in graceful shutdown, and increment counters only at the corresponding receive/accept/reject/failure points. A listener failure must fail startup rather than silently disabling the path.

- [ ] **Step 3: Add Helm values, Deployment env, Service port, and ingress rule**

  Add `ingest.otlpGrpcEnabled: true`, `ingest.otlpGrpcPort: 4317`, and `ingest.deepflowTenantId` wiring. Add a NetworkPolicy ingress rule allowing TCP 4317 only from pods labeled `component: deepflow-server` in namespace `deepflow`; do not allow arbitrary namespace traffic.

- [ ] **Step 4: Render and test**

  Run: `helm lint deploy/helm/aiops` and the repository render checks; inspect that the Service, Deployment and NetworkPolicy agree on 4317.

- [ ] **Step 5: Commit**

  Run: `git add ai-apm-ingest-go/cmd/ingest ai-apm-ingest-go/internal/metrics deploy/helm/aiops && git commit -m "feat: expose authenticated OTLP grpc ingest path"`

---

### Task 4: Enable the official DeepFlow exporter without changing DeepFlow source

**Files:**
- Modify: `deploy/helm/aiops/values-deepflow.yaml`
- Create: `deploy/scripts/test-deepflow-otlp-render.sh`

**Interfaces:**
- Consumes: Ingest Service DNS `ingest.observability.svc.cluster.local:4317` and the configured platform tenant ID.
- Produces: DeepFlow `configmap.server.yaml.ingester.exporters` with protocol `opentelemetry`, source `flow_log.l7_flow_log`, required export fields, and tenant metadata.

- [ ] **Step 1: Add a failing render contract**

  The script must render or inspect the DeepFlow release manifest and fail unless the exporter has the exact protocol, endpoint, data source, batch settings, and `x-tenant-id`; it must also fail if a ClickHouse password or provider key appears in the exporter block.

- [ ] **Step 2: Add the nested Helm values**

  Add only the official chart-supported `configmap.server.yaml.ingester.exporters` values. Preserve the existing server `ckdb` ownership configuration; do not replace DeepFlow's schema or templates.

- [ ] **Step 3: Run chart rendering and the contract**

  Run: `helm lint deploy/helm/aiops` and `bash deploy/scripts/test-deepflow-otlp-render.sh`.

- [ ] **Step 4: Commit**

  Run: `git add deploy/helm/aiops/values-deepflow.yaml deploy/scripts/test-deepflow-otlp-render.sh && git commit -m "feat: configure DeepFlow official OTLP exporter"`

---

### Task 5: Stage deployment and prove the real OTLP path

**Files:**
- Create: `deploy/scripts/verify-deepflow-otlp-cutover.sh`
- Do not modify: `AIOps前端全功能及真实性验收测试方案_细化排查版_最终版.md`, `部署验证.md`

**Interfaces:**
- Consumes: Kubernetes contexts, running Ingest metrics, DeepFlow-owned ClickHouse read-only evidence, and platform ClickHouse read-only evidence.
- Produces: a non-secret evidence report with timestamps, counts, image tags, and pass/fail gates.

- [ ] **Step 1: Build and deploy the receiver image**

  Use `IMAGE_TAG=git-<current-sha> ./deploy/scripts/build-images.sh ingest`, deploy with the same explicit tag, and wait for Ingest readiness and Service port 4317 endpoints.

- [ ] **Step 2: Upgrade DeepFlow with exporter values**

  Run the existing DeepFlow Helm upgrade using `deploy/helm/aiops/values-deepflow.yaml`; wait for `deepflow-server` rollout and verify the rendered ConfigMap contains the exporter without printing secrets.

- [ ] **Step 3: Verify received OTLP evidence**

  Require all of the following before cutover: Ingest gRPC accepted counter increases; platform `observability.trace_spans` has a fresh row with DeepFlow exporter attributes; DeepFlow `flow_log.l7_flow_log` has fresh rows; all pods remain Ready for two exporter flush intervals.

- [ ] **Step 4: Keep rollback safe**

  If any gate fails, disable the exporter configuration only and keep DeepFlow data intact; do not remove the legacy syncer until the evidence report passes.

- [ ] **Step 5: Commit the evidence harness**

  Run: `git add deploy/scripts/verify-deepflow-otlp-cutover.sh && git commit -m "test: verify real DeepFlow OTLP cutover evidence"`

---

### Task 6: Remove the legacy DeepFlow ClickHouse runtime path

**Files:**
- Delete: `ai-apm-ingest-go/internal/pipeline/deepflow_sync.go`
- Delete: `ai-apm-ingest-go/internal/pipeline/deepflow_sync_test.go`
- Delete: `ai-apm-ingest-go/internal/pipeline/redsink_test.go`
- Modify: `ai-apm-ingest-go/cmd/ingest/main.go`
- Modify: `ai-apm-ingest-go/internal/pipeline/deepflow.go`
- Modify: `deploy/helm/aiops/templates/ingest/deployment.yaml`
- Modify: `deploy/helm/aiops/values.yaml`
- Modify: `deploy/helm/aiops/values-local-validation.yaml`
- Modify: `deploy/scripts/local-validation.sh`
- Modify: `deploy/helm/aiops/templates/networkpolicy.yaml`

**Interfaces:**
- Consumes: successful staged OTLP evidence from Task 5.
- Produces: Ingest with no DeepFlow ClickHouse runtime connection, no TTL mutation path, and no DeepFlow ClickHouse NetworkPolicy exception.

- [ ] **Step 1: Add a failing source/config invariant test**

  Assert that the Ingest source tree has no `DEEPFLOW_CH_HOST`, `DEEPFLOW_CH_ENDPOINTS`, `queryDF`, `execDF`, `ensureRetention`, or `NewDeepFlowSyncer`, and rendered Ingest manifests contain none of those environment variables.

- [ ] **Step 2: Remove runtime wiring and old configuration**

  Delete the syncer and its tests, remove the startup closure and native endpoint that only redirected to the syncer, remove DeepFlow CH values/env, and remove the external CH egress block. Keep only the platform ClickHouse sink and OTLP receivers.

- [ ] **Step 3: Update comments and health semantics**

  State that DeepFlow raw evidence is external to Ingest and that `trace_spans` is populated by OTLP/SDK inputs. Do not claim `log_records` contains DeepFlow logs.

- [ ] **Step 4: Run source invariants and Go tests**

  Run the invariant script, `cd ai-apm-ingest-go && go test ./... -count=1`, and Helm lint/render tests.

- [ ] **Step 5: Build, deploy, and verify the post-cutover state**

  Build all affected images with one `git-<sha>` tag, upgrade AIOps, wait for rollout, verify Ingest has no DeepFlow CH env, verify raw DeepFlow rows continue independently, and verify platform OTLP rows continue increasing.

- [ ] **Step 6: Commit**

  Run: `git add ai-apm-ingest-go deploy/helm/aiops deploy/scripts && git commit -m "refactor: remove DeepFlow clickhouse runtime coupling"`

---

### Task 7: Full regression, consistency audit, and GitHub synchronization

**Files:**
- Modify only implementation/test files required by failed gates.
- Do not modify the two user-owned untracked acceptance documents.

- [ ] **Step 1: Run all affected Go tests**

  Run each module's full test suite, including `ai-apm-ingest-go`, `ai-apm-query-go`, and `ai-orchestrator`; record failures with the first inconsistent layer.

- [ ] **Step 2: Run Helm and deployment contract gates**

  Render the exact deployed values, verify all self-built images use one tag, verify 4317 wiring, verify DeepFlow exporter values, and verify no legacy DeepFlow CH variables remain.

- [ ] **Step 3: Execute the non-AI frontend acceptance regression**

  Use the existing local browser session only for page behavior; verify dashboard, topology, traces, VictoriaLogs, alerts, capacity, infrastructure, login/password behavior, and system status against API and storage evidence. Do not invoke AI Chat.

- [ ] **Step 4: Audit three-way consistency**

  Require `git rev-parse HEAD`, `git rev-parse origin/main`, every running self-built image tag, and the deployed Helm `global.imageTag` to identify the same SHA. Never print secrets while collecting evidence.

- [ ] **Step 5: Push and re-read GitHub state**

  Push the implementation commits, then fetch/read `origin/main` and verify the worktree is clean except the two pre-existing user-owned untracked documents.

- [ ] **Step 6: Produce the final evidence report**

  Report each acceptance item as PASS, FAIL, or BLOCKED_BY_ENV with its authoritative command/runtime evidence. Do not mark the overarching goal complete while any required item lacks direct evidence.
