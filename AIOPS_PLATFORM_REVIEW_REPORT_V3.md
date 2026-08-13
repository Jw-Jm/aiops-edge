# AIOps 智能可观测平台 · 全新深度评测报告（V3）

> 评测时间：2026-08-13 10:46 – 11:20（CST）
> 评测方式：真实浏览器黑盒走查（全部 13 个页面）+ 真实数据注入验证故障发现闭环 + 真实 LLM（DeepSeek）自然语言对话 + 四数据源只读核验 + 代码根因定位
> 评测账号：admin/admin123 ｜ 平台地址：http://localhost:30253
> 证据截图：`gui-test-screenshots/fresh-20260813/`（41 张，t01~t45 编号，部分编号被覆盖保存）

---

## 0. 执行摘要

平台整体可用、可演示，实时数据链路（DeepFlow → ingest → ClickHouse/VictoriaMetrics/VictoriaLogs）畅通且新鲜，AICHAT 真实 LLM 闭环、告警触发→自动恢复→UI 展示→删除的故障处理链路完整可用。但在**安全、数据口径、告警分级、日志页交互**四个方面存在必须优先修复的问题。

| 级别 | 数量 | 摘要 |
|---|---|---|
| P0 | 3 | ① LLM API Key 明文落库（安全）② 日志页交互整体失效（聚合统计切换无效+Select 无法操作+下拉面板残留锁页）③ 告警严重度字段丢失（critical 显示为"警告"） |
| P1 | 12 | 数据口径不一致、AI 告警状态误判、RCA 命令占位符、规则阈值误报、时区不一致、埋点缺失、日志无业务价值等 |
| P2 | 14 | 时间格式、报告标题脏乱、命令面板遮罩、术语不一致等 |
| P3 | 8 | 小瑕疵 |

---

## 1. 评测方法与覆盖范围

### 1.1 方法
- **黑盒 GUI 走查**：真实点击/输入/导航/快捷键（⌘K），每个页面 DOM 快照 + 截图双证据（截图保存在 `gui-test-screenshots/fresh-20260813/`，本环境模型无法内联渲染图片，视觉结论基于 A11y 树 + DOM 几何 + 文本证据，截图留待人工复核）
- **数据注入**：经 ingest 服务（`POST /v1/traces`、`POST /v1/logs`）注入故障服务 `demo-fault-svc` 的错误 Span + ERROR 日志，验证"发现→告警→恢复→删除"全链路
- **真实 LLM**：系统配置为 DeepSeek `deepseek-v4-flash`（https://api.deepseek.com/v1），AICHAT 全部回答为真实 LLM 推理结果
- **数据源只读核验**：ClickHouse / MySQL / VictoriaMetrics / VictoriaLogs / Redis / MinIO
- **代码交叉定位**：问题根因回落到具体文件行号

### 1.2 覆盖范围
前端 13 页全部覆盖：工作台首页、服务全景（拓扑+列表+详情）、链路追踪、日志与指标、告警事件、告警规则、AI 对话、AI 工具（NL2SQL/MCP/技能）、容量预测、报告中心、用户管理、系统设置（3 个 tab）、登录页；全局功能：集群切换、⌘K 命令面板、AI 浮窗（部分受阻）、通知（未测完）。

---

## 2. 功能走查总览

| 页面 | 结论 | 关键证据 |
|---|---|---|
| 登录 | ✅ 正常 | 营销面板+登录表单，演示账号提示；登录成功跳 /overview |
| 工作台首页 | ⚠️ 可用但口径问题 | KPI：服务 16 个/调用 304,314 次/错误率 0.01%/P95 33.1ms/健康度 100/待办 0；"告警按服务分布"空态 |
| 服务全景 | ⚠️ 列表/拓扑/详情口径打架 | 拓扑 16 节点·43 关系（canvas）；列表 12 行；详情健康度"--未知"、上下游 0/0、调用链空 |
| 链路追踪 | ✅ 基本可用 | 搜索/空态/瀑布图正常；列表 Spans=2 与详情 Spans=4 不一致；时间显示纳秒 UTC |
| 日志与指标 | ❌ 交互失效 | "聚合统计"切换无效；数据源/级别/时间 Select 无法选择；日志全为平台内部 SQL 查询记录；URL 编码+UTC 时间不可读 |
| 告警事件 | ⚠️ 功能闭环✅ 数据有 bug | 当前/历史/全部分级正常；RCA 分析✅；删除✅；严重度 critical 显示"警告" |
| 告警规则 | ⚠️ 仅 threshold 型 | 6 条内置 K8s 规则；新建表单只有 > 条件；Deployment 不可用阈值 0 必然误报 |
| AI 对话 | ✅ 核心闭环可用 | 真实 LLM 流式；工具调用+报告+处置建议+审批流；但存在活跃告警误判/上下文污染 |
| AI 工具 | ✅ NL2SQL 好用 | 翻译/执行/复制闭环；MCP 8 工具、技能 8 项展示 |
| 容量预测 | ✅ 数据真实 | CPU 14.17%/内存 83.48%/磁盘 40.04%；预测"窗口内不触达" |
| 报告中心 | ✅ 闭环 | AICHAT 自动生成报告入库；预览/下载正常；标题脏乱 |
| 用户管理 | ⚠️ 可用 | 删除 admin 无确认无反馈；角色术语不一致 |
| 系统设置 | ✅ 可用 | DeepSeek 配置/脱敏/测试；纳管集群 API Server 列空；审计日志可用 |

