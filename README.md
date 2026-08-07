# AIOps 平台（自研 ongrid 风格）

> 自研云原生 AIOps 平台：采集 → 存储 → AI 编排 → 可视化，四维对标 ongrid（界面/功能/框架/架构），**全部自研**（ongrid 为 AGPL-3.0，仅对标不复制）。

## 架构

```
浏览器 ──NodePort:30253──▶ frontend(nginx:80)
  /api/v1/ai/, /api/v1/ops/ → ai-orchestrator:8080
  /grafana/                 → deepflow-grafana
  /metrics 默认             → query-api:8080
query-api(Go:8080) ── ClickHouse / VictoriaMetrics / VictoriaLogs / Redis / MinIO / MySQL
ai-orchestrator:8080 ── ChromaDB / Redis / VictoriaMetrics / VictoriaLogs / MinIO / LLM(mock)
ingest ── ClickHouse / DeepFlowSyncer(deepflow-clickhouse, 实时增量) / X-Api-Key
```

- **自研服务（4）**：frontend / query-api / ingest / ai-orchestrator
- **中间件（8）**：ClickHouse / VictoriaMetrics / VictoriaLogs / Redis / ChromaDB / MinIO / MySQL / vmalert
- **deepflow（完整）**：agent / server / clickhouse / mysql / grafana（独立 namespace）
- 命名空间：`observability`（自研+中间件）、`deepflow`

## 本机部署（macOS arm64 + OrbStack K8s）

### 前置
- OrbStack K8s 已启用，`kubectl config current-context` = `orbstack`
- helm v3、Docker

### 步骤

```bash
cd aiops

# 1. 构建 4 个自研服务镜像（arm64）
./deploy/scripts/build-images.sh

# 2. 部署 observability（自研 + 中间件 + 自动建表）
./deploy/scripts/apply.sh

# 3. 部署 deepflow（完整 eBPF 采集）
helm upgrade --install deepflow deepflow/deepflow --version 7.1.002 \
  --namespace deepflow --create-namespace

# 4. 验证
curl -s -o /dev/null -w "%{http_code}" http://localhost:30253/   # 期望 200
kubectl -n observability get pods                                  # 全部 Running
kubectl -n observability exec clickhouse-0 -- clickhouse-client \
  --query "SELECT count() FROM system.tables WHERE database='observability'"  # 期望 8
```

### 清理

```bash
# 卸载（保留 PVC 数据）
./deploy/scripts/destroy.sh
# 彻底清除（含 PVC/namespace）
./deploy/scripts/destroy.sh --purge-data
```

## 关键配置（values.yaml / apply.sh）

| 项 | 默认 | 说明 |
|---|---|---|
| 副本数 | 1 | 唯一偏离生产标准 |
| 镜像架构 | arm64 | OrbStack 原生 |
| 存储类 | 集群默认（`local-path`） | PVC `storageClass: ""`，可移植改 SC |
| 前端 NodePort | 30253 | |
| LLM | mock（`LLM_MOCK=true`） | 配真实 Key：改 `aiOrchestrator.llm.*` + `LLM_MOCK=false` 后 `helm upgrade` |
| 实时同步 | `DEEPFLOW_SYNC_INTERVAL=10s` | ingest 增量拉取 deepflow-clickhouse |

### 密钥（生产覆盖）
`apply.sh` 用 `--set secrets.*` 注入本机开发默认值。**生产环境务必**：
- 用 `values-prod.yaml` 覆盖真实密钥（JWT_SECRET / INTERNAL_TOKEN / INGEST_API_KEY / MINIO / MySQL 密码）
- 密钥不写入代码仓库（`.gitignore` 已排除 `.env`/`secrets/`）

## 可移植到其他环境

- 本机部署与 63（192.168.0.63 amd64 K8s）解耦，仅一次性参考其 ClickHouse 表结构固化为 `deploy/helm/aiops/files/clickhouse/init_clickhouse.sql`。
- 迁移到任何集群：改 `values.yaml` 的 `storageClass`（如 `nfs-client`）、镜像架构（amd64）、`secrets.*`（真实密钥）。
- 若环境已有某中间件：对应组件设 `enabled: false` + `external.host`，Chart 复用外部实例，不重复部署。
- ClickHouse/MySQL 由 Chart 内 InitContainer 自动建库建表（幂等 `IF NOT EXISTS`），`helm install` 即完成初始化，无历史数据。

## 项目结构

```
aiops/
├── ai-apm-ingest-go/       # 采集/同步（Go）
├── ai-apm-query-go/        # 查询/告警/设置（Go）
├── ai-orchestrator/        # AI 编排（Python/FastAPI）
├── observability-frontend/ # 前端（React/Vite）
├── deploy/
│   ├── helm/aiops/         # Helm Chart（自研+中间件）
│   └── scripts/            # build-images / apply / destroy / init-db
├── docs/superpowers/       # spec + plan（superpowers 流程文档）
└── ongrid-ref/             # ongrid 只读对标副本（AGPL，.gitignore 排除，不入库）
```
