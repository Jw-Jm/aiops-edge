# AIOps 智能可观测平台 — 全量功能使用与改进建议报告（全新独立分析 V4）

- **测试时间**: 2026-08-13（UTC+8）
- **测试环境**: localhost:30253（k8s observability 命名空间，单节点 orbstack）
- **测试账号**: admin/admin123
- **测试方法**: 代码级架构分析 + 全量 API 探测 + Playwright 前端全页面交互 + 真实数据注入（loadgen 注入 payments/orders/checkout 服务 trace+log）+ 真实 LLM（deepseek-v4-flash）对话测试 + 四数据源（ClickHouse/MySQL/VictoriaMetrics/VictoriaLogs）交叉核验
- **测试范围**: 12 个前端页面、60+ API 端点、AI 对话完整闭环（诊断→方案→风险评分→人工审批→执行→结果回传）、告警规则全链路（创建→评估→触发→恢复）、NL2SQL、MCP 工具、容量预测、报告中心、用户/审计/系统设置

---

## 一、功能覆盖总览

| 模块 | 页面 | 测试结果 |
|---|---|---|
| 工作台首页 | /overview | ✅ 渲染正常（KPI/趋势/健康度/AI 入口），告警分布空 |
| 服务全景 | /observability/service | ✅ 拓扑 18 节点 43 边可交互，服务列表 21 个服务，详情抽屉正常 |
| 链路追踪 | /observability/trace | ✅ 列表 50 条，Trace ID 点击打开瀑布图 Drawer |
| 日志与指标 | /observability/log | ✅ 检索/聚合统计正常，数据源可切换（ClickHouse/VictoriaLogs） |
| 告警事件 | /alerts/events | ⚠️ 当前/历史切换正常，AI 根因分析可触发，但严重度过滤失效（见 P0-2） |
| 告警规则 | /alerts/rules | ✅ 新建/详情/删除/历史告警正常，但类型被锁定 threshold（见 P0-4） |
| AI 对话 | /ai/chat | ✅ 8 步工作流完整闭环（含人工审批+真实执行），见第四章 |
| AI 工具 | /ai/tools | ✅ NL2SQL 翻译+执行+复制正常；MCP 工具/技能目录可浏览，MCP 无调用入口（P1） |
| 容量预测 | /capacity | ✅ CPU 10.4%/内存 79.5%/磁盘 39.5% 三指标预测+环比+ETT |
| 报告中心 | /report | ✅ 75 份报告，预览/下载正常 |
| 用户管理 | /admin/users | ✅ 3 用户，新增/编辑/删除表单完整 |
| 系统设置 | /admin/settings | ✅ AI 模型配置（DeepSeek 生效）/纳管集群/审计日志均正常 |

**数据源核验**（交叉验证一致）:
- ClickHouse: trace_spans 388,898 条 / log_records 281,484 条 / service_topology 1,686 边 / alert_events 2 条
- MySQL(aiops): service_catalog=0、topology_nodes=13、users=3、alert_rules=6、slo_targets=0、reports=75、audit_logs=60、devices=0、snmp_devices=0、ipmi_sensors=0
- VictoriaMetrics: ingest 指标 + node-exporter 指标 + service_errors_total/service_requests_total（ingest 写入）共 339+ 序列
- VictoriaLogs: 日志流经 query-api StartLogShipper 同步至 ClickHouse log_records，双向一致

**数据注入验证**：loadgen 注入 payments/orders/checkout 服务（error 模式）→ 数据成功入库（20 spans/24 logs）→ 前端 services 列表出现（18→21 个服务）→ 错误率 100% 正确显示 → 手工创建 error_rate 规则后 60s 内触发告警 → 数据窗口过期后自动 resolved。**告警引擎评估-触发-恢复全链路真实可用**。

---

## 二、P0 级问题（功能不可用 / 数据缺失）

### P0-1 服务自动识别完全失效，AI 无法使用平台核心观测数据
- **现象**: AI 对话中报告头部"**目标**: "恒为空；RCA 工具恒返回"未指定目标服务，已跳过"；对"payments 错误率 100%"提问时，AI 只能靠 k8s Pod 列表+告警+LLM 推测，无法获取 payments 的 RED 指标/Trace/日志，回答出现"payments 服务实体缺失（可能部署在集群外部）"这类明显偏离事实的推断（实际是我们注入的服务，trace_spans 中有 80+ 条错误数据）。
- **根因**: `tools.py` 的 `get_service_list()` 检查 `data["data"]` 键，但 `/api/v1/services` 实际返回 `{"services": [...], "total": N}`（顶层键是 `services`）→ 返回整个 dict 的 JSON 字符串；`orchestrator.py:_detect_service()` 期望 list → `if not isinstance(data, list): return ""` 恒成立 → **服务检测永远返回空**；`node_collect` 中 `if svc:` 分支（RED 指标/trace 采集）永远被跳过，`services_data` 也因同样原因为空。
- **影响**: P0 级。AI 诊断降级为"k8s 状态+告警+LLM 泛化推理"，平台最有价值的 trace/log/RED 数据无法进入 AI 上下文，RCA 能力名存实亡，`before_metrics`/`after_metrics` 验证环节（node_verify 依赖 svc）全部失效。
- **修复方案**: `get_service_list()` 兼容 `data["services"]` 与 `data["data"]`；或将 `/api/v1/services` 响应包装为 `{"data": [...]}` 统一契约。
- **涉及文件**: `ai-orchestrator/tools.py:70-90`、`ai-orchestrator/orchestrator.py:1561-1578`、`ai-orchestrator/orchestrator.py:node_collect`

