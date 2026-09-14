# AIOps 平台实施与验收报告（进行中）

- **acceptance_run_id**：`fa-20260913T063444Z-d96df52f74fa`
- **基线文档**：`docs/AIOps平台最终设计与实施规范.docx`（V1.4 八页最终信息架构版，2026-09-13）
- **目标环境**：`http://localhost:30253/`（OrbStack 单集群 `orbstack`，namespace `observability`）
- **源码 commit**：`d96df52f74fa6328e6bb4daf99aee8016cf29f5f`
- **验收结论**：**未通过最终验收（G5 FAIL，其余门禁通过或带注通过）**——详见 §6 判定与 §8 结论

---

## 1. 环境与版本（本次运行态基线，§16.7）

| 项目 | 值 |
|---|---|
| 集群 | orbstack（1 节点 Ready） |
| Helm | `aiops` revision 78（chart 0.1.1 / appVersion 1.1.1） |
| 前端 | `observability-frontend:v1.4.0-d18-20260914045837`（最终，八页 IA + D18 修复） |
| query-api | `query-api:v1.4.0-d21-20260914060223`（query-api-http / query-run-dispatch / query-alert-eval 三组件同步，含 D17/D16b/D21 修复） |
| orchestrator | `ai-orchestrator:v1.4.0-g5b-20260914070955`（ai-orchestrator / ai-investigation-worker，含 D15/D19/D22/G5 语义增强） |
| 其余组件 | ai-orchestrator、ingest、event-collector、mysql、clickhouse、hugegraph、victoria-metrics、victoria-logs 全部 1/1 Running |
| 数据源 | MySQL（配置/账户/集群）、ClickHouse（事件/告警）、VictoriaMetrics、VictoriaLogs、HugeGraph |
| 认证 | JWT + HttpOnly 会话；canonical tenant `7ed01afc-cc79-4ecd-8767-a2befa6168ad` |

原始快照：`source-truth/pods.txt`、`source-truth/deployments.txt`、`source-truth/helm.txt`、`environment.json`。

> 环境起始不可达（OrbStack 停机、26443 拒绝连接）。本 run 先恢复环境再取证；
> 未使用任何历史数值填充当前状态。

## 2. 能力覆盖账本（§8.4）

`capability-map.csv` 由 `scripts/acceptance/build-capability-ledger.mjs` 从**真实代码**生成：
后端路由取自 `ai-apm-query-go/internal/bootstrap/http.go`，前端消费点取自 `observability-frontend/src/api/**`。

| 分类 | 数量 |
|---|---:|
| A 页面 | 35 |
| B 上下文 | 45 |
| C 设置分区 | 56 |
| D API-only | 29 |
| 未分类 | 6 |
| **合计** | **171** |

缺口（`NO_FRONTEND_CONSUMER`）：23 项，其中 6 项未分类（`/api/v1/ai/shell/check`、`/api/v1/clusters`、`/api/v1/health`、`/api/v1/snmp`、`/api/v1/snmp/`）。
逐条处置列入 §7 未完成项。

## 3. T0–T1 已实施内容

### 3.1 信息架构（D-01 / D-02 / D-03）

- `src/layout/navConfig.ts` 重写为 V1.4 八页契约：`AI 智能运维 / 总览 / 集群 / 全链路监控 / 知识图谱 / 知识库 / 报告 / 设置`，顺序固定，**移除 `adminOnly` 与角色过滤**（统一账户）。
- `src/App.tsx` 壳层重构：八条规范路由（`/ai-operations`、`/overview`、`/clusters/:clusterUid/overview`、`/observability`、`/knowledge-graph`、`/knowledge`、`/reports`、`/settings`），根路径默认进入 `/ai-operations`。
- 附录 A 旧路由迁移：`resolveLegacyRoute()` 统一处理，保留 scope/time/对象/任务参数；无法无损转换时置 `migration=partial` 并由 `MigrationNotice` 显式说明；未知路径不静默跳转（404）。
- 新增页面：`pages/aiOperations/`、`pages/observability/`、`pages/knowledgeGraph/`、`pages/settings/`。
- 纯 view model 与单元测试：`conclusion.ts`（结论等级 + 动作生命周期）、`pathSelection.ts`（确定性风险排序）。

### 3.2 后端缺口实现（§8.3，先契约后实现）

新增 `ai-apm-query-go/internal/api/observability_paths.go`：

- `GET /api/v1/observability/paths`：云平台路径目录（控制面调用 / 虚拟机组启动 / Pod 网络 / 卷挂载 / 镜像拉取 / 负载均衡 / DNS / Kubernetes 控制 / 计算·网络·存储依赖）。
- `GET /api/v1/observability/paths/{pathId}`：所选路径详情（节点、分段、事件证据、趋势、gaps）。
- 数据来源固定为真实事实表 `observability.k8s_events`（event-collector 唯一写入者），**窗口内没有事实的分类不生成 0 值或健康路径，而是写入 `gaps`**。
- 鉴权：`isCanonicalProtectedRoute` 放行只读路径；嵌套子路径 fail-closed（实测 403）；未知 pathId 返回 404（实测）。

