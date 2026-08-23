# AIOps Agentic 全面重构 V9.2 Phase 1 基线

**Phase:** V9.2 Phase 1 (Freeze Baseline & Build Real Map)
**Baseline date:** 2026-08-19 (Asia/Shanghai)
**Repository:** `/Users/mssc/Documents/Code/agent/aiops`
**Head SHA:** `a8fdb5d973378ad7ed4653df0414b944fc413c94`
**Branch:** `main` (`main...origin/main` ahead 20)
**Kubernetes context:** `orbstack`

## Scope and safety

Phase 1 is read-only. It records the repository, environment, runtime, API, data, page, dependency, and image baseline. It does not modify production source, deployment behavior, dependencies, or runtime data. No Secret, kubeconfig, token, private key, password, database dump, or LLM API key was printed. Only variable names and existence were recorded.

Baseline artifacts produced:
- `docs/AIOPS_REFACTOR_BASELINE.md` (this file)
- `docs/AIOPS_CODE_MAP.md`
- `docs/AIOPS_API_MAP.md`
- `docs/AIOPS_DATA_MAP.md`
- `docs/AIOPS_FRONTEND_MAP.md`
- `docs/AIOPS_DEPENDENCY_MAP.md`

Prior-artifact note: the old-flow `BEFORE_BASELINE.md`, `docs/luna/phase0-*.md`, and `docs/luna/phase1-*.md` are preserved on disk (not deleted) but are **not** the V9.2 baseline authority. V9.2 is the only execution contract.

## Git baseline

```text
HEAD:     a8fdb5d973378ad7ed4653df0414b944fc413c94 (merge: integrate aiops phase 0-2 security refactor)
branch:   main, ahead 20 of origin/main
untracked: aiops-1.md, aiops-agentic-v9.2-final.md (assistant working files; not to be committed)
```

Recent commits (old-flow security refactor already merged):
```text
a8fdb5d merge: integrate aiops phase 0-2 security refactor
591fe41 docs(gate): record phase 2 task 4 completion
adf38c0 fix(security): propagate context through all query callers
2ed6b69 fix(security): close remaining signed-context caller gaps
9a6dd24 feat(security): migrate orchestrator internal callers to signed context
8b2bf15 fix(security): persist login sessions and close proxy forwarding
7350174 feat(security): enforce mysql authorization and resource resolution
127b415 fix(authz): close mysql authority gaps
61448eb feat(authz): add mysql authority and canonical cluster registry
4a2a2e8 fix(security): retain replay nonce through clock skew
```

**Git rule:** per V9.2 contract, the entire implementation forbids `git add`, `git commit`, `git push`. Only working-tree changes, diff, and reports are produced.

## Repository inventory

| Area | Entrypoint / key paths | Observed status |
|---|---|---|
| Frontend | `observability-frontend/src/main.tsx`, `src/App.tsx` | source present; `node_modules` present; production build PASS |
| Query API | `ai-apm-query-go/cmd/api/main.go` | Go tests PASS (incl. `internal/contract`) |
| Ingest | `ai-apm-ingest-go/cmd/ingest/main.go` | Go tests PASS |
| Event collector | `ai-event-collector/main.go` | no test files; package test PASS |
| AI orchestrator | `ai-orchestrator/main.py`, `orchestrator.py` | `.venv-312` present; pytest 375 passed / 4 failed |
| IPMI exporter | `ipmi-exporter/collect.py`, `build.sh` | in scope; no production change in Phase 1 |
| Deployment | `deploy/helm/aiops/`, `deploy/scripts/` | `helm lint` PASS (0 failed) |

Existing user files, backups, bundles, binaries, and untracked files were not deleted or moved.

## Local toolchain and runtime probes

| Capability | Result | Evidence |
|---|---|---|
| Git | AVAILABLE | 2.50.1 |
| Python | AVAILABLE | system python3 is 3.9.6; project `.venv-312` is present (3.12) |
| Go | AVAILABLE | 1.26.4 darwin/arm64 |
| Node/npm | AVAILABLE | Node v25.1.0, npm 11.6.2; `node_modules` present |
| Helm | AVAILABLE | v3.19.0 |
| Docker | AVAILABLE | 29.4.0; daemon reachable via OrbStack |
| kubectl | AVAILABLE | client v1.36.3; context `orbstack` |
| Kubernetes API | AVAILABLE | control plane + observability workloads reachable |
| kind | AVAILABLE | v0.32.0; **no kind clusters yet** (`aiops-kind-02` not created) |
| K8sGPT | AVAILABLE (container) | `ai-orchestrator/bin/k8sgpt` = 116MB ARM aarch64 static ELF; installed at `/usr/local/bin/k8sgpt` inside orchestrator container |
| Chroma | AVAILABLE (container) | `/usr/local/bin/chroma` + `/app/.venv-312/bin/chroma` + `/root/.cache/chroma` inside orchestrator container |
| MinIO | UNAVAILABLE | no workload/service found in any namespace; not present in container |
| VictoriaMetrics | AVAILABLE | `victoria-metrics` service + Running pod |
| VictoriaLogs | AVAILABLE | `victoria-logs` service + Running pod |
| ClickHouse | AVAILABLE | `clickhouse` service + Running pod |
| MySQL | AVAILABLE | `mysql` service + Running pod |
| DeepFlow | AVAILABLE | deepflow-server/app/agent running (deepflow namespace) |
| LLM Provider | UNKNOWN | config path exists (`LLM_ENCRYPTION_KEY`, `LLM_MOCK=OFF`, encrypted settings), but real provider/model readiness not verified in Phase 1 |
| Playwright | UNAVAILABLE | `@playwright/test` not resolvable in `observability-frontend/node_modules` |