---

## 3. P0 问题（必须立即修复）

### P0-1　LLM API Key 明文存储于 MySQL
- **现象**：`platform_settings` 表 llm_settings 字段以明文 JSON 保存 API Key（实测为 deepseek 密钥原文）；前端虽已脱敏显示（`T7oO********JxJ+`），但数据库可读原文。
- **根因**：`ai-apm-query-go/internal/api/settings.go` 保存 llm_settings 时未加密（settings.go:268-330，前端声称"保存时先验证 API Key 可用"，但存储未加密）。
- **影响**：数据库泄露 = 供应商账号密钥泄露；多租户/审计合规风险；与"AES 加密"的产品文案不符。
- **修复方案**：写入时用平台主密钥（如 JWT_SECRET 派生）AES-GCM 加密，读取时解密；存量数据迁移；`/api/v1/settings/llm/internal`（明文下发）增加访问审计。
- **涉及文件**：`ai-apm-query-go/internal/api/settings.go`、`ai-apm-query-go/internal/store/*`、`docs/SCHEMA_OWNERSHIP.md`

### P0-2　日志与指标页交互整体失效（tab 切换无效 + Select 无法操作 + 残留面板锁页）
- **现象**：
  1. 点击"聚合统计"（Segmented 文本与 radio 两种方式、共 4 次尝试）页面无任何变化，radio 始终停在"日志检索"；
  2. 数据源/级别/时间三个 antd Select 下拉：选项在 A11y 树可见但 Playwright 全部判定 hidden，点击超时；唯一一次打开 listbox 后，面板残留于 DOM 并遮挡整页——后续导航点击全部被挡（报 `covered by`），reload 后仍残留，只能靠 ⌘K 面板导航脱困；
  3. 页面顶栏"过滤探针"、查询按钮可正常点击（功能本身工作，探针过滤 toggle 生效）。
- **根因**：`observability-frontend/src/pages/observability/LogMetrics.tsx:85` 的 Segmented `onChange` 绑定与渲染状态存在缺陷（GUI 层点击事件未触发状态切换）；Select 下拉面板（body 层定位）在渲染尺寸/层级异常时形成全页隐形遮挡（实测截图 t13-t18 显示面板残留）。该页与"服务全景"页同为 Segmented 组件但行为不一致，说明非通用组件问题而是本页特有缺陷。
- **影响**：日志模块 2 个核心能力（聚合统计、跨数据源切换）实际不可用，且一旦打开下拉，整个页面被锁死只能刷新/⌘K——这是演示级 P0。
- **修复方案**：重构 LogMetrics.tsx 的 Segmented/Select 状态管理（改用受控组件+显式 onClick）；Select 下拉改为 `getPopupContainer={() => document.body}` 时确保 `position: fixed` 与 z-index 正确；增加下拉关闭时销毁面板（`destroyOnClose`）；回归测试覆盖"切换聚合统计→查询→切回"。
- **涉及文件**：`observability-frontend/src/pages/observability/LogMetrics.tsx`

### P0-3　告警严重度字段丢失，critical 告警显示为"警告"
- **现象**：历史告警里两条规则（k8s-deployment-unavailable=critical、我创建的 demo 错误率规则=critical）在 UI 全部显示"警告"；API `GET /api/v1/alerts/events` 返回 `severity: ""`；ClickHouse `alert_events` 表 severity 字段 3 行全空。
- **根因**：触发链路未把 `rule.Severity` 写入事件。`ai-apm-query-go/internal/api/alert_engine.go` 评估时使用 `rule.Severity`（日志打印正确），但 `saveAlertEvents()`（alerts.go:438-455）写入 CH 时 severity 变量为空；前端 `AlertEvents.tsx:35` 兜底逻辑 `e.severity || ... || 'warning'` 把空值降级为"警告"——双重缺陷叠加。
- **影响**：告警分级（严重/警告/信息）是运维处置优先级的第一依据，当前全部告警被错误降级为 warning，P0 级"严重"告警无法区分；首页"严重 0/警告 0/信息 0"统计也随之失真。
- **修复方案**：`alert_engine.go` 触发路径将 `rule.Severity` 显式传入 `saveAlertEvents`；写入后 CH 全量回填历史数据（`ALTER TABLE ... UPDATE severity=...`）；前端 `AlertEvents.tsx:35` 删除静默兜底，改为显示"未知"并告警日志。
- **涉及文件**：`ai-apm-query-go/internal/api/alert_engine.go`、`ai-apm-query-go/internal/api/alerts.go:224,438`、`observability-frontend/src/pages/alerts/AlertEvents.tsx:35`

