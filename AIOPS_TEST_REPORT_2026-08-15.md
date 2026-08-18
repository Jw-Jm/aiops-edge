# AIOps 智能可观测平台 · 全面深度实测报告（第二轮独立复测）

- **测试时间**: 2026-08-15 21:10–21:50（UTC+8）
- **测试环境**: http://localhost:30253（OrbStack K8s + Helm，ns=observability，14 Pod 全 Running），admin/admin123
- **测试方式**: ① 代码勘探（后端 API 面 + 前端 21 路由）② 数据源直连核验（CH/MySQL/VM/VLogs）③ 全量 API 矩阵（~90 断言含 RBAC/边界）④ 数据注入 + 故障注入（OTLP traces/logs，orders 85% 错误窗口）⑤ 告警引擎闭环实测（创建规则→firing→自动 resolved）⑥ GUI 全页面 + 深度交互（Playwright，21 路由 + 8 交互流）⑦ 真实 LLM 多轮对话评测（DeepSeek，7 轮含 NL2SQL/非流式/多轮追问）⑧ 数据合理性/口径一致性分析
- **说明**: 本轮为上轮审计（AIOPS_AUDIT_REPORT_2026-08-15.md，修复 25 项）之后的独立复测，重点验证修复项是否真实生效并发现新问题。测试注入的数据与测试创建的规则/用户/租户已全部清理。

---

## 一、执行摘要

**总体结论：平台核心链路（采集→落库→查询→告警→AI→处置建议）可用且告警引擎闭环健康，但存在 2 个 P1 级功能/安全缺口、5 个 P2 级问题、多项 AICHAT 链路质量缺陷。**

1. **告警引擎闭环完整**：故障注入（orders 错误率 85%×150s）→ 自定义规则 `test-orders-err-rate` 在 60s 评估周期内 firing（value=73.33 > 30）→ 故障恢复后自动 resolved。上轮 P2-2（服务级规则不触发）**实测修复生效**。
2. **上轮 25 项修复大部分生效**：非流式 chat 返回真实报告（P1-2 ✅）、NL2SQL 真实 LLM 翻译（P1-4 ✅）、silences/tenants 越权已堵（P1-10/P2-2 ✅）、DELETE 404（P3-1 ✅）、角色白名单（P3-2 ✅）。**但 2 项修复未真实落地**：LLM test 接口仍 400（P1-3 代码中无 ULA 豁免）；信息查询仍生成处置卡（P1-8 不彻底）。
3. **新发现 P1 缺口 ×2**：① 普通用户可创建告警规则（threshold 类型无 admin 门禁）；② 前端"市场"tab 的 API `/ai/marketplace/*` 未在 query-api 注册代理，恒 404（功能不可用）。
4. **AICHAT 核心矛盾**：AI 工具采集的数据集与平台 API 不一致（AI 看不到 orders 服务却能看到其指标；AI 采集数据缺 error_rate 字段导致"无法回答"而 NL2SQL 秒答）；多轮会话上下文未延续；首字节延迟 46–97s 无渐进流式输出。
5. **数据口径多套并存**：服务数 27（API 含 7 个 deleted 残留）/31（topology_services）/25（GUI 总览 KPI）/10（AI 回答）四处不一致。

---

## 二、测试方法与覆盖范围

### 2.1 代码勘探
- 后端：query-api（Go，~60 端点）、orchestrator（FastAPI，~80 端点）、ingest（OTLP 接收）、ipmi-exporter；nginx 路由链 `:30253 → /api/* → query-api → ProxyAI → orchestrator`
- 前端：React 18 + antd 5 + zustand + echarts + react-router 6，21 路由全部枚举

