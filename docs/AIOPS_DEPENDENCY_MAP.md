# AIOps V9.2 Phase 1 — Dependency Map

Baseline HEAD: `a8fdb5d`. Read-only inventory of dependency manifests, offline assets, and image baseline. No dependency modified in Phase 1.

## Dependency manifests by component

| Component | Manifest / build | Observed direct stack |
|---|---|---|
| Query API | `go.mod`, `go.sum`, `vendor/`, `Dockerfile` | Go 1.26; JWT, MySQL driver, sqlmock, crypto; vendored |
| Ingest | `go.mod`, `Dockerfile` | Go; OTLP/DeepFlow/ClickHouse/VM pipeline |
| Event collector | `go.mod`, `Dockerfile` | Go; Kubernetes event + IPMI/ClickHouse pipeline |
| Orchestrator | `requirements.txt`, `Dockerfile`, `.dockerignore`, `bin/*.tar.gz` | FastAPI, LangGraph, CrewAI, ChromaDB, sentence-transformers, HTTPX, Pydantic, MySQL, APScheduler |
| Frontend | `package.json`, `package-lock.json`, `Dockerfile`, `Dockerfile.offline` | React/Vite/TS, Ant Design, Zustand, ECharts, XYFlow, G6, xterm, markdown, workflow/layout libs |
| IPMI exporter | `Dockerfile`, `build.sh` | Python exporter, host/IPMI runtime boundary |
| Deployment | `Chart.yaml`, `values*.yaml`, templates, scripts | Helm, Docker, MySQL/ClickHouse init jobs, NetworkPolicy |

## Offline assets in `ai-orchestrator/bin/` (present, ~779MB)

```text
chroma.tar.gz   166,302,672   ChromaDB offline package
hf.tar.gz       55,971,953    HuggingFace model cache
k8sgpt         116,195,512    ARM aarch64 static ELF (K8sGPT binary)
kubectl         55,640,226    ARM binary
pybin.tar.gz           467    helper
sp.tar.gz      422,511,660    site-packages offline bundle
```

These are used for offline container build and must be reused, not deleted. K8sGPT and Chroma are confirmed available inside the orchestrator container.

## Environment probes (result set only AVAILABLE/UNAVAILABLE/UNKNOWN)

| Capability | Result |
|---|---|
| Python (venv-312) | AVAILABLE |
| Go | AVAILABLE |
| Node/npm | AVAILABLE |
| Helm | AVAILABLE |
| Docker | AVAILABLE |
| kubectl / Kubernetes API | AVAILABLE |
| kind (aiops-kind-02) | AVAILABLE tool / cluster NOT CREATED |
| K8sGPT | AVAILABLE (container) |
| Chroma | AVAILABLE (container) |
| MinIO | UNAVAILABLE |
| VictoriaMetrics / VictoriaLogs / ClickHouse / MySQL | AVAILABLE |
| DeepFlow | AVAILABLE |
| LLM Provider | UNKNOWN (config path exists, real provider unverified) |
| Playwright | UNAVAILABLE |

## Image baseline (`docker image inspect .Size`, latest build tag v1.1.3-dirty.20260819005955)

| Image | Size bytes |
|---|---:|
| ai-orchestrator | 2,609,765,224 |
| query-api | 26,682,862 |
| ingest-pipeline | 9,247,032 |
| observability-frontend | 22,816,395 |
| event-collector | 12,015,220 |
| **Total (BASELINE_IMAGE_SIZE)** | **2,680,526,733** |

Final target (Phase 15): `FINAL_IMAGE_SIZE <= BASELINE_IMAGE_SIZE × 0.80`. Orchestrator image (2.6GB) is the dominant optimization target.

## Dependency discipline (V9.2 §89)

New dependency only if: stdlib/current dep cannot reasonably implement, version pinned, actively maintained, acceptable license, no known critical vuln, offline/cacheable, acceptable prod image impact. Forbidden to add large frameworks for simple features. All pip/docker/apt operations use domestic mirrors or offline assets per prior standing constraint.