---

## 4. P1 问题（功能受损 / 明显缺陷）

### P1-1　服务数量口径不一致（拓扑 16 / 列表 12 / NL2SQL 13 / 首页 16）
- **现象**：同一时刻：拓扑视图"16 节点·43 关系"；服务列表 12 行（单页）；NL2SQL 执行 `GROUP BY service_name` 返回 13 行；首页 KPI"服务数量 16 个"；MySQL topology_nodes 13 个节点。
- **根因**：四个数据来源/时间窗口/过滤条件各自为政：拓扑（实时 trace 聚合+动态边）、列表（`handler.go:330` 的 ListServices，1440 分钟窗口）、NL2SQL（24h 窗口）、首页（DashboardStats）、MySQL（静态拓扑 4 天未更新）。
- **影响**：用户无法信任任何一个数字；演示时不同页面数字互斥，产品可信度受损。
- **修复**：统一"服务"定义与统计 SQL（单一口径：trace_spans 近 N 小时 DISTINCT service_name），拓扑/列表/首页/详情共用；静态拓扑表改为实时聚合+缓存。
- **涉及文件**：`ai-apm-query-go/internal/api/handler.go:330,811,983`、`observability-frontend/src/pages/Overview/index.tsx`、`ServiceObservability.tsx`

### P1-2　服务详情面板：健康度"-- 未知"、上下游调用 0/0、调用链空
- **现象**：点击任意服务（query-api、frontend 等）详情：健康度显示"-- 未知"（有 39K/10K 调用且 0 错误，理应可算）；"调用（下游 0）/被调用（上游 0）"——拓扑明明有 43 条关系；"调用链/Span 明细"均"暂无数据"。
- **根因**：详情接口（`handler.go:501 ServiceDetail`）的健康度依赖 Apdex 指标（VM 未采集 apdex 或取值字段不匹配）；上下游关系查询条件与拓扑构建逻辑不一致（TopologyNodeDetail handler.go:1247 与实时边数据未打通）；调用链查询时间窗/条件过严。
- **影响**：详情页是拓扑/列表的落地页，核心信息全部缺失，等于"只有外壳"。
- **修复**：健康度改为"可用数据时必算"（错误率即可算部分），Apdex 缺失时降级展示成分项；上下游查询复用拓扑实时边聚合；调用链按服务名+近 1h 放宽查询并验证。
- **涉及文件**：`ai-apm-query-go/internal/api/handler.go:501,1247`、`observability-frontend/src/pages/observability/ServiceObservability.tsx`

### P1-3　Trace 列表"Spans 2"与详情"Spans 4"不一致
- **现象**：链路追踪列表某 trace 行"服务数 2 / Spans 2"，点击详情后标题显示"Spans 4"且瀑布渲染 3 条 span。
- **根因**：列表聚合（ListTraces handler.go:542）与详情（TraceRouter handler.go:602）统计口径不同（列表可能按 parent 数量或去重口径）。
- **影响**：数字自相矛盾，误导排查。
- **修复**：统一 `count()` 口径，详情与列表一致。
- **涉及文件**：`ai-apm-query-go/internal/api/handler.go:542-652`、`observability-frontend/src/pages/observability/Trace.tsx`

### P1-4　AICHAT 把已恢复（resolved）告警报告为"活跃告警"
- **现象**：问题"当前集群有哪些服务？最近有没有异常或告警？"，AI 回答"当前存在 1 条活跃告警：Deployment 不可用（最近触发 02:36:19）"——该告警 3 个版本（firing→resolved），当前态已 resolved（告警事件页"当前告警 0"）。
- **根因**：`ai-orchestrator/orchestrator.py` collect 节点 `_collect_alerts`（L355）查询告警时未过滤 status（含 resolved），LLM 直接采信。
- **影响**：AI 结论与事实相悖，运维按"活跃告警"投入处置会扑空；这是 LLM 输出失真的高危信号。
- **修复**：`_collect_alerts` 增加 `status=firing`（或 active）过滤 + 返回 active_count/resolved_count；summarize 提示词要求区分状态。
- **涉及文件**：`ai-orchestrator/orchestrator.py:355`、`ai-orchestrator/main.py`

### P1-5　RCA 处置命令占位符未替换 + 上下文污染
- **现象**：RCA 处置方案含 `kubectl describe pod <异常Pod> -n observability`（占位符未替换）；另一次对话中处置命令混入上轮 redis 的命令（`kubectl describe pod redis-...` 出现在 no-such-svc 的分析里）。
- **根因**：处置方案生成时未做参数回填（LLM 输出模板占位符）；会话上下文（state dict）携带上轮工具结果未清理。
- **影响**：复制命令直接执行会失败或误操作其他对象；上下文污染使 AI 建议失去针对性。
- **修复**：plan 节点输出结构化命令参数（对象名/namespace 从实体抽取结果回填，缺失时提示"无法确定对象"而非输出占位符）；每轮对话隔离工具上下文。
- **涉及文件**：`ai-orchestrator/orchestrator.py`（plan/risk 节点 L833-896）、`ai-orchestrator/tools.py`

