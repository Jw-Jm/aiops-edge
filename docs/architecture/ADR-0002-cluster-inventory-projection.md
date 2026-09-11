# ADR-0002: Persistent Cluster Inventory Projection

- Status: **暂缓（Deferred）**
- Date: 2026-09-11
- Decision owners: Platform Architecture / Data Platform
- Related contract: [kubernetes-inventory-projection](../contracts/kubernetes-inventory-projection.md)
- Related ADR: [ADR-0001 control plane ownership](ADR-0001-control-plane-ownership.md)

## 1. Context

Today every user-visible Kubernetes/KubeVirt read is served on demand through the
canonical cluster boundary:

```
query-api → k8sboundary (cluster_id → credential_ref → Secret → kubeconfig → kube-system UID)
          → kubectl get <resource> -A -o json   (bounded, allow-listed resource set)
```

This satisfies the security contract (no default kubeconfig, no current context, no
name-based cross-cluster fallback) but it re-reads the same objects on every page
and graph rebuild. A *persistent inventory projection* (full snapshot + watch
deltas into MySQL/object storage) has been proposed to remove that repeated cost.

## 2. Current-state measurement

Measurements below are produced by running `KubeGraphObjects` against an isolated
test cluster and recording total response bytes, wall time, query-api peak RSS,
API-server request count, and graph build time.

| Objects per cluster | Response bytes | Latency p95 | query-api peak RSS | API-server requests | Graph build |
| ------------------- | -------------- | ----------- | ------------------ | ------------------- | ----------- |
| 1k                  | NOT_MEASURED   | NOT_MEASURED | NOT_MEASURED      | NOT_MEASURED        | NOT_MEASURED |
| 10k                 | NOT_MEASURED   | NOT_MEASURED | NOT_MEASURED      | NOT_MEASURED        | NOT_MEASURED |
| 100k                | NOT_MEASURED   | NOT_MEASURED | NOT_MEASURED      | NOT_MEASURED        | NOT_MEASURED |

Capacity extrapolation:

| Clusters | Objects total | Projected storage | Projected feed rate |
| -------- | ------------- | ----------------- | ------------------- |
| 1        | NOT_MEASURED  | NOT_MEASURED      | NOT_MEASURED        |
| 10       | NOT_MEASURED  | NOT_MEASURED      | NOT_MEASURED        |
| 50       | NOT_MEASURED  | NOT_MEASURED      | NOT_MEASURED        |

No number in this ADR is an estimate. Every unmeasured cell is `NOT_MEASURED`,
and per the decision rule below that alone forces **defer**.

## 3. Considered options

1. **Adopt** — run a collector that uploads full snapshots and watch deltas into
   an inventory store; query-api reads the projection instead of the live API.
2. **Defer** — keep on-demand canonical reads, complete the measurement and
   prototype work, revisit with real numbers.
3. **Reject** — keep on-demand reads permanently because the current path meets
   the SLO at the target scale and the projection does not pay for itself.

## 4. Decision

**暂缓（Deferred）。**

The adoption threshold requires all of the following, and today none of them is
proven:

- On-demand snapshotting demonstrably hurts page/graph latency or API-server
  health at the 10k-object baseline — NOT_MEASURED.
- A prototype proves `resourceVersion`-invalidated reconnection degrades into a
  fresh full snapshot — NOT_BUILT.
- Out-of-order, duplicate, missing-chunk, and cross-cluster identity mismatch are
  all fail-closed — specified in the data contract, NOT_IMPLEMENTED.
- End-to-end projection freshness p95 ≤ 120 seconds with no silent object loss —
  NOT_MEASURED.
- query-api key read p95 does not regress by more than 10% — NOT_MEASURED.
- Capacity, retention, compression, rebuild and rollback cost are quantified and
  approved by the architecture owner — NOT_MEASURED / NOT_APPROVED.

Because the measurements are missing rather than failing, the correct verdict is
**defer**, not reject: the on-demand path may still be the right answer, and no
inventory table, collector, or second API may be created until the threshold is
either met or shown to be unreachable.

## 5. Consequences

- No new MySQL tables, collectors, workers, or Helm resources are created by this
  decision.
- The canonical boundary (`k8sboundary`) remains the only Kubernetes credential
  and identity path for any future collector.
- HugeGraph stays a rebuildable projection and never becomes the resource
  authority.
- The measurement plan in §6 becomes the entry condition for reopening this ADR.

## 6. Measurement plan (required before reopening)

1. Deploy an isolated test cluster; seed 1k / 10k / 100k objects (mixed
   Deployment/Pod/Service/PVC/VMI shapes).
2. For each size, run 50 sequential `KubeGraphObjects` calls and record response
   bytes, p50/p95 latency, query-api peak RSS, API-server request count, and
   graph build time.
3. Repeat with 1, 10, and 50 registered clusters to extrapolate feed rate and
   storage.
4. Record the numbers directly into §2 — estimates are not acceptable.
5. Only then evaluate §4's threshold.

## 7. Ownership if adopted later

- **collector** — read-only pull through `k8sboundary`, chunked upload; never
  stores kubeconfig/token, never writes to Kubernetes.
- **query-api control plane** — receives chunks, validates identity and order,
  owns the inventory store schema and retention.
- **inventory store** — a cleaned resource-fact projection only; no secrets, no
  user data.
- **graph worker** — consumes the projection; keeps HugeGraph rebuildable.

## 8. Rollback

Because nothing is adopted, there is nothing to roll back. If a future
implementation ships, its rollback must be: stop the collector → drain the
receive queue → drop reads from the projection (readers fall back to the
on-demand canonical path) → retain or drop the projection data per its retention
contract. The on-demand path must be proven live before any cutover.
