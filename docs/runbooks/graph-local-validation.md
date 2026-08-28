# Local graph validation

The validation profile uses OrbStack Kubernetes, namespace `observability`, and
the only mutation canary namespace `aiops-canary`. It deploys Apache HugeGraph
1.7.0 with its RocksDB data directory at `/var/lib/hugegraph/data`. The local
200k/1M gate profile requests 2Gi and limits HugeGraph to 10Gi, with a startup
probe that allows the large RocksDB fixture to open before liveness checks.

```bash
LLM_PROVIDER_KEYS='deepseek:<real-key>' \
  bash deploy/scripts/local-validation.sh --skip-deepflow
```

The command performs Helm contract checks, MySQL/graph schema migration,
runtime readiness, executor-disabled/RBAC checks and graph recovery evidence.
No fake provider key is generated. If the local cluster or a real provider is
not reachable, the result is explicitly `BLOCKED_BY_ENV` rather than an empty
success state.

Read-only follow-ups:

```bash
bash deploy/scripts/graph-recovery-test.sh
bash deploy/scripts/validate-local-stack.sh
```

## Graph performance gate

The performance gate is a real fixture load, not a one-entity smoke test. It
uses the typed `ai-apm-query-go/cmd/graph-load-generator` to write exactly
200,000 vertices and 1,000,000 unique `DEPENDS_ON` edges, then measures P95 for
entity, 1-hop, 2-hop, shortest path, RCA candidate, impact and batch mutation.
The fixed limits are 500/1000/2000/3000/3000/3000/1000 ms in that order.

```bash
HUGEGRAPH_URL=http://127.0.0.1:8080 \
GRAPH_API_BASE_URL=http://127.0.0.1:8080/api/v1/ai/kg \
GRAPH_API_TOKEN='<jwt>' \
bash deploy/scripts/graph-load-test.sh --output /tmp/aiops-graph-load-report.json
```

`--dry-run` only validates the requested 200k/1M shape. It is not a performance
pass. Missing HugeGraph access, authentication, or real observations must be
reported as `BLOCKED_BY_ENV`.

The report validates that the fixture loader returned the exact requested
counts and stores a `fixture_loader` record. It also stores explicit resource
evidence for HugeGraph JVM RSS/heap, RocksDB disk/WAL, query-api CPU/RSS,
orchestrator CPU/RSS, frontend bundle size, and browser long tasks. Use
`GRAPH_LOAD_REQUIRE_RESOURCES=1` in a release environment to make incomplete
resource collection a blocking result; local runs remain transparent when
cluster metrics or a browser trace are unavailable.

The fixture writer is idempotent by UID but repeated full loads in the same
RocksDB process can grow the server cache. Run the full gate once per fresh
validation install, archive its JSON report, and do not treat a second load of
the same fixture as a new independent performance sample.

## Service panorama contracts

The service panorama does not join legacy `/services` and `/topology/global`
responses in the browser. Query API owns the dedicated contracts:

```text
GET /api/v1/services/overview
GET /api/v1/services/map?group_by=application|namespace
GET /api/v1/services/{entity_uid}/dependencies
GET /api/v1/services/dependency-matrix
```

The map is grouped by Application when identity is available and falls back to
Namespace. Cross-group edges are aggregated with route/call/error/latency
metrics. The matrix is sparse and capped at 200 services; expert G6/Dagre
exploration is the only raw relationship view.

## RCA production wiring

Persistent Investigation work is accepted only after query-api returns the
stored `ai_runs.time_range_start/time_range_end`. The worker carries those
absolute bounds into `RCARequest`, then executes the fixed sequence:

```text
resolve_entity → candidate_subgraph → bounded typed evidence queries
→ deterministic temporal/root score → graph-context append → explanation
```

Graph and evidence reads use `InternalQueryClient` with a distinct,
retry-stable ToolRun/lease identity per graph or evidence operation. Query API
rejects missing, reversed, or Run-mismatched bounds and applies the persisted
absolute window to metrics, logs, traces, alerts, and changes; it never
re-expands a historical Investigation as `now - N minutes`. Query results
eligible for evidence are consumed through the control-plane evidence
boundary; graph context is appended to `ai_run_graph_contexts` and marked final
only at the terminal Run commit (including an explicit local-only context when
the graph backend is unavailable). The old `rca.py` exports remain a
compatibility facade for non-Run development/K8s callers; production Run RCA
cannot execute without a persisted Run and active lease.