### P1-6　k8s-deployment-unavailable 规则阈值 0 → 滚动更新必然误报
- **现象**：规则 `unavailable_replicas > 0`（threshold=0, critical）；实测 query-api 一次滚动更新（Scaled up from 0→1，22 分钟后 1/1 Running）即触发告警；规则详情标注"持续时间 5 分钟"，但瞬时（<1 分钟）变化仍触发，持续时间未生效。
- **根因**：`K8sDefaultRules()`（alerts.go:1871-1878）阈值 0 未考虑滚动更新瞬时窗口；evalK8s* 求值未应用 duration 持续判断（或采样间隔使其失效）。
- **影响**：任何 deploy 操作都产生 critical 告警噪音，告警疲劳使真实故障被淹没（本次 demo 的 2 条历史告警 1 条即为此类）。
- **修复**：阈值改为 `>=1` 且持续 N 分钟（duration 真正生效）；或排除 available=0 但 Replicas 变化中的状态；增加滚动更新模式识别。
- **涉及文件**：`ai-apm-query-go/internal/api/alerts.go:1871-1878,1416-1590`、`alert_engine.go`

### P1-7　ClickHouse time_bucket 时区不一致（logs CST vs spans UTC）
- **现象**：`log_records.time_bucket` 最大值为 10:46（CST），`log_records.timestamp` 最大 02:46（UTC）；`trace_spans.time_bucket` 为 UTC——同一数据库两张表桶口径相差 8 小时，按 bucket 聚合日志会错排"未来 8 小时"。
- **根因**：`ai-apm-ingest-go` 日志写入（log_writer.go）与 span 写入（ingest.go）分别用不同时区生成 time_bucket。
- **影响**：日志趋势/聚合统计（logs/aggregate 的 trend 桶）数据错位；跨表 JOIN 桶对齐失败。
- **修复**：统一使用 UTC 生成 time_bucket；存量数据重算。
- **涉及文件**：`ai-apm-ingest-go/internal/pipeline/log_writer.go`、`ai-apm-ingest-go/internal/pipeline/ingest.go`

### P1-8　VictoriaMetrics 计数器埋点缺失（spans_received/requests 恒为 0）
- **现象**：`ai_ingest_spans_received_total`、`ai_ingest_requests_total` 近 1 小时恒为 0，而 `ai_ingest_spans_written_total` 正常增长（835k→844k/4min）。
- **根因**：`ai-apm-ingest-go/internal/metrics/metrics.go` 未在接收/请求路径递增对应计数器。
- **影响**：入口丢量率（received vs written）无法监控——观测平台自己的可观测性缺失，收数中断无法被发现。
- **修复**：在 HTTP handler 与 OTLP 解析入口补埋点；补充 received/written 差值告警。
- **涉及文件**：`ai-apm-ingest-go/internal/metrics/metrics.go:103-142`、`ai-apm-ingest-go/cmd/ingest/main.go`

### P1-9　ClickHouse service_topology 陈旧且数据逻辑错误
- **现象**：service_topology 1,685 行，14.5 小时无更新；`avg_duration_ns` 全部为 0；20 行 `error_count > call_count`；总 error/call 比 72%。
- **根因**：拓扑表由独立同步任务维护（已停/失败）；时长聚合未实现；错误计数口径错误。
- **影响**：容量/链路/拓扑相关聚合统计失真（error>call 直接违反事实逻辑）。
- **修复**：检查拓扑同步任务是否运行（`POST /api/v1/topology/sync` 链路）；补 avg_duration 计算；修正 error_count 聚合口径；对脏数据清洗。
- **涉及文件**：`ai-apm-query-go/internal/api/topology_graph.go:318`、`ai-apm-query-go/internal/api/handler.go:236`

### P1-10　日志内容无可运维价值 + 不可读（URL 编码、UTC、平台自日志）
- **现象**：日志检索默认结果全为平台内部调用日志（`query-api -> clickhouse /?query=SELECT... [200] 87ms`，URL 编码未解码、全文一长串无换行）；时间列显示 `2026-08-13 02:49:12.101833000`（9 位纳秒+UTC）；近 24h 仅 8 条 ERROR（242,794 条 INFO）——没有一条业务/应用错误日志。
- **根因**：当前采集面只有 ingest 自产调用日志（StartLogShipper 只同步 pod 日志到 VictoriaLogs，ClickHouse log_records 由 OTLP 写入但无业务接入）；前端未解码、未本地化时间、未默认过滤探针。
- **影响**：运维用户无法用日志模块排查任何业务问题；错误日志缺失使"日志驱动告警"无从谈起。
- **修复**：默认勾选"过滤探针"；消息列解码+自动换行+语法高亮；时间本地化（HH:mm:ss）；接入真实业务日志（k8s pod 日志进 CH 或默认数据源切 VictoriaLogs 并建 stream 标签）。
- **涉及文件**：`observability-frontend/src/pages/observability/LogMetrics.tsx`、`ai-apm-query-go/internal/api/handler.go:1424`

