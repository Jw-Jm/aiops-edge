# Phase 0 Dependency and Build Map

## Dependency manifests

| Component | Manifest / build files | Observed direct stack |
|---|---|---|
| Query API | `go.mod`, `go.sum`, `vendor/`, `Dockerfile` | Go 1.25; JWT, MySQL driver, sqlmock, crypto; vendored dependencies |
| Ingest | `go.mod`, `Dockerfile` | Go 1.23; OTLP/DeepFlow/ClickHouse/VM pipeline |
| Event collector | `go.mod`, `Dockerfile` | Go 1.21; Kubernetes event and IPMI/ClickHouse pipeline |
| Orchestrator | `requirements.txt`, `Dockerfile`, `.dockerignore`, `bin/*.tar.gz` | FastAPI, LangGraph, CrewAI, ChromaDB, sentence-transformers, HTTPX, Pydantic, MySQL, APScheduler |
| Frontend | `package.json`, `package-lock.json`, `Dockerfile`, `Dockerfile.offline` | React/Vite/TypeScript, Ant Design, Zustand, ECharts, XYFlow, G6, xterm, markdown, workflow/layout libraries |
| IPMI exporter | `Dockerfile`, `build.sh` | Python exporter and host/IPMI runtime boundary |
| Deployment | `Chart.yaml`, `values*.yaml`, templates, shell scripts | Helm, Docker build/deploy, MySQL/ClickHouse init jobs, NetworkPolicy |

## Baseline dependency facts

- `ai-orchestrator/.venv-312` is absent; the required Python test environment is not available.
- `observability-frontend/node_modules` is absent; `npm run build` stops at `tsc: command not found`.
- System `python3 -m pip check` fails on pre-existing LangChain/LangGraph version conflicts, an invalid checkpoint distribution, and an unsupported grpcio platform. This did not modify the environment.
- Go tests pass when run with network-enabled test execution. The sandbox-only run failed before assertions because `httptest` could not bind a local port.
- `helm lint deploy/helm/aiops` passes; deployment requires real Secret values and must not use placeholders.

## Image and context audit targets

- Orchestrator image is the dominant baseline at 2,609,765,224 bytes and must be audited for offline site-packages, model caches, and tarballs.
- Frontend has no `.dockerignore` in the baseline inventory and uses both standard and offline Dockerfiles.
- Query-api, ingest, and event-collector are small Go runtime images but still require non-root/read-only-rootfs verification in later phases.
- `deploy/scripts/build-images.sh` and `apply.sh` control clean build/tag/deploy behavior and must be preserved until the final image workflow is verified.

## Required later probes

Phase 14 must repeat clean builds from the Phase 0 SHA, generate SBOM/vulnerability evidence, and compare the five runtime images with the exact same size metric. Missing Python/frontend/K8sGPT/Chroma/MinIO capabilities are blockers for their corresponding test or acceptance gates, not reasons to delete dependencies or substitute mocks.
