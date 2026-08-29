# Fix aiops HugeGraph Graph Creation and Kubernetes Backfill Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the configured `DEFAULT/aiops` HugeGraph graph exist at server startup, make schema migration fail closed when it does not, and prove that the configured Kubernetes cluster is written into the graph.

**Architecture:** HugeGraph will load the named `aiops` graph from a static `.properties` file at startup instead of relying only on a dynamic REST graph-creation request. The schema migrator will verify the named graph after ensuring it and before recording MySQL schema metadata. Once the graph is ready, the existing investigation-worker graph source runtime will perform a fresh Kubernetes reconcile through the existing signed query and control-plane boundaries.

**Tech Stack:** Apache HugeGraph 1.7.0, RocksDB, Kubernetes StatefulSet/ConfigMap, Helm, Go, Python, pytest, shell contract tests.

**Spec:** `docs/superpowers/specs/2026-08-28-graph-validation-gates-and-local-auth-design.md`

## Global Constraints

- `HugeGraph = Derived Graph Projection`; MySQL, Kubernetes, and observability stores remain authoritative.
- Kubernetes data must be read through the existing canonical cluster identity and credential boundary.
- Graph writes remain behind the existing query-api/control-plane boundary.
- Empty or unavailable sources must not become a successful graph build.
- Production values remain on their existing rollback-safe backend configuration.
- Do not delete MySQL, ClickHouse, VictoriaMetrics, VictoriaLogs, or ingest PVCs.
- Graph credentials and the local admin password must never be printed.

---

## Current diagnosis

Fresh runtime evidence shows that the source side is working but the graph projection is not:

```text
query_k8s.v1: HTTP 200, quality=complete, current cluster objects returned
[kg-reconcile] source=kubernetes status=failed mutations=0 batches=0 error=unavailable
GET /graphs: {"graphs":["hugegraph"]}
GET /graphspaces/DEFAULT/graphs/aiops: HTTP 500
```

The MySQL `graph_schema_state` row says schema version 2 was applied to `aiops`, but that row is metadata only. The repair must make server-side graph existence authoritative before accepting that metadata.

## Files and responsibilities

- Create `deploy/helm/aiops/templates/hugegraph-graph-config.yaml`: static named-graph configuration.
- Modify `deploy/helm/aiops/templates/hugegraph-statefulset.yaml`: mount config and create isolated RocksDB paths.
- Modify `ai-apm-query-go/internal/graph/hugegraph_client.go`: verify the configured graph after creation.
- Modify `ai-apm-query-go/internal/graph/hugegraph_client_test.go`: graph creation/verification regression tests.
- Modify `ai-apm-query-go/cmd/graph-schema-migrator/main.go`: persist schema state only after graph verification.
- Modify `deploy/scripts/test-deployment-contracts.sh`: assert the named graph deployment contract.
- Add `deploy/scripts/test-aiops-graph-contract.sh`: render-only Helm contract.
- Add `deploy/scripts/verify-kubernetes-graph.sh`: read-only runtime verification.
- Update `docs/runbooks/graph-cutover.md`: named graph and backfill diagnosis.

### Task 1: Add a regression test for post-creation graph verification

**Files:**
- Modify: `ai-apm-query-go/internal/graph/hugegraph_client_test.go`
- Modify: `ai-apm-query-go/internal/graph/hugegraph_client.go`

- [ ] Add an httptest case where graph listing returns only `hugegraph`, the POST to `/graphspaces/DEFAULT/graphs/aiops` returns `201`, and the following GET to `/graphspaces/DEFAULT/graphs/aiops` returns `500`. Assert `EnsureGraph` returns an error containing `HTTP 500`.

- [ ] Run the focused test before implementation:

```bash
cd ai-apm-query-go
go test ./internal/graph -run TestEnsureGraphVerifiesCreatedGraph -count=1 -v
```

Expected: FAIL because the current implementation returns after POST and does not verify the graph.

- [ ] Add a helper that GETs the configured graph root, parses the graph object, and rejects a non-RocksDB backend. Call it after both the existing-graph branch and the create-graph branch.

- [ ] Run all graph client tests:

```bash
cd ai-apm-query-go
go test ./internal/graph -count=1 -v
```

Expected: PASS.

### Task 2: Load `DEFAULT/aiops` statically at HugeGraph startup

