# SDD ledger — plan: docs/superpowers/plans/2026-08-19-aiops-phase2-trust-boundary.md

## Pre-flight review

| Item | Files/interfaces | Finding | Ruling |
|---|---|---|---|
| Task 1 self-consistency | Go/Python TrustedRequestContext signing/verifying | Go is the enforcement boundary; Python only signs. Both use the Phase 1 context field set and stable errors. | Proceed; keep private keys out of fixtures and commits. |
| Task 2 self-consistency | MySQL schema, UserDAO, ClusterDAO, AuthorizationDAO | Existing integer IDs, legacy `role/scope`, and `kubeconfig` conflict with target identity. | Add canonical UUID/authority fields and fail-closed DAO seams; retain legacy columns only as non-authoritative migration metadata. |
| Task 3 self-consistency | auth middleware, handlers, resolver, ProxyAI | Existing handlers read JWT role/scope and request headers directly. | Centralize effective context and make compatibility `X-Tenant-ID` a validated request hint only. Do not silently preserve old authority. |
| Task 4 self-consistency | orchestrator callers and query-api proxy | Existing callers depend on permanent `INTERNAL_TOKEN` and forged internal role headers. | Migrate to service credential + signed context; raw shell remains outside canonical path. |
| Task 5 self-consistency | frontend client/store/contracts | Existing UI persists numeric cluster IDs and `all`, and sends tenant header globally. | Keep header only as transitional requested scope, migrate identity to UUID/slug, and permit aggregate only through explicitly authorized server endpoints. |
| Task 1 ↔ Task 3 | `internal/contract.RequestContext` consumed by auth verifier | Task 3 needs the signed context verifier and stable errors from Task 1. | Task 1 must expose a package-level verifier without wiring routes. |
| Task 2 ↔ Task 3 | `ClusterDAO.ResolveRef`, `AuthorizationDAO.Authorize` | Resolver and middleware share canonical UUID and authorization decision types. | Task 2 defines the exact store interfaces before Task 3 integrates them. |
| Task 3 ↔ Task 4 | ProxyAI internal headers and signed context | Task 4 must send exactly what Task 3 accepts, and Task 3 must strip client-forged headers. | Use one versioned internal header contract; no fallback to old role headers. |
| Task 3 ↔ Task 5 | `/resources/resolve`, cluster selection and tenant hint | Frontend consumes canonical UUID responses while compatibility tenant input remains temporary. | Task 5 does not bypass server authorization or create trusted context in the browser. |

## Rulings

- Ruling: `X-Tenant-ID` remains accepted only as a requested tenant scope during migration — the user explicitly froze this compatibility boundary, and removing it immediately would break existing callers without preserving authorization safety.
- Ruling: legacy `clusters.id` and `clusters.kubeconfig` remain physically present but non-authoritative in Phase 2 — immediate destructive schema removal would risk effective cluster configuration, while new code must not read or expose kubeconfig.
- Ruling: JWT role/scope claims are ignored immediately, even before all callers are migrated — stale authorization claims are a P0 security defect and MySQL is the only authority.
- Ruling: `all`, empty cluster and current kube-context fallback are rejected for cluster-scoped operations — broad aggregation must be an explicit server-authorized capability, not an implicit target.

## Task progress

- Task 1: complete — commits `8e5747f`, `4a2a2e8`; task review initially found and re-review approved the replay-window fix. Full Python suite remains blocked by the pre-existing Python 3.9/LangGraph environment noted in the report.
- Task 2: complete — commits `61448eb`, `127b415`; initial review found four P1 authority gaps and scoped re-review approved all fixes. Live MySQL DDL/backfill remains unexecuted and is documented as a Gate concern.
- Task 3: complete — commit `7350174`; P1 fix `8b2bf15`; scoped review and re-review approved. Login now persists canonical sessions before JWT issuance; ProxyAI fails closed without a server-signed context. Legacy protected handlers remain fail-closed pending Task 4/5 migration.
- Task 4: complete — commit `9a6dd24`; follow-up fixes `2ed6b69`, `adf38c0`; scoped review and final re-review approved. All active orchestrator/query-api callers now require explicit canonical RequestContext, use service credential plus signed context, and fail closed on missing/default/implicit scope. R4 shell proxy remains manual-only/fail-closed.
- Task 5: pending
