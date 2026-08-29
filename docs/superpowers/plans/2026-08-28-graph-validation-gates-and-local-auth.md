# Graph Validation Gates and Local Authentication Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Align graph validation scripts with the eight-operation acceptance contract, collect real runtime resources, enforce complete Shadow/Recovery/Evidence gates, and make the local admin password `admin1234` without requiring first-login password change.

**Architecture:** Keep MySQL as the authority and HugeGraph as a rebuildable projection. Make each gate consume an explicit JSON contract and fail closed on missing data. Apply the password override at query-api runtime with a secure default of requiring first-login change, while enabling the temporary bypass only in local validation Helm profiles.

**Tech Stack:** Bash, Python 3, `kubectl`, `curl`, Go 1.25, MySQL-backed JWT sessions, bcrypt, Helm/Kubernetes, Playwright/PerformanceObserver when available.

**Spec:** `docs/superpowers/specs/2026-08-28-graph-validation-gates-and-local-auth-design.md`

## Global Constraints

- Production `graph.backend` remains `legacy_mysql` until every pre-cutover gate passes.
- The eight graph operations use strict P95 thresholds: 100, 200, 200, 500, 1000, 1500, 1500, and 2000 milliseconds in the documented order.
- `GRAPH_LOAD_REQUIRE_RESOURCES=1` rejects any resource item whose status is not `collected`.
- Missing Shadow, Recovery, observability, RCA, physical-server, or browser evidence is `BLOCKED_BY_ENV`, never PASS.
- `AUTH_REQUIRE_FIRST_LOGIN_PASSWORD_CHANGE` defaults to `true`; only local/validation profiles set it to `false`.
- `admin1234` is a local bootstrap credential; password hashes, plaintext secrets, and JWTs must never be printed.
- Recovery injection is allowed only for explicit `local` or `staging` environments and never for production-like namespaces or contexts.

---

### Task 1: Make the local authentication override testable

**Files:**
- Modify: `ai-apm-query-go/internal/api/auth.go`
- Modify: `ai-apm-query-go/cmd/api/main.go`
- Modify: `deploy/helm/aiops/templates/query-api/deployment.yaml`
- Modify: `deploy/helm/aiops/values.yaml`
- Modify: `deploy/helm/aiops/values-local-validation.yaml`
- Test: `ai-apm-query-go/internal/api/password_test.go`
- Test: `ai-apm-query-go/cmd/api/main_test.go`

**Interfaces:**
- Produces `requireFirstLoginPasswordChange() bool`, defaulting to `true` and recognizing explicit false values.
- Login and `AuthorizationContext` expose the effective flag (`stored_flag && runtime_switch`), while the stored MySQL flag remains unchanged.
- Helm renders `AUTH_REQUIRE_FIRST_LOGIN_PASSWORD_CHANGE` from a local-profile value.

- [ ] **Step 1: Add failing tests for the runtime switch.**

  Cover unset/true/false/invalid environment values and prove a stored
  `must_change_password=1` authorization context is effective false only when
  the switch is explicitly disabled. Add a deployment contract assertion that
  local validation renders the switch as false and the base default is true.

- [ ] **Step 2: Run the focused Go tests and verify the expected failure.**

  Run:

  ```bash
  cd ai-apm-query-go
  go test ./internal/api ./cmd/api -run 'Test.*FirstLogin|Test.*PasswordChange|Test.*Auth'
  ```

  Expected: FAIL because the runtime switch and Helm wiring do not exist.

- [ ] **Step 3: Implement the minimal runtime switch and effective flag.**

  Parse `AUTH_REQUIRE_FIRST_LOGIN_PASSWORD_CHANGE` on each authorization
  decision so tests and profile changes are deterministic. Treat an unset or
  malformed value as enabled. Use the effective value in login responses,
  `resolveMySQLAuthorizationContext`, and `/me`; do not update the database
  flag merely because the bypass is active.

- [ ] **Step 4: Set local bootstrap password and switch values.**

  Change the local default `secrets.adminInitialPassword` to `admin1234`, add
  `authRequireFirstLoginPasswordChange: true` to the base values, set it to
  `false` in `values-local-validation.yaml`, and render it as an environment
  variable in the query-api Deployment. Keep `values-prod.yaml` on its
  placeholder secret and legacy MySQL backend.

- [ ] **Step 5: Run focused tests and Helm contract checks.**

  Run `go test ./internal/api ./cmd/api` and
  `bash deploy/scripts/test-deployment-contracts.sh`. Fix implementation or
  expectations until both exit successfully.