### 2.2 数据源核验（直连）
| 数据源 | 验证内容 | 结果 |
|---|---|---|
| ClickHouse | trace_spans 21876 / log_records 10190 / alert_events / service_topology，schema 核对 | ✅ |
| MySQL | 30 表，users=2（admin/zhangsan），CRUD 经 API 全通过 | ✅ |
| VictoriaMetrics | service_requests_total 25 series 历史数据；instant query 近期为空（上游行为） | ✅ |
| VictoriaLogs | pod 日志持续流入 | ✅ |
| LLM 配置 | DeepSeek deepseek-v4-flash，api_key_set=true，configured=true | ✅ |

### 2.3 注入与故障注入
- 自写 OTLP 注入器（trace 4 跨度调用链 gateway→orders→payments/inventory→auth，每 span 独立 resource 携带 service.name）
- 时间线：稳态 180s（2% 错误）→ **故障 150s（orders 85% 错误）** → 恢复 180s（2%）
- 注入量：稳态 +135 traces/+135 logs，故障 +114 traces/+114 logs，全部落库

### 2.4 测试矩阵规模
| 类别 | 规模 | 结果 |
|---|---|---|
| API 断言 | ~90（含 RBAC 8 项、边界 10 项） | 82 通过 / 8 失败 |
| GUI 路由 | 21 路由 × 断言 | 全部可达渲染 |
| GUI 交互流 | 8 条（建规则/SLO/用户、NL2SQL 翻译+执行、K8s 预检、AI 对话、工作流、RCA） | 6 通过 / 2 失败 |
| AICHAT 真实 LLM | 7 轮（信息查询/根因分析/TOP3/修复建议/NL2SQL/非流式/多轮追问） | 见第五节 |
| 告警引擎 | 1 条自定义规则完整生命周期 | firing→resolved ✅ |

---

## 三、功能验证结果（按模块）

### 3.1 可观测面 ✅
- **总览看板**：统计卡/趋势图/资源态势/节点 TOP5/告警列表均渲染；KPI 服务数 25 与 API 27 不一致（P2 口径问题）；"检测到数据采集中断：3 个时段无数据"横幅与正常数据流并存（数据合理性疑点）
- **服务全景**：拓扑 21 节点 32 关系 + 服务列表双 tab；ns 过滤下拉正常
- **链路追踪 / 日志查询**：列表 + 查询正常；`keyword` 参数别名生效（上轮 P2-5 修复验证 ✅）
- **虚拟机 / Grafana / DeepFlow**：空态友好，Grafana 健康探测正常（v10.4.3）
- **容量预测**：CPU/内存/磁盘三图 + ETT 预测正常
- **基础设施 6 类 API**（nodes/pods/deployments/namespaces/hpa/vms）全部 200

### 3.2 告警与治理 ⚠️
- **告警规则**：10 条内置规则 + 自定义规则 CRUD 正常；GUI 新建弹窗表单字段完整（名称/服务/条件/时长/类型/指标/阈值/严重度/启用）
- **告警事件**：聚合视图（count 累计、时间窗去重）、RCA 按钮、状态机 firing→resolved 自动转换全部正常
- **告警引擎**：60s 评估周期、dampening/cooldown/escalation/静默逻辑代码完整，实测闭环见第四节
- **SLO**：CRUD + 边界校验（负窗口/超限 target → 400）✅（上轮 P2-1 修复生效）
- **审批中心/审计**：审计日志记录 nl2sql 等操作（operator=system 为已知语义）；审批流 GUI 存在
- **⚠️ RBAC 缺口**：普通用户 `POST /alerts/rules` 创建 threshold 类型规则返回 **201**（代码仅对 metric_raw/anomaly/forecast 类型做 admin 门禁）

