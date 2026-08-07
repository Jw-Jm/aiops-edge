# AIOps 平台本机 K8s 部署设计文档（spec）

> 日期：2026-08-07 ｜ 状态：已逐节评审确认
> 定位：在**本机 macOS(arm64) + OrbStack K8s** 上，用 Helm Chart 方式部署自研 AIOps 平台，除副本数=1 外严格按生产标准；与环境（63）解耦、可移植到其他正式生产环境。

---

## 1. 目标与约束

- **目标**：本机 K8s 资源方式部署 4 个自研服务 + 全部中间件 + deepflow，作为可移植的生产级基线。
- **关键约束（已确认）**：
  1. 方案 B：Helm Chart 部署（helm v3.19 已装）。
  2. 副本数 = 1，其余严格按生产标准。
  3. 中间件一次性全拉起（ClickHouse/VictoriaMetrics/VictoriaLogs/Redis/ChromaDB/MinIO/MySQL/vmalert + deepflow 完整装）。
  4. 命名空间照建 `observability` + `deepflow`。
  5. 镜像 arm64 原生。
  6. LLM 用 mock/空跑，后续用户自配 Key。
  7. 与 63 解耦、清历史数据、可移植。
  8. 代码先改本机 `aiops/`，再同步到环境。
  9. 实时同步走"路径 1"：DeepFlowSyncer 改可配置间隔 + 增量拉取。
  10. 代码鲁棒性优先（TDD 保障）。

## 2. 环境实况

| 项 | 值 |
|---|---|
| OS | macOS Darwin arm64 |
| K8s | OrbStack 单节点，v1.35.6+orb1，control-plane，运行时 docker |
| kubectl context | `orbstack` |
| helm | v3.19.0 |
| 存储类 | `local-path`（rancher.io/local-path，default，WaitForFirstConsumer） |
| docker | 29.4.0，compose v5.1.2 |
| go / node / python | 1.26.4 / v25.1.0 / 3.9.6（本机构建用；orchestrator 镜像内 python3.12） |

## 3. 架构与组件

### 3.1 Chart 结构（新增 `aiops/deploy/`）
```
deploy/
├── helm/aiops/
│   ├── Chart.yaml
│   ├── values.yaml          # 全局 + 各组件（副本数=1、镜像、存储类、secrets 占位）
│   ├── values-prod.yaml     # 生产标准 overrides（资源限额/探针严格值）
│   ├── requirements.yaml    # deepflow 子 chart 依赖（condition: deepflow.enabled）
│   ├── templates/
│   │   ├── namespaces.yaml  # observability / deepflow
│   │   ├── _helpers.tpl
│   │   ├── clickhouse/  ├── victoria-metrics/  ├── victoria-logs/
│   │   ├── redis/       ├── chromadb/          ├── minio/
│   │   ├── mysql/       ├── vmalert/
│   │   ├── frontend/    ├── query-api/         ├── ingest/
│   │   ├── ai-orchestrator/
│   │   └── secrets.yaml  ├── configmaps.yaml  └── service.yaml/ingress.yaml
│   └── files/
│       ├── clickhouse/init_clickhouse.sql   # 已生成（63 导出）
│       └── mysql/migrations/*.sql           # 版本化
└── scripts/
    ├── build-images.sh   # 构建 4 自研 arm64 镜像 + 导入 OrbStack
    ├── apply.sh          # helm install/upgrade
    ├── destroy.sh        # helm uninstall + 可选清 PVC
    └── init-db.sh        # 手动建表逃生门
```

### 3.2 组件清单
- **自研（4）**：frontend / query-api / ingest / ai-orchestrator
- **中间件（8）**：clickhouse / victoria-metrics / victoria-logs / redis / chromadb / minio / mysql / vmalert
- **deepflow（官方子 chart 依赖，完整装）**：deepflow-agent(DaemonSet) / deepflow-server / deepflow-clickhouse / deepflow-mysql / deepflow-grafana

### 3.3 中间件可插拔机制（应对其他环境已有实例）
每个中间件 values 用统一三段：
```yaml
clickhouse:
  enabled: true      # true=Chart 内拉起；false=复用外部
  external:          # 仅 enabled=false 时生效
    host: ""; user: ""; password: ""
  image: ...; storageClass: ""; resources: ...
```
- `enabled=true`：模板创建 Deployment/Service/PVC
- `enabled=false`：不创建资源，仅把 `external.*` 注入依赖服务的 env
- 三种模式：全内置（本机）/ 混合 / 纯外部 —— 一套 Chart 全支持
- 自研 4 服务同样有 `enabled` 开关

## 4. 连接拓扑与配置注入

```
浏览器 ─NodePort:30253─▶ frontend(nginx:80)
  /api/v1/ai/, /api/v1/ops/ → ai-orchestrator:8080
  /grafana/                 → deepflow-grafana.deepflow:3000
  /metrics 默认             → query-api:8080
query-api(Go:8080) ── ClickHouse / VictoriaMetrics / VictoriaLogs / Redis / MinIO / (MySQL P1b)
ai-orchestrator:8080 ── ChromaDB / Redis / VictoriaMetrics / VictoriaLogs / MinIO / LLM(mock)
ingest ── ClickHouse / DeepFlowSyncer(deepflow-clickhouse) / X-Api-Key
```

