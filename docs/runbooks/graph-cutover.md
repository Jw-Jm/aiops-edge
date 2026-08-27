# Graph cutover runbook

## Preconditions

1. MySQL migration `0011_graph_projection` and HugeGraph schema migrator have completed successfully.
2. `graph_schema_state` checksum equals the embedded `schema_manifest_v2.json` checksum.
3. `shadow-gate.sh` is PASS for identity, structural, scope, lag and P95; outbox has no dead rows.
4. `graph-load-test.sh` meets the published P95 limit and the fixed RCA scenarios have evidence.

## Sequence

1. Keep `GRAPH_BACKEND=legacy_mysql` while running catalog/hardware/Kubernetes/KubeVirt backfill in the fixed order.
2. Set `GRAPH_BACKEND=shadow` and observe projection lag, stale generations, retry/dead counts and shadow diff for the full observation window.
3. Run `deploy/scripts/validate-local-stack.sh` and archive `/tmp/aiops-shadow-gate-result.json`.
4. Change only the query-api graph backend to `hugegraph`; restart through Helm and verify graph health/schema before serving graph reads.
5. If any gate fails, return to `legacy_mysql`. The MySQL control plane and outbox remain authoritative; do not manually edit HugeGraph.

`legacy_mysql` is a rollback adapter, not a second source of truth. `CAUSED_BY` is written only after confirmed RCA evidence is persisted.