### P1-11　VictoriaLogs 日志无任何标签（单空 stream）+ 消息内时区混乱
- **现象**：620,306 条日志全部落在唯一 stream `{}`；`_msg` 内时间戳 UTC/CST 混存。
- **根因**：StartLogShipper（data_sync.go:15-40）写入时未带 stream 标签（namespace/app/pod/level）。
- **影响**：无法按维度高效检索分组；未来体量上来后查询性能与成本失控。
- **修复**：写入时设置 `_stream` 标签（namespace/service/phase 已有字段直接复用）；同字段双时区清洗。
- **涉及文件**：`ai-apm-query-go/internal/api/data_sync.go`

### P1-12　MySQL 骨架 2/3 空表 + 拓扑重复边 + 4 天未更新
- **现象**：31 张表约 2/3 为空（service_catalog、devices、ipmi_*、slo_targets 等 0 行）；topology_nodes 13 节点创建于 08-09 后再无更新；topology_relations 13 条边含 3 对双向重复。
- **根因**：功能骨架已建表未接线；拓扑同步只跑过一次；边表写入未去重。
- **影响**：服务目录/设备/IPMI/SLO 等页面（若上线）无数据；静态拓扑与实时数据脱节。
- **修复**：按产品路线决定空表去留（或标注"未启用"）；拓扑同步定时化；边写入 upsert 去重。
- **涉及文件**：`ai-apm-query-go/internal/store/mysql.go`、`ai-apm-query-go/internal/api/topology_graph.go`

---

## 5. P2 问题（体验/展示/产品打磨）

| # | 现象 | 涉及文件 | 建议 |
|---|---|---|---|
| P2-1 | 全站时间显示 UTC+纳秒（`2026-08-13T02:36:19Z`、`02:49:12.101833000`），未本地化 | 各页面时间列 | 统一本地时区 + 相对时间（x 分钟前） |
| P2-2 | 报告标题脏乱（"巡检报告 1."、"巡检报告 -"），由用户问题截断生成 | `ai-orchestrator/main.py` final_report | 标题生成规则：意图+对象+时间；空标题给默认值 |
| P2-3 | AICHAT 报告"目标"字段为空（`**目标**: \|`） | orchestrator summarize 模板 | 模板校验，空字段不输出 |
| P2-4 | AICHAT 把累计重启次数当"持续崩溃"（redis 15 次标 🔴 极高，未区分时间窗） | orchestrator collect 节点 | 提供"近 1h/24h 重启次数"窗口化指标再让 LLM 判断 |
| P2-5 | 首页"告警按服务分布"永远空态（无告警分布数据时无引导） | `Overview/index.tsx` | 空态给"创建告警规则/查看告警"引导按钮 |
| P2-6 | 命令面板（⌘K）：遮罩点击不可关闭（backdrop 无点击点）；搜索框点击不打开面板；文案"搜索页面、资源、告警…"与实际（仅页面）不符；reload 后面板残留 | `CommandPalette.tsx` | backdrop 可点击关闭；搜索框聚焦打开；文案改"搜索页面"；状态清理 |
| P2-7 | 审计日志操作人恒为"user"（无法区分真实用户） | orchestrator audit 记录 | 透传 JWT 用户名 |
| P2-8 | 纳管集群表"API Server"列为空 | `AdminSettings.tsx` | 后端 clusters 表补 api_server 字段 |
| P2-9 | 用户管理：删除 admin 无确认框、无任何反馈（后端拒绝静默）；"普通用户"（表单）与"普通成员"（列表）术语不一致 | `AdminUsers.tsx`、users.go | 删除高危对象加确认+错误 toast；统一角色术语 |
| P2-10 | 日志页表头多一个无名空列；"来源"列恒为 ClickHouse（语义=存储源，非日志来源） | `LogMetrics.tsx` | 列定义修正；来源列改显示 service/namespace |
| P2-11 | AI 工具页 MCP/技能仅目录展示，无"执行/调用"入口 | `AiTools.tsx` | 技能卡片加"试运行"入口 |
| P2-12 | 告警规则新建表单仅 threshold+`>`，后端支持的 anomaly/burn_rate/forecast 无入口；服务/指标为自由文本无联动 | `AlertRules.tsx` | 规则类型分段；指标下拉取自真实指标列表 |
| P2-13 | 日志页 tooltip 提示存在但 Select 无法操作（见 P0-2） | LogMetrics.tsx | 随 P0-2 一并修复 |
| P2-14 | 告警"触发时间"列无相对时间与状态标签（firing/resolved 不可见，需点历史 tab 才懂） | `AlertEvents.tsx` | 列表加状态标签+相对时间 |

