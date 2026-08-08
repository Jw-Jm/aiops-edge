# AIOps 平台完整实施文档（结合环境实况与部署经验）【2】

> 版本：v2.0 ｜ 日期：2026-08-07 ｜ 编制：架构组
> 定位：在 **192.168.0.63 单节点 K8s 真实环境**上，将「ongrid 复刻方案」落地为可执行的完整实施指南。
> 本文档 = **环境实际情况 + 部署以来全部问题（经验固化）+ ongrid 复刻四维实施** 的整合，是本次构建的唯一可执行依据。

---

## 目录

1. [环境实际情况](#一环境实际情况)
2. [部署以来问题总览与根治](#二部署以来问题总览与根治)
3. [复刻目标与原则](#三复刻目标与原则)
4. [现状架构 vs 目标架构](#四现状架构-vs-目标架构)
5. [分阶段实施计划（P0→P2）](#五分阶段实施计划p0p2)
6. [实施操作手册（含验证/回滚）](#六实施操作手册含验证回滚)
7. [数据保留与运维](#七数据保留与运维)
8. [风险与约束](#八风险与约束)

---

## 一、环境实际情况

### 1.1 服务器环境（63 服务器）

| 项目 | 实际值 | 备注 |
|------|--------|------|
| 节点 | 单节点 K8s（主机名 mssc），K8s v1.32 | |
| 架构 | x86_64 (amd64) | 镜像必须 amd64 单架构 |
| 系统盘 | **NVMe SSD nvme0n1**（238G，`/` 挂载）| 高 IO 组件应放这里 |
| 数据盘 | 机械盘 sda（1.8T，`/data` 挂载）| 低 IO / 冷数据 |
| 容器运行时 | containerd | `ctr -n k8s.io` 导入镜像 |
| SSH | root@192.168.0.63 | 应用入口 NodePort 30253 |
| 磁盘现状 | NVMe 56G/233G；sda 25G/1.8T | 空间充足 |

### 1.2 已部署组件（当前实际状态）

| 命名空间 | 组件 | 状态 |
|---|---|---|
| **observability** | frontend / query-api / ingest / ai-orchestrator / clickhouse / redis / victoria-metrics / victoria-logs / minio / vmalert | 运行中 |
| **deepflow** | deepflow-agent / deepflow-server / deepflow-clickhouse / deepflow-mysql / deepflow-grafana / deepflow-app | 运行中，稳定采集 |

### 1.3 当前镜像版本（实测）

| 服务 | 镜像 | 版本 |
|---|---|---|
| ingest | docker.io/library/ingest-pipeline | **v7**（含 DeepFlow TTL 30 天代码）|
| frontend | docker.io/library/observability-frontend | **v1.54** |
| query-api | docker.io/library/query-api | v2.33 系 |
| ai-orchestrator | docker.io/library/ai-orchestrator | v9 系 |

### 1.4 数据链路（当前有效）

```
真实服务调用 → DeepFlow agent(eBPF 零侵扰) → deepflow-clickhouse
→ ingest DeepFlowSyncer(每60s) → observability ClickHouse
   ├─ application_map → service_topology（拓扑）
   ├─ l7_flow_log    → trace_spans（服务/链路）
   └─ l7_flow_log    → log_records（日志）
→ query-api → 前端（NodePort 30253）
```

### 1.5 存储职责（当前）

| 存储 | 用途 | 位置 |
|---|---|---|
| ClickHouse(observability) | trace_spans / service_topology / metric_service_red / log_records / 业务设置 | NFS→已关注性能 |
| ClickHouse(deepflow) | flow_metrics / flow_log（TTL 30 天）| 稳定 |
| VictoriaMetrics | 指标（PromQL）| |
| VictoriaLogs | 日志（log-shipper 采集 pod 日志）| |
| Redis | 会话 / 任务 / 缓存 | |
| ChromaDB(ai-orchestrator) | 知识库 RAG | |

---

## 二、部署以来问题总览与根治

> 以下问题均已在真实环境遇到并解决，**全部固化**，新环境/新部署必须规避。

### 2.1 镜像与运行时类

| # | 问题 | 根因 | 根治方案 |
|---|---|---|---|
| P1 | 镜像 tag 不匹配 | yaml 引用 `:latest` 与实际版本不符 | 固定实际版本 tag；`kubectl set image` + `rollout restart` |
| P2 | ImagePullBackOff | K8s 解析 `docker.io/library/xxx` 但 containerd 无前缀 tag | **必须打 `docker.io/library/` 前缀 tag**（`tag_images.sh` 固化）|
| P3 | otel-collector 启动失败 `/otelcol-contrib not found` | 多架构 manifest + digest 引用，CRI 解析错层 | **单架构镜像** + **tag 引用**（禁 `@sha256:`）|
| P4 | busybox 离线拉取失败 | 离线环境无该镜像 | 改用环境已有的 redis:7-alpine / 或本地脚本 |

### 2.2 存储与数据类

| # | 问题 | 根因 | 根治方案 |
|---|---|---|---|
| P5 | ClickHouse PVC Pending | 无默认 StorageClass | 设默认 SC：`kubectl patch sc nfs-client -p '{...is-default-class:true}'` |
| P6 | 节点 load 虚高 → DeepFlow 熔断（melt_down）| NFS 机械盘 `sync` 模式 → nfsd 阻塞 jbd2 → load 飙高 | **NFS export 改 `async`** + 高 IO 迁移 NVMe + 重启 agent |
| P7 | ClickHouse 库表未初始化 → 所有页面无数据 | 从未执行建库建表 | 执行 `init_clickhouse.sql`（7 张表）|
| P8 | 前端 /logs 无数据 | log-shipper 无 RBAC 读 pod 日志 | 建专用 SA + ClusterRole + 挂载 |
| P9 | ClickHouse 内存过高 / 启动不稳 | K8s limits 设过小或过大 | 内部 config 限 `max_server_memory_usage`，K8s limits 留缓冲 |
| P10 | DeepFlow MySQL 崩溃 | 机械盘 InnoDB redo 失败 + 探针误杀 | 数据切 SSD + InnoDB 兼容配置 + 探针延迟调大 |
| P11 | DeepFlow server 迁移慢/重启 | 机械盘 + 探针延迟不足 | MySQL 切 SSD + 探针 initialDelay=600 |

### 2.3 数据正确性类

| # | 问题 | 根因 | 根治方案 |
|---|---|---|---|
| P12 | 时间错乱（多 8 小时）| DeepFlow 返回 Asia/Shanghai，未转 UTC | ingest 同步时 ParseInLocation(Asia/Shanghai)→UTC；前端统一 fmtLocalTime |
| P13 | 时间窗口过滤失效 | 用 `date >= today() - INTERVAL N MINUTE`（天粒度减法无效）| 改用 `start_time/time_bucket >= now() - INTERVAL N MINUTE` |
| P14 | 时长单位错 | DeepFlow 微秒，误当纳秒 | duration 微秒 ×1000 = 纳秒 |
| P15 | SQL 污染（探针请求混入）| DeepFlow 内部 SQL/探针请求 | `isInternalQuery` 过滤 |
| P16 | span 写入列错位 | OTLP span 缺合法 spanId | 业务必须带 16 位 hex spanId / 32 位 traceId |

### 2.4 前端/展示类

| # | 问题 | 根因 | 根治方案 |
|---|---|---|---|
| P17 | 指标趋势错乱（错误率/调用量共轴）| 双量级悬殊共单 y 轴 | 双 y 轴 + 错误率折线 / 调用量柱状 |
| P18 | 时区展示错误 | 直接 `dayjs(v).format()` 把 UTC 当本地 | 统一 `utils/date.ts` 的 fmtLocalTime/fmtLocalHM/fmtLocalMs |
| P19 | 拓扑/服务/链路/日志不同步 | 数据源/时间窗口/后端不一致 | 统一 DeepFlowSyncer 三维同步 + 时间窗口修正 |

### 2.5 生产化改造（已完成）

| 项 | 实现 |
|---|---|
| ingest 数据不丢失 | WAL 持久化 + 重试 + 崩溃恢复 |
| 采集鉴权 | `X-Api-Key` 校验（Secret 注入）|
| 过载防护 | 固定窗口限流 + 10MB body 上限 |
| 可观测性 | ingest `/metrics` Prometheus 格式 |
| DeepFlow 数据保留 | TTL 30 天（代码层面）+ 每日清理脚本 `/opt/deepflow-cleanup.sh`（crontab 01:30 UTC）|

---

## 三、复刻目标与原则

### 3.1 目标
在现有平台上，从**界面 / 功能 / 框架 / 架构**四维向 ongrid 看齐，但**全部自研**（ongrid 为 AGPL-3.0，严禁复制代码）。

### 3.2 合规红线
- 只借鉴 ongrid 的界面设计、功能清单、架构思想。
- **严禁复制** ongrid 任何代码文件（Go/TSX/CSS/脚本）。
- 参考源 `/tmp/ongrid-src` 仅用于阅读理解。

### 3.3 设计基因（复刻必须遵循）
G1 深色 zinc 极简 ｜ G2 聊天为第一入口 ｜ G3 状态胶囊+迷你图 ｜ G4 工具调用可视化 ｜ G5 ⌘K/⌘P+侧面板

---

## 四、现状架构 vs 目标架构

### 4.1 现状架构（如上 §1.2）
三服务（query-api / ingest / ai-orchestrator）+ 前端 + 存储栈。

### 4.2 目标架构（复刻后）

```
               ┌─────────────────────────────┐
  用户 ─HTTPS─▶│  frontend (nginx SPA+TLS)    │  Tailwind+Zustand
               └──────────┬──────────────────┘
                          │ /api 反代
               ┌──────────▼──────────────────┐
               │  query-api (Go) :8080         │◀─ MySQL(新增,P1b)
               │ 查询/告警/K8s/设置/审批/审计      │
               └───┬──────┬──────┬────────────┘
                   │      │      │
   ┌───────────────┘      │      └───────────────┐
   ▼                      ▼                      ▼
┌──────────┐     ┌────────────┐          ┌──────────────┐
│ clickhouse│     │ ingest (Go) │          │ ai-orchestrator│
│ (observ+df)│◀─OTLP/DeepFlow──│          │ AI 编排核心    │
└──────────┘     └────────────┘          └──────┬───────┘
┌──────────┐ ┌────────────┐ ┌──────────┐        │
│ victoria  │ │  victoria   │ │  mysql    │   ChromaDB + flow
│ metrics   │ │  logs       │ │  (新增)    │   + 审批 + 工具注册
└──────────┘ └────────────┘ └──────────┘
```

---

## 五、分阶段实施计划（P0→P2）

> 每阶段含：目标、改动文件、验收标准、回滚方案。

### P0a — 前端基座（界面观感统一）

**目标**：引入 Zustand；深色 token 收敛到 ongrid 色板；Sidebar 重构为 7 分区；新增页用 Tailwind。
**改动**：
- `observability-frontend/package.json`：+ `zustand`（+ 可选 `tailwindcss`）
- `src/App.tsx`：Sidebar 7 分区 + 懒加载路由 + RequireAuth
- `src/store/`：新建 `auth.ts` / `ui.ts` / `theme.ts` / `chatSessions.ts`
- `src/index.css`：全局深色 CSS 变量
**验收**：Sidebar 可折叠；7 分区导航正确；全局深色一致；存量页面无回归。
**回滚**：前端镜像回退到 v1.54。

### P0b — AI 聊天升级（工具卡片流）

**目标**：聊天支持工具卡片 + Agent 徽章 + PromptCard 首页 + Markdown。
**改动**：
- `ai-orchestrator/server.py`：SSE 增加 `tool_start`/`tool_end` 事件（工具名/参数/耗时/结果）
- `ai-orchestrator/tools.py`：工具收敛 12-15 个（现有 8 + 告警/日志/主机负载）
- `observability-frontend/src/pages/AIChat/`：重做首页 + 聊天线程
- `package.json`：+ `react-markdown` + `remark-gfm`
**验收**：聊天内工具卡片实时渲染；Esc 中断；Markdown 正常渲染。
**回滚**：orchestrator deployment 回滚；前端镜像回退。

### P0c — 告警详情 + 手动 RCA

**目标**：告警详情页 + 手动触发根因报告面板。
**改动**：
- `observability-frontend/src/pages/Alerts/`：新增 `/alerts/:id` 详情页
- 复用 query-api `rcaAlertAnalysis`（`/ops/rca/alert`）
**验收**：详情页展示根因/证据/置信度。
**回滚**：前端镜像回退。

### P1a — skills 元数据驱动 + 技能目录

**目标**：SkillRegistry 支持权限分级（safe/mutating/dangerous）+ 自动派生 schema；技能目录页。
**改动**：
- `ai-orchestrator/skill_registry.py`：ToolDef 增加 `cls`/`scope` + 自动派生
- `ai-orchestrator/skills/`：补诊断类技能（probe_http/tcp、read_journal、tail_file 等）
- `observability-frontend/src/pages/Skills/`：新增技能目录页
**验收**：技能可注册/列出/运行；权限分级生效。
**回滚**：orchestrator 回滚；前端新增页可隐藏。

### P1b — 知识库/MCP 前端 + 审批/审计 + 引入 MySQL

**目标**：知识库/代码索引/MCP 管理前端；审批中心 + 审计日志；**引入 MySQL 业务状态库**。
**改动**：
- `ai-orchestrator`：rag.py 补代码索引；审批/审计存储
- **`query-api`：引入 MySQL**（审批/审计/Agent/规则/报告持久化，版本化迁移）
- `observability-frontend/src/pages/`：新增 `/knowledge` `/mcp` `/approvals`
**验收**：知识可检索；审批可批/拒并审计；MySQL 持久化生效。
**回滚**：MySQL 迁移有版本化回滚；服务镜像回退。

### P1c — flow 引擎 MVP

**目标**：工作流可视化编排（React Flow），顺序执行 + 手动/定时触发 + tool/llm/notify/condition。
**改动**：
- `ai-orchestrator`：新增 flow 引擎（DAG 执行器 + 模板表达式 + 条件分支 + 运行记录）
- `observability-frontend/`：+ `@xyflow/react`；新增 `/workflows` `/workflows/:id`
**前置依赖**：P0b（工具注册）、P1a（技能）、notify 就绪。
**验收**：可编排并运行一个工作流。
**回滚**：新服务/模块独立，回退即可。

### P1d — Dashboard + 告警自动调查 + NL→ClickHouse SQL

**目标**：新增 `/dashboard`（KPI+Sparkline+趋势+告警环形图）；告警自动触发 RCA；NL→CH SQL 翻译。
**改动**：
- `observability-frontend/src/pages/Dashboard/`：新增
- `query-api`：告警评估命中后触发 RCA；`/dashboard/stats` 聚合接口
- `ai-orchestrator`：NL→ClickHouse SQL 翻译端点
**验收**：KPI 与后端一致；告警自动触发调查；NL 查询生效。

### P2a — WebShell（只读优先）+ 用户管理 + 报告中心

**目标**：xterm.js 终端（安全专项，只读优先 + 审计）；用户/组织管理；报告中心。
**改动**：
- `observability-frontend/`：+ `xterm.js`；新增 `/devices/:id/shell` `/pages` `/admin/*`
- `ai-orchestrator`：shell 流式 + 审计
- `query-api`：用户/组织 CRUD
**安全约束**：JWT + 会话审计 + shell_policy 白名单 + 超时/并发限制。

### P2b — IM 通知 + 完善

**目标**：飞书/钉钉/Slack 通知通道；Agent/技能/工作流完善。
**改动**：query-api 通知通道扩展；ai-orchestrator 完善。

---

## 六、实施操作手册（含验证/回滚）

### 6.1 前端构建与部署（每次前端改动后）

```bash
cd observability-frontend
npm install && npm run build
# 推送 dist 到远程构建镜像（或直接覆盖 pod）
scp -r dist/* root@192.168.0.63:/tmp/frontend-img/dist/
ssh root@192.168.0.63 'cd /tmp/frontend-img && docker build -t observability-frontend:vNEXT .'
ssh root@192.168.0.63 '
  docker tag observability-frontend:vNEXT docker.io/library/observability-frontend:vNEXT
  docker save docker.io/library/observability-frontend:vNEXT | ctr -n k8s.io images import -
  kubectl -n observability set image deploy/frontend frontend=docker.io/library/observability-frontend:vNEXT
  kubectl -n observability rollout restart deploy/frontend
  kubectl -n observability rollout status deploy/frontend'
```

**验证**：`curl -s -o /dev/null -w "%{http_code}" http://localhost:30253/` → 200；访问新资源 200。
**回滚**：`kubectl -n observability set image deploy/frontend frontend=docker.io/library/observability-frontend:v1.54`（回退到已知稳定版）。

### 6.2 后端（Go）构建与部署（query-api / ingest）

```bash
cd ai-apm-query-go && GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o /tmp/q ./cmd/...
scp /tmp/q root@192.168.0.63:/tmp/qimg/api
ssh root@192.168.0.63 '
  cd /tmp/qimg && docker build -t query-api:vNEXT .
  docker tag query-api:vNEXT docker.io/library/query-api:vNEXT
  docker save docker.io/library/query-api:vNEXT | ctr -n k8s.io images import -
  kubectl -n observability set image deploy/query-api query-api=docker.io/library/query-api:vNEXT
  kubectl -n observability rollout restart deploy/query-api
  kubectl -n observability rollout status deploy/query-api'
```

**回滚**：`kubectl -n observability set image deploy/query-api query-api=<上一稳定版本>`。

### 6.3 后端（Python）构建与部署（ai-orchestrator）

```bash
cd ai-orchestrator
# 打 tar 上传远程构建（含 skills/ 目录）
scp -r . root@192.168.0.63:/tmp/orch-src/
ssh root@192.168.0.63 'cd /tmp/orch-src && docker build -t ai-orchestrator:vNEXT . && ...'
# 同上导入 containerd + set image + rollout restart
```

**注意**：ai-orchestrator 改动后必须确认 `skill_registry`/`tools`/`skills` 文件齐全（有子目录）。

### 6.4 ClickHouse 建表/DDL

```bash
# 执行 SQL（含新表/字段）
cat scripts/xxx.sql | kubectl exec -i -n observability clickhouse-0 -- clickhouse-client --multiquery
```

### 6.5 MySQL（P1b 引入后）迁移

```bash
# 使用版本化迁移工具，每个版本一个文件，支持 up/down
# 示例：migrations/0001_init.sql / 0001_init.down.sql
```

---

## 七、数据保留与运维

### 7.1 DeepFlow 数据保留（已生效）
- **TTL 30 天**：ingest 启动时对 `l7/l4_flow_log_local` 执行 `MODIFY TTL time + toIntervalDay(30)`（代码层面兜底）。
- **每日清理**：本地脚本 `/opt/deepflow-cleanup.sh`（crontab 01:30 UTC）删除前一天数据 + OPTIMIZE。

```bash
# 查看清理脚本日志
cat /var/log/deepflow-cleanup.log
# 查看 TTL
kubectl exec -n deepflow deepflow-clickhouse-0 -- clickhouse-client \
  --query "SHOW CREATE TABLE flow_log.l7_flow_log_local" | grep TTL
```

### 7.2 observability 数据保留
- ClickHouse 表按天分区，建议配置 TTL（如 trace_spans 30 天、log_records 30 天）。
- 定期检查数据量，防止磁盘写满。

### 7.3 日常巡检
```bash
# 节点负载（应健康）
cat /proc/loadavg
# DeepFlow 无熔断
kubectl logs -n deepflow $(kubectl get pods -n deepflow | grep deepflow-agent | awk '{print $1}') | grep -c melt_down  # 期望 0
# 三表实时（UTC）
kubectl exec -n observability clickhouse-0 -- clickhouse-client \
  --query "SELECT max(start_time) FROM observability.trace_spans"
# ingest 同步正常
kubectl logs -n observability deploy/ingest | grep -i synced
```

---

## 八、风险与约束

| 风险 | 说明 | 应对 |
|---|---|---|
| AGPL 合规 | ongrid 为 AGPL-3.0 | 全自研，仅借鉴设计/功能/架构思想 |
| 双框架（AntD+Tailwind）| 样式冲突 | 渐进式：新增页用 Tailwind，存量页仅收敛主题 |
| 存量页回归 | 前端全量改动风险 | 每阶段独立验收 + 镜像版本回退 |
| MySQL 引入 | 新存储组件 | 版本化迁移 + 与数据仓职责分离 |
| flow 引擎复杂度 | 独立调度子系统 | MVP 收缩 + 前置依赖就绪后再做 |
| WebShell 安全 | 高危功能 | 只读优先 + 审计 + 白名单 + 超时限制 |
| 离线镜像 | 新增依赖拉取困难 | 优先用环境已有镜像 / 本地构建 |

---

## 附：当前已知稳定版本快照（回滚基准）

| 服务 | 稳定版本 | 说明 |
|---|---|---|
| ingest | v7 | 含 DeepFlow TTL 30 天 |
| frontend | v1.54 | 最新界面（指标趋势双轴）|
| query-api | v2.33 系 | 稳定 |
| ai-orchestrator | v9 系 | 稳定 |

> 每次实施前，先记录当前版本作为回滚基准；实施后必须通过 §6 的验证步骤确认无误。