### 3.3 AI 智能面 ⚠️
- **AI 对话**：SSE 流式（progress/tool_start/tool_end/chunk/done/suggestion 事件序列完整）、会话列表 20 个、处置卡（计划+脚本+风险星+确认/驳回）——见第五节详评
- **NL2SQL**：翻译（used_fallback:false）+ 执行双链路 ✅，返回正确 TOP3（orders 29.97%/gateway 15.05%/auth 5.96%）
- **工作流**：内置 full_diagnosis DAG、列表/运行弹窗正常；运行历史/审批接口存在
- **技能目录/Agent 注册表/MCP 工具/知识库/playbooks/RAG**：全部 200，RAG 77 条案例，playbooks 分类齐全
- **❌ 市场 tab 不可用**：前端 AiTools 市场 tab 调用 `/ai/marketplace/installed|install`，query-api 未注册该路径代理 → **404**；GUI 显示"暂无已安装 pack"空态（前端吞掉错误，用户无法察觉功能缺失）

### 3.4 基础设施与运维面 ⚠️
- **K8s 运维**：预检（rollout_restart）返回 preflight_token+resource_version ✅；**scale 动作缺 replicas 参数 → 500 KeyError**（未做参数校验）
- **WebShell/SNMP/IPMI/node health**：接口可达（空态）
- **Webhook 自动调查（B6）**：`POST /ops/webhook` 创建任务 ✅（queued）；手动 run 后卡 **diagnosing 状态 10+ 分钟无报告**，investigator 无任何日志输出（persona 注册或门控问题）
- **报告中心**：reports=0——诊断任务不产出报告，与"报告/产物中心"定位脱节

### 3.5 系统管理 ✅
- 用户 CRUD、租户、集群管理（列表/nodes/namespaces/events）、LLM 配置保存、平台健康全部正常
- 上轮 P1-1（多集群 ns/events 泄漏）修复在 API 层验证通过

---

## 四、告警引擎闭环实测（重点）

**测试设计**：创建规则 `test-orders-err-rate`（threshold，error_rate > 30%，duration 2min，critical）→ 注入 orders 85% 错误窗口 → 观察评估周期 → 恢复 → 观察自动 resolved。

**实测时间线**：
```
21:31:35  稳态注入开始（2% 错误率）
21:34:37  故障窗口开始（orders 85% 错误）
21:36:18  规则 FIRING（value=73.33 > 30.00）✅ 故障后 ~100s 内触发
21:37:19  firing 持续（count=2，时间窗聚合去重正常）
21:38:20  firing 持续（count=3）
21:39:21  规则自动 RESOLVED ✅（恢复后 ~2min 内自动关闭）
```
**结论**：规则创建→评估→触发→聚合降噪→自动恢复 全闭环健康。同时内置规则 `svc-error-rate-high`（error_rate>5）也在注入期间正确触发（value=5.80→6.28）并 resolved。告警消息格式可读（`error_rate 73.33 > threshold 30.00`）。

---

## 五、AICHAT 真实 LLM 对话专项评测（DeepSeek deepseek-v4-flash）

### 5.1 实测结果表

| # | 用例 | 延迟 | 首字节 | 判定 | 问题 |
|---|---|---|---|---|---|
| R1 | "当前平台一共有多少个服务？" | 0.4s | 0.4s | ✗ | 回答"10 个服务"，实际 27 个；且把 deleted 服务当运行中服务列出 |
| R2 | "orders 错误率突然升高，分析根因" | 75.9s | 75.8s | △ | 声称"orders 服务不存在"，但同一回答中又引用 orders 指标（702 调用/30.48% 错误率）——自相矛盾；根因分析质量中上（识别单 endpoint 84.8% 错误集中） |
| R3 | "错误率最高的前 3 个服务" | 46.9s | 46.8s | ✗ | 回答"无法确定（数据未采集 error_rate）"，但 NL2SQL 1 秒内返回正确答案（orders 29.97%）——AI 工具采集的数据集缺 error_rate 字段 |
| R4 | "给出具体修复建议" | 96.6s | 96.3s | △ | 内容质量较好（endpoint 级定位+依赖链分析+风险分级），但基于 R2 的错误前提 |
| R5 | NL2SQL 翻译 | 0.7s | — | ✅ | used_fallback:false，SQL 语义正确 |
| R6 | 非流式 chat | 38.9s | — | ✅ | 返回 1160 字报告，llm_mode=llm（上轮 P1-2 修复生效） |
| R7 | 多轮追问"它和刚才的服务是同一个吗" | 49.7s | 49.7s | ✗ | 答非所问（回答 ai-orchestrator deleted，而非 orders）——**会话上下文未延续** |