---

## 6. P3 问题（小瑕疵）

1. 登录页版权"© 2026 演示环境"年份硬编码。
2. 首页 KPI"▲ 0.01%"错误率上升指示与"0.01%"同值同色，无法判断方向好坏。
3. 容量预测"预计触达阈值：预测窗口内不触达"文案可读性差（改为"24h 内安全"）。
4. 审计日志 tabpanel 出现异常文本 `* * * *`（疑似 cron/字段渲染泄漏）。
5. 服务列表分页控件在单页时仍显示"1"（无禁用态视觉区分不足）。
6. 新建规则"持续时间(分钟)"默认 5 但内置 K8s 规则未展示该字段。
7. AICHAT 输入框快捷按钮（分析根因/巡检/延迟排查）与 AI 对话页重复，建议首页保留、详情页统一。
8. 集群切换后各页无"当前集群"视觉强调（仅顶栏文本变化）。

---

## 7. 数据源核验（只读，2026-08-13 02:50 UTC）

| 数据源 | 状态 | 关键数据 | 问题 |
|---|---|---|---|
| ClickHouse | ✅ 实时 | trace_spans 304,314（近1h 61,399）；log_records 239,325（近1h 30,786）；alert_events 3（severity 空） | time_bucket 时区不一致；k8s_namespace/pod 100% 空；service_topology 陈旧/duration 全 0/error>call |
| MySQL | ⚠️ 骨架 | 31 表 2/3 空；alert_rules 6；users 3；reports 54；audit_logs 54 | LLM Key 明文（P0-1）；拓扑 4 天未更新；3 对重复边 |
| VictoriaMetrics | ✅ 8s 延迟 | 323 指标/3,152 序列；采集正常 | spans_received/requests 恒 0（P1-8） |
| VictoriaLogs | ✅ 4.3 条/秒 | 620,306 条 | 单空 stream 无标签（P1-11）；消息双时区 |
| Redis | ⚠️ | dbsize=0 | 无消费方（见死代码分析：tasks.py 删除后 Redis 可下线） |
| MinIO | ✅ | ops-reports 71 对象 | 含 test-script-card 测试残留 |

**数据质量结论**：实时写入链路（deepflow→ingest→CH/VM/VL）健康；风险集中在元数据侧（MySQL 骨架、静态拓扑、时区、埋点）。

---

## 8. 故障发现→处理链路实测（数据注入）

**注入**（`POST /v1/traces` ×5 + `POST /v1/logs`）：服务 `demo-fault-svc`，HTTP 500 错误 Span（父子两条，is_error=2/2）+ ERROR 日志"checkout failed - mysql connection timeout"。

| 步骤 | 结果 | 证据 |
|---|---|---|
| 数据落库 | ✅ trace_spans 2 行（ReplacingMergeTree 幂等去重，均 is_error）；log_records 1 条 ERROR | CH 查询 |
| 创建规则（error_rate > 50, critical） | ✅ API 返回 id 16f59d197e8c78fd | API |
| 告警引擎评估（60s 周期） | ✅ ~1 分钟内触发：`error_rate 100.00 > threshold 50.00` | API events |
| 自动恢复 | ✅ 下一轮评估错误率回落 → status=resolved（无需人工） | API events |
| UI 展示 | ✅ 历史告警可见"demo 服务错误率过高 / demo-fault-svc / 2026-08-13T03:11:20Z" | 截图 t43 |
| RCA 分析 | ✅ 按钮可用（对已 resolved 告警也可分析） | 截图 t21 |
| 删除 | ✅ 物理删除，列表即时刷新（无确认框） | 截图 t44 |

**结论**：故障发现→告警→恢复→展示→处置的引擎闭环**完整可用**；但默认只有 6 条 K8s 规则，应用层错误率/延迟/日志类规则需用户自建（产品应预置 demo 规则集）。注入实验产物（demo-fault-svc 数据与规则）已清理/可删除。

---

## 9. AICHAT 深度评估（真实 LLM：DeepSeek deepseek-v4-flash）

### 9.1 交互与工作流（✅ 合理）
- SSE 流式输出 → Markdown 诊断报告 → 处置建议卡片（命令+风险评分+确认执行/驳回/自定义命令）→ 最终报告 → 报告中心入库：全链路闭环，**符合"对话式运维"产品预期**。
- 驳回交互正常（"⛔ 已驳回该处置建议，未执行。"）。
- NL2SQL 工具独立可用（翻译/执行/复制，SQL 正确）。
- MCP 工具 8 个、技能 8 项与文档一致。

### 9.2 内容准确性（⚠️ 3 个失真点）
1. **活跃告警误判**（P1-4）：resolved 告警被报为"活跃"。
2. **风险夸大**（P2-4）：累计重启 15 次被标"🔴 极高，持续崩溃"，未看时间窗。
3. **上下文污染**（P1-5）：新问题混入上轮处置命令。