### P0-2 告警事件 severity 字段全部丢失
- **现象**: ClickHouse `alert_events` 表中所有事件的 severity 为空（规则为 critical 的事件也是空）；前端"严重度"列空、"严重/警告/信息"过滤完全失效；工作台"严重 0 / 警告 0 / 信息 0"与"告警按服务分布：暂无"永远为空，即使有 firing 告警。
- **根因**: `alert_engine.go:154` 创建 `AlertEvent` 结构体时**未赋值 `Severity: rule.Severity`**。
- **影响**: P0 级。告警分级能力（平台核心卖点"分级处理告警"）整体失效；首页告警态势统计恒为 0，误导用户。
- **修复方案**: `event := AlertEvent{ ... Severity: rule.Severity ... }`（一行修复）。
- **涉及文件**: `ai-apm-query-go/internal/api/alert_engine.go:154-170`

### P0-3 预置告警规则仅 6 条 K8s 类规则，无任何 trace/log/anomaly/burn_rate 规则
- **现象**: 注入 100% 错误率服务数据，预置规则无一触发（没有任何 trace 类规则可命中）；平台实现了 error_rate/latency_p99/call_count/anomaly/burn_rate/log 六类规则评估引擎，但开箱只有 k8s-* 阈值规则。
- **根因**: 规则种子数据只包含 K8s 基础规则。
- **影响**: 平台最核心的"应用故障发现"能力（错误率突增、延迟劣化、异常检测、SLO 烧毁率）开箱不可用，用户必须手工建规则且 UI 还不支持建这些类型（见 P0-4）→ 双重障碍。
- **修复方案**: 预置 error_rate>5、latency_p99、call_count 突变、log_error_rate 等常用规则（severity 合理分级）；在首页/告警页增加"一键启用推荐规则"引导。
- **涉及文件**: 规则初始化逻辑（`ai-apm-query-go/internal/api/alerts.go` 规则种子）

### P0-4 前端新建告警规则硬编码 `type:'threshold'`
- **现象**: 新建规则表单只有名称/服务/条件/时长/指标/阈值/严重度/启用，无"规则类型"字段；提交时前端固定 `type: 'threshold'`。anomaly/burn_rate/log/trace_latency 等类型仅能通过 API 手工创建（本次测试经 API 创建 anomaly 与 burn_rate 规则均成功触发）。
- **根因**: `AlertRules.tsx:36` `type: 'threshold'` 硬编码。
- **影响**: 平台已实现的异常检测（ZScore/MAD）、SLO 烧毁率、日志关键字告警能力对用户完全隐藏；且后端 anomaly 规则创建时首次请求出现 120s 超时（疑似评估循环锁竞争，需复现排查）。
- **修复方案**: 表单增加"规则类型"下拉（threshold/anomaly/burn_rate/log/trace），按类型动态渲染字段（阈值语义、baseline 窗口、关键词、关联 SLO）。
- **涉及文件**: `observability-frontend/src/pages/alerts/AlertRules.tsx:20-44`

---

## 三、P1 级问题（功能受损 / 数据不准确）

### P1-1 anomaly/burn_rate 告警消息语义错误
- **现象**: anomaly 规则触发消息为"测试-异常检测2: error_rate 0.00 > threshold 3.00"——0>3 显然为 false 却触发，用户完全无法理解告警含义；burn_rate 消息为"error_rate 26.67 > threshold 14.40"（未体现烧毁率语义）。两者均为 threshold 格式拼装。
- **根因**: `alert_engine.go` 触发消息统一用 `"%s: %s %.2f > threshold %.2f"` 模板，未按规则类型区分。
- **影响**: 异常检测/烧毁率告警的可读性差，运维无法判断"为什么告警"。
- **修复方案**: 按类型拼装：anomaly → "zscore=3.2（MAD/zscore 检测，偏离历史基线）"；burn_rate → "烧毁率 26.7x（SLO 99.9%，窗口 30d）"。
- **涉及文件**: `ai-apm-query-go/internal/api/alert_engine.go:145-155`