- **Secret**（敏感，values 注入、不进 git）：DB 密码、MinIO 凭据、Redis 密码、`X-Api-Key`、MySQL 密码
- **ConfigMap**（非敏感）：host/port 端点、ClickHouse 建表开关、LLM mock 开关
- **LLM mock**：`LLM_MOCK=true`（values 默认），RCA/NL 走 mock；后续配 Key 只需 `helm upgrade`（`LLM_MOCK=false` + `aiOrchestrator.llm.*`）
- **端口**：frontend NodePort 30253（与 63 对齐）；中间件仅 ClusterIP

## 5. 存储与数据生命周期

- PVC `storageClass: ""`（集群默认 SC，本机 local-path，可移植改 SC 名）
- 有状态组件独立 PVC，`ReadWriteOnce`，容量 values 参数化（CH 20Gi/MySQL 10Gi/Chroma 5Gi/MinIO 10Gi 等）
- **初始化**：
  - ClickHouse：InitContainer 执行 `files/clickhouse/init_clickhouse.sql`（幂等 `IF NOT EXISTS`，仅建结构、无历史数据）
  - MySQL：InitContainer 跑 `files/mysql/migrations/*.sql`（版本化，schema_migrations 记录版本）
  - Chroma/MinIO：首次空；MinIO 用 InitContainer 的 mc 建 bucket
  - deepflow：独立 namespace，首次空库初始化
- **TTL**：observability 表按分区 TTL（trace_spans/log_records 30 天等，在 init SQL 中定义）；deepflow 沿用 30 天（ingest 兜底 ensureRetention）
- **清理**：destroy.sh 含 `--purge-data` 开关，默认不删 PVC
- **建表可移植**：SQL 固化在 Chart 的 `.Files`，`helm package` 后随 tar 分发，任意环境 `helm install` 自动建表

## 6. deepflow 集成

- 作为 parent chart `dependencies` 引入官方 Chart（`deepflowio/deepflow`，`condition: deepflow.enabled`）
- 需 `helm dependency build`（联网拉子 chart）
- agent 以 DaemonSet 跑每节点（本机单节点 1 个）
- 跨 namespace 衔接：ingest 的 DeepFlowSyncer 连 `deepflow-clickhouse.deepflow.svc.cluster.local`
- 单节点无真实 eBPF 流量时拓扑/链路初始为空（符合干净部署），随测试流量产生

## 7. 实时同步（路径 1，代码改动）

改 `ai-apm-ingest-go/internal/pipeline/deepflow_sync.go`：
- `interval` 硬编码 60s → 读 env `DEEPFLOW_SYNC_INTERVAL`（默认 60s，可调 5–3600s，非法值回退默认）
- Sync 查询窗口 → 增量拉取（`lastSyncTime`，首次回退最近 N 分钟全量）
- `NewDeepFlowSyncer` / `cmd/ingest/main.go` 装配处同步更新

**鲁棒性要求（TDD 保障）**：
1. `lastSyncTime` 线程安全（Mutex/原子）
2. 失败容错：单次失败不影响循环、不 panic、优雅降级
3. 边界：首次无 lastSyncTime 回退全量；时钟回拨 clamp；间隔上下限校验
4. 增量幂等：窗口与上次重叠"保护重叠"，避免漏/重
5. 可观测：日志含 lastSyncTime/窗口/条数，Prometheus /metrics
6. 向后兼容：默认 60s，不破坏现有 63 部署

## 8. 鲁棒性、安全、验证

### 8.1 部署鲁棒性
- 每 workload：readiness/liveness 探针、resources.requests/limits、securityContext（非 root、只读根 FS、allowPrivilegeEscalation:false）
- InitContainer 保证中间件就绪 + 建表完成才起服务
- Helm 幂等可重复 upgrade；镜像版本固化（不用 latest）
- 回滚：helm rollback + 版本固化

### 8.2 安全
- Secret 从 values 注入（不硬编码、不进 git）
- 仅 frontend NodePort 对外，内部 ClusterIP
- 容器最小权限、非 root、只读 FS

### 8.3 验证策略
- 阶段冒烟：每服务就绪（kubectl get pods + readiness）
- 链路验证：前端 30253 → 注入测试流量 → 拓扑/链路/日志实时更新 → AI mock 可交互
- 建表验证：kubectl exec SHOW TABLES 确认 8 表齐全、无历史数据
- 鲁棒性验证：单测覆盖增量拉取 + 运行期观察增量日志连续无漏

## 9. 代码改动范围

- **A（业务逻辑，唯一改动）**：`ai-apm-ingest-go/internal/pipeline/deepflow_sync.go` + `cmd/ingest/main.go` 装配
- **B（新增部署工程）**：`aiops/deploy/`（Helm Chart + scripts），不动业务逻辑
- **C（从 63 获取，已完成）**：8 张表建表 SQL → 已落盘 `files/clickhouse/init_clickhouse.sql`

## 10. 待办/开放项

- [ ] MySQL migrations 内容待定（P1b 业务表结构，本期可先空/最小）
- [ ] 各服务实际 env 变量清单需从代码逐一确认（query-api/ingest/orchestrator 的 os.Getenv 全量）
- [ ] nginx.conf 反代是否需调整（当前硬编码 observability/deepflow DNS，本机照建两 namespace 即可不改）
- [ ] deepflow 官方 chart 具体版本/values 待 `helm dependency build` 时锁定
- [ ] 本机 git 初始化（spec 提交）待确认