## Baseline test matrix

| Command / probe | Result | Failure or limitation |
|---|---|---|
| `ai-orchestrator/.venv-312/bin/python -m pip check` | PASS | No broken requirements found |
| `ai-orchestrator/.venv-312/bin/python -m pytest tests -q` | **4 failed / 375 passed** | see below |
| `ai-orchestrator/.venv-312/bin/python -m pytest tests/test_contracts.py -q` | PASS | 7 passed |
| `ai-orchestrator/.venv-312/bin/python -m compileall -q contracts.py tests/test_contracts.py` | PASS | |
| `cd ai-apm-query-go && go test ./...` | PASS | incl. `internal/contract` |
| `cd ai-apm-ingest-go && go test ./...` | PASS | |
| `cd ai-event-collector && go test ./...` | PASS | no test files |
| `cd observability-frontend && npm run build` | PASS | built in 4.30s |
| `helm lint deploy/helm/aiops` | PASS | 0 failed; INFO: real Secret values required |

### orchestrator 4 failing tests (baseline)

These are pre-existing failures in the current source at HEAD `a8fdb5d`. They split into two classes and are **not** to be blindly "fixed" (see decision below):

- **Class B (V9.2 core semantics, likely regressions to fix):**
  1. `tests/test_internal_context_callers.py::test_orchestrator_alert_collection_requires_explicit_context` — requires `_collect_alerts` to reject when no explicit `request_context`.
  2. `tests/test_orchestrator_routing.py::test_stream_marks_empty_k8sgpt_and_rag_results_unavailable` — requires `stream_sync` to emit `status=unavailable` `tool_end` for empty k8sgpt/rag results.

- **Class A (old AI Chat / free-text state / suggestion main path, to be removed in V9.2 Phase 14, not "fixed" now):**
  3. `tests/test_loop_iterations.py::test_stream_sync_initial_injects_exec_context` — validates `AgentState.exec_context/iteration` free-text fields + old multi-turn AI Chat.
  4. `tests/test_checkpointer.py::test_streamed_suggestion_execution_and_reload_preserve_aichat_card` — validates old `/api/v1/ai/chat` + `/suggestion/execute` + session/execresult card; fails because the old test was not adapted to the newly required `X-Trusted-Request-Context` signed context.

**Phase 1 disposition:** record only; do not modify. Handling of these two classes is a decision point to confirm with the user before proceeding.

## Runtime image baseline

Measured with `docker image inspect .Size` (local uncompressed size) for the current latest build tag `v1.1.3-dirty.20260819005955`:

| Runtime image | Size bytes |
|---|---:|
| `ai-orchestrator` | 2,609,765,224 |
| `ai-apm-query-go` (`query-api`) | 26,682,862 |
| `ai-apm-ingest-go` (`ingest-pipeline`) | 9,247,032 |
| `observability-frontend` | 22,816,395 |
| `ai-event-collector` | 12,015,220 |
| **Total** | **2,680,526,733** |

**BASELINE_IMAGE_SIZE = 2,680,526,733 bytes.** Final target (Phase 15): `FINAL_IMAGE_SIZE <= BASELINE_IMAGE_SIZE × 0.80`. The orchestrator image (2.6 GB) is the dominant optimization target (offline site-packages + model caches + tarballs).

## Environment blockers / not-yet-ready (recorded for later gates)

- **Playwright**: UNAVAILABLE → blocks Browser E2E (Phase 19) until installed (must respect domestic-source / offline constraints).
- **MinIO**: UNAVAILABLE → blocks large-evidence-object and knowledge-object storage unless introduced in a later Phase.
- **LLM Provider**: UNKNOWN → real-LLM acceptance (Phase 19) requires confirmed provider/model config.
- **Second cluster `aiops-kind-02`**: not created → needed in Phase 3.
- **`ai-orchestrator/bin`**: offline assets present (chroma.tar.gz 166MB, hf.tar.gz 56MB, k8sgpt 116MB, kubectl 55MB, pybin.tar.gz, sp.tar.gz 422MB, total ~779MB) — reused for offline container build; not to be deleted.

## Gate 1 status

**Baseline evidence complete with downstream environment blockers recorded.** Repository maps, test results, runtime observations, and failure reasons are captured. Phase 18/19 cannot claim completion until Playwright, MinIO, real LLM, and second cluster are independently verified.