### P1-2 工作台 top_services 的 avg_latency_ms / error_rate 恒为 0
- **现象**: `/api/v1/dashboard/stats` 的 top_services 中，lat_sum_ns 非零的服务 avg_latency_ms 显示 0、error_rate 显示 0（如 query-api: lat_sum_ns=221460360000 → 应 4.93ms 但为 0）；工作台"请求错误率 0.00%"与实际 0.005% 不符；服务列表页（另一个 API）数据正确。
- **根因**: `biz.AggregateStats()` 只累加 TotalCalls/TotalErrors，**未计算 AvgLatency/ErrorRate**（`internal/biz/dashboard.go:61-73`）。
- **影响**: 工作台核心 KPI 失真；延迟/错误率趋势误导运维判断。
- **修复方案**: `AvgLatency = LatSumNs / Calls / 1e6`、`ErrorRate = Errors / Calls * 100`。
- **涉及文件**: `ai-apm-query-go/internal/biz/dashboard.go:61-73`

### P1-3 trace 数据存在 10 小时缺口且 UI 无提示
- **现象**: trace_spans 在 2026-08-12 15:00 ~ 2026-08-12 23:00 无任何数据（10 小时采集中断）；dashboard 趋势图照常绘制（12/14 点之间大跨度连线），无缺口标识；容量预测基于不完整历史。
- **根因**: 该时段 deepflow/ingest 数据采集中断（环境变更所致），前端无数据缺口检测与提示。
- **影响**: 用户将缺口误读为"正常波动"；基于缺口数据做容量预测/异常基线会失真。
- **修复方案**: 趋势/图表组件对缺失时间窗口渲染"数据缺口"占位提示；后端可输出 `data_gaps` 元信息；采集端增加探活与告警（采集器失联 >5min 触发采集断流告警）。
- **涉及文件**: 前端趋势组件（Overview）、`ai-apm-query-go/internal/api/dashboard.go`

### P1-4 SLO 功能前端缺失且创建默认 disabled
- **现象**: `/api/v1/slo` API 与 `client.ts` 封装齐全，但前端无任何 SLO 管理页面；POST 创建 SLO 默认 `enabled:false`（用户无从知晓与启用）；burn_rate 规则依赖 SLO（`evaluateRuleBurnRate` 要求 `rule.SLOID != ""`），因此**SLO 烧毁率告警对普通用户完全不可用**。
- **根因**: SLO 页面未实现；创建接口 `enabled` 默认 false。
- **影响**: P1。平台能力清单里的"SLO 烧毁率"名存实亡。
- **修复方案**: 新增 SLO 管理页（目标/窗口/启用开关）；创建默认 enabled=true 或显式提示。
- **涉及文件**: `observability-frontend/src/api/client.ts:258-271`（有封装无页面）、`ai-apm-query-go/internal/api/slo.go`

### P1-5 deepflow/status 状态矛盾
- **现象**: `/api/v1/deepflow/status` 返回 `"status":"available"`，但 `deepflow_url/grafana_url` 均为空、`http_status=404`。
- **根因**: 状态判定逻辑未校验 URL 配置与连通性结果的一致性。
- **影响**: 用户误判 DeepFlow 集成可用；页面若据此展示"DeepFlow 已接入"则误导。
- **修复方案**: URL 为空或探测 404 时状态应返回 `not_configured`/`unavailable`。
- **涉及文件**: `ai-apm-query-go/internal/api/deepflow.go`

### P1-6 多条数据质量小问题（聚合列出）
- `/api/v1/traces` 列表的 `services`/`spans` 字段为字符串（"2"）而非数字 → 前端类型不严谨；建议统一为 int。
- dashboard/stats 的 top_services 含空服务名 `service: ""`（687 调用）→ 应过滤空服务名或归入"未知"。
- NL2SQL 翻译"最近一小时"时生成的 SQL 无时间过滤条件（`WHERE start_time > now()-3600` 缺失）→ 结果与提问意图不符，建议在翻译 prompt 中强化时间窗口推导，或在执行前做 SQL 时间窗口校验补全。
- `loadgen` 的 `-error-rate` flag 未读取 `LOADGEN_ERROR_RATE` 环境变量（写死默认 0.05），调试时参数易被忽略。
- MCP 工具页仅展示工具列表，无"调用/测试"入口 → 功能半成品，建议增加工具试调面板（参数表单 + 结果展示）。
- 前端 AiChat 硬编码 `intent:'diagnosis'`、`service:''`（`AiChat.tsx:113`）→ 用户无法选择意图，服务识别完全依赖后端 `_detect_service`（已被 P0-1 破坏）；建议前端支持快捷意图切换并回填服务名。
- stream_sync 中 tool_start/tool_end 事件是按节点名推断的"虚拟工具事件"（`orchestrator.py:1410-1425` 注释"真实工具级采集为独立后续"）→ 用户看到"RCA 根因分析 成功"实为节点占位，建议改为真实工具调用事件或明确标注"节点完成"。

