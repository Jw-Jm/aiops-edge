# AIOps Agentic Phase 0 Baseline

**Baseline date:** 2026-08-19 (Asia/Shanghai)
**Repository:** `/Users/mssc/Documents/Code/agent/aiops`
**Source baseline SHA:** `6823ea29b6294e7f00ebb1f088b6e772ad80028a`
**Source branch:** `main`
**Implementation branch:** `codex/aiops-phase0`
**Current local Kubernetes context:** `orbstack`

## Scope and safety

Phase 0 only records the repository, environment, runtime, API, data, page, dependency, and image baseline. It does not modify production source, deployment behavior, dependencies, or runtime data. No Secret, kubeconfig, token, private key, password, database dump, or LLM API key was printed.

The only implementation-branch changes before this report are:

- `aiops-agentic.md`: confirmed architecture decisions and Phase 0 discovery constraints;
- `docs/superpowers/plans/2026-08-19-aiops-phase0-baseline.md`: Phase 0 execution plan;
- this report and the five `docs/luna/phase0-*.md` maps.

## Git baseline

```text
main HEAD: 6823ea2 1
previous reference: 2ce8df5 fix: preserve unavailable ai tool evidence
working source checkout before Phase 0: clean, tracking origin/main
implementation checkout: linked worktree at .worktrees/aiops-phase0
```

Recent source commits before this work:

```text
6823ea2 1
2ce8df5 fix: preserve unavailable ai tool evidence
35c1c0f docs: record real llm audit evidence
1593e31 fix: show explicit not-found page and publish audit
1eacd52 chore: order default deployment stages
```

## Repository inventory

| Area | Observed entrypoint / key paths | Approx. tracked file count | Baseline result |
|---|---|---:|---|
| Frontend | `observability-frontend/src/main.tsx`, `src/App.tsx` | 62 | source present; build unavailable because `node_modules` is absent |
| Query API | `ai-apm-query-go/cmd/api/main.go` | 166 including vendor | Go tests pass with network-enabled test run |
| Ingest | `ai-apm-ingest-go/cmd/ingest/main.go` | 21 | Go tests pass with network-enabled test run |
| Event collector | `ai-event-collector/main.go` | 9 | no test files; package test passes |
| AI orchestrator | `ai-orchestrator/main.py`, `orchestrator.py` | 176 | `.venv-312` absent; system Python syntax compile passes with redirected pycache |
| IPMI exporter | `ipmi-exporter/collect.py`, `build.sh` | 4 | included in scope; no production change in Phase 0 |
| Deployment | `deploy/helm/aiops/`, `deploy/scripts/` | 45 | `helm lint deploy/helm/aiops` passes |

Existing user files, backups, bundles, binaries, and untracked files were not deleted or moved.

## Local toolchain and runtime probes

| Capability | Result | Evidence |
|---|---|---|
| Git | AVAILABLE | 2.50.1 |
| Python | AVAILABLE | system `python3` is 3.9.6; required `.venv-312` is absent |
| Go | AVAILABLE | 1.26.4 darwin/arm64 |
| Node/npm | AVAILABLE | Node v25.1.0, npm 11.6.2; frontend `node_modules` absent |
| Helm | AVAILABLE | v3.19.0 |
| Docker | AVAILABLE | daemon server 29.4.0 |
| kubectl | AVAILABLE | client v1.36.3; current context `orbstack` |
| Kubernetes API | AVAILABLE | control plane and CoreDNS reachable at the local OrbStack endpoint |
| second test cluster | NOT CREATED | current Phase 0 environment has one cluster; kind cluster remains a later prerequisite |
| K8sGPT | UNKNOWN | no binary, container, or repository asset was found; no runtime claim made |
| ChromaDB | UNKNOWN | no repository asset or Kubernetes workload/service was found; no runtime claim made |
| MinIO | UNKNOWN | no repository asset or Kubernetes workload/service was found; no runtime claim made |
| active LLM provider/model | UNKNOWN | source has configuration paths, but no secret/config value was printed or inferred |

The current `observability` namespace contains Ready workloads for `ai-orchestrator`, `query-api`, `ingest`, `event-collector`, `frontend`, `mysql`, `clickhouse`, `victoria-metrics`, and `victoria-logs`. The cluster also contains DeepFlow workloads and metrics-server. This is a runtime observation, not an authorization decision.

## Baseline test matrix

| Command / probe | Result | Failure or limitation |
|---|---|---|
| `ai-orchestrator/.venv-312/bin/python -m pytest tests -q` | NOT RUN | required venv is absent |
| `ai-orchestrator/.venv-312/bin/python -m compileall -q .` | NOT RUN | required venv is absent |
| `ai-orchestrator/.venv-312/bin/python -m pip check` | NOT RUN | required venv is absent |
| `PYTHONPYCACHEPREFIX=/tmp/aiops-pyc python3 -m compileall -q ai-orchestrator` | PASS | fallback syntax check used system Python 3.9.6; not equivalent to the required venv |
| `python3 -m pip check` | FAIL | pre-existing system environment conflicts: LangChain/LangGraph version mismatches, invalid checkpoint distribution, unsupported grpcio platform |
| `cd ai-apm-query-go && go test ./...` | PASS | rerun with network-enabled test execution; sandbox-only run was blocked at `httptest` socket binding |
| `cd ai-apm-ingest-go && go test ./...` | PASS | same sandbox socket limitation on first run; network-enabled rerun passed |
| `cd ai-event-collector && go test ./...` | PASS | package has no test files |
| `cd observability-frontend && npm run build` | NOT RUN | `tsc` unavailable because `node_modules` is absent |
| `helm lint deploy/helm/aiops` | PASS | lint reports informational warnings that real Secret values are required for deployment |
| `git diff --check` | PASS | no whitespace errors in the implementation-branch diff |

## Runtime image baseline

Measured from currently running local containers with `docker image inspect .Size` (local uncompressed size):

| Runtime image | Image ID prefix | Size bytes |
|---|---|---:|
| `ai-apm-query-go` | `6343b5f8bd69` | 26,682,862 |
| `ai-apm-ingest-go` | `542cdb4af47d` | 9,247,032 |
| `ai-orchestrator` | `9ceadfc9ca90` | 2,609,765,224 |
| `observability-frontend` | `604f8287f543` | 22,816,395 |
| `ai-event-collector` | `252b3fe0278b` | 12,015,220 |
| **Total** | — | **2,680,526,733** |

This is the provisional Phase 0 runtime baseline. The final image report must identify the exact Dockerfile build SHA and use the same size metric.

## Gate 0 status

**Baseline evidence complete with downstream environment blockers recorded.** The repository maps, test results, runtime observations, and failure reasons are captured. Phase 14/18 cannot claim completion until the required Python environment, frontend dependencies, K8sGPT/knowledge dependencies, second kind cluster, real LLM configuration, and browser E2E capability are independently verified.