**Files:**
- Create: `deploy/helm/aiops/templates/hugegraph-graph-config.yaml`
- Modify: `deploy/helm/aiops/templates/hugegraph-statefulset.yaml`
- Add: `deploy/scripts/test-aiops-graph-contract.sh`

- [ ] Add a render-only contract that requires these strings in the enabled HugeGraph manifest:

```text
name: hugegraph-graph-config
aiops.properties
store=aiops
rocksdb.data_path=/var/lib/hugegraph/data/aiops
rocksdb.wal_path=/var/lib/hugegraph/wal/aiops
mountPath: /hugegraph-server/conf/graphs/aiops.properties
```

- [ ] Render a ConfigMap key named `aiops.properties` with the following properties, using Helm values for the graph name:

```properties
gremlin.graph=org.apache.hugegraph.auth.HugeFactoryAuthProxy
backend=rocksdb
serializer=binary
store={{ .Values.hugeGraph.graph }}
task.scheduler_type=local
task.schedule_period=10
task.retry=0
task.wait_timeout=10
search.text_analyzer=jieba
search.text_analyzer_mode=INDEX
rocksdb.data_path=/var/lib/hugegraph/data/{{ .Values.hugeGraph.graph }}
rocksdb.wal_path=/var/lib/hugegraph/wal/{{ .Values.hugeGraph.graph }}
```

- [ ] Add a read-only `graph-config` volume and mount the configured `<graph>.properties` file at `/hugegraph-server/conf/graphs/<graph>.properties` with `subPath`.

- [ ] Add a pod-template checksum annotation for the graph ConfigMap so changing the config restarts HugeGraph.

- [ ] Extend the existing container startup command with:

```bash
mkdir -p "/var/lib/hugegraph/data/{{ .Values.hugeGraph.graph }}" "/var/lib/hugegraph/wal/{{ .Values.hugeGraph.graph }}"
```

Keep the current default `hugegraph.properties` rewrite; the named graph needs its own `store` and paths.

- [ ] Run the render contract and Helm lint:

```bash
deploy/scripts/test-aiops-graph-contract.sh
helm lint deploy/helm/aiops
```

Expected: both exit 0.

### Task 3: Make schema migration fail closed

**Files:**
- Modify: `ai-apm-query-go/cmd/graph-schema-migrator/main.go`
- Modify: `ai-apm-query-go/internal/graph/hugegraph_client.go`
- Modify: `ai-apm-query-go/internal/graph/hugegraph_client_test.go`

- [ ] Keep the migrator sequence explicit:

```text
NewHugeGraphClient
EnsureGraph
VerifyConfiguredGraph
EnsureSchema
Upsert graph_schema_state
```

- [ ] Add a focused test proving that verification failure happens before the schema-state upsert and before a success log.

- [ ] Ensure verification errors log only graphspace/graph and error class; never credentials or authorization headers.

- [ ] Run the client and migrator tests/build:

```bash
cd ai-apm-query-go
go test ./internal/graph ./cmd/graph-schema-migrator -count=1
go build -mod=vendor ./cmd/graph-schema-migrator
```

Expected: exit 0.

### Task 4: Add a read-only Kubernetes graph verification command

**Files:**
- Create: `deploy/scripts/verify-kubernetes-graph.sh`
- Modify: `deploy/scripts/test-deployment-contracts.sh`
- Modify: `docs/runbooks/graph-cutover.md`

- [ ] Accept `--namespace` with default `observability`; use existing Secret references only inside `kubectl exec`; never print passwords or tokens.

- [ ] Require the authenticated HugeGraph graph list to contain `aiops`, require `GET /graphspaces/DEFAULT/graphs/aiops` to return 2xx, and require the `Entity` vertex label to be readable.

- [ ] Read recent investigation-worker logs and require a Kubernetes line matching:

```text
source=kubernetes status=success mutations=[1-9][0-9]* batches=[1-9][0-9]*
```

Reject missing, `failed`, `unavailable`, or `no_data` results.

- [ ] Obtain a current node UID from the configured Kubernetes source, derive the existing `k8s_entity_uid("k8s_node", cluster_id, object_uid)` format, and call the authenticated typed entity endpoint. Require `entity_type=k8s_node`, the configured cluster ID, and `source=kubernetes`.