### Task 2: Correct the graph load gate and resource contract

**Files:**
- Modify: `deploy/scripts/graph-load-test.sh`
- Create: `deploy/scripts/graph-resource-snapshot.sh`
- Modify: `deploy/scripts/test-graph-load-contract.sh`
- Create: `deploy/scripts/test-graph-resource-snapshot-contract.sh`

**Interfaces:**
- `graph-load-test.sh` emits exactly eight operation rows and consumes the
  resource snapshot JSON when resources are required.
- `graph-resource-snapshot.sh [--namespace NS] [--output PATH] [--frontend-dist PATH] [--browser-url URL]` emits the required resource object with per-item `status` and values.
- `resource_gate_status` is `PASS` only when every required item is collected.

- [ ] **Step 1: Extend the contract tests with the alias row and strict gates.**

  Assert the alias endpoint, all eight operation names, every exact threshold,
  strict comparison semantics, snapshot invocation, and fail-closed resource
  status. Add a fixture-mode snapshot test that rejects a report containing
  `not_collected` when `GRAPH_LOAD_REQUIRE_RESOURCES=1`.

- [ ] **Step 2: Run the shell contracts and verify the new assertions fail.**

  Run:

  ```bash
  bash deploy/scripts/test-graph-load-contract.sh
  bash deploy/scripts/test-graph-resource-snapshot-contract.sh
  ```

  Expected: FAIL on the absent alias row, old thresholds, or absent snapshot
  script.

- [ ] **Step 3: Implement the resource snapshot collector.**

  Use `kubectl top pod` or metrics API for CPU/RSS, pod exec/cgroup or metrics
  for HugeGraph RSS, `jcmd`/JVM endpoint where available for heap, pod `du`
  for `/var/lib/hugegraph/data` and `/var/lib/hugegraph/wal`, local `du` for
  frontend `dist`, and a Node/Playwright `PerformanceObserver` probe for Long
  Tasks. Record explicit `not_collected` reasons instead of silently using
  zero. Support `--fixture PATH` only for deterministic contract testing.

- [ ] **Step 4: Wire alias search and the eight strict gates.**

  Add `measure alias_search GET "${base_url}/entities/search?q=...&limit=20"`,
  update all operation thresholds, require `p95_ms < gate_ms`, append the
  batch mutation row, and make missing/duplicate operation rows fail.

- [ ] **Step 5: Wire and enforce the resource gate.**

  Invoke the snapshot collector (or consume `GRAPH_RESOURCE_REPORT`), merge
  its result into the load report, and set `gate_status=FAIL` when resources
  are required but incomplete. Preserve `BLOCKED_BY_ENV` only for an entirely
  unavailable runtime, not for a failed measured operation.

- [ ] **Step 6: Run focused shell tests and a dry-run.**

  Run both contract tests and:

  ```bash
  bash deploy/scripts/graph-load-test.sh --dry-run --output /tmp/aiops-graph-load-dry-run.json
  ```

  Verify the dry-run does not claim a runtime PASS and the contract tests exit
  zero.

### Task 3: Turn Shadow and Recovery into complete gates

**Files:**
- Modify: `deploy/scripts/shadow-gate.sh`
- Modify: `deploy/scripts/graph-recovery-test.sh`
- Create: `deploy/scripts/test-shadow-gate-contract.sh`
- Create: `deploy/scripts/test-graph-recovery-contract.sh`
- Modify: `docs/runbooks/graph-cutover.md`

**Interfaces:**
- `shadow-gate.sh` accepts `--report` and `--output`, evaluates the complete
  required metric contract, and returns nonzero for any missing or failed row.
- `graph-recovery-test.sh --inject` is mutation-enabled only with explicit
  local/staging guard variables; default and `--offline` modes are read-only.

- [ ] **Step 1: Write JSON fixture tests for every Shadow threshold.**

  Create passing and failing reports covering identity, structural, scope,
  dead outbox, outbox P99, each source lag, Graph 5xx, all eight P95 rows,
  trace mismatch, and fixed RCA status. Verify absent fields fail.

- [ ] **Step 2: Run the Shadow contract test and verify it fails.**

  Run `bash deploy/scripts/test-shadow-gate-contract.sh`; expected failure is
  the current script accepting reports that omit the new metrics.

- [ ] **Step 3: Implement the complete Shadow evaluator.**

  Normalize the report into canonical values, apply exact `<`/`<=` operators,
  require all eight graph P95 rows, require `fixed_rca_scenario=PASS`, write a
  machine-readable result with failures, and return 1 on any failure.