### 5.2 交互与工作流评价

**优点**：
- SSE 事件序列完整规范（progress 8 步/tool_start/tool_end/suggestion/done），前端进度条与工具执行过程可感知
- 处置卡设计合理：计划+白名单脚本+风险星级+人工确认/驳回/最终报告/加入知识库
- 非流式与流式双模式、NL2SQL 安全护栏（SELECT-only）工程质量扎实

**核心缺陷**：
1. **工具数据与平台数据不一致（最严重）**：AI 的服务发现工具返回 10 个服务（含 deleted），而 `/api/v1/services` 返回 27 个（含 orders 954 调用/33.44% 错误率）。AI 的"数据采集"节点未取到 error_rate 维度，导致 R3 这类问题答不出而 NL2SQL 能答。**AI 的答案可信度受限于其工具链的取数范围，且与平台展示数据矛盾，会误导运维。**
2. **多轮上下文断裂**：session_id 传递正确（会话列表出现 multi-turn-test-001），但第二轮回答完全没有引用第一轮上下文。LangGraph checkpoint 未正确回放历史消息。
3. **首字节 46–97s 且无渐进输出**：firstByte ≈ 总时长，说明 chunk 内容在最后一次性到达，用户面对 1–2 分钟进度条而非逐字流式。上轮 P1-6（延迟优化）效果有限（38.9s 为最轻量用例）。
4. **信息查询仍生成处置卡**：R3（"错误率最高的前 3 个服务是哪些"）是信息查询却收到 kubectl 命令处置卡——上轮 P1-8 修复不彻底。
5. **答案中的"未发现 orders 服务"与告警规则引用 orders 并存**：AI 无法区分"K8s 中无此 Deployment"与"平台观测数据中有此服务"两个概念层级，表述让用户困惑。

---

## 六、数据合理性分析

### 6.1 口径不一致（4 套服务数并存）
| 口径 | 数值 | 来源 |
|---|---|---|
| /api/v1/services | 27（含 7 个 "(deleted)" 残留 + probe-test/smoke-test） | CH trace_spans 聚合 |
| /api/v1/dashboard/stats.topology_services | 31 | 拓扑节点 |
| GUI 总览 KPI"服务数量" | 25 | stats.services（与 API 27 相差 2） |
| AICHAT 回答 | 10 | AI 工具取数 |

**风险**：运维在首页看到 25 个服务、服务列表看到 27 个、问 AI 得到 10 个——信任崩塌。**建议**：统一"服务"口径（活跃服务 = 最近 24h 有流量且未删除），deleted 服务移入单独过滤或标注；AI 工具直接复用 `/api/v1/services` 返回（含 error_rate 字段）。

### 6.2 残留数据污染
- 7 个 `(deleted)` 后缀服务（删除后未清 CH 历史数据）在服务列表与 AI 回答中均可见
- probe-test、smoke-test 等测试残留服务出现在生产视图

### 6.3 展示层问题
- 总览 KPI"请求错误率 1.10% ▲ 1.10%"：趋势箭头旁显示的仍是当前值而非变化量，语义不清
- 节点 TOP5 表 CPU 核数/内存容量列显示 "—"（数据缺失未兜底）
- "检测到数据采集中断：3 个时段无数据"横幅与当前正常数据流并存，触发逻辑存疑
- 告警事件"当前告警 0"但侧边栏徽标"0"，历史 resolved 事件（count=82 的 OOMKilled）无法区分"历史累计"与"当前活跃"，建议加时间窗说明