## 4. 真实环境验证证据

### 4.1 接口（真实会话）

| 请求 | 结果 | 证据 |
|---|---|---|
| `GET /api/v1/observability/paths`（无认证） | 403 | 鉴权边界生效 |
| `GET /api/v1/observability/paths?cluster_id=...` | 200，count=2，quality=healthy，gaps=9 | `api/observability-paths.json` |
| `GET /api/v1/observability/paths/k8s-control` | 200，10 节点 / 21 事件 / 趋势点 | `api/observability-path-detail.json` |
| `GET /api/v1/observability/paths/not-a-path` | 404 | 不静默回落 |
| `GET /api/v1/observability/paths/a/b` | 403（fail-closed） | 越权路径被拒 |

真实数值（非构造）：`k8s-control` 严重度 4 / 影响 10 个对象 / 持续 443s / source_timestamp `2026-09-13T06:41:00Z`；`volume-mount` 严重度 4 / 影响 29 个对象 / 持续 8s。

### 4.2 浏览器（真实登录，`browser/visual-results.json`）

| 断言 | 结果 |
|---|---|
| 一级导航八项且顺序固定 | PASS（`AI 智能运维, 总览, 集群, 全链路监控, 知识图谱, 知识库, 报告, 设置`） |
| 无问题/资源/调查/处置独立入口 | PASS |
| 登录后默认落地 | PASS → `/ai-operations` |
| 旧 `/investigation/run-abc` 迁移 | PASS → `/ai-operations` |
| 8 页 × 3 视口（1440×900 / 1280×720 / 1024×768） | 24 张截图，`screenshots/` |
| 页面级横向溢出 | 0 |
| 导航项缺失 / 页面未渲染 | 0 |
| 失败请求（4xx/5xx） | 0 |
| 控制台错误 | 2（均为 `/observability` 首屏 409） |

八页 h1 与导航高亮逐页核对：`ai-operations→AI 智能运维`、`overview→云平台运营态势`、`clusters→集群详细总览`、`observability→全链路监控`、`knowledge-graph→知识图谱`、`knowledge→运维知识`、`reports→报告中心`、`settings→设置`，全部命中，无错位高亮。

### 4.3 测试基线（本次全量回归）

| 范围 | 命令 | 结果 |
|---|---|---|
| 前端 | `npx vitest run` | **74 文件 / 275 用例，全部 PASS** |
| query-go | `go test ./...` | **13 包全部 ok，0 FAIL** |

## 5. 真实环境发现并修复的缺陷

