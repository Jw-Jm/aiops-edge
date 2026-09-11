# Kubernetes Inventory Projection Data Contract

- Status: **Specified, not implemented** (see [ADR-0002](../architecture/ADR-0002-cluster-inventory-projection.md))
- Owner: query-api control plane
- Consumers: inventory store, graph worker, resource catalog

This contract fixes the wire shape and invariants of a future inventory
projection so that an implementation cannot drift from the ADR. Nothing here is
implemented today.

## Envelope

```text
InventoryEnvelope {
  tenant_id, cluster_id, kubernetes_identity_uid,
  snapshot_generation, mode(full|delta),
  chunk_index, chunk_count, payload_bytes,
  started_resource_version, finished_resource_version,
  observed_at, resources[], deletions[]
}
```

## Chunking

- Target chunk size **4 MiB**, hard ceiling **8 MiB** (`payload_bytes` is the
  serialized chunk size; the receiver rejects anything above the ceiling).
- Chunks of one generation must have **contiguous `chunk_index`** starting at 0
  and a consistent `chunk_count`.
- All chunks must carry the same `tenant_id`, `cluster_id`, and
  `kubernetes_identity_uid`; a mismatch with the registered cluster identity is a
  **fail-closed rejection**.

## Ordering, replay, and deletion

- `full` establishes a generation; `delta` must reference the last successfully
  completed `full` generation.
- Deltas carrying an older `snapshot_generation` than the accepted one are
  **idempotently ignored**, not applied.
- A repeated chunk (same generation + `chunk_index`) is idempotently ignored.
- A missing chunk makes the generation incomplete: it must not be served.
- Deletions are keyed by **UID** in `deletions[]`; name-only deletion is invalid.
- Every resource carries canonical `cluster_id`, UID, `resourceVersion`,
  `observed_at` and `last_seen`. `last_seen` is how staleness is computed; it is
  never inferred from arrival time.

## Reconnection

- The collector resumes from its last acknowledged
  `finished_resource_version`.
- If the API server reports that the resource version is too old (410 Gone), the
  collector must start a **new full snapshot generation**; it must never guess or
  reuse a stale delta.

## Security invariants

1. `k8sboundary` remains the only credential and identity path; the collector is
   read-only and never persists kubeconfig or token.
2. Only the fixed, allow-listed resource set may be projected.
3. The inventory store holds cleaned resource facts only — no Secrets, no user
   data, no raw payload dumps.
4. Cross-tenant or cross-cluster reads are rejected by the same authorization
   predicate that governs the live read path.

## Acceptance criteria before any implementation ships

- Full/delta ordering, reconnection, out-of-order and duplicate rejection, and
  missing-chunk detection are covered by fail-closed tests.
- Identity mismatch between envelope and the registered cluster fails closed.
- Projection freshness p95 ≤ 120 seconds with no silent object loss.
- query-api key read p95 does not regress by more than 10%.

Until these are demonstrated, [ADR-0002](../architecture/ADR-0002-cluster-inventory-projection.md)
remains **deferred** and this contract stays unimplemented.
