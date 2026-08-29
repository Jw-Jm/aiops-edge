# Graph cutover runbook

## Preconditions

1. MySQL migration `0011_graph_projection` and HugeGraph schema migrator have completed successfully.
2. `graph_schema_state` checksum equals the embedded `schema_manifest_v2.json` checksum.
3. `shadow-gate.sh` is PASS for identity mismatch, structural mismatch, scope leak,
   dead outbox, outbox age, Kubernetes/KubeVirt/hardware lag, Graph API 5xx,
   all eight graph P95 operations, trace dependency mismatch, and the fixed RCA scenario.
4. `graph-load-test.sh` has loaded 200,000 ontology-shaped vertices and 1,000,000 unique typed edges and its eight operation P95 gates are PASS; a dry-run or one-entity smoke test is insufficient.
5. `graph-resource-snapshot.sh` has collected HugeGraph JVM/RocksDB, Query API,
   investigation worker, frontend bundle, and browser long-task evidence.
6. The fixed RCA scenarios have evidence, including frozen `ai_runs` time windows and actual propagation paths.

## Sequence

1. Keep `GRAPH_BACKEND=legacy_mysql` while running catalog/hardware/Kubernetes/KubeVirt backfill in the fixed order.
2. Set `GRAPH_BACKEND=shadow` and observe projection lag, stale generations, retry/dead counts and shadow diff for the full observation window.
3. Run `deploy/scripts/validate-observability-evidence.sh` with real metric/log/event,
   DeepFlow, dependency, and RCA evidence, then run `deploy/scripts/validate-local-stack.sh`
   and archive `/tmp/aiops-shadow-gate-result.json`.
4. Change only the query-api graph backend to `hugegraph`; restart through Helm and verify graph health/schema before serving graph reads.
5. If any gate fails, return to `legacy_mysql`. The MySQL control plane and outbox remain authoritative; do not manually edit HugeGraph.

`legacy_mysql` is a rollback adapter, not a second source of truth. `CAUSED_BY` is written only after confirmed RCA evidence is persisted.

## Local Kubernetes graph verification

本机测试环境不执行生产容量与资源门禁；只确认 graph 创建、schema 可读、Kubernetes
source reconcile 成功，以及一个当前 Node 已投影到 `DEFAULT/aiops`。部署后执行：

```bash
helm upgrade aiops deploy/helm/aiops -n observability --reuse-values --wait --timeout 15m
kubectl -n observability rollout restart deployment/ai-investigation-worker
kubectl -n observability rollout status deployment/ai-investigation-worker --timeout=10m
deploy/scripts/verify-kubernetes-graph.sh --namespace observability --since 15m
```

验证脚本只读访问 HugeGraph 与 Kubernetes；不会删除 PVC、修改集群对象或写入测试数据。