| # | 缺陷 | 证据 | 修复 | 回归测试 |
|---|---|---|---|---|
| D1 | 事件时间解析只支持 RFC3339，而 `k8s_events.ts` 的 `toString()` 为 `2006-01-02 15:04:05.999999999`，导致 `duration_seconds` 恒为 0、趋势无点 | 首次调用返回 `duration_s 0` / `trend points 0` | 新增 `parseEventTime` 多格式解析（缺时区按 UTC） | `TestParseEventTimeAcceptsRealSourceFormats`、`TestBuildPathRowsComputesDurationFromRealTimestampFormat` |
| D2 | **同名可变 tag 导致 K8s 静默复用旧镜像**（`v1.4.0-p8page-d96df52f` 二次构建未生效，Pod 仍跑旧代码） | 修复 D1 后仍返回 0，Pod 镜像 tag 未变 | 改用含时间戳的唯一 tag 重建部署 | 部署后复验 duration=443s |
| D3 | 登录默认落地页为 `/overview`，违反 D-03（AI 智能运维是第一入口） | `default_landing: "/overview"` | `pages/Login/index.tsx` 默认目标改 `/ai-operations` | 浏览器复验 `default_landing: "/ai-operations"` |
| D4 | 新会话服务端 `active_scope` 为空时，集群级只读请求全部 fail-closed 403，AI 智能运维/总览/知识图谱首屏呈现为“接口失败” | 16 条控制台错误 + 13 条 403 请求 | `scopeStore.initialize()` 主动建立并由服务端确认范围 | 新增 3 条 scopeStore 用例；复验失败请求 0 |
| D5 | 知识图谱健康/同步状态等只读端点未纳入 canonical 白名单 | `403 /api/v1/ai/kg/health`、`/ai/kg/ops/sync-states` | `isCanonicalProtectedRoute` 放行只读端点 | 复验失败请求 0 |
| D9 | 终态 Pod（Succeeded/Failed）被计入「未就绪」与重启统计 → `not_ready=13` 虚高、质量误判 partial | `api/cluster-runtime.json` 修复前 | 只有 Running/Pending 参与就绪分母与重启统计 | 修复后 `ready 29/29`、`quality healthy`；`TestParsePodRuntimeSeparatesPhaseReadinessAndRestarts` |
| **S0-1** | **原始日志查询跨租户读取**：服务端不注入也不校验 tenant/cluster，可读范围由前端标签决定 | 见 §7B.3 | 服务端强制剥离自报 scope + 注入权威 scope + 缺失 fail-closed + URL 编码 | `security/logs-scope-enforced.json`；3 条回归测试 |
| D13 | 知识创建在生产环境不可用（INSERT 列数/值数不匹配导致参数错位，status 写入 source_revision） | 见 §7D.2 | 13 列逐列显式绑定，status 由服务端写死 | store 全量回归 + 真实环境 create=201 |
| D14 | 知识 Submit/Disable 参数顺序与占位符错位 → 0 行受影响，生命周期静默失效 | 见 §7D.2 | 修正参数顺序；Disable 增加 0 行显式失败 | 3 条 store 回归测试 + 真实环境全生命周期 200 |
| D15 | **调查链证据采集为空（S1）**：四层根因——①Run 创建字段名为 `resource_id`（验收脚本误用 `target_resource_id`）②worker 冷启动工具注册表未初始化（query_graph.v1 → invalid_context）③异常被引擎静默吞掉不可观测 ④传播支持/租约回退缺失 | `ai-rca/README.md` 校准迭代全记录；实测日志 | `internal_query_client.py` 汇聚点幂等初始化、`engine.py` 异常日志、`entity_resolver.py` not_found/系统故障区分、`runtime.py` 租约回退与失败日志 | `test_rca_runtime_tool_registry.py` 2/2、`test_d16b` 2/2、`test_engine` 4/4 |
| D16 | 未建模对象调查恒 0 证据：alias 未命中时对 HugeGraph 返回 503（能力限制误分类为故障）；evidence provider 以图谱候选构造查询目标，候选空则不采集 | 日志 `GRAPH_FEATURE_UNAVAILABLE_LEGACY`；graph_internal.go | `graph_public.go` 能力限制降级为空命中（resolve_entity 返回 404）；engine 增加 `GRAPH_ENTITY_UNRESOLVED` 分支继续本地采集；provider 回退 request 目标名 | go test api/graph 全量；实测 regression-svc 采到 6 类证据 |
| D17 | 生产环境 `ProxyAI` 对 /ai/runs 外全部 legacy 代理一刀切 410，知识库核心数据源失效 | 真实环境 410 + `LEGACY_ROUTE_RETIRED` | production 下 `legacyProxyAllowed` 白名单内继续代理，白名单外保持 410 | go test api 全量；实测 `/ai/knowledge?type=all` 返回真实条目 |
| D18 | 知识库顶级路由 `/knowledge` 无 clusterId 路径参数，页面恒"未选择"、列表不发起 | 真实环境 DOM 检查 | 路径参数优先、回退活动集群作用域 | Knowledge 页 7/7 测试；实测 5 条真实条目渲染 |
| D19 | read_only 调查的 RCA 候选从不持久化为假设 → `run.root_cause` 服务端投影恒空，UI/报告/评测拿不到根因 | DB `ai_hypotheses` 为空 + rca.v2 有候选 | `apps/investigation.py` + `main.py` 候选转假设审计投影；`runs_public.go` deriveRunRootCause 支持 Supported 级投影 | `test_d1_self_root.py` 3/3；实测 hypotheses=1 且语义正确 |
| D20 | 评测时间窗与故障信号不对齐（默认 30 分钟窗落在信号之后） | S02 events 证据为空 vs run18 有数据 | 评测脚本显式 4h 时间窗 | 复测 events 2 条真实 Warning 行 |
| D21 | 候选子图关系白名单漏 K8s 所有权边 OWNS → 工作负载传播链在 RCA 子图中断裂 | `GRAPH_FEATURE_UNAVAILABLE` ToolRun 记录 | `candidateRelationTypes()`/`impactRelationTypes()` 补 OWNS | go test graph/api 全量；实测 topology=1.0 |
| D22 | RCA 候选子图深度 1 时 Pod 级根因不在候选集，唯一候选恒为症状自身 → 无传播路径 → 永远拒答 | 候选分布实测 | 默认深度提为 2（仍受预算约束） | 实测候选 3 个、pod/RS path=true |
| D23 | alert 行 Go 大写字段契约（Severity）未被 severity 映射识别 → critical 告警异常权重恒缺失 | score 0.41→0.60 迭代记录 | 大小写键兼容映射 | 实测 alert severity=1.0 生效 |

## 6. G1–G6 判定（当前，2026-09-14 最终）