---

## 四、AI 对话（AICHAT）专项评估

### 4.1 工作流（总体评价: 架构优秀，闭环完整 ✅）
8 步流程 collect→clean→rca→rag→crewai→summarize，SSE 流式推进，真实 LLM（deepseek-v4-flash）驱动。完整验证场景：
1. **提问** "payments 服务错误率突增到100%，帮我定位根因" → 返回诊断报告（命中活跃告警、指出 payments 实体缺失、给出 3 条处置命令）
2. **巡检提问** → 返回 24 Pod/3 命名空间/节点健康/重启分析
3. **审批闭环**：诊断报告附处置命令 → 风险评分 4/100 → 前端"确认执行/驳回"卡片 → 确认后**真实执行 kubectl**（"No resources found" 结果回传对话）→ 支持"输出最终版本报告"与"执行自定义命令"
4. 会话历史、最终报告持久化到报告中心（已验证 75 份报告含本次生成的）

### 4.2 交互合理性
- ✅ 进度分步展示（数据采集/清洗/根因/案例/分析/汇总）符合运维心智
- ✅ 审批卡内联在对话流中，避免跳转，交互自然
- ✅ 处置命令可直接执行，执行结果回传形成多轮上下文（`exec_context` 机制）
- ⚠️ "目标"字段恒空（P0-1），报告头部 `**目标**:  | **问题**:` 明显残缺
- ⚠️ 快捷入口（分析根因/集群巡检/服务延迟排查）发送后无意图回显，无法感知当前模式
- ⚠️ 无对话打断/停止按钮；长回答期间无"生成中"光标细节

### 4.3 内容准确性
- ✅ 告警态势引用真实（能命中"测试-错误率告警"并给出触发详情）
- ✅ k8s 基础设施数据真实（Pod 状态/重启次数/节点资源）
- ❌ **服务级 RED/trace/日志数据完全缺失**（P0-1 连锁），导致：对已注入 100% 错误率服务的回答声称"payments 服务实体缺失，可能部署在集群外部"，并推断"采集链路不稳定导致虚高/误报"——**与事实严重背离**（数据明明在 ClickHouse）
- ❌ "数据不足说明"节反复出现（缺 RED 指标/日志/终止原因），实际这些数据平台都采集了，只是工作流没拿到
- ⚠️ 处置命令场景化不足：对 payments 生成 `kubectl get pods -A | grep payments` 合理，但未结合链路/日志给出更有针对性的排查命令

### 4.4 改进空间（按优先级）
1. **修复 P0-1 服务识别**（最高优先，修复后 AI 诊断质量将质变）
2. RCA 节点应真实调用：拉取目标服务 RED 指标窗口、错误 trace 样本、错误日志样本，作为 evidence 注入 LLM；当前 chat 模式 RCA 为确定性空实现
3. 对话报告头部补齐"目标服务/意图/数据时间窗/数据源覆盖度"元信息，让用户知道 AI 依据了哪些数据
4. 增加"依据数据"折叠区（展示本次分析使用的 trace/log/指标片段），增强可解释性
5. 意图分类：前端允许选择"诊断/巡检/查询"模式，或后端增加轻量意图识别后再进 DAG，避免所有问题都走 diagnosis
6. 长回答支持流式停止；会话列表支持搜索/重命名

---

## 五、数据源核验明细

| 数据源 | 表/指标 | 数据量 | 一致性 |
|---|---|---|---|
| ClickHouse | trace_spans | 388,898 | ✅ 与前端/API 一致；⚠️ 08-12 15:00-23:00 缺口 |
| ClickHouse | log_records | 281,484 | ✅ 与前端一致；VictoriaLogs 经 shipper 同步 |
| ClickHouse | service_topology | 1,686 边/18 节点 | ✅ 与拓扑页一致 |
| ClickHouse | alert_events | 2 | ⚠️ severity 全空（P0-2） |
| MySQL | users 3 / alert_rules 6 / reports 75 / audit_logs 60 | ✅ | service_catalog=0 / slo_targets=0 / devices=0 / snmp=0 / ipmi=0（能力未启用或环境无硬件） |
| VictoriaMetrics | 339+ 序列（ingest RED 指标 + node-exporter + 组件指标） | ✅ | 注入的 payments service_errors_total=84 可见，anomaly 检测数据源正常 |
| VictoriaLogs | pod 日志流 | ✅ | query-api StartLogShipper 同步至 ClickHouse 正常 |

