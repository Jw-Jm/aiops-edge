# AIOps 使用与实现审计（2026-08-19）

## 结论

本次审计覆盖前端路由与组件、Go 查询/采集服务、Python 编排服务、Helm 模板和当前 Orbstack 部署。代码层的核心回归已通过：query-api 全量 Go 测试、ingest 全量 Go 测试、event-collector 全量 Go 测试、Python 全量 340 项测试和前端生产构建均通过。修复后的源码已按项目流程构建并部署到本地集群；`observability` 命名空间的四个自研 Deployment 使用 `v1.1.2-dirty.20260819003611` 镜像并处于 Available。

“通过”仅表示代码/契约和本地部署状态证据，不等同于真实 LLM 已配置、所有数据源都有数据或所有页面已经完成浏览器深度复测。

## 页面、Tab、弹窗和入口清单

| 领域 | 页面/路由 | 主要 Tab、抽屉或弹窗 | 关键调用链 |
|---|---|---|---|
| 总览 | `/overview` | 健康统计、数据中断提示、集群切换 | frontend → query-api `/dashboard/stats`, `/dashboard/resources`, `/clusters` |
| 可观测 | `/observability/service` | 拓扑/服务列表、服务详情抽屉、趋势/调用链 | `/topology/global`, `/services/{name}`（重定向 canonical topology detail） |
| 可观测 | `/observability/trace` | 时间范围、服务筛选、加载更多、Trace 详情 | `/traces?hours=&service=&limit=&offset=` → ClickHouse `trace_spans` |
| 可观测 | `/observability/log` | 日志/聚合模式、source/level/时间筛选 | `/logs/query`, `/logs/aggregate` → VictoriaLogs/ClickHouse |
| 可观测 | `/observability/vms`, `/observability/grafana` | VM 详情、Grafana iframe、iframe 错误重试 | `/kubevirt/vms*`, `/grafana/*` |
| 告警 | `/alerts/events`, `/alerts/rules` | 严重度筛选、确认/解决/静默、规则新建/编辑/删除 | MySQL rules + ClickHouse alert_events；写操作受 admin/approver 门禁 |
| 智能运维 | `/ai/chat` | 会话列表、处置建议卡、批准/拒绝、执行结果、报告/加入知识库 | frontend SSE → query-api AI proxy → orchestrator DAG → CH/VM/VLogs/K8s/ChromaDB/LLM |
| 智能运维 | `/ai/tools` | NL2SQL、MCP、技能目录、Marketplace | `/ai/nl2sql/*`, `/mcp/*`, `/ai/skills/*`, `/ai/marketplace/*` |
| 智能运维 | `/ai/workflows`, `/ai/workflows/editor`, `/ai/workflows/:id` | DAG 编辑、运行、审批、运行历史/节点明细 | flow API → SQLite `flow_runs`/`flow_run_nodes` |
| 智能运维 | `/slo`, `/knowledge`, `/kg` | SLO 表单、案例导入/删除/重载、Playbook、图谱筛选 | MySQL SLO；ChromaDB RAG；Playbook 文件；知识图谱 API |
| 容量/资源 | `/capacity`, `/infra/k8s`, `/hardware` | 集群/节点范围、K8s 预检/确认执行、硬件空态 | `/capacity/*`, `/infrastructure/*`, `/k8s/preflight`, `/k8s/execute`, `/hardware/*` |
| 报告/治理 | `/report`, `/changes` | 报告预览/下载/入库、变更登记/筛选 | MinIO/报告 API；MySQL change_events |
| 管理 | `/admin/approvals`, `/admin/users`, `/admin/settings` | 审批、用户 CRUD、LLM/组件/审计设置 | MySQL、query-api `/settings/*`、orchestrator internal endpoints |

应用壳还提供登录、全局集群切换、告警角标、AiDock、通知抽屉和退出登录。未知路径现已显示专门的 404 页面，并提供返回工作台入口；回归测试位于 `observability-frontend/tests/unknown-route.test.mjs`。