### 9.3 幻觉防护（✅ 及格）
- 询问不存在的服务 `no-such-svc-xyz`：AI 明确"没有采集到该服务的任何 RED 指标、调用链、日志或工作负载信息"，未编造数字——**entity 校验生效**；但处置建议仍生成（含验证命令，可接受，需加强"无数据时不给处置"）。

### 9.4 改进空间
- 诊断报告"目标"字段空；处置命令占位符未回填；报告标题脏乱；审计操作人不区分用户；建议增加"数据置信度/证据引用"标注（引用哪条指标/日志）。

---

## 10. 工作台首页重新设计方案（产品视角）

### 10.1 设计原则
首页 = 当前选定集群的"值班台"：30 秒内回答 4 个问题——①现在有事吗？②哪里最危险？③最近发生了什么？④我该做什么？**所有信息必须有真实数据源**（本平台能力已具备），空态给引导。

### 10.2 页面布局（自上而下，左窄右宽）

```
┌────────────┬──────────────────────────────────────────────┐
│ 集群态势    │ 服务健康 TOP 风险（错误率/延迟 P95 双榜）        │
│ ┌────────┐ │ ┌──────────────────────────────────────────┐ │
│ │ 当前告警 │ │  服务名 | 错误率 | P95 | 调用量 | 趋势↑↓     │ │
│ │ 严重 1   │ │  ...（TOP 8，红色优先，点击跳服务详情）      │ │
│ │ 警告 0   │ ├──────────────────────────────────────────┤ │
│ │ 信息 0   │ │ 容量预警（CPU/内存/磁盘 3 个迷你条 + 预测    │ │
│ │ ┌────┐  │ │  触达天数"约 N 天"）                        │ │
│ │ │健康度│ │ └──────────────────────────────────────────┘ │
│ │ │ 92  │ │                                                │
│ │ └────┘  │ ┌─────────────────────┬────────────────────┐  │
│ │ 待办审批 │ │ 拓扑缩略图（可点击）  │ 最近 24h 调用/错误  │  │
│ │ 2 项     │ │ （复用 topology API）│ 趋势双折线          │  │
│ └────────┘ │ └─────────────────────┴────────────────────┘  │
│ AI 快问快答 │ 最近 AI 诊断/巡检报告（3 条，点击预览）          │
│ (保留现状)  │ 最近告警时间线（状态/对象/时长）                  │
└────────────┴──────────────────────────────────────────────┘
```

### 10.3 具体信息与数据源映射（全部基于现有 API）
| 区块 | 展示内容 | 数据源（现有） |
|---|---|---|
| 集群态势 | 集群名/版本/节点数 + 当前告警分级统计 + 系统健康度（分服务加权）+ 待审批任务数 | `/api/v1/clusters`、`/alerts/events?status=firing`、`/api/v1/dashboard/stats`、`/api/v1/ops/tasks?status=pending` |
| 服务健康 TOP | 错误率 TOP5 + 延迟 P95 TOP5 双榜（红黄绿状态点） | `GET /api/v1/services`（现有 RED） |
| 容量预警 | CPU/内存/磁盘当前值+阈值+线性预测触达天数 | `POST /api/v1/capacity/forecast`（现有） |
| 拓扑缩略图 | 16 节点小图，告警节点红色脉冲 | `GET /api/v1/topology/global`（现有） |
| 趋势图 | 近 24h 调用量/错误数双折线（分钟桶） | `/metrics/query_range` 或 trace_spans 聚合（现有） |
| 最近报告 | AICHAT 自动生成的报告前 3 条 | `/api/v1/ops/reports/history`（现有） |
| 告警时间线 | 近 24h 告警事件（触发/恢复/时长） | `/alerts/events`（现有） |
| 待办审批 | 处置命令待审批数（可一键进入 AI 对话审批） | `/api/v1/ops/tasks`（现有） |

### 10.4 建议
1. **时间范围联动条**（1h/6h/24h/7d）作用于全页区块；
2. **空态引导**：无告警 → "一切正常 + 查看历史告警"；无报告 → "去 AI 对话生成第一份巡检报告"；
3. 保留现有 AI 快问快答（已经是亮点），但快捷按钮文案与 AI 对话页统一；
4. 集群切换器从顶栏提升为首页第一视觉元素（当前集群名大字标题）。

---

## 11. 冗余代码与可精简项（全新审计）