---

## 六、工作台首页重新设计建议（基于平台现有能力）

**设计原则**：一屏掌握"集群健康 + 风险 + 待办"，运维 30 秒内判断"要不要处理、处理什么"。

### 建议布局（上→下，左→右）
1. **顶部集群条**（替代现在零散展示）：集群名+节点数+健康度环形图+切换器；右侧放全局时间范围选择（1h/6h/24h/7d）——现在全站时间范围不可调，是明显缺失。
2. **KPI 行**（保留现有但修正数据）：服务数（纳管口径）、调用总量、请求错误率（修复 P1-2）、P95 延迟（修复 P1-2）、**活跃告警数**（修复 P0-2 后才有意义，当前恒 0）、数据缺口提示角标。
3. **风险区（新增，核心）**：
   - 活跃告警列表（firing 事件，按严重度着色，可点击跳转事件页并一键 AI 根因分析）
   - 资源风险卡：内存 79.5%/90% 阈值已接近——应展示"距阈值剩余空间+预计触达时间"（capacity/forecast 已有 ETT 数据，目前只出现在容量页）
   - 采集健康卡：deepflow-agent 重启 12 次、redis 重启 15 次等信号（AI 诊断已能发现，工作台应直接呈现），含数据缺口提示
4. **趋势区**：调用量/错误率双轴趋势（复用现有 trend 数据，加缺口占位）。
5. **AI 区**：保留"AI 快问快答"+ 新增"最近 AI 诊断摘要"卡片（最近 3 条会话结论/处置状态），让工作台成为 AI 运维入口。
6. **服务健康榜**：按错误率/延迟降序 Top 异常服务（数据已存在），点击进服务详情。
7. **告警分布**：修复 severity 后恢复"按服务分布"环形图；无数据时给引导文案（"创建 trace 告警规则以启用"）而非空态。
8. **最近报告**：最近 5 份报告入口（reports 表已有 75 份）。

### 产品建议补充
- 工作台数据刷新策略：30s 轮询 + 手动刷新按钮；告警变化时卡片微闪烁提示。
- 增加"集群视角"与"全局视角"切换（多集群纳管时每集群独立健康卡）。
- 所有 KPI 可点击下钻（数字 → 对应页面过滤态）。

---

## 七、冗余代码/文件分析（本机部署环境未使用）

### 可安全删除（明确无用）
| 项 | 位置 | 体积 | 说明 |
|---|---|---|---|
| 前端 v2 旧版 | `observability-frontend/src_legacy_v2/` | 620K/58 文件 | redesign 后无引用（已 gitignore，物理占用仍在） |
| query-api 编译产物 | `ai-apm-query-go/query-api`（ELF 9M）、`ai-apm-query-go/api`（Mach-O 11M） | 20M | **query-api ELF 已误入 git 跟踪**，应 `git rm`；api 为本地编译产物 |
| 前端测试截图 | 根目录 4 png + `observability-frontend/*.png` 143 张 + `*.yaml` 68 个 | ~15M | 测试产物（gitignore 已挡，物理可清） |
| gui-test-screenshots | 根目录 | 13M | 历史测试截图 |
| 历史评测报告 | 根目录 AIOPS_PLATFORM_REVIEW_REPORT*.md ×5 + aiops-platform-review-report.md | ~120K | 用户已声明"不考虑历史报告"，建议归档 |
| .playwright-cli | 根目录 | 22M/283 文件 | 测试工具（gitignore），保留工具链可、清理快照可 |

### 部署需要但非本机运行需要（建议移出工作区或入 build/ 目录）
| 项 | 位置 | 体积 | 说明 |
|---|---|---|---|
| ai-orchestrator/bin 离线包 | `ai-orchestrator/bin/`（sp/chroma/hf/pybin tar.gz + k8sgpt 111M + kubectl 53M） | 779M | 仅 Dockerfile 构建镜像时 COPY；本地调试用不上，建议 .gitignore + 构建脚本从独立存储拉取 |
| kubectl 二进制 | `ai-apm-query-go/docker/kubectl` | 53M | 仅镜像构建需要；本机已有 kubectl |
| ai-orchestrator 缓存 | `__pycache__` 828K | 可清 | |