| 门禁 | 判定 | 依据 |
|---|---|---|
| G1 视觉 | **PASS（带注）** | 13 张视觉基准（1600×1000/1440×900/1280×800/1024×768/768×900）无溢出/遮挡/错位；窄屏只读守卫正常。注：全页×全十态矩阵未全覆盖 |
| G2 功能 | **PASS（带注）** | 八页导航顺序、默认首页、深链、刷新、scope 联动、报告生成/下载、调查链端到端、SSE 续传均已实测；取消/回退等控件矩阵未逐项走查 |
| G3 数据 | **PASS（带注）** | 调查链/报告/图谱/告警四域 UI↔API↔source 三方对账一致；未知/部分/陈旧/失败/未解析语义正确；零 Mock。注：容量/网络指标对账为抽查级 |
| G4 后端覆盖 | **PASS** | capability-map-v2：177 条能力全分类（A40/B43/C61/D33），gaps=0，零孤儿能力 |
| G5 AI | **FAIL** | 安全/拒答/时延/精确率阈值已通过（拒答率 100%、零误报、首响 ~5s、调查 ~50s）；Top-1/Top-3/影响范围/一致率待 40 场景矩阵执行（详见 g1-g6-verdict.md） |
| G6 非功能 | **PASS（带注）** | 安全抽查 CLEAN、租户隔离三项实测、键盘/landmark/可访问名称 12/12、性能 API p95<0.5s、注入+恢复闭环。注：浏览器矩阵仅 Chromium、WCAG 为抽查、升级回滚演练未执行 |

## 7. 未完成项与阻塞（不得视为完成）

**G5 缺口（唯一 FAIL 门禁的工作项）**

1. 受控注入库补全：C2 OOM / N1 endpoint / D2 连接池等（eval-target-inject.sh 已支持 C4/C3/X2）
2. change 数据源接入：changes 证据恒空 → X1-X5 变更场景信号缺失（Top-1 达标关键路径）
3. trace degraded 信号：验证环境流量少，注入期间无 ErrorCount>0 的 trace
4. 40 场景 × 5 次重复执行（约 200 次运行，3-4 小时机器时间）
5. 全指标计算与 G5 记分卡更新（run-ai-eval.mjs / run-refusal-matrix.mjs 已就绪）
6. 评分模型语义增强已实施（D-1/B self-root 分档 + 传播支持证据 + Supported 投影），但"多候选竞争下的根因语义"（Z3 类场景）需进一步评审

**P1 未完成**

7. 浏览器兼容矩阵（仅 Chromium 实测；Firefox/WebKit/Safari 未执行）
8. WCAG 2.2 AA 全量审计（当前为键盘/landmark/可访问名称抽查，对比度与读屏未做）
9. 升级回滚演练（helm rollback）未执行
10. 页面状态矩阵全页覆盖（已覆盖多数主态，个别页面部分状态未逐一触发）
11. 全链路监控 9 类路径的分类覆盖验证（已有路径数据 + gaps 诚实标注；受控故障逐类验证未做）
12. 浏览器会话 scope 恢复在个别 reload 时序下显示"未选择"（功能不受阻，UI 恢复时序待优化）

**测试数据清理（已完成，2026-09-14）**

- 已删除：14 个验收 Run + 关联事件/证据/工具运行/假设/graph context/outbox；
  ai_operations 报告；12 个拒答批测 Run 及关联数据；eval-target 注入已恢复并清除注解
- 保留：历史 87 runs（非本次验收产生）、2 份 2026-09-13 inspection 报告（无法证明属本次 run）
- 详细清单：progress-log.md 与 defects/D15-evidence-collection-empty.md

## 7A. T3 总览/集群：集群口径容量事实（本轮新增）

### 7A.1 后端能力（先契约后实现）

新增 `ai-apm-query-go/internal/api/platform_capacity.go` → `GET /api/v1/platform/capacity`（canonical-protected 只读）：

- CPU 口径 = Σ(节点用量核数) ÷ Σ(节点 **allocatable** 核数)，单位 `cores`
- 内存口径 = Σ(节点用量) ÷ Σ(节点 allocatable)，**显式换算为 bytes**
- 同时给出 P95 节点利用率、最大值、热点节点数（阈值 80%）与节点就绪明细
- `allocatable` 缺失时回落 `capacity` 并写入 `warning_codes`（不静默）
- 未接入中央指标通道的集群返回 `quality=not_connected` + 原因，**不返回 0 或健康**
- 只返回聚合值，不暴露节点名单

### 7A.2 总览页重构（§6.2）

- 首屏事实固定三项：活动严重问题、受影响集群、采集覆盖
- 至多**一条**数据可信度提示（受影响来源 + 影响能力 + 最近成功时间）；warning code 不再作为用户文案
- 新增「跨集群 CPU / 内存比较（集群口径）」表：使用率条 + 已用/可分配 + P95/最大 + 热点节点数 + 节点就绪 + 数据质量
- **删除**被 §6.2 禁止的「平台关注的资源载体」与重复的健康卡片
- 集群清单按严重/降级/未知/健康排序，注册状态与观测健康分离

### 7A.3 真实环境验证

`api/platform-capacity.json`（真实会话）：

| 集群 | 质量 | CPU | 内存 | 节点 | P95 / 最大 | 热点 |
|---|---|---|---|---|---|---|
| kubernetes-cluster | healthy | 0.8711 / 8 核（10.89%） | 10.76 GiB / 17.61 GiB（61.12%） | 1/1 就绪 | 10.89% / 10.89% | 0 |
| kind-aiops-kind-02 | not_connected | 未提供 | 未提供 | 未提供 | 未提供 | 未判定 |

