# Graph Validation Gates and Local Authentication Design

## Scope

This change closes the validation gaps described in the 2026-08-28 acceptance
instructions while keeping the production rollback path unchanged. It covers
the graph load gate, resource evidence, Shadow/Recovery gates, real
Observability-to-RCA evidence validation, and the local first-login
authentication override.

The implementation is code and contract work only. It must not change
`deploy/helm/aiops/values-prod.yaml` from `graph.backend: legacy_mysql` to
`hugegraph`, and it must not put a plaintext password in production values.
External cluster evidence is reported as blocked when the required dependency
is unavailable; an empty response is never treated as a passing observation.

## Decisions

### Graph performance

`deploy/scripts/graph-load-test.sh` will measure exactly eight operations:

| Operation | P95 gate |
| --- | ---: |
| entity UID exact lookup | `<100ms` |
| alias search `limit=20` | `<200ms` |
| 1-hop neighborhood | `<200ms` |
| 2-hop neighborhood | `<500ms` |
| shortest path, depth <= 6 | `<1000ms` |
| RCA candidate | `<1500ms` |
| impact | `<1500ms` |
| batch mutation of 500 | `<2000ms` |

Every operation must have `success_rate == 1` and satisfy its strict P95
threshold. A failure in any operation makes the performance gate `FAIL`.
The report must contain all eight rows; reports with missing rows cannot pass.

### Resource evidence

`deploy/scripts/graph-resource-snapshot.sh` will emit a JSON report with
collected status and values for:

- HugeGraph JVM RSS and heap used/max;
- RocksDB data and WAL size;
- query-api CPU/RSS;
- ai-investigation-worker CPU/RSS;
- frontend bundle bytes;
- browser Long Task count and maximum duration.

The collector uses Kubernetes metrics/cgroup and pod filesystem inspection
where available, `du` for the HugeGraph data/WAL paths, the frontend `dist`
directory for bundle bytes, and a Playwright/PerformanceObserver probe for
Long Tasks. `GRAPH_LOAD_REQUIRE_RESOURCES=1` requires every item to be
`collected`; any `not_collected`, unavailable metric, or malformed item makes
the resource gate fail.

### Shadow gate

`shadow-gate.sh` will require and evaluate the complete report contract:

```text
identity mismatch               = 0
structural mismatch             = 0
scope leak                      = 0
dead outbox                     = 0
outbox oldest P99               < 30s
Kubernetes sync lag             <= 900s
KubeVirt sync lag               <= 300s
Hardware sync lag               <= 1800s
Graph API 5xx                   < 0.1%
all Graph P95                   at the operation gates
Trace dependency mismatch       < 1%
fixed RCA scenario              PASS
```

Missing metrics are failures, not zeroes. The script accepts both the
canonical report fields and a bounded `sources`/`graph_p95` representation so
sampling and report generation remain separate from gate evaluation.

### Recovery gate

`graph-recovery-test.sh --inject` is an explicitly destructive local/staging
mode. It refuses production-like namespaces, contexts, and environment
markers, requires an explicit `GRAPH_RECOVERY_ENV` of `local` or `staging`,
and writes an evidence report. Read-only mode remains the default. The script
does not modify MySQL authority; recovery rebuilds the HugeGraph projection
from MySQL and checks schema/entity/edge identity, outbox state, source sync,
fixed paths, and historical RCA records after the rebuild.

### Real Evidence and RCA

`deploy/scripts/validate-observability-evidence.sh` is a separate validator.
It requires a unique marker and verifies, through configured real service
endpoints or `kubectl` queries, that metrics, logs, Kubernetes events,
DeepFlow flow/span data, service dependency edges, and a real RCA run all
contain evidence for that marker. It checks the RCA run time window,
independent evidence categories, final graph context, bounded propagation
path, deterministic root score, and the `CAUSED_BY` confirmation rule.
Unavailable dependencies produce `BLOCKED_BY_ENV` and a non-passing report.

### Local authentication

The existing `users.must_change_password` column remains authoritative state.
The query-api gains a fail-closed runtime switch:

```text
AUTH_REQUIRE_FIRST_LOGIN_PASSWORD_CHANGE=true   # default
```

Local bootstrap/validation values set it to `false` temporarily, so an
existing local admin with `must_change_password=1` can authenticate without
being redirected. Re-enabling the switch restores the stored first-login
policy. The local seed password becomes `admin1234`; existing users are never
overwritten by normal startup or Helm upgrade. A one-time local admin reset is
performed only when the current validation environment is explicitly
available, and the password value is never printed.

## Verification

Focused shell contract tests and Go unit tests run before broader suites. The
real 200k/1M graph load, observability/RCA evidence, physical-server chain,
full recovery, 24-hour Shadow, cutover, and 2-hour post-cutover observation
are executed only when their external dependencies are present. The code
changes may prepare and gate those runs, but cannot claim those external
stages passed without fresh evidence.