### 功能代码已存在但本机环境未激活（不算冗余，属能力空置）
- **SNMP**（snmp_collector.py、db_snmp、/api/v1/snmp/*）：collector 在跑但 snmp_devices=0，无设备可采 → 需在"纳管集群/设备"UI 支持添加 SNMP 设备才可激活。
- **IPMI**（ipmi-exporter daemonset 已部署，ipmi_sensors=0）：本机为虚拟机无 IPMI 硬件 → 环境限制，非代码问题；建议 UI 显示"无 IPMI 硬件"状态而非空表。
- **服务目录**（service_catalog=0、/api/v1/catalog/services 空）：后端 CRUD 与前端客户端封装齐全，但无 UI 页面 → 能力半成品。
- **设备/告警静默**：devices=0；silences API 正常（测试创建/删除成功）但前端无静默管理 UI → 建议补页面。
- **dashboard/panels**（B4 看板）：API + client.ts 封装完整，**前端无任何页面使用**（listPanels 无引用）→ 要么实现自定义看板页，要么删除该能力避免误导。
- **knowledge**（/api/v1/ai/knowledge 返回空）：seed 文件 `data/knowledge_cases.json` 存在但未导入 MySQL knowledge_base 表 → 建议启动时自动 seed。

---

## 八、问题优先级总表

| 级别 | 编号 | 一句话 | 修复成本 |
|---|---|---|---|
| P0 | 1 | 服务识别失效（get_service_list 契约不匹配）→ AI 无核心数据 | 低（几行） |
| P0 | 2 | 告警事件 severity 丢失 → 分级/统计全失效 | 低（一行） |
| P0 | 3 | 无预置 trace/log/anomaly 告警规则 → 开箱无法发现应用故障 | 低 |
| P0 | 4 | 前端规则类型硬编码 threshold → 高级告警能力不可配 | 中 |
| P1 | 1 | anomaly/burn_rate 告警消息语义错误 | 低 |
| P1 | 2 | 工作台 avg_latency/error_rate 恒 0 | 低 |
| P1 | 3 | 10h 数据缺口无提示 | 中 |
| P1 | 4 | SLO 无前端页面且默认 disabled → burn_rate 不可用 | 中 |
| P1 | 5 | deepflow/status 状态矛盾 | 低 |
| P1 | 6 | 数据质量杂项（字符串类型/空服务名/NL2SQL 时间窗/loadgen env/MCP 无调用/AiChat 硬编码/虚拟工具事件） | 各低~中 |

**建议修复顺序**：P0-1 → P0-2 → P0-3 → P1-2（1 小时级修复即可让产品演示质量大幅提升）→ P0-4/P1-4（补 UI）→ 其余。

---

## 九、测试过程产物
- 注入数据：payments/orders/checkout 服务（约 200 spans/200 logs，已留存演示用）
- UI 截图：/tmp/aiops_uitest/*.png（12 页面 + 交互过程）
- 测试脚本：/tmp/aiops_uitest/*.js
- 测试规则/SLO/静默：已全部清理，恢复至 6 条预置规则

---

# 附章 A：多集群纳管功能专项验证（模拟注入 prod-cluster）

## A.1 验证方法
- **模拟方式**：① 创建集群记录（API 触发 Bug 后改 SQL 直插）② 向 ClickHouse 注入 `cluster_id='prod-cluster'` 的 trace_spans（9 条，5 服务含错误）/log_records（5 条含 ERROR）/service_topology（5 边）③ 向 VictoriaMetrics 注入 `cluster="prod-cluster"` 标签的 RED 指标（4 服务、含错误计数）——完全等效于该集群部署一个 `INGEST_CLUSTER_ID=prod-cluster` 的 ingest 实例上报
- **验证方式**：逐 API 带 `cluster_id=prod-cluster / default / all` 对比返回（不经前端，纯 API 层验证）
- 平台多集群机制：采集侧每集群独立 ingest 打 `cluster_id` 标；查询侧前端 axios 拦截器全局注入 `cluster_id` 参数 → 后端 `extractClusterClause` → SQL `AND cluster_id='xxx'`；VM 指标带 `cluster` 标签

## A.2 验证结果总表

| # | 功能 | API | 集群过滤是否生效 | 验证证据 |
|---|---|---|---|---|
| 1 | 工作台统计 | /dashboard/stats | ✅ **生效** | prod-cluster→5服务/9调用/3错误(shop-frontend等)；default→21服务(deepflow-agent等) |
| 2 | 链路追踪 | /traces | ✅ **生效** | prod-cluster 仅返回 prod-trace-001~009；default 返回原集群 trace |
| 3 | 日志检索 | /logs/query | ✅ **生效** | prod-cluster 5条(含 ERROR: DB连接池耗尽/死锁/上游拒连)；default 100条无 ERROR |
| 4 | 全局拓扑 | /topology/global | ✅ **生效** | prod-cluster 5节点5边(catalog/orders/payments/inventory/shop-frontend)；default 21节点43边 |
| 5 | RED 指标 | /metrics/query | ✅ **生效** | prod-cluster 查 shop-frontend 有3个数据点；default 查询为空 |
| 6 | 服务列表 | /services | ❌ **不生效** | prod-cluster 与 default 返回完全相同 20 个服务（SQL 无 clusterClause） |
| 7 | 容量预测 | /capacity/forecast | ❌ **不生效** | prod-cluster 与 default 返回相同 cpu 8.45（VM 查询未按 cluster 过滤） |
| 8 | 告警事件 | /alerts/events | ❌ **不生效** | 两集群返回相同事件（alert_events 表无 cluster_id 字段，引擎全局评估） |
| 9 | 告警规则 | /alerts/rules | ❌ **不生效** | 规则为全局配置（MySQL alert_rules 无 cluster 列） |
| 10 | AI 对话 | /ai/chat | ❌ **不生效** | 传 cluster_id=prod-cluster 提问 shop-frontend，AI 回答基于本地集群 24 Pod，明确"未发现 shop-frontend"，无法读取 ClickHouse 中已注入的 prod 数据（orchestrator tools.py 查询不带 cluster_id） |
| 11 | 报告中心 | /ops/reports/history | ❌ **不生效** | cluster_id 参数被忽略；reports 表无 cluster 列，前端"集群"列恒显示主集群 |
| 12 | 审计日志 | /api/v1/audit | ❌ **不生效** | 无集群维度 |
| 13 | anomaly 规则评估 | 告警引擎 metricPromQL | ❌ **跨集群串数据** | VM 中同名服务 orders 存在 default(8) 与 prod-cluster(23) 两序列，PromQL 仅按 service 过滤不带 cluster 标签 → 两集群数据聚合在一起，异常检测基线被污染 |

**结论**：多集群能力是"半成品"——**数据查询层（stats/traces/logs/topology/metrics）集群隔离真实可用且验证通过；但管理面（告警/规则/报告/审计/SLO）无集群维度，且服务列表、容量预测、AI 对话三大核心应用模块未接入集群过滤**。切换集群后用户会看到"统计/拓扑/链路/日志变了，但服务列表不变、容量不变、AI 感知不到"的不一致体验。

## A.3 新增 Bug（本次模拟暴露）

### A-1（P1）创建集群 API 直接失败——多集群纳管入口不可用
- **现象**: `POST /api/v1/clusters {"name":"prod-cluster",...}` 返回 `{"error":"Error 1265 (01000): Data truncated for column 'status' at row 1"}`
- **根因**: MySQL `clusters.status` 为 `ENUM('active','degraded','down')`，而 `clusterCreate` 未给 Status 赋值（默认空串 ""），插入 ENUM 列被拒
- **影响**: 用户在 UI 无法纳管新集群（前端"纳管集群"功能不可用）；只能手工 SQL 插入
- **修复**: `clusterCreate` 默认 `Status: "active"`；DAO 层同时给 Region/Version/NodeCount 兜底默认值
- **涉及文件**: `ai-apm-query-go/internal/api/clusters.go`（clusterCreate）、`ai-apm-query-go/internal/store/clusters.go:111`

### A-2（P1）集群列表 API 崩溃——切换器无集群可选
- **现象**: SQL 插入不带 kubeconfig 的集群后，`GET /api/v1/clusters` 返回 `{"clusters":[],"error":"sql: Scan error on column index 8, name \"kubeconfig\": converting NULL to string is unsupported"}`
- **根因**: `store/clusters.go` 用 `string` 扫描 kubeconfig 列，kubeconfig 为 NULL 时报错；且错误被吞成空列表返回（前端 ClusterSwitcher 只显示"全部集群"）
- **影响**: 任何 kubeconfig 为 NULL 的集群都会让整个集群列表 API 失效；多集群切换 UI 崩溃
- **修复**: kubeconfig 改用 `sql.NullString`（或 SQL 层 `COALESCE(kubeconfig,'')`）；DAO 错误不应吞为空列表
- **涉及文件**: `ai-apm-query-go/internal/store/clusters.go:47`、`clusters.go`（List）

### A-3（P1）服务列表不随集群切换
- **现象**: `/services?cluster_id=prod-cluster` 与 `cluster_id=default` 返回完全相同的服务集合
- **根因**: `ListServices` 的 SQL 未调用 `extractClusterClause`（handler.go 中该方法内多处已用，此处理漏）
- **影响**: 服务全景页切换集群后列表不变，误导用户
- **修复**: 两个查询（服务列表 + 指标聚合）均追加 `extractClusterClause(r)`
- **涉及文件**: `ai-apm-query-go/internal/api/handler.go`（ListServices, 约 435-490 行）

### A-4（P1）容量预测不随集群切换
- **现象**: `/capacity/forecast?cluster_id=prod-cluster` 与 default 返回相同数据
- **根因**: 容量查询走 VM 但未带 cluster 标签过滤（VM 指标带 cluster 标签，具备过滤条件）
- **修复**: capacity handler 透传 cluster_id 到 VM PromQL（如 `{cluster="prod-cluster"}`）
- **涉及文件**: `ai-apm-query-go/internal/api/capacity.go`

### A-5（P1）AI 对话不感知集群
- **现象**: 传 `cluster_id=prod-cluster` 提问，AI 回答基于本地集群（"未发现 shop-frontend 相关资源"），ClickHouse 中已注入的 prod-cluster trace/log 完全不可见
- **根因**: orchestrator 的 `tools.py` 所有工具（query_metrics/query_traces/query_logs/get_service_list）构造 URL 均不带 cluster_id；main.py 的 chat 入口也未把 cluster_id 存入 AgentState
- **影响**: 多集群场景 AI 分析必然张冠李戴（分析 A 集群问题用的是 B 集群数据），比单集群更危险
- **修复**: ChatRequest.cluster_id → AgentState → tools.py 工具 URL 追加 `cluster_id` 参数；与前端 Axios 拦截器语义对齐（空/all 不传）
- **涉及文件**: `ai-orchestrator/main.py`（ChatRequest）、`ai-orchestrator/orchestrator.py`（AgentState/_run_dag）、`ai-orchestrator/tools.py`（全部查询工具）

### A-6（P2）告警体系全局化（事件/规则/静默无集群维度）
- **现象**: alerts/events、alerts/rules、silences 均不随 cluster_id 变化；alert_events 表无 cluster_id 列
- **根因**: 告警引擎全局评估（evaluateAlerts 遍历全部规则查全量数据），无集群维度设计
- **影响**: 多集群下告警无法按集群分级查看/管理；同一规则阈值作用于所有集群（prod 与 dev 阈值不同无法配置）
- **建议**: ① alert_events 增加 cluster_id 列（事件落库时从规则/数据带出）② 规则表增加 cluster 范围字段（all/指定）③ 评估引擎按集群维度执行
- **涉及文件**: `ai-apm-query-go/internal/api/alert_engine.go`、`alerts.go`、CH 表 alert_events

### A-7（P2）anomaly 规则跨集群数据混合
- **现象**: VM 中 `orders` 服务存在 default(错误8) 与 prod-cluster(错误23) 两个序列，`metricPromQL` 生成的 PromQL（`service_errors_total{service="orders"}`）会聚合两个集群的数据
- **根因**: metricPromQL 未带 cluster 标签过滤；anomaly/forecast/burn_rate 类规则评估时无集群上下文
- **影响**: 异常检测基线被跨集群数据污染，多集群同名服务会产生误报/漏报
- **修复**: 规则增加 cluster 字段，metricPromQL 追加 `cluster="xxx"`（A-6 联动）
- **涉及文件**: `ai-apm-query-go/internal/api/alerts.go:1125`（metricPromQL）

## A.4 架构建议（多集群产品化路径）
1. **采集侧**：明确"每集群一个 ingest 实例 + INGEST_CLUSTER_ID"部署模型，Helm 值中提供 clusterId；文档化 cluster_id 命名规范（与集群 name 对齐，前端 ClusterSwitcher 按 name 取值）
2. **查询侧**：ListServices/Capacity 补齐 extractClusterClause；AI tools 全链路透传 cluster_id（含 AgentState 与工具 URL）
3. **管理侧**：告警规则/事件/静默/SLO/报告/审计增加集群维度（规则可限定范围，事件带 cluster 标记，报告记录 cluster_id）
4. **UI 侧**：ClusterSwitcher 增加"未纳管数据"提示（选中集群无数据时给出引导）；服务列表/容量页明确显示当前集群范围；anomaly 规则创建时强制选择集群
5. **修复 A-1/A-2**（纳管入口）后，配合前端"纳管集群"页面（添加 kubeconfig + 连通性探测）完成纳管闭环

## A.5 模拟数据与清理
- **保留数据**（供演示验证）: MySQL clusters 表 id=626 `prod-cluster`（3 节点/active）；ClickHouse cluster_id='prod-cluster' 的 9 spans/5 logs/5 拓扑边；VM cluster="prod-cluster" 标签 8 条 RED 指标
- **清理命令**（如需恢复单集群环境）:
  - MySQL: `DELETE FROM aiops.clusters WHERE id=626`
  - ClickHouse: `ALTER TABLE observability.trace_spans DELETE WHERE cluster_id='prod-cluster';`（log_records/service_topology 同理）
  - VictoriaMetrics: 数据自带保留期自动过期，或 `curl -X POST 'http://victoria-metrics:8428/api/v1/admin/tsdb/delete_series?match[]={cluster="prod-cluster"}'`