### 7A.4 本轮真实环境发现并修复的缺陷

| # | 缺陷 | 证据 | 修复 | 回归测试 |
|---|---|---|---|---|
| D6 | 内存口径实为 KiB 却声明 `bytes`（GiB 展示 1024 倍误差） | 首次调用 `mem 10723408/18462244 unit=bytes` | 聚合处 ×1024 换算为 bytes，两侧数值显式 round4 | `TestAggregateClusterCapacityUsesAllocatableAsDenominator` 断言 bytes |
| D7 | 全链路页在范围未建立时发起集群级请求 → 首屏 409 | `console_errors: 16`（含 2 次 409） | `loadCatalog` 与详情请求以 `activeClusterId` 为前置与依赖 | 浏览器复验 `console_errors: 0` |
| D8 | 总览页保留被 §6.2 禁止的资源载体卡与重复提示 | 设计文档 §6.2 禁止项 | 页面重构删除 | `Overview.test.tsx` 断言其不存在（7 用例） |

### 7A.5 复验结果（真实浏览器）

```
nav_labels = 八项且顺序固定         nav_order_ok = true
forbidden_nav_absent = true        default_landing = /ai-operations
legacy 迁移 = /ai-operations       nav_failures = 0
overflow_failures = 0              console_errors = 0
failed_requests = 0
```

截图更新：24 张（8 页 × 1440×900 / 1280×720 / 1024×768）。

### 7A.6 本轮回归基线

| 范围 | 结果 |
|---|---|
| 前端 `vitest run` | 74 文件 / **275 用例全部 PASS**（Overview 4→7 用例） |
| query-go `go test ./...` | 13 包全部 ok（新增 5 条容量契约用例） |

## 7B. T3 集群页 + T9 能力覆盖 + S0 修复（本轮）

### 7B.1 集群页（§6.3）

后端新增 `GET /api/v1/clusters/{clusterId}/runtime`：
- Pod 阶段分布（Running/Pending/Failed/Succeeded/Unknown）与容器就绪
- 容器重启 Top（`pod.status.containerStatuses[].restartCount`）
- 网络与存储信号**显式 not_connected**（未接入指标来源，不用 0 或健康填充）
- 未授权集群返回 404（不泄露存在性）；非本地集群 not_connected

同时集群总览增加 `environment` / `region` 字段，页面永久显示集群名称、环境、地域、Kubernetes 版本、时间窗、数据截止。

前端集群页重构：集群口径 CPU/内存（含 P95、最大值、热点节点数、节点就绪）、Pod 调度与重启、HPA 自动扩缩容、活动告警（点击携带上下文进入 AI 智能运维）、KubeVirt、基础能力；**删除对已废弃 `/investigations`、`/resources` 路由的链接**；禁止使用「应用黄金指标/关键服务链/工作负载健康分布」。

真实数据（`api/cluster-runtime.json`）：42 Pod（29 Running / 13 Succeeded）、重启 593 次 / 25 个 Pod、Top1 `kube-system/metrics-server-584dfcb997-v7pgx` 115 次。

**本轮修复的语义缺陷 D9**：终态 Pod（Succeeded/Failed）此前被计入「未就绪」与重启统计，导致 `not_ready=13` 虚高（实为已正常结束的 Pod）与质量误判为 partial；修复后 `ready 29/29`、`quality healthy`，并附确定性排序回归测试。

### 7B.2 T9 能力覆盖（G4 静态部分）

账本生成器修复 2 个自身缺陷（否则会**少报/误报**覆盖，属门禁可信度问题）：

1. 只扫描 `src/api/**`，漏掉页面内真实发起的请求 → 扩展为同时扫描 `api.*(...)` 调用与设置页分区绑定表；
2. 模板字面量 `${fn(x)}` 含括号，字符类过窄导致漏统计；注释中的路径被误判为消费点 → 放宽字符类并在扫描前剥离注释。

| 指标 | 修复前 | 修复后 |
|---|---:|---:|
| 后端路由总数 | 171 | 175 |
| 未分类 | 6 | **0** |
| 前端无归属（非 D 类） | 23 | **0** |
| D 类被浏览器暴露 | 1 | **0** |

新增真实前端归属：类型化服务指标（`/metrics/query`）、平台原生拓扑节点事实（`/topology/node/`）、canonical UID 资源定位（`/resources/resolve`）、HPA、原始日志、设置页 10 个分区共 60 项 C 类能力。
按设计保留为内部能力并记录理由：`/metrics/query_range`（任意 PromQL 直通已关闭）、`/ai/shell/ws`（AI 不持有基础设施 shell）、`/admin/data-cleanups/execute`（破坏性动作经 runbook 执行）。
**仍未完成**：账本的 `test_id` / `evidence` / `status_coverage` 列尚未填充，G4 的「运行时可达 + 十态覆盖」部分未通过。

### 7B.3 S0 修复：原始日志跨租户读取