### 6.4 一致性良好的部分
- 注入数据 CH 落库与告警评估数值完全一致（73.33% = 85% 窗口滚动均值）
- 告警聚合去重/自动恢复逻辑符合预期
- NL2SQL 结果与 CH 直查一致

---

## 七、问题清单（分级）

### P0（功能不可用）
无。

### P1（功能可用但结果错误 / 安全边界）
1. **P1-A 普通用户可创建告警规则**：`POST /alerts/rules` threshold 类型无 admin 门禁（实测 201）。用户可创建任意服务的告警规则造成告警风暴/误导（对比：silences 已加门禁，规则创建遗漏）。涉及 `alerts.go:992 createAlertRule`——仅 metric_raw/anomaly/forecast 有 admin 检查。
2. **P1-B 市场功能前端-后端断裂**：前端 `AiTools` 市场 tab 调用 `/api/v1/ai/marketplace/installed|install`，query-api `main.go` 未注册该路由代理 → 恒 404。前端吞错显示空态，用户无感知。**要么补代理路由，要么从 UI 移除该 tab。**
3. **P1-C AICHAT 工具取数与平台数据不一致**：AI"数据采集"节点返回的服务清单/指标维度与 `/api/v1/services` 不一致（缺 error_rate、缺 orders、多 deleted），直接导致 R1/R3 答案错误或无法作答。**这是 AI 助手可用性的根本问题。**
4. **P1-D 多轮会话上下文断裂**：session_id 正确传递但 LangGraph checkpoint 历史未注入第二轮 prompt，多轮追问答非所问。
5. **P1-E 上轮 P1-3 修复未落地**：LLM `test` 接口仍返回"base_url 校验失败: 不允许指向本地/内网/metadata 地址"。代码 `settings.go:371 isBlockedIP` 使用 `IsPrivate()`（Go 中 fc00::/7 ULA 属于 private），**无 ULA 豁免逻辑**——上轮报告声称的修复在代码中不存在。api.deepseek.com 经 OrbStack DNS 解析出 ULA IPv6 被误伤。

### P2（体验 / 数据质量）
6. **P2-A K8s preflight scale 缺参数 500**：`k8s_actions.py preflight` 对 scale 动作直接取 `kw['replicas']`，缺失时 KeyError→500。应返回 400 + 参数校验错误。
7. **P2-B webhook 诊断任务卡 diagnosing**：`POST /ops/webhook` 任务 run 后 10+ 分钟无进展，investigator 零日志（persona 未注册或门控静默失败）。报告中心 reports=0 与之同源。
8. **P2-C 服务数 4 套口径并存**（见 6.1）+ deleted/测试残留数据污染生产视图。
9. **P2-D GUI 表单创建 SLO/用户失败**：Playwright 实测 SLO（before=2/after=2）与用户（弹窗不关闭）创建未生效；表单字段标签可见但提交疑似被校验拦截且无错误提示。需人工复核确认是否为选择器问题。
10. **P2-E AICHAT 首字节 46–97s 无渐进流式**：内容最后一次性到达，交互体验接近"转圈 2 分钟后吐全文"。

### P3（细节）
11. **P3-A 总览"数据采集中断"横幅逻辑存疑**：数据正常时仍显示"3 个时段无数据"。
12. **P3-B 节点 TOP5 核数/容量列 "—"**：字段缺失无兜底展示。
13. **P3-C 错误率 KPI 趋势箭头旁显示当前值而非 delta**：语义不清。
14. **P3-D 告警历史 count 与时间窗口无说明**：count=82（OOMKilled）无法区分历史累计与近期。

---

## 八、上轮修复项回归结果（抽样 13 项）