### 11.1 部署无关/可删除（不影响任何构建）
| 路径 | 体积 | 结论 |
|---|---|---|
| `ongrid-ref/` | 35M | 第三方参考项目完整拷贝，deploy 零引用（已 gitignore），**移出仓库归档** |
| `observability-frontend/src_legacy_v2/` | 620K | v2 旧版备份，零引用（tsconfig 仅 include src），归档后删除 |
| `observability-frontend/Dockerfile.offline` | 1K | 引用过期基础镜像 v3.3.14，删除 |
| `ai-orchestrator/tasks.py` | 2.6K | arq worker，无任何 import/进程启动，**删除**（连带 values.yaml 中"ARQ 依赖 Redis"注释失效，Redis 组件可评估下线） |
| `ai-orchestrator/seed_knowledge.py` | 2.1K | 与 knowledge_seed.py 重复，删除 |
| `ipmi-exporter/build.sh` | 400B | 产出镜像名 `aiops/ipmi-exporter` 与 values.yaml `ipmi-exporter:latest` 不匹配，删除或改调 build-images.sh |
| `ai-apm-query-go/query-api`（ELF 9M） | 9M | **唯一被 git 跟踪的二进制**，`git rm --cached` + gitignore |
| `ai-apm-query-go/api`（Mach-O 11M） | 11M | 本机构建产物，删除（已 ignore） |
| 前端 `client.ts` 92 个未引用导出 | — | v2 API 包装残留，确定未使用（Vite 可 tree-shake，低风险裁剪） |
| 根目录测试截图/`.playwright-cli/`/`gui-test-screenshots/`/`.superpowers/` | ~30M | 测试产物，清理 + gitignore 补 `*.jpg` |
| `ai-orchestrator/.venv-312/`、前端 `node_modules/dist/`、`__pycache__` | ~2.1G | 本地构建产物（已 ignore） |

### 11.2 必须保留（易误删）
- `ai-orchestrator/bin/`（779M，Dockerfile COPY 的离线包：torch/chroma/k8sgpt 等）
- `ai-apm-query-go/docker/kubectl`（53M，构建依赖）
- `ai-apm-ingest-go/cmd/loadgen/`（故障注入/压测工具，本次评测即依赖该协议）
- `deploy/scripts/init-db.sh`、`values-prod.yaml`（运维逃生门/生产范例）

### 11.3 配置死重
- `deploy/helm/aiops/values.yaml` 中 `deepflow:` 块与 `nodeExporterTarget`：**无任何模板读取**（grep 证实），删除或补注释
- vmalert 模板整体休眠（enabled:false 有 guard，属有意的保留配置，可暂留）
- **`.gitignore` 缺口**：`ai-apm-query-go/query-api` 未覆盖；`*.jpg` 未覆盖；`postlogin` 陈旧规则

### 11.4 精简收益
工作区直接清理约 **2.2G** 磁盘（约 12 万文件）；git 仓库瘦身 9M（filter-repo 可选，风险：改写历史）；代码级删除 4 个文件（~6K）+ client.ts 裁剪。

---

## 12. 已验证亮点（保持并强化）

1. **故障处理引擎闭环**：告警触发→自动恢复→UI→RCA→删除，60s 评估周期真实生效（本次注入实测）。
2. **AICHAT 真实 LLM 闭环**：DeepSeek 流式输出 + 工具调用 + 审批流 + 报告入库，幻觉防护及格。
3. **NL2SQL**：翻译准确、执行有分页结果、复制按钮齐备。
4. **拓扑/追踪**：力导向拓扑 16 节点、Trace 瀑布图、搜索空态体验良好。
5. **容量预测**：真实 VM 数据 + 触达阈值预测。
6. **设计语言**：亮色极简、导航分组清晰、⌘K 面板可用。
7. **审计日志/报告中心**：操作留痕、报告闭环（AICHAT 自动入库）。

---

## 13. 修复优先级建议

| 阶段 | 内容 |
|---|---|
| 1 周内（阻断交付） | P0-1 Key 加密；P0-2 日志页交互重做；P0-3 severity 链路修复+存量回填 |
| 1 个月内 | P1-1~P1-6（口径统一、AI 告警过滤、RCA 占位符、规则阈值、详情面板） |
| 1 季度（产品化） | P1-7~P1-12（时区统一、埋点、拓扑同步、日志业务化、stream 标签、MySQL 骨架）；首页重设计落地；死代码清理 |

---

## 附录 A：测试资产
- 截图证据：`gui-test-screenshots/fresh-20260813/`（41 张，覆盖每页每交互点，部分编号被覆盖保存）
- 注入载荷：`/tmp/fault_trace.json`、`/tmp/fault_log.json`
- 测试规则：`demo 服务错误率过高`（id 16f59d197e8c78fd，已从告警列表删除，规则仍存在可删）

## 附录 B：本评测的客观限制
- 本环境模型无法内联查看截图（Read 返回 Unsupported Image），视觉结论基于 A11y 树/DOM 几何/文本证据交叉验证，截图已存档供人工复核；
- 日志页 antd Select 下拉在 GUI 层不可点击（P0-2），VictoriaLogs 数据源查询链路改由 API（`/api/v1/logs/victorialogs`）验证通过；
- AI 浮窗与通知中心因命令面板遮罩（P2-6）未完成交互测试，已列入问题清单；
- 本报告所有"修复方案"为产品+工程建议，未修改任何代码。
