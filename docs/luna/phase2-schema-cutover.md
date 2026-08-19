# Phase 2 MySQL Authority and Cluster Registry Cutover

## Scope and ownership

`ai-apm-query-go` is the sole MySQL DDL/DML owner for the authority tables introduced here. The schema is initialized by `store.EnsureSchema`; the orchestrator does not connect to MySQL. This cutover adds effective identity, authorization, and cluster-configuration metadata only. It does not copy, rewrite, or delete metrics, logs, traces, topology, alert/event history, AI run history, evidence, action history, or audit history.

## Additive schema changes

- `users.user_uuid` is an additive canonical user identity. Existing users receive a generated lowercase UUID only when this field is missing.
- `user_sessions` records a UUID session, owning `user_uuid`, current lifecycle status, token version, expiry, and revocation time.
- `user_tenants` records active user-to-tenant membership.
- `roles`, `permissions`, `user_roles`, and `role_permissions` express role-to-action grants. A role alone grants nothing without a matching permission.
- `scope_assignments` binds a role to one explicit tenant, canonical cluster UUID, namespace, resource type, resource name, and action. Empty values and implicit `all` scopes are not authorization fallbacks.
- `clusters` retains existing rows and adds `cluster_id`, `tenant_id`, `slug`, `environment`, `credential_ref`, and `lifecycle_status`. Existing rows receive a UUID and an unambiguous `legacy-<id>` slug only when unset. An unmapped `tenant_id` remains unset and cannot authorize requests until explicitly mapped. Existing `active`, `degraded`, and `down` status values become `ready`, `degraded`, and `disabled` lifecycle metadata respectively.

`cluster_id` and `slug` receive unique indexes. The legacy integer `clusters.id` stays during the migration, but new registry resolution accepts only lowercase UUID or slug and always returns `cluster_id` as authority.

## Authorization decision

`AuthorizationDAO.Authorize` reads current MySQL rows in this order: user/session state, tenant membership, canonical cluster resolution, role permission for the requested action, then the exact scope assignment. It does not accept caller-provided role or scope claims. Missing/disabled identity, tenant mismatch, cluster mismatch, missing action, missing scope, malformed context, and MySQL errors all deny access; MySQL errors also return an error for caller-level availability handling.

## Credential and legacy-column handling

The canonical registry returns only `credential_ref`; it never selects, writes, or serializes `kubeconfig`. The following legacy storage remains intact solely to avoid destructive configuration loss while callers are migrated:

- `clusters.id`: legacy integer migration metadata.
- `clusters.kubeconfig`: legacy credential storage; new registry and authorization methods never access it, and the `Cluster` JSON form excludes it.
- `clusters.status`: legacy operational status retained while `lifecycle_status` becomes the canonical registry lifecycle field.
- Existing `name`, `provider`, `version`, `node_count`, and `api_server`: retained presentation/inventory metadata; `name` is not an authorization reference.

## Controlled deletion order

1. Migrate all API/Kubernetes access callers to accept canonical `cluster_id` or slug, resolve via `ClusterDAO.ResolveRef`, and use a credential access boundary that consumes only `credential_ref`.
2. Verify no API route, serializer, DAO query, migration, or deployment config reads/writes `kubeconfig`; verify integer IDs and names are no longer accepted as cluster authority.
3. Back up/rotate credential material through the designated Secret/Vault owner, then remove the legacy `kubeconfig` read/write paths and column in a separately approved migration.
4. Migrate dependent foreign keys and persisted references from `clusters.id` to `cluster_id`, verify UUID-only joins and data ownership, then remove `id` only after all consumers and rollback windows are retired.
5. Retire legacy `status` only after every writer and reader uses `lifecycle_status`; keep historical observability and AI runtime data out of this operation.

No destructive step is part of Task 2.