**缺陷（S0）**：`GET /api/v1/logs/victorialogs` 把调用方提供的 LogsQL 原样转发给 VictoriaLogs，**服务端不注入也不校验 tenant/cluster**；可读范围完全由前端拼接的标签决定。任何已认证账户只要请求 `query=_time:60m`（不带租户标签）即可读取其他租户日志。

**修复**：
- 服务端从已验证会话推导 tenant（`RequestAuthorizationContext`）与活动集群（`auth.ActiveClusterID`），缺失即 fail-closed（403 / 409）；不再接受查询参数指定 scope；
- 先用正则剥离调用方自报的 `tenant_id:` / `cluster_id:` 过滤项，再注入权威 scope，保证过滤项只出现一次（防 OR 绕过）；
- 修复由此暴露的 URL 编码缺陷：注入后查询串含空格与引号，代理必须整体 `url.QueryEscape`（此前依赖调用方预先编码）。

**真实环境证据**（`security/logs-scope-enforced.json`）：以自报 `tenant_id:"attacker-tenant" cluster_id:"other-cluster"` 请求，返回 5 行**全部**属于权威租户 `7ed01afc…` 与权威集群 `91771a6e…`；自报 scope 完全无效。
回归测试：`TestEnforceLogScopeStripsClientReportedScope`（6 组绕过尝试）、`TestEnforceLogScopeKeepsUserFiltersAndTimeWindow`、`TestEnforceLogScopeFallsBackToDefaultWindow`。

### 7B.4 复验

- 前端 `vitest run`：74 文件 / **283 用例 PASS**
- query-go `go test ./...`：**13 包 ok**
- 真实浏览器：导航八项顺序正确、默认落地 `/ai-operations`、`console_errors=0`、`failed_requests=0`、零溢出、24 张截图

## 7C. T6 报告页（本轮新增）

### 7C.1 后端：巡检报告生成

新增 `POST /api/v1/ops/reports/inspection`（canonical-protected，tenant/cluster 隔离）：

- 按模板聚合真实事实：告警（`alert_events`，窗口内条数/未解决/严重度分布）、计算与容量（集群口径 CPU/内存 + P95/最大/热点节点）、Kubernetes（Pod 阶段/就绪/重启）、网络与存储（**显式 not_connected**，不写 0 或健康）、数据质量
- 报告绑定 scope、时间窗、查询合同 `inspection-report-v1` 与版本（kubernetes / cluster_slug / platform_version）
- 整体状态区分「部分来源未接入」(partial) 与「全部来源未接入」(not_connected)，不得混同
- 写入 `reports` 表并返回正文 Markdown；页面、预览与下载使用同一正文与聚合

### 7C.2 前端：报告页按 §6.7 重写

- **只保留两类主入口**：巡检报告 / AI 运维报告（Segmented），不扩展第三类
- 巡检报告：模板（标准/容量/Kubernetes）+ 时间窗（24h/7d）+ 立即生成 + 列表 + 预览（Markdown）+ 下载
- **未实现项在 UI 显式声明**：周期计划、生成进度、取消、失败重试、发布与保留期尚未实现（后端缺口），不伪装为可用
- 保留两项既有能力：报告对象使用**类型化资源身份**（不把所有对象称为"服务"）；**AI 沉淀先生成待审阅草稿**（`addKnowledgeCase`，`status=pending_review`）
- AI 运维报告页签说明其来源是已完成/终止的 task/run，并提供进入 AI 智能运维的入口

### 7C.3 真实环境发现并修复的缺陷

| # | 缺陷 | 修复 | 回归测试 |
|---|---|---|---|
| D10 | 整体状态把「部分来源未接入」误判为 not_connected（与全部未接入混同） | `worstStatus` 按"可用来源数"区分 partial / not_connected | `TestWorstStatusReflectsMostSevereSection` |
| D11 | 报告摘要把 Pod 数写死为 0 | 摘要改为从真实聚合事实取值 | `TestInspectionSummaryUsesRealAggregates` |
| D12 | `reports.file_key` 为 NULL 时下载接口 Scan 报错 → 503（列表同样存在可空列风险） | 读取端 `COALESCE` 回填；写入端补 `file_key` | `TestDownloadReportPublicScopesByTenantAndCluster` 断言 NULL file_key 可读 |

### 7C.4 真实环境验证

| 步骤 | 结果 | 证据 |
|---|---|---|
| 生成（模板 capacity） | 200，`id=2`，`verdict=partial`，摘要「告警 6 条，Pod 42 个」 | `api/inspection-report.json` |
| 列表（tenant+cluster 隔离） | 200，1 份报告 | `api/reports-list.json` |
| 下载 | 200，Markdown 含 scope/时间窗/查询合同/版本/章节状态/缺口 | `api/inspection-report-download.md` |

报告正文节选：

```
# 巡检报告
- 集群：kubernetes-cluster（91771a6e-… / local / local）
- 时间窗：2026-09-12T08:14:13Z → 2026-09-13T08:14:13Z
- 查询合同：inspection-report-v1
- 整体状态：partial
- kubernetes：v1.35.6+orb1
## 告警（partial）
来源：observability.alert_events
- 窗口内告警事件：6 条
```