## 证据与数据源反查

| 数据源/边界 | 证据 | 当前判断 |
|---|---|---|
| ClickHouse | query-api/ingest Go 测试通过；Trace、日志、拓扑、告警事件查询代码均落到 `observability.*` 表 | 已接入；无运行时数据时必须展示“无数据”，不能当作成功采集 |
| VictoriaMetrics | Dashboard、节点指标、容量接口和 VM PromQL 代码存在；容量页面已区分 cluster/node scope | 已接入；指标口径仍需部署数据回归确认 |
| VictoriaLogs | `/logs/query`/聚合接口和 LogMetrics 页面已接线 | 已接入；错误/无数据 UI 仍需浏览器复测 |
| MySQL | 用户、集群、规则、SLO、审计、变更、服务元数据 DAO；query-api 测试通过 | 已接入；不可达时应保持错误语义而非伪造列表 |
| Kubernetes API/metrics-server | 当前 Orbstack 中 metrics-server 2/2 Running；K8s actions 有预检 token、resourceVersion 和确认弹窗 | 已部署；集群事件、非默认集群 kubeconfig 和实际写操作尚未在本轮执行 |
| ChromaDB/RAG | `test_checkpointer`、marketplace、frontmatter 及 RAG 路由测试通过；显式知识库请求走 `query_knowledge` | 已接入；真实案例数量/相似度依赖运行时数据 |
| LLM | `orchestrator.py` 有服务端配置和降级路径；测试验证未配置时不伪造 LLM 证据 | 不能宣称真实 LLM 可用；需在设置页重新配置真实 key 后再做真实对话复测 |
| K8sGPT | 显式 `k8sgpt` 关键词优先路由、工具调用事件和错误结果测试通过 | 代码契约已具备；集群内二进制/权限/扫描输出仍需运行时证据 |
| DeepFlow | DeepFlow namespace 当前 agent/server/app/CH/Grafana Running；同步链路存在 | 已部署；DeepFlow 数据新鲜度和噪声过滤需专项浏览器/数据查询复测 |
| MinIO/报告 | 报告 API/前端预览下载链路存在 | 本轮未下载真实报告产物 |
| Redis | 代码已改为真实 TCP 探测；部署清单未发现独立 Redis Pod（当前版本按降级/内存语义处理） | 需避免 UI 把未部署组件显示为 connected |

## 已验证的修复/回归

- query-api：`go test ./...` 通过；重点回归包括告警规则 PUT、Trace `hours` 起始时间条件、节点指标 `cluster_id` 过滤。
- ingest：`go test ./...` 通过；WAL ack、writer 和指标测试通过。
- event-collector：`go test ./...` 通过。
- Python：Python 3.12 venv 全量 `tests` 为 340 passed；其中会话重载保留 suggestion/execresult 字段、Marketplace 转发 `source_type`、Playbook frontmatter 独立解析均有回归覆盖。
- 前端：`npm run build`（tsc + Vite）通过。
- Helm：`helm lint deploy/helm/aiops` 通过；默认无 Secret 值的 `helm template` 按设计拒绝，避免占位符部署；使用仅用于渲染验证的临时值后成功渲染。
- 运行时：`apply.sh` 已完成构建与 Helm 升级（AIOps revision 23、DeepFlow revision 8）；observability 的 query-api、orchestrator、frontend、ingest 均为 `v1.1.2-dirty.20260819003611`，Running/Available；本地入口 HTTP 200，未知路由实测显示 404 页面；公开 `/health` 返回 `200 ok`。部署过程复用了既有 Secret，未读取或打印密钥。

## 仍需处理或接受的缺口

### P0/P1 风险