- [ ] Add a shell contract test that checks the graph, schema, reconcile-success, and entity checks and rejects password output.

- [ ] Document the diagnosis flow:

```text
source=kubernetes status=failed error=unavailable
→ inspect GET /graphs
→ if aiops is absent, repair the static named-graph ConfigMap/StatefulSet
→ wait for graph-schema-migrator Complete
→ restart investigation-worker for a fresh initial reconcile
→ run verify-kubernetes-graph.sh
```

### Task 5: Deploy locally and trigger a fresh Kubernetes backfill

**Files:**
- Deployment state only; no authoritative data deletion.
- Test: `deploy/scripts/verify-kubernetes-graph.sh`

- [ ] Record release state:

```bash
git status --short
helm history aiops -n observability
```

The current known local rollback target is revision 5; record the actual revision again before applying changes.

- [ ] Render and lint the exact local release:

```bash
helm lint deploy/helm/aiops
helm template aiops deploy/helm/aiops -n observability --reuse-values > /tmp/aiops-graph-repair-rendered.yaml
```

Confirm the named graph config is mounted before applying.

- [ ] Apply the local Helm upgrade:

```bash
helm upgrade aiops deploy/helm/aiops -n observability --reuse-values --wait --timeout 15m
```

Do not delete unrelated PVCs.

- [ ] Before restarting workers, require the named graph in the graph list and require `graph-schema-migrator` completion. Stop if either check fails.

- [ ] Trigger the existing startup backfill:

```bash
kubectl rollout restart deployment/ai-investigation-worker -n observability
kubectl rollout status deployment/ai-investigation-worker -n observability --timeout=10m
```

The per-source lease prevents duplicate writes while the two workers start.

- [ ] Run the read-only verification:

```bash
deploy/scripts/verify-kubernetes-graph.sh --namespace observability
```

Expected: named graph present, schema readable, Kubernetes reconcile success with non-zero mutations/batches, and one current node entity readable.

- [ ] Verify the old graph-load marker count remains zero; the verification command must not create test vertices or edges.

### Task 6: Final verification and rollback

**Files:**
- Test: `ai-apm-query-go/internal/graph/hugegraph_client_test.go`
- Test: `deploy/scripts/test-aiops-graph-contract.sh`
- Test: `deploy/scripts/test-deployment-contracts.sh`

- [ ] Run focused tests:

```bash
cd ai-apm-query-go
go test ./internal/graph -count=1
cd ..
deploy/scripts/test-aiops-graph-contract.sh
deploy/scripts/test-deployment-contracts.sh
helm lint deploy/helm/aiops
```

- [ ] Capture only non-secret runtime evidence: Helm status, HugeGraph readiness/restarts, PVC binding, schema migrator completion, named graph presence, Kubernetes reconcile status, entity source/type, and graph API health.

- [ ] If named graph or backfill verification fails, preserve evidence and roll back to the revision recorded in Task 5:

```bash
helm rollback aiops 5 -n observability --wait --timeout 15m
```

Do not remove the new graph PVC during rollback.

- [ ] Commit the repair only after all code, Helm, graph existence, and Kubernetes entity checks pass:

```bash
git add deploy/helm/aiops/templates/hugegraph-graph-config.yaml deploy/helm/aiops/templates/hugegraph-statefulset.yaml deploy/scripts/test-aiops-graph-contract.sh deploy/scripts/verify-kubernetes-graph.sh deploy/scripts/test-deployment-contracts.sh docs/runbooks/graph-cutover.md ai-apm-query-go/cmd/graph-schema-migrator/main.go ai-apm-query-go/internal/graph/hugegraph_client.go ai-apm-query-go/internal/graph/hugegraph_client_test.go
git commit -m "fix: restore Kubernetes knowledge graph backfill"
```

## Self-review

- The plan addresses the observed failure: complete Kubernetes source reads, absent `aiops` graph, and fail-closed write errors.
- It does not treat MySQL schema metadata or the HugeGraph health probe as proof that `DEFAULT/aiops` contains data.
- It verifies both graph existence and one real Kubernetes entity through the typed public API.
- It adds no production backend switch, authoritative-data deletion, password output, or unrestricted graph query.
- It reuses the existing reconcile runtime and access boundaries.