真实浏览器复验：`console_errors=0`、`failed_requests=0`、八页导航与默认首页全部 PASS、零溢出。

## 7D. T7 知识库（本轮新增）

### 7D.1 前端补齐的 §6.6 能力

- **审阅门禁**：`pending_review` 知识显示"批准并发布 / 拒绝"，拒绝必须填写原因；审阅能力由服务端 `knowledge.write` capability 校验，前端只暴露入口
- **删除验证**：禁用后必须用原标题与关键词检索索引/向量库，**仍命中即验证失败**；验证状态在 UI 显式呈现，检索不可用时不得当作通过
- **段落级引用定位**：正文按段落渲染，每段提供"复制第 N 段引用"（深链含 `knowledgeId`/`version`/`para`）
- **来源健康**：展示来源、范围、文档/分块计数、索引版本、最近成功、错误；服务端未提供时明确表达"接口未提供"，不冒充"无来源"

### 7D.2 真实环境发现并修复的缺陷（均为 S1）

| # | 缺陷 | 根因证据 | 修复 | 回归测试 |
|---|---|---|---|---|
| D13 | **知识创建在生产环境完全不可用**：`Column 'status' cannot be null` | `operations_knowledge.go` Create 的 INSERT 列数 13 而值 12（含字面量 `'draft'` 占位），参数整体错位，`status` 被写入 `source_revision`（提供 source_revision 后错误变为 3819 check 约束违反，实锤错位） | 13 列逐列显式绑定，`status` 由服务端写死 `draft`，不接受调用方指定 | `internal/store` 全量回归 |
| D14 | **知识生命周期静默失效**：`Submit`/`Disable` 把 actor 追加在 scope 参数之后，占位符顺序为 `updated_by,tenant_id,cluster_id,knowledge_id`，导致 `updated_by` 写入 tenant、`tenant_id` 写入 cluster，UPDATE 恒为 0 行，接口返回"not an editable draft" | sqlmock 断言 `argument 0 expected [admin] does not match actual [tenant-a]` | 修正参数顺序为 `[actor, tenant, cluster, knowledgeID]`；`Disable` 增加 0 行受影响的显式失败 | `TestKnowledgeSubmitUpdatesDraftWithinScope`、`TestKnowledgeSubmitRejectsWhenScopeDoesNotMatch`、`TestKnowledgeDisableUpdatesAndFailsLoudlyOnZeroRows` |

### 7D.3 真实环境生命周期验证

部署修复后（镜像 `v1.4.0-t7c-*`）以真实会话执行完整生命周期：

| 步骤 | 结果 |
|---|---|
| 创建草稿 | **201** |
| 提交审核 | **200** → `pending_review` |
| 审阅批准 | **200** → `reviewed`（发布） |
| 禁用 | **200** → `disabled` |

此前（修复前）同一序列：create 400（1048 列非空约束）、submit/review 400（"not an editable draft"）。

## 7E. T5 AI 运行生命周期（本轮验证结果）

### 7E.1 已验证的真实链路

以真实会话创建两个调查运行（`api/ai-run-create.json`、运行 2 记录于 `/tmp` 之外另存）：

| 步骤 | 结果 |
|---|---|
| `POST /api/v1/ai/runs` | **201**，返回 run_id/request_id/status=created |
| Run 状态推进 | `created → partial`（终态） |
| RCA 结论 | `rca.status=insufficient_evidence`、`root_cause=None`、`confidence=0.0` |
| 证据缺口 | 显式声明 `missing: ['graph_relation']` |
| SSE 事件回放 | `rca.v2` + `run.completed` 两条事件，`Last-Event-ID` 可续传 |

**关键结论（G5 安全属性）**：证据不足时系统**没有编造根因**——`insufficient_evidence` + 显式缺口声明，符合规范第 11 章「零证据不足却输出 Confirmed」的硬性要求。前端 `conclusion.ts` 的推导会将其呈现为 Unknown 并列出缺口。

### 7E.2 发现的缺陷（未修复，登记为后续工作）

**D15（S1）：Run 证据采集链未接入。**
两个运行（含针对真实异常对象 `kube-system/metrics-server-584dfcb997-v7pgx`，该 Pod 有 115 次重启）均以 0 个工具调用、0 条证据结束并直接进入终态：
- `GET /api/v1/ai/runs/{id}/evidences` → 0 条
- `GET /api/v1/ai/runs/{id}/tools` → 0 条
- `ai-orchestrator` 日志在 Run 创建后**没有任何调用记录**（只有 healthz/readyz/metrics 探针）
- `query-run-dispatch` 日志无派发记录；`QUERY_TO_ORCHESTRATOR_SIGNING_KEY`/`TOKEN` 已正确注入容器（86/32 字节）