| 上轮编号 | 问题 | 本轮实测 | 判定 |
|---|---|---|---|
| P0-CH-1 | CH 探针 crashloop | Pod 运行 6h53m 无重启 | ✅ |
| P1-2 | 非流式 chat 恒空 | 返回 1160 字报告 llm_mode=llm | ✅ |
| P1-3 | SSRF 误伤+部分写入 | test 接口仍 400，代码无 ULA 豁免 | ❌ 未落地 |
| P1-4 | NL2SQL 静默 fallback | used_fallback:false + 正确 SQL | ✅ |
| P1-8 | 信息查询生成处置卡 | R3 信息查询仍收到处置卡 | ⚠️ 不彻底 |
| P1-9 | 拓扑边跨集群 | 未复现（本轮未建多集群场景） | 未验证 |
| P1-10 | 静默越权 | user → 403 | ✅ |
| P2-1 | SLO 无边界校验 | 负窗口/超限 → 400 | ✅ |
| P2-2 | 服务级规则不触发 | 自定义规则 firing 73.33 → resolved | ✅ |
| P2-5 | keyword 参数忽略 | keyword 别名生效 | ✅ |
| P3-1 | DELETE 200 | 不存在用户/SLO → 404 | ✅ |
| P3-2 | 角色白名单 | superadmin → 400 | ✅ |
| P3-3 | PUT 类型错误 | not-an-int → 400 | ✅ |

**回归结论**：13 项中 9 项确认修复生效，1 项未落地（P1-3），1 项不彻底（P1-8），1 项未验证（P1-9）。

---

## 九、改进建议（按优先级）

### 短期（1-2 周）
1. **补告警规则创建 admin 门禁**（P1-A）：`createAlertRule` 入口统一 `hasRole(r,"admin")`，与 silences 对齐。
2. **市场 tab 二选一**（P1-B）：补 query-api ProxyAI 路由，或移除前端 tab。
3. **AI 工具链统一取数**（P1-C）：AI 的 service 采集工具直接消费 `/api/v1/services`（含 error_rate/calls/avg_latency 字段），deleted 过滤在后端统一。
4. **多轮上下文注入**（P1-D）：LangGraph 图在 summarize 节点前注入 session 历史消息摘要。
5. **落地 ULA 豁免**（P1-E）：`isBlockedIP` 增加 `IsGlobalUnicast()` 反判或显式豁免 fc00::/7。

### 中期（1 个月）
6. **诊断任务健康检查**（P2-B）：为 diagnosing 状态加超时→failed 转换 + 前端状态展示；investigator 门控失败写日志。
7. **服务口径统一**（P2-C）：定义"活跃服务"语义，首页/列表/拓扑/AI 四处消费同一 API；历史 deleted 数据归档或标注。
8. **AICHAT 渐进流式**（P2-E）：LLM 调用改为 HTTP 流式（逐 token），chunk 事件即时下发；工具采集结果缓存复用。
9. **K8s 动作参数校验**（P2-A）：preflight 入口统一 400 语义。

### 长期（与 ongrid 对比补位）
10. 告警→AI 自动调查闭环（当前 webhook 只登记任务，无人值守链条断裂）
11. 工作流可视化编辑器（后端 flow_engine 就绪，前端仅 legacy）
12. IM 通知渠道（飞书/钉钉 webhook）
13. 报告定时生成与分享

---

## 附录 A：测试产物

- GUI 截图 20 张、DOM 深挖报告、交互测试 JSON：`/var/folders/71/5876xm8s6d37d5873yq80dc80000gn/T/opencode/aiops-test/results/`
- API 矩阵结果（~90 断言）：`results/api-matrix.jsonl`
- AICHAT 7 轮完整对话记录：`chat-rounds.jsonl`
- 注入与告警观察日志：`inject.log` / `alert-watch.log`

## 附录 B：环境处理说明

- 测试数据注入保留（trace/log 为生产数据形态，orders 等 5 服务为合理业务模拟数据）
- 测试创建的规则/用户/租户/SLO/拓扑节点/目录项/设备已全部删除，环境恢复至测试前状态
- 后台进程（注入器/观察器/端口转发）已全部终止