1. LLM 运行时密文历史密钥无法从代码恢复；必须由管理员在系统设置重新录入真实 API key，并验证保存后回读、SSE `notice`、真实 provider 响应和审计记录。
2. 真实 K8sGPT 扫描、RAG 命中和多 Agent 轨迹必须使用运行时 `tool_start/tool_end` 与真实返回证据验收；测试桩不能作为运行时成功证据。
3. 工作流运行历史代码已持久化到 `flow_runs`/`flow_run_nodes`，但需在浏览器完成运行→审批→终态→历史抽屉的真实链路复测。

### 真实 LLM 只读实测（2026-08-19）

已获授权发送问题：要求使用 K8sGPT 只读诊断当前集群，并检索 OOMKilled 处置手册。SSE/UI 工具轨迹依次显示 `k8sgpt_diagnose`、RCA、RAG 案例匹配和 CrewAI 分析“已完成”。但最终回答暴露出两个 P1 问题：

- K8sGPT 工具结果实际为“未发现名为 k8sgpt 的服务或工具入口”，随后报告仍出现“未发现集群问题”，语义上把“工具不可用”误导成“集群健康”。
- RAG 步骤显示“已完成/无相似案例”，正文却称“当前未提供知识库检索接口”，并使用通用 OOMKilled 手册；这不是可追溯的知识库命中证据。
- K8s 采集结果为运行中 Pod 0、节点 0，并提示 `endpoints` Forbidden；与页面实际运行的 Pod、告警事件和服务数据矛盾，说明 K8s 采集权限/错误状态应显式阻断健康结论。

因此本次真实 LLM 场景判定为 **不通过（P1）**。修复要求：工具不可用时返回 `unavailable` 而不是 `success`；RAG 未检索到结果时不得显示“已完成”，最终报告必须标注数据源、权限错误和不确定性，禁止生成“未发现问题”的结论。

### P2/P3 可用性与数据质量

- 已修复未知路由静默 fallback：`NotFound` 页面会明确提示地址不存在，并可返回工作台；静态回归测试与生产构建均通过。
- 若无 K8s、VictoriaMetrics、VictoriaLogs、DeepFlow、SNMP/IPMI 数据，页面需显式区分“无数据/未配置/不可达/未安装”；禁止用 mock 列表填充成功态。
- 变更、知识库、服务详情、容量、图谱、审计和固定宽度 Drawer 仍应在窄屏和 API 失败场景做浏览器复测；已有前端 `catch` 仍有静默降级点，不能把它们报告成“无数据”。
- 真实部署发现 MySQL 有重启记录（当前 Pod Running），应在后续运维窗口核对原因和事件；本轮没有执行重启或修改。

## 建议的下一轮验收顺序

1. 由管理员重新配置 LLM key；记录配置状态、provider、SSE notice/tool 事件，不记录密钥。
2. 用真实数据执行六个场景：服务错误根因、明确 K8sGPT、知识库检索、纯信息查询、NL2SQL 近 1 小时、处置审批执行。
3. Playwright 逐路由点击所有 Tab/Drawer/Modal，保存 API/console/截图证据；重点复测上述 P2 缺口。
4. 使用既有 `deploy/scripts/build-images.sh` + `apply.sh`（仅在管理员确认并提供非占位密钥后），核对 Deployment image、Pod rollout、`/health`、版本标签和回滚路径。

## 历史测试与修复报告

- [2026-08-18 修复方案](</Users/mssc/Documents/Code/agent/aiops/AIOPS_FIX_PLAN_2026-08-18.md>)：A/F 系列数据正确性、AI 路由、分页、SLO、工作流和 UI 修复方案。
- [2026-08-18 第二轮测试报告](</Users/mssc/Documents/Code/agent/aiops/AIOPS_TEST_REPORT_R2_2026-08-18.md>)：认证授权、采集链路、可靠性和部署安全风险。
- [2026-08-18 第一轮测试报告](</Users/mssc/Documents/Code/agent/aiops/AIOPS_TEST_REPORT_2026-08-18.md>)：核心页面、API 和闭环测试基线。