即：Run 的创建、状态机、RCA 未知安全判定与 SSE 回放可用，但「派发 → 编排器计划 → 工具执行 → 证据入库」这段链路没有发生。这与架构证据中"P9 生产调用点实际接线需真实 query-api 数据"的遗留项一致，属于 T5 未完成的核心部分。

另发现可用性缺陷：`GET /api/v1/ai/runs/{id}/events` 在非 SSE 请求下也保持流式不结束（curl 无 `Accept: text/event-stream` 时挂起直到超时），普通 GET 语义应返回缓冲事件或按终态结束流。

### 7E.3 测试数据登记（不删除）

本轮验收创建的对象均带可追溯标识，保留作为证据；如需清理按下列清单精确删除：
- 知识 4 条（`b36db703…`、`76d94b15…`、`69d2e95a…`、另 1 条标题含 `fa-t7-`），状态分别为 draft/pending_review/published/disabled
- 巡检报告 2 份（reports 表 id 1、2，`task_id` 前缀 `inspection-`）
- AI Run 2 个（`ec91e79d…`、`a6001d60…`，均为只读调查，无基础设施副作用）

## 8. 结论

**未通过最终验收（G5 FAIL），其余门禁通过或带注通过。**

已达成：八页 IA（AI 智能运维默认首页、顺序正确、无禁止入口）、真实数据接入（零 Mock）、
调查链端到端（创建→派发→实体解析→6 类证据→确定性评分→诚实结论→假设审计投影→报告/UI）、
报告双链路（巡检 + AI 运维）、零孤儿能力（177 条全分类）、
G5 安全/拒答/时延类阈值（拒答率 100%、零误报、首响 ~5s、调查 ~50s）、
16 个真实环境缺陷闭环（D1-D5、D9、D13-D23，其中 S1 级 8 个）。

未达成：G5 命中类指标（Top-1/Top-3/影响范围/一致率）——依赖场景矩阵执行与
change/trace 数据源接入；G1/G3/G6 的"带注"部分（全态矩阵、兼容矩阵、WCAG
全量、回滚演练）。

按规范 §10 与完成门禁，此时**不得**输出“100% 开发和验收完成”。
剩余工作已全部文档化并可执行（见 §7 与 ai-rca/scenario-matrix.md）。

---

## 9. 阶段二汇总（T5 语义修复 / T6 报告 / T7-T8 走查 / T9 / T10）

### 9.1 本阶段闭环缺陷（8 个，全部真实环境发现）

D15（证据采集为空，四层根因）、D16（未建模对象 0 证据）、D17（知识库数据源
410）、D18（知识库路由 scope 失联）、D19（根因投影链断裂）、D20（评测时间窗
不对齐）、D21（候选子图漏 OWNS 传播边）、D22（候选深度不足）、D23（Severity
大小写映射缺失）。全记录：defects/D15-evidence-collection-empty.md。

### 9.2 调查链语义（全部实测验证）

```
Run 创建(resource_id) → outbox 派发 → 实体解析（UID/名称双路径，
not_found≠故障） → 6 类证据采集（metrics/traces/logs/alerts/changes/events）
→ entity_uid 绑定 → anomaly 校准（Warning 事件 0.6 / alert critical 1.0）
→ 传播支持证据（路径终点症状证据佐证根因候选，不计独立类别）
→ self-root 分档（直接因果+类别≥2 → confirmed；单直因 → probable）
→ 因果路径门禁（无传播解释不 confirmed） → 假设审计投影
→ Supported/Confirmed 投影 → UI/报告/评测
```

### 9.3 G5 评测资产

- `scenario-matrix.md`：40 场景（8 领域），每场景标注 ground truth/注入方法/
  信号类别数/期望行为（Top-1 或拒答）
- `run-ai-eval.mjs`：单场景评测（确定性判定、时间窗对齐、重复运行）
- `run-refusal-matrix.mjs`：拒答率并行批测（12/12 = 100%，≥95% PASS）
- `eval-target-inject.sh`：受控注入库（setup/inject/recover/status）
- `scoring-semantic-review.md`：评分语义评审提案与 D-1~D-5 判定

### 9.4 已部署版本链（时间序）

frontend: p8page → ia8 → t6 → d18 → t6-2；query-api: p8page → d17 → d16b →
t6 → d17(修复重建) → d18?（最终 v1.4.0-d17-20260914042919）→ t6 →
v1.4.0-d21 → v1.4.0-g5 时代（frontend v1.4.0-t6-20260914031930 /
query-api v1.4.0-d21 链 / orchestrator+worker v1.4.0-g5b）
以集群当前镜像为准：`kubectl -n observability get deploy -o jsonpath`。

### 9.5 测试统计

- 前端全量 287/287（74 文件）
- Go：internal/api、internal/graph、internal/store 全量通过
- RCA 引擎：25 项（test_engine 4 + d16b 2 + d1_self_root 3 + engine 相关 16）
- 新增回归文件：test_d16b_entity_unresolved.py、test_d1_self_root.py、
  test_rca_runtime_tool_registry.py（前端 navConfig 14/14 亦在 T1 建立）