- [ ] **Step 4: Write recovery safety tests before implementation.**

  Assert `--inject` rejects unset/production environment, production-like
  namespace/context, and missing required `kubectl`/report settings; assert
  read-only and offline modes never invoke mutation commands.

- [ ] **Step 5: Implement guarded recovery injection and evidence output.**

  Add explicit environment/context checks, record pre-recovery counts and
  historical RCA identifiers without printing secrets, execute the documented
  local/staging recovery sequence, wait for `graph_ready=false` then true,
  and verify schema/identity/edge/outbox/source/RCA invariants. Any failed or
  unavailable check produces a non-passing report.

- [ ] **Step 6: Run focused shell tests and update the cutover runbook.**

  Run both contract tests and `bash -n` on the modified scripts. Document the
  exact `--inject` safety variables and keep the production cutover sequence
  unchanged.

### Task 4: Add separate real Observability-to-RCA evidence validation

**Files:**
- Create: `deploy/scripts/validate-observability-evidence.sh`
- Create: `deploy/scripts/test-observability-evidence-contract.sh`
- Modify: `deploy/scripts/validate-local-stack.sh`
- Modify: `docs/runbooks/graph-local-validation.md`

**Interfaces:**
- `validate-observability-evidence.sh --marker MARKER --output PATH` emits a
  report with individual evidence checks and `gate_status`.
- The local stack validator delegates evidence validation instead of treating
  `AIOPS_VALIDATION_DATA_MARKER` as proof by itself.

- [ ] **Step 1: Write fixtures for real evidence and RCA invariants.**

  Cover marker presence in metrics/logs/events/flows, a real dependency edge,
  an RCA run with frozen start/end/symptom times, at least two evidence
  categories, final graph context, bounded propagation path, deterministic
  score equality, and `CAUSED_BY` only for confirmed runs. Add fixtures for
  missing and contradictory responses.

- [ ] **Step 2: Run the evidence contract test and verify it fails.**

  Run `bash deploy/scripts/test-observability-evidence-contract.sh`; expected
  failure is the missing validator and current marker-only behavior.

- [ ] **Step 3: Implement the validator with real command adapters.**

  Use `curl`/`kubectl` against configured VictoriaMetrics, VictoriaLogs,
  Kubernetes, DeepFlow, and query-api endpoints. Keep each check explicit,
  bound all queries to the marker/time window, and preserve raw response
  metadata without secrets. Implement an optional `--fixture` mode solely for
  deterministic contract tests.

- [ ] **Step 4: Replace marker-only validation in the local stack validator.**

  When no marker is supplied, report `BLOCKED_BY_ENV`; when supplied, invoke
  the new validator and propagate its nonzero result. Do not print credentials,
  tokens, or password values.

- [ ] **Step 5: Run focused shell tests and offline validation.**

  Run the evidence contract test, `bash -n`, and
  `bash deploy/scripts/validate-local-stack.sh --offline`.

### Task 5: Execute available local verification and record evidence

**Files:**
- Modify: `docs/runbooks/graph-local-validation-report-2026-08-28-current.md`
- Modify: `部署验证.md` only for non-secret local password/configuration notes

**Interfaces:** Produces fresh reports without claiming unavailable external
  stages passed.

- [ ] **Step 1: Run the complete focused verification matrix.**

  Run all new shell contracts, `go test ./...` in `ai-apm-query-go`,
  `helm lint deploy/helm/aiops`, and the production Helm template render with
  placeholder values. Fix regressions before environment work.

- [ ] **Step 2: Inspect the live local environment without printing secrets.**

  Check Kubernetes context, namespaces, workload readiness, migration state,
  and current admin row status. Never print password hashes, Secret values,
  tokens, or credential-bearing environment output.

- [ ] **Step 3: Apply the requested local authentication state.**

  If the explicit local cluster is available, set the local admin password to
  the bcrypt hash of `admin1234` using a one-time local-only operation and
  deploy the bypass switch. Verify only HTTP statuses and error codes.

- [ ] **Step 4: Run available graph/evidence/recovery gates.**

  Execute only against the verified local/staging environment. Generate the
  requested report paths; leave unavailable external stages as
  `BLOCKED_BY_ENV`. Do not switch production values or run destructive
  recovery against a production-like context.

- [ ] **Step 5: Perform final verification before reporting completion.**

  Run `git diff --check`, all focused tests again, full relevant suites, Helm
  lint/template, and inspect `git status --short`. Report exact evidence and
  any remaining environment blocks; do not claim 24-hour Shadow, cutover, or
  two-hour stability without elapsed observation data.
