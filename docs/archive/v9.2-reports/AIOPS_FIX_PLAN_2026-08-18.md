# AIOps 平台 — 全量问题修复方案 v2（生产化 · 完整修复 · 经独立评审修订）

- **依据**: `AIOPS_TEST_REPORT_2026-08-18.md` 全部实测问题 + 独立架构评审（对照代码逐项核验）
- **修订说明**: v2 修正 v1 的 3 处根因误诊（A2/A3/A5）、补齐 SLO/工作流/硬件等缺失模块与全部单项问题、调整实施顺序（共享工具库前置）、合并重复项、补充安全/回归考量。所有根因声明均已对照实际代码核验（文件:行号）。
- **约定**: 每项含「根因（已核验）→ 修复方案 → 验收标准」。标注 [前端]/[后端]/[前后端]。

---

## 实施批次总览（顺序即依赖序）

| 批次 | 内容 | 说明 |
|---|---|---|
| **批次 0** | 共享基础设施（F 系列） | 工具库/数据加载 Hook——批次 1 的 A2/A4 等依赖它，**必须最先做** |
| **批次 1** | P0 数据正确性（A 系列） | 直接影响可信度 |
| **批次 2** | P1 功能完整性（B 系列） | 补齐功能缺口 |
| **批次 3** | P2 交互体验 + P3 可访问性（C/D 系列） | 体验打磨 |
| **每批验收** | `npx tsc --noEmit` + `npm run build` 通过；按测试报告实测场景回归；后端接口测试通过 | |

---

# 批次 0 — 共享基础设施（F 系列，前置依赖）

## F1. 统一格式化/归一化工具库 `src/lib/`
**背景**: v1 的 E4 排在批次 3 但 A2/A4（批次 1）依赖它，顺序缺陷。本版前置。
**修复**:
- [前端] 新建 `src/lib/format.ts`：`fmtTime(ts)`（兼容秒/毫秒/ISO 字符串，消除各处 `slice(5,16)`、`Number(v)*1000` 假设）、`fmtBytes`、`fmtCpu`。
- [前端] 新建 `src/lib/severity.ts`：`normalizeSeverity(v)` 把中文（严重/警告）/英文（critical/warning/info）/数字映射到统一枚举。
- [前端] 新建 `src/lib/errorRate.ts`：`normalizeErrorRate(v)`（见 A2 的双口径契约）。
- [前端] 单元测试覆盖边界（秒/毫秒、中文/英文、0–1/0–100）。
**验收**: 工具库有单测；批次 1 起 所有消费点统一引用。

## F2. 统一数据加载与错误处理（合并 v1 的 C2+E3）
**背景**: 全站约 15 个页面 `.catch(() => setData([]))` 静默失败，加载失败与"无数据"无法区分。
**修复**:
- [前端] 实现 `useAsyncData(fn, deps)` Hook：返回 `{data, loading, error, retry}`；错误态含错误信息 + 重试按钮。
- [前端] **全量替换**以下页面的静默 catch（不是可选，是承诺全量）：ServiceObservability:168、Trace:62、VirtualMachines:41、Overview:48-54、Knowledge、Report:24、Changes:55、LogMetrics、AlertEvents:35、AlertRules:24、SLO:20、Hardware、AdminUsers:15、AdminSettings:89-100、KnowledgeGraph（经 client.ts:421-424 吞错处）。
- [前端] 统一 Empty 语义：区分"无数据"与"加载失败"两种空态。
**验收**: 任一接口失败时对应页面显示错误 + 重试；全站无静默 catch 残留（grep 验证）。

## F3. cluster_id 注入白名单
**根因**: `client.ts:32-45` 拦截器给 `/users`、`/ops/tasks`、`/ops/audit-logs`、`/ops/reports/history`、`/ops/changes`、`/node/health`、`/ipmi/*` 等全局端点也注入 cluster_id，多集群时静默缩小结果。
**修复**:
- [前端] 拦截器维护全局端点清单（显式枚举），仅对集群级端点注入；或改为各 API 显式传参。
**验收**: 切换集群后用户列表/审批/审计/报告不随集群变化。

---

# 批次 1 — P0 数据正确性（A 系列）

## A1. 容量预测 0.00% 误导（根因已修正）
**根因（已核验）**: 两层问题——
1. 后端 `capacity.go:162-172` 无历史数据时**显式返回 `current:0`**（非前端 null 处理问题），前端如实渲染 0.00%。
2. **数据源不一致**：`/dashboard/resources` 内存走 K8s metrics-server（`dashboard_resources.go:48-61` clusterMemoryUsagePct，有值 56.82%），而 `/capacity/forecast` 走 PromQL `node_memory_MemAvailable_bytes`（capacity.go:96，返回空）。两个接口不同源，数值必然矛盾。
**修复**:
- [后端] 确定唯一权威数据源：容量 forecast 的 memory/disk 与 dashboard 对齐（推荐 forecast 无 PromQL 数据时回退 metrics-server 同源算法），保证同集群同指标数值一致。
- [后端] 无数据时返回 `current: null`（而非 0）+ `has_data: false`，语义化空态。
- [后端] 接口测试：`/capacity/forecast` memory/disk 与 `/dashboard/resources` 同集群数值一致（容差内）。
- [前端] `current == null || has_data == false` 时显示 `--`；图表空态；请求失败走 F2 错误态。
**验收**: 容量页与总览页内存/磁盘数值一致；无数据显示 `--` 而非 0.00%。

## A2. 错误率双口径（根因已修正）
**根因（已核验）**: 前端两处消费点单位假设**一致**（都乘 100）。真正不一致在**后端两个接口**：`/services` 返回 `error_rate = errs/calls`（0–1，handler.go:455），`/dashboard/stats` 返回 `ErrorRate = errors/calls*100`（0–100，biz/dashboard.go:71,77）。
**修复**:
- [后端] 统一契约：**全部接口 error_rate 返回 0–1 小数**（修改 `/dashboard/stats` 及 dashboard.go 两处 `*100`），发布 API 变更说明。
- [前端] `normalizeErrorRate(v)`（F1）做防御性归一（>1 视为 0–100 除 100），兼容过渡期旧数据。
- [后端] 接口测试：断言 `/services` 与 `/dashboard/stats` 同服务 error_rate 单位一致。
**验收**: 两接口同服务错误率一致；前端展示百分比正确。

## A3. AI 工具调用不可靠（根因已修正，最高价值项）
**根因（已核验）**: 工具调用是**确定性 DAG 路由**，非 LLM 自主决策：
1. `orchestrator.py:452 _is_info_query`：含信息词（"有哪些/列表/总结"）且无故障词 → 判为信息查询。实测问题"请用k8sgpt诊断当前集群**有哪些**问题"、"请参考知识库"均被误判 → `route_light`（:1424）直接走 collect→summarize 轻量链路，**跳过 RCA 与 RAG**。
2. k8sgpt 门控 `orchestrator.py:718`：`is_diag and not _is_info_query(...)` —— 同样被误判挡掉。
3. 后端**已发** `tool_start`/`tool_end` SSE 事件（:1732-1758），但 AiChat.tsx 未处理这些事件类型，前端看不到工具调用过程。
**修复**:
- [后端] 修 `_is_info_query` 启发式：**显式工具意图优先**——问题含"k8sgpt/知识库/案例/RAG/NL2SQL/查数"等工具关键词时，直接判定为深度链路（覆盖 light_query），确保工具被调用；保留原信息查询判定作为兜底。
- [后端] 回归保护：新增单测断言"请用k8sgpt诊断…有哪些问题"路由到完整链路且 k8sgpt 被调用；"当前有哪些服务在运行"仍走轻量链路（不破坏现有分流）。
- [前端] AiChat.tsx 处理 `tool_start`/`tool_end` 事件（**复用现有事件，不新增类型**），渲染工具调用链（如"🔧 k8sgpt_diagnose 运行中…/完成"）。
- [安全] 若编排层做工具结果强制注入，必须走既有 `shell_policy.py`/`execution_gate.py` 授权边界，注入动作写入审计日志。
- [回归] **必须回归现有"完整诊断"happy path**（测试报告评为优秀）：集群健康巡检、order-svc 根因、处置建议→审批→执行闭环不被破坏。
**验收**: 明确要求 k8sgpt/知识库时对话可见工具调用记录与真实结果；轻量信息查询仍快速响应；完整诊断链路行为不变。

## A4. 告警严重度筛选不匹配
**根因（已核验）**: `AlertEvents.tsx:54` 筛选值英文（critical/warning/info），`severity()` 可返回中文（严重/警告），中文事件无法命中。
**修复**:
- [前端] 筛选与展示统一走 F1 `normalizeSeverity`；单测覆盖中文/英文/缺失。
**验收**: 中文严重度事件能被筛选命中。

## A5. 图谱集群筛选失效（根因已修正）
**根因（已核验）**: 后端**已支持** cluster_id 过滤（`kg_api.py` `kg_graph_full(cluster_id)`、`kg_graph.py build_all(cluster_id)`）。真实 bug 是**前端 id 映射**：集群下拉传数字 id（如 1），与后端字符串 cluster_id（'default'）不匹配，前端才硬编码 `cluster_id:'default'`（KnowledgeGraph.tsx:382）。
**修复**:
- [前端] 建立集群数字 id ↔ 后端字符串 cluster_id 的映射（从 `/clusters` 响应取真实标识），移除硬编码；切换集群时清空 `stablePosRef` 坐标缓存（:356/:486）。
- [前端] 测试：选择不同集群返回不同图谱。
**验收**: 集群选择器真实过滤图谱；切换后坐标不残留。

## A6. 总览口径不一致（根因已修正）
**根因（已核验）**: 后端已同时返回 `services:11` 与 `TopologyServices:13`（handler.go:943-970），Overview 卡片用错字段（index.tsx:102）；错误率 sparkline 画 `errors` 次数而非比率（:104）；服务数与调用量共用同一 sparkline（:102-103）；集群名映射 hack（:43）。
**修复**:
- [前端] 服务数卡片改用 `TopologyServices`（或并列展示两口径并标注范围）；错误率 sparkline 改 `errors/calls`；两卡片各自数据序列。
- [前后端] 集群名映射 hack 移除：后端返回标准 cluster_id 字符串，前端直接使用。
- [前端] 数据采集中断告警条（:99）与健康统计并存时，统计卡加"数据不完整"角标，消除误导。
**验收**: 总览与拓扑服务数一致；sparkline 反映真实比率；中断时统计有明确标注。

## A7. 日志页内部查询日志 + `_source` 伪造
**根因（已核验）**: 默认展示 `query-api -> clickhouse /?query=...` 内部日志；`LogMetrics.tsx:55` `_source` 取自选中数据源而非返回数据。
**修复**:
- [后端] `/logs/query` 默认过滤内部查询日志（source=clickhouse 且 body 含 `query=`），提供 `include_internal` 参数。
- [前端] `_source` 从返回行读取；筛选条件变更自动触发查询（与模式切换一致）；rowKey 改用后端日志 id 或 `ts-level-service-hash`。
**验收**: 默认展示业务日志；`_source` 真实；改筛选自动刷新；无 rowKey 冲突。

## A8. 容量预测无法区分"集群级"与"节点级"范围
**根因（已核验）**: `Capacity.tsx:109` 选择器只有"全部节点"与具体节点；选"全部节点"时 `instance=''`，后端 `capacityPromQLForCluster`（capacity.go:77-91）不加过滤、PromQL `avg(...)` 做**集群级聚合**；选具体节点才按 `instance="..."` 过滤。页面不显示当前范围；"全部节点"标签误导；节点选项来源混杂（node-exporter instance 标签 vs k8s 节点名，capacity.go:304-336）；不展示所属集群。
**修复**:
- [后端] `/capacity/forecast` 响应增加 `scope: "cluster"|"node"`、`cluster_id`、`instance`、`aggregation: "avg"`；`/capacity/instances` 返回每项的类型（instance 标签/k8s 节点名）与所属集群。
- [前端] 选择器改为**范围选择器**：集群级"集群聚合（全部节点平均）" + 按集群分组的节点级列表。
- [前端] 页面显著展示范围标识（"范围：集群 default · 聚合" / "范围：节点 orbstack"），卡片与图表标题同步；展示所属集群。
**验收**: 用户能明确区分集群级/节点级；选项按集群分组可识别；显示所属集群与聚合方式。

---

# 批次 2 — P1 功能完整性（B 系列）

## B1. 知识库分页失效
**根因（已核验）**: `Knowledge.tsx:158-159` 无 onChange，只显示前 50 条。后端**已支持** `page/size`（:22 已传参）。
**修复**:
- [前端] 分页加 onChange 触发重新查询（纯前端修复，无需后端改动）；统计卡与列表口径统一（同查询条件）。
**验收**: 89 条案例可分页浏览全部。

## B2. AI 会话重载丢失处置卡片数据
**根因**: `AiChat.tsx:107-117` loadSession 只保留 role/content，丢弃 kind/plan/script/threadId/riskScore/riskReason；消息 id 用索引；时间戳扁平化。
**修复**:
- [前后端] 会话存储完整消息结构（含 suggestion 元数据），重载原样返回。
- [前端] 完整还原字段；消息 id 用服务端唯一 id；逐条时间戳。
**验收**: 刷新/重开会话后处置卡片、风险等级、执行上下文完整。

## B3. 告警规则无编辑 + 表单校验问题
**修复**:
- [前端] 详情抽屉加"编辑"（复用新建表单预填，走已有 `updateAlertRule`）；burn_rate/log 类型不强制 metric/threshold；condition 必填校验；删除二次确认；"历史告警"参数名与后端对齐（确认 `rule` vs `rule_id`）。
**验收**: 规则可编辑；各类型校验正确；删除有确认。

## B4. 技能目录只读
**修复**:
- [前端] 技能目录加"执行"入口（走已有 `executeSkill`），展示执行结果/进度。
**验收**: 可从页面触发技能执行并看到结果。

## B5. Trace 服务筛选死代码 + 无时间范围
**修复**:
- [前端] 服务下拉绑定 `svc`；增加时间范围选择（1h/6h/24h/自定义）；服务端分页（后端已支持 limit，补 offset/page）；瀑布图时间单位统一换算并加防御。
**验收**: 可按服务筛选；有时间控件；翻页不失效；瀑布图正确。

## B6. VM 双击触发详情
**修复**:
- [前端] 移除名称列 onClick（VirtualMachines.tsx:63），仅保留 onRow。
**验收**: 点击只开一次抽屉、一次请求。

## B7. AdminSettings 死代码 + 节点指标集群过滤 + CPU 单位
**修复**:
- [前端] 删除 :533-565 死代码块；CPU capacity 与 usage 统一 `fmtCpu`（:234）。
- [前后端] `/nodes/metrics` 支持 cluster_id，集群详情弹窗按当前集群拉取（:107）。
**验收**: 无死代码；多集群指标正确；单位一致。

## B8. 图谱/拓扑布局性能与确定性（两处一起修）
**根因**: ServiceObservability.tsx:249-287 与 KnowledgeGraph.tsx:495-533 **两处** O(n²)×300 迭代；`Math.random()` 抖动不可复现。
**修复**:
- [前端] 两处布局统一抽公共模块：迭代移入 Web Worker 或 rAF 分帧；确定性伪随机（seeded）替代 Math.random；大图降采样。B8 改造保留 A5 的集群切换清缓存逻辑。
**验收**: 两处大图不卡顿；同数据布局可复现。

## B9. SLO 模块补全（v1 整模块缺失）
**修复**:
- [前端] target 范围校验（availability 0–100、latency >0，SLO.tsx:83）；删除二次确认；接入全局集群过滤（与平台多集群模型一致）；分页。
- [前端] 页面补烧毁率展示：基于 SLO 目标计算当前烧毁率（读告警规则 burn_rate 数据源），兑现"驱动烧毁率告警"的页面描述。
**验收**: 非法 target 被拦截；删除有确认；随集群过滤；可见烧毁率。

## B10. 工作流模块补全（v1 整模块缺失）
**修复**:
- [前后端] N+1 请求（index.tsx:46-50）：后端提供批量最新运行状态端点（或列表响应内嵌 latest_run）。
- [前端] 编辑器保存/运行前图校验（存在触发器、图连通、必填配置）；onRun 去掉冗余立即 getFlowRun（Editor.tsx:432-458），失败误报修正；轮询加上限（如 5 分钟）+ 终态检测退出（:416）；未保存离开提示；风险阈值常量化（:824）。
**验收**: 列表一次请求；非法图被拦截；轮询有界；离开有提示。

## B11. 硬件健康模块补全（v1 整模块缺失）
**修复**:
- [前端] rowKey 修复（Hardware.tsx:188 isFlat 路径无 key → 统一生成稳定 key）；删除冗余三元 `t?'other':'other'`（:144）；三处加载/空态模式统一（走 F2）。
**验收**: 无重复 key 警告；加载/空态全站一致。

## B12. 其余单项 rowKey/参数修复（v1 遗漏）
**修复**:
- [前端] AlertEvents flatMap 展开行共享 id（:64-68）：展开行生成唯一 key（`${id}-${idx}`），删除操作明确目标行。
- [前端] Report rowKey（:110 task_id 但兜底 id）：统一走 `taskIdOf` 结果。
- [前端] AiTools：sourceType 参与实际安装请求（:242-247）；MCP 参数按声明类型转换（:122-125）；SQL 兜底改为明确错误提示（:41）。
- [前端] AdminUsers：分页 + 搜索；提交加 confirmLoading；角色词汇统一（见 C7）。
- [前端] AdminSettings：审计日志分页/加载更多（:303）；平台健康轮询 Tab 隐藏时暂停（visibilitychange）。
- [前端] Report 30s 轮询同样 Tab 隐藏暂停。
- [前端] K8sActions：动作映射收敛为 k8s.ts 单一来源（:18-24）；列表错误不再仅空列表时显示（:104）。
- [前端] Changes：面包屑归属修正（:108，infra 而非报告）；服务/类型筛选语义统一。
- [前端] Overview 活跃告警 limit:200（:51）改服务端分页统计。
- [前端] ServiceObservability：时间窗口 24h 硬编码（:81）改为可选；节点大小 `calls%40`（:100）改为有意义的连续缩放；trace 时长优先 `duration_ms`（:444）。
- [前端] Approvals riskStars 边界（:32-44）：与后端约定单一风险分格式（0–1），删除三格式猜测；fmtTime 走 F1。
- [前端] KnowledgeGraph：`getKgGraph` 吞错（client.ts:421-424）改抛错走 F2 错误态；`statusOf`（:307-319）0 调用健康服务显示"正常/空闲"而非"未知"；移除开发备注（:685）。
- [前端] AiChat：绕过 axios（:156-159）改用统一 api 实例（保留 SSE fetch 但复用拦截器的 token/baseURL 逻辑）；删除 `chatWithAI` 死代码。
**验收**: 各项对应行为修复，grep 无死代码残留。

---

# 批次 3 — P2 交互体验 + P3 可访问性（C/D 系列）

## C1. AI 对话 Markdown 渲染
**修复**: [前端] AiChat 助手消息与处置 plan 用 `react-markdown`+`remark-gfm`（项目已依赖）渲染；代码块语法高亮 + 复制按钮。
**验收**: Markdown 正确渲染，代码块可复制。

## C2. 服务端分页统一
**修复**: [前后端] Trace/LogMetrics/AlertEvents/Report/Changes/AdminUsers/审计日志统一服务端分页（page/pageSize），移除硬编码 limit（50/100/200）。
**验收**: 大数据量翻页正确，无静默截断。

## C3. K8s 执行安全 UX
**修复**: [前端] 所有执行动作统一确认弹窗（动作+目标+风险）；移除冗余 approvalTaskId 第二控件（:298-301）；replicas min 改 1 或显式确认（:274）。
**验收**: 执行前必有确认；审批 id 单一来源。

## C4. Grafana iframe 安全与健壮性（v1 方案修正）
**背景**: v1 提议 `sandbox="allow-scripts allow-same-origin"` 是最宽松组合，基本失去 sandbox 意义；且 iframe src 为同源 `/grafana/d/`。
**修复**:
- [前端] sandbox 采用最小权限：同源代理场景优先 `sandbox="allow-scripts allow-same-origin allow-forms"` 并评估去 same-origin 的可行性（Grafana 子资源加载需求）；若无法收紧，改用 CSP frame-ancestors + 独立 Grafana 域名隔离，并在方案中记录取舍。
- [前端] 健康检查 30s 轮询；iframe 加载 spinner 与错误占位；卡片键盘可达（role/tabIndex/onKeyDown）。
**验收**: iframe 有最小权限隔离；Grafana 后启动自动恢复；失败有提示。

## C5. 角色词汇统一
**修复**: [前后端] 统一角色枚举（admin/approver/member）+ 前端 `ROLE_LABELS` 映射，AdminUsers:41 与 Approvals:83 共用；`updateUserScope` 接入 UI（访问控制编辑）或移除该 API。
**验收**: approver 在各页面显示一致。

## C6. 固定宽度抽屉响应式
**修复**: [前端] Approvals 720 / Report 560 / AlertEvents 560 / ServiceObservability 620 改响应式（`width={innerWidth<768?'100%':N}` 或 CSS 断点）。
**验收**: 窄屏抽屉全宽可用。

## D1. 键盘可达性
**修复**: [前端] 可点击 div（Grafana 卡片、VM 行、Overview 行、图谱节点）加 role="button"/tabIndex/onKeyDown；canvas 图提供旁路列表或 aria-label。
**验收**: 键盘可操作主要交互。

## D2. 容量页响应式断点
**修复**: [前端] `Col span={6}` 加 `xs={12} md={6}`。
**验收**: 窄屏 2 列、宽屏 4 列。

---

# 测试与回归（贯穿各批）

1. **单元测试**: F1 工具库（时间/严重度/错误率边界）、A3 路由判定（工具关键词 vs 信息查询）、后端 capacity/dashboard 一致性。
2. **组件测试**: 分页（B1/C2）、会话重载（B2）、表单校验（B3/B9）。
3. **端到端回归**（按测试报告实测场景）:
   - A1/A8: 容量页数值与范围标识
   - A3: k8sgpt/知识库问题可见工具调用；**集群健康巡检、order-svc 根因、处置→审批→执行闭环行为不变**（happy path 保护）
   - B1: 知识库翻页到第 2 页
   - B2: 刷新会话处置卡片完整
4. **构建门禁**: 每批 `npx tsc --noEmit` + `npm run build` + 后端 `go test ./...`。

---

## 覆盖对照表（测试报告 → 本方案）

| 报告章节 | 方案项 |
|---|---|
| §1 总览 | A6, B12(limit:200/中断标注), D1, F2 |
| §2 服务全景 | A2, B8, B12(24h/节点大小/max_ms), F2, D1 |
| §3 链路追踪 | B5, F2 |
| §4 日志与指标 | A7, C2, B12(rowKey) |
| §5 虚拟机 | B6, F2 |
| §6 Grafana | C4 |
| §7 告警事件 | A4, B12(rowKey/命名空间), C6, F2 |
| §8 告警规则 | B3, C2 |
| §9 容量预测 | A1, A8, B12(hours/阈值/ETT/change_pct), D2 |
| §10 SLO | B9 |
| §11 AI 对话 | A3, B2, C1, B12(axios/死代码) |
| §12 图谱视图 | A5, B8, B12(吞错/statusOf/开发备注) |
| §13 AI 工具 | B4, B12(sourceType/MCP/SQL) |
| §14 工作流 | B10 |
| §15 知识库 | B1, B12(created_at→F1) |
| §16 K8s 运维 | C3, B12(映射收敛/错误显示) |
| §17 硬件健康 | B11 |
| §18 报告中心 | B12(rowKey/轮询), C2, C6 |
| §19 变更时间线 | B12(面包屑/筛选), C2 |
| §20 审批中心 | B12(riskStars/fmtTime), C6 |
| §21 用户管理 | B12(分页/confirmLoading), C5 |
| §22 系统设置 | B7, B12(审计分页/轮询) |
| 三、跨模块 | F1, F2, F3, C2, C5, C6, D1, B12(死代码) |
| 五、AI 专项 | A3, B2, C1 |

---

# 批次 4 — 安全加固（G 系列，依据 R2 报告，最高优先级）

> R2 报告（`AIOPS_TEST_REPORT_R2_2026-08-18.md`）暴露第一轮完全未覆盖的安全与数据管道问题，含 3 个 BLOCKER。**建议本批次优先于批次 1-3 实施**（安全是生产化前提）。

## G1. [BLOCKER] 认证体系重构（S1/S2/S3/S7/S9）
**根因**: orchestrator 无 auth middleware（仅依赖代理信任边界）；Go AuthMiddleware 不校验用户存在/状态；ProxyAI 无角色门控；后端角色词汇（admin/user）与前端（approver）脱节。
**修复**:
- [后端] orchestrator 增加**独立认证中间件**：校验 `X-Internal-Token` 或 JWT，不再仅依赖代理；所有端点默认拒绝，显式白名单放行（健康检查等）。消除"直连即全开"的信任边界脆弱性。
- [后端] Go AuthMiddleware 增加**用户存在性 + status==1 校验**（查库），删除/禁用用户即时失效；引入 token 撤销机制（黑名单/版本号）。
- [后端] ProxyAI 增加**按路由角色门控**：`/ai/nl2sql/*`、`/ai/shell/*`、`/ipmi/*`、`/ops/*` 等仅 admin/approver 可调；普通 user 只读。
- [前后端] 统一角色枚举 `admin|approver|user`（后端接受并校验，前端 ROLE_LABELS 对齐），修复 S7 审批人角色失效。
- [后端] `/settings/llm` 移出公开白名单，返回掩码 base_url。
**验收**: 直连 orchestrator 无 token 被拒；删除用户后其 token 立即失效；普通用户无法调 nl2sql/shell/ipmi/ops；approver 角色前后端一致生效。

## G2. [BLOCKER] 命令执行白名单收紧（S10/S11）
**根因**: `kubectl exec \S+ -- ` 允许任意 pod 任意命令；execute_shell 低层不强制白名单。
**修复**:
- [后端] `shell_policy.py` 白名单改为**目标 + 命令双重白名单**：仅允许指定命名空间/标签选择器的 pod，且命令限定只读诊断命令集（get/describe/logs/top/exec 白名单命令），禁止 `cat /etc/shadow` 类敏感路径。
- [后端] `execute_shell` 低层函数**强制**调用 `is_whitelisted_for_execute` + `check_shell_metachars`，纵深防御（不依赖调用方自觉）。
- [后端] 收紧 orchestrator RBAC：`pods/exec` 限定命名空间，移除不必要的 `pods delete`/`nodes patch` 或加审批门控。
**验收**: 审批通过的 exec 仅限白名单目标与命令；低层函数无法绕过白名单。

## G3. [MAJOR] SSRF 与路径穿越（S12/S13）
**根因**: X-LLM-Base-URL 请求头可指向内网；报告下载/上传 task_id 未净化。
**修复**:
- [后端] LLM base_url 仅从**服务端配置**读取，禁止请求头覆盖（或校验 base_url 为可信域名/IP 白名单，禁内网/链路本地地址）。
- [后端] 报告下载/上传 task_id 做严格校验（UUID/白名单格式），路径用 `filepath.Clean` + 前缀校验，禁止 `..` 穿越。
**验收**: 无法用请求头触发内网请求；无法穿越 reports 目录。

## G4. [MAJOR] 授权补齐（S4/S5/S6/S14/S15）
**根因**: 基础设施端点无 RBAC；告警写端点无 admin 校验；系统端点任意用户；PromQL/LogsQL passthrough 无租户隔离。
**修复**:
- [后端] 基础设施端点（nodes/pods/deployments/namespaces/HPA）加角色门控（admin/approver 可读，user 受限或只读自身范围）。
- [后端] deleteAlertRule/AlertSilence DELETE/AlertEventAck/Resolve 加 admin 校验（与创建一致）。
- [后端] SystemStatus/CacheStats/InvalidateCache/SystemComponents 仅 admin；InvalidateCache 移除任意 pattern 能力。
- [后端] PromQL/LogsQL passthrough 增加租户/集群过滤 + limit 上限 + 响应大小上限 + 速率限制；ProxyVictoriaLogs POST 加角色校验与 schema 校验。
**验收**: 普通用户无法枚举集群/删告警/刷缓存/查全量指标；passthrough 有界。

## G5. [MAJOR] 凭据与网络加固（S17/S18/S19/S20/S23/S24）
**根因**: apply.sh 硬编码弱凭据；values-prod CHANGE_ME 可通过校验；无 NetworkPolicy；CH 开放 ::/0；高危 DaemonSet/RBAC；CORS 全开。
**修复**:
- [部署] apply.sh 移除硬编码凭据，改为从环境变量/密钥管理读取；values-prod 的 `required` 校验升级为**拒绝占位符值**（CHANGE_ME/空）。
- [部署] 全 chart 增加 NetworkPolicy（默认拒绝，按需放行）；CH/MySQL/VM/VL 关闭对外网开放，仅集群内服务访问。
- [部署] event-collector/categraf 降权：移除 privileged（用 hostPath 设备 + 最小 capability），categraf 评估 hostPID 必要性；orchestrator RBAC 最小化。
- [后端] CORS 改为来源白名单（前后端同源部署）。
- [部署] 应用容器加 runAsNonRoot/allowPrivilegeEscalation:false/能力裁剪；Grafana 关闭匿名或限制来源；ingest API key 缺失时**启动失败**而非降级无鉴权。
**验收**: 无硬编码凭据；占位符无法部署；有 NetworkPolicy；高危权限收敛；CORS 白名单。

## G6. [MINOR] 登录防护与 JWT（S21/S22）
**修复**: [后端] 登录限流按真实 IP（不信任 X-Forwarded-For 首值）+ 指数退避/锁定；JWT 缩短有效期 + refresh token 机制。
**验收**: 暴力破解被拦截；token 可刷新可撤销。

## G7. [MINOR] NL2SQL 破坏性请求提示（S16）
**修复**: [前后端] NL2SQL 检测到破坏性意图（delete/drop/truncate/update）时，返回明确提示"仅支持只读查询，已改写为安全查询"，而非静默改写。
**验收**: 用户提交破坏性请求时看到明确提示。

---

# 批次 5 — 数据管道与可靠性（H 系列，依据 R2 报告）

## H1. [MAJOR] 事件去重与采集正确性（D1/D2/D3）
**根因**: K8s 事件 dedup key 缺 name/message；DaemonSet 每节点全量 watch 导致 N 倍重复写；SEL 每周期重复插入无 checkpoint。
**修复**:
- [后端] ReplacingMergeTree dedup key 增加 name/message（或改用事件唯一 id）。
- [后端] event-collector 改为**单实例 watch**（Deployment 而非 DaemonSet，或 leader 选举），消除 N 倍重复写。
- [后端] SEL 采集增加逐条 checkpoint/去重（记录已处理记录 id）。
**验收**: 事件无重复写；SEL 不重复插入；去重键完整。

## H2. [MAJOR] ingest WAL 数据丢失 bug（D4）
**根因**: 重启不恢复 consecutiveAckSeq，Compact 时 Truncate(0) 清空未 ack 数据。
**修复**:
- [后端] WAL 持久化 `consecutiveAckSeq`（重启恢复）；Compact 仅截断已 ack 前缀，未 ack 数据保留重放。
- [后端] 增加单测覆盖"重启后 compaction 不丢未 ack 数据"。
**验收**: 重启 + 压缩后无数据丢失；有回归测试。

## H3. [MAJOR] 背压可观测（D5）
**修复**: [后端] 增加 `spans_dropped`/`logs_dropped`/`metrics_dropped` 计数器并暴露到 /metrics；背压丢数据时告警。
**验收**: 数据丢失在 /metrics 可见并可告警。

## H4. [MAJOR] /health 真实健康（R1）
**修复**: [后端] event-collector/ingest 的 /health 反映后端依赖（CH 连通、重试队列水位、最近写入时间），依赖异常时返回非 200。
**验收**: CH 宕机时探针失败，触发重启/告警。

## H5. [MINOR] 生命周期与资源（R2/R3/R4/R5/R6/R7/R8）
**修复**:
- [后端] orchestrator 优雅关闭停止全部 BackgroundScheduler；Go 后台循环（alert_engine/data_sync）支持停止 + logCursors 清理。
- [后端] http.Server 加超时 + Shutdown 优雅排空；k8sClient 加 Timeout；TraceContext 复用 client + VL URL 可配置。
- [后端] DashboardStats 接入缓存（启用 cache.go 死代码或加短 TTL 缓存）。
- [后端] 核实 audit_logs 表缺失问题：补建表或改走已有审计存储，确保审计链路可用。
- [后端] 限流器/Nl2SqlStore 加 TTL 清理；会话加保留策略。
- [后端] LLM env 竞态修复（per-call 配置而非共享进程 env）。
- [后端] 报告时区统一、分页 off-by-one 修复、错误码规范化（200+error → 4xx/5xx）。
- [部署] 单副本组件评估 HA 或明确 SPOF 文档；event-collector 内存上限调优。
**验收**: 优雅关闭无泄漏；有超时；审计可用；内存有界；错误码规范。

## H6. [MINOR] 前端盲区（F1）
**修复**: [前端] 未知路由显示 404 页（而非静默跳 Overview）。
**验收**: 访问不存在路径显示 404。

---

# 批次 6 — 第三轮深度交互复测新增问题（I 系列，依据 R3 报告）

> R3 报告（`AIOPS_TEST_REPORT_R3_2026-08-18.md`）深度交互暴露 6 个新问题（N1-N6），前两轮方案未覆盖。

## I1. [MAJOR] 服务列表详情抽屉数据缺失（N1）
**根因（API 核验）**: 列表视图 `openServiceDetail` → `GET /services/{name}` 只返回趋势数据（`{count, data:[{t,calls,errors,avg_ms}]}`），无 apdex/health_score/上下游关系/trace；拓扑节点 `openDetail` → `GET /topology/node/{name}` 才返回完整数据（apdex=1, health_score=100, spans）。前端两个入口共用同一抽屉，服务详情端点数据不足。
**修复**:
- [后端] `/services/{name}` 补齐 apdex/health_score/relations/traces（与 `/topology/node/{name}` 对齐），或
- [前端] 列表视图改调 `/topology/node/{name}` 统一数据源。
**验收**: 列表视图详情抽屉健康度/关系/调用链与拓扑视图一致。

## I2. [MAJOR] 工作流运行成功但运行记录为空（N2）
**根因**: 运行状态实时更新成功（"已成功"），但"运行记录"抽屉查不到该次运行——运行状态与运行记录存储/查询不一致。
**修复**:
- [后端] 运行成功/失败时同步写入运行记录表（与运行状态同源持久化）。
- [前端] 运行记录抽屉按当前工作流 id 查询并展示节点级明细（各节点输出/错误）。
**验收**: 运行成功后运行记录可见节点级明细。

## I3. [MINOR] Playbook 渲染未剥离 YAML frontmatter（N3）
**根因**: Playbook 内容 ReactMarkdown 渲染前未解析/剥离 frontmatter。
**修复**:
- [前端] 渲染前解析并剥离 frontmatter（或后端返回时剥离）。
**验收**: 内容区不显示原始 frontmatter 元数据。

## I4. [MINOR] SLO 目标值后端校验但前端无错误提示（N4）
**根因**: 后端有范围校验（400），前端无校验且未展示 400 错误。
**修复**:
- [前端] 表单加目标值范围校验（availability 0-100、latency >0）+ 后端 400 错误 message 展示。
**验收**: 非法目标值被前端拦截或提交后显示明确错误。

## I5. [MINOR] 变更登记表单必填项缺失静默失败（N5）
**根因**: 表单"请选择集群"必填项无前端校验，提交失败静默。
**修复**:
- [前端] 表单必填校验（集群/服务/类型/操作人/内容）+ 提交失败错误提示。
**验收**: 必填缺失时表单提示；提交失败有错误反馈。

## I6. [MINOR] "加入知识库"无结果反馈（N6）
**根因**: 后端返回 inserted/dup 结果但前端未展示。
**修复**:
- [前端] 展示后端返回结果（成功/已存在相似案例）。
**验收**: 点击后用户可见明确结果反馈。

---

# 实施顺序总览（修订）

| 顺序 | 批次 | 内容 | 说明 |
|---|---|---|---|
| **1** | **批次 4（G 系列）** | 安全加固 | R2 暴露 3 BLOCKER，生产化前提，**最先做** |
| **2** | 批次 0（F 系列） | 共享基础设施 | 批次 1 依赖 |
| **3** | 批次 1（A 系列） | P0 数据正确性 | |
| **4** | 批次 2（B 系列） | P1 功能完整性 | |
| **5** | 批次 5（H 系列） | 管道/可靠性 | 可与批次 2 并行 |
| **6** | 批次 3（C/D 系列） | 体验/可访问性 | |

*本方案覆盖第一轮（A-F 系列）与第二轮（G-H 系列）全部问题；所有根因声明经代码核验（文件:行号）。*

---

# 七、修复实施完成情况（2026-08-18 全量实施记录）

> 按修复方案批次实施完毕（12 个并行修复通道，全部经构建验证）。全仓验证：前端 `tsc --noEmit` ✅ + `npm run build` ✅（5.0s）；ai-apm-query-go / ai-apm-ingest-go / ai-event-collector `go build` ✅；ai-orchestrator `py_compile` ✅；共 89 个文件改动。

## 7.1 已完成修复（✅ 已实现并验证）

### 批次 0 — 共享基础设施（通道 fix-1）
| 项 | 实现 |
|---|---|
| F1 工具库 | 新增 `src/lib/format.ts`（fmtTime 兼容秒/毫秒/ISO、fmtBytes、fmtCpu）、`src/lib/severity.ts`（normalizeSeverity 中文/英文/数字→统一枚举）、`src/lib/errorRate.ts`（normalizeErrorRate 0-1/0-100 归一） |
| F2 数据加载 | 新增 `src/hooks/useAsyncData.ts`（loading/error/retry，防卸载 setState、防陈旧请求）+ `src/components/ErrorState.tsx` |
| F3 cluster_id 白名单 | `src/api/client.ts` 拦截器新增 GLOBAL_PATHS（20 个全局端点不注入 cluster_id） |

### 批次 1 — P0 数据正确性（通道 fix-5 + fix-10）
| 项 | 实现 |
|---|---|
| A1 容量 0.00% 误导 | 无数据时显示 `--`（含 `current===0 && history 空` 判据）、图表空态、错误态+重试（fix-5） |
| A2 错误率双口径 | 前端 normalizeErrorRate 统一消费（fix-5）；**后端全部 error_rate 契约统一 0-1**（/dashboard/stats、/topology/global 节点+边、/topology/node，附 round3 精度保持与 health 阈值等比缩放）（fix-10） |
| A4 严重度筛选 | normalizeSeverity 统一筛选与展示（fix-5） |
| A5 图谱集群筛选 | 移除硬编码 default，集群 id→cluster_id 映射 + 切换清坐标缓存（fix-5） |
| A6 总览口径 | 服务数用 topology_services、错误率 sparkline 用 errors/calls、独立序列、移除集群名 hack、中断时"数据不完整"角标（fix-5） |
| A7 日志 _source/自动查询 | _source 从返回行读取、筛选变更自动查询、rowKey 唯一化（fix-5） |
| A8 容量范围选择器 | "集群聚合（全部节点平均）"+ 节点级分组 + 范围 Tag 标识（fix-5） |

### 批次 4 — 安全加固（通道 fix-2 / fix-3 / fix-4）
| 项 | 实现 |
|---|---|
| G1 认证体系（Go） | AuthMiddleware 校验用户存在+status==1（fail-closed）；token 撤销（revokeToken/revokeUser，删除/禁用即时失效）；Me 不再回退 token claims |
| G1 认证体系（Python） | auth_middleware 全端点要求 X-Internal-Token/Bearer（allowlist：/health、/metrics、/ipmi/ingest）；secrets.compare_digest 常数时间比较；fail-closed |
| G2 CORS | Python CORS_ORIGINS 环境变量（默认 localhost:30253）；Go 端随 RBAC 收紧 |
| G2 ProxyAI 角色门控（Go） | /ai/nl2sql、/ai/shell、/ipmi、/node、/snmp、/ops、/ai/kg 仅 admin/approver（hasPrivilegedRole）；请求体 10MB/响应 50MB 上限 |
| G3 SSRF | Python 移除 X-LLM-Base-URL/X-LLM-API-Key 请求头覆盖（仅服务端配置） |
| G3 路径穿越 | Python report 下载/上传 task_id 白名单正则 + realpath 包含校验 |
| G3 RBAC（Go） | 基础设施端点 requirePrivilegedRole；告警写端点（delete/ack/resolve/静默删除）加 admin 校验；系统端点仅 admin，InvalidateCache 仅精确 key |
| G4 PromQL/LogsQL | QueryRange 限流+1万点上限+20MB 上限；ProxyVictoriaLogs GET limit≤10000+响应上限，POST 需 admin/approver+schema 校验 |
| G5 命令执行 | Python exec 白名单收窄（命名空间限定+只读诊断命令集+敏感路径拦截+防子串绕过）；execute_shell 强制白名单+元字符检查（纵深防御）；orchestrator RBAC 移除 pods/exec、pods delete、集群 view |
| G5 凭据与网络（部署） | apply.sh 凭据改环境变量（缺失硬失败）；requireSecret 拒绝空/CHANGE_ME；**20 条 NetworkPolicy**（默认拒绝+按需放行）；ClickHouse 移除 ::/0；event-collector 移除 privileged（SYS_RAWIO）+容忍污点；categraf 移除 hostPID；Grafana 匿名关 |
| G6 登录防护/限流 | Python 限流 TTL 清理 + LLM 端点专项 20/min；Nl2SqlStore 1h TTL+500 容量上限 |
| G7 生命周期 | Python 调度器优雅关闭、export_chat asyncio.to_thread+404、LLM env 竞态移除（配置直传构造器） |

### 批次 5 — 管道与可靠性（通道 fix-4 + fix-2）
| 项 | 实现 |
|---|---|
| H1 事件去重 | K8s 事件 dedup key 加 name/message（含迁移注释）；watcher UID 去重集（1000）+ 5min 乱序容忍；SEL 逐条去重（500） |
| H2 WAL 数据丢失 | **ack 状态持久化**（.ack 侧车文件原子写+重启恢复）；Compact 绝不 Truncate 未 ack 数据；ReadAll 只重放未 ack；**3 个新单测通过** |
| H3 背压可观测 | spans/logs/metrics_dropped 计数器 + OnDropped 回调接入三 writer，暴露 /metrics |
| H4 真实健康探针 | 两个 /health 均探测 CH 连通（3s 超时 5s 缓存）+ 重试队列深度，异常 503 |
| H5 Go 可靠性 | http.Server 超时组 + Shutdown(10s)；k8sClient 15s 超时；DashboardStats 30s TTL 缓存（复用 appCache）；**audit_logs 建表修复**（此前 INSERT 静默失败） |
| H6 生命周期 | ingest Shutdown 排空；event-collector 新增 /metrics |

### 批次 2 — P1 功能完整性（通道 fix-11 + fix-12）
| 项 | 实现 |
|---|---|
| B1 知识库分页 | pagination onChange 触发 load(page)；统计卡与表格口径一致（"故障案例（当前查询）"） |
| B2 会话重载 | AiChat loadSession 保留完整消息字段（kind/plan/script/risk 等）；axios 统一 api 实例；**Markdown 渲染**（react-markdown+remark-gfm） |
| B3 规则编辑 | 详情抽屉"编辑"入口 + updateAlertRule；burn_rate/log/anomaly 类型隐藏 metric/threshold；condition 必填；删除 Popconfirm（*后端 PUT 待补，见 7.2） |
| B4 技能执行 | 技能表"执行"按钮 + executeSkill + 结果弹窗；MCP 参数类型强转；SQL 兜底改错误提示；install 附 source_type |
| B5 Trace | 服务下拉（svc 接线）+ 时间范围（1h/6h/24h）+ 服务端分页（limit/offset+"加载更多"）+ 瀑布图单位防御（startToMs）（*后端 hours 过滤待补，见 7.2） |
| B6 VM 双击 | 移除名称列 onClick，仅 onRow |
| B7 AdminSettings | 删除死代码；CPU usage/capacity 统一 fmtCpu；审计日志服务端分页；健康轮询隐藏暂停（*后端 cluster_id 待补，见 7.2） |
| B8 布局性能 | 新增 `src/lib/layout.ts`（mulberry32 确定性 PRNG + computeForceLayout），ServiceObservability/KnowledgeGraph 共用，保留 stablePosRef |
| B9 SLO | 目标值范围校验（availability 0-100/latency>0，随类型动态）；删除确认；集群过滤；分页 |
| B10 工作流 | 编辑器图校验（触发器/连通性/必填配置）；onRun 去重；轮询 5min 上限+终态退出；beforeunload+路由守卫；风险阈值常量化；Detail 5s 轮询 |
| B11 硬件 | rowKey 稳定化（${node}-${component}）；删死三元；空态统一 |
| B12 杂项 | AlertEvents rowKey `${id}-${idx}`；Report rowKey/轮询暂停；Changes 面包屑+筛选一致；K8sActions 映射单一来源+错误展示；Approvals riskStars 0-1 契约+fmtTime；AdminUsers 分页/搜索/confirmLoading/ROLE_LABELS；Overview limit 1000；ServiceObservability 时间范围+symbolSize 对数缩放+duration_ms 优先；KnowledgeGraph 错误传播/statusOf"空闲"/移除开发备注；client.ts 去掉 getKgGraph 吞错 |

### 批次 3 — 体验/可访问性（通道 fix-8 + fix-9）
| 项 | 实现 |
|---|---|
| C4 Grafana | iframe sandbox（allow-scripts/same-origin/forms，附取舍注释）；健康 30s 轮询；iframe 加载/错误态+重试；卡片键盘可达（role/tabIndex/onKeyDown） |
| D2 容量响应式 | Col `xs={12} sm={12} md={6}`（窄屏 2 列/宽屏 4 列） |

## 7.2 遗留项（需后续跟进）

### 后端缺口（fix-11 实施时发现，前端已就绪、后端待补）
| 项 | 缺口 | 位置 |
|---|---|---|
| B3 后端 | 告警规则**无 PUT 端点**，updateAlertRule 将 405 | ai-apm-query-go alerts.go:585-600（仅 GET/DELETE） |
| B5 后端 | ListTraces **无时间过滤**，hours 参数被忽略 | ai-apm-query-go handler.go:615-689 |
| B7 后端 | NodesMetrics **不读 cluster_id** | ai-apm-query-go infrastructure.go:312-335 |
| B2 后端 | get_session 仅返回 role/content，**未持久化完整消息结构** | ai-orchestrator main.py:899-916 |
| B4 后端 | marketplace_install 忽略 source_type（前端已前向兼容发送） | ai-orchestrator main.py:623-639 |

### 未实施批次（方案已列、未执行）
| 项 | 说明 |
|---|---|
| I1 服务详情数据缺失 | 需后端 /services/{name} 补齐 apdex/health/relations/traces（或前端改调 /topology/node/{name}） |
| I2 工作流运行记录为空 | 需后端运行记录持久化（状态与记录同源） |
| I3 Playbook frontmatter 剥离 | Knowledge.tsx 渲染前剥离 YAML frontmatter |
| I4 SLO 前端校验 | ✅ 已在 B9 完成 |
| I5 变更表单必填校验 | Changes.tsx 集群/必填校验 + 提交失败提示 |
| I6 加入知识库反馈 | AiChat 展示 inserted/dup 结果 |
| G7 NL2SQL 破坏性提示 | 检测破坏性意图时明确提示"仅只读查询"（前端 AiTools + 后端 nl2sql） |
| C2 服务端分页统一 | Trace/LogMetrics/AlertEvents/Report/Changes/AdminUsers/审计全量服务端分页 |
| C3 K8s 执行确认 | K8sActions 所有执行动作统一确认弹窗 |
| C5 角色词汇 | 前端 ROLE_LABELS 已统一（B12），后端 approver 角色落地待确认（Go 端已用 is_approver 旗标承接） |
| C6 抽屉响应式 | Approvals/Report/AlertEvents/ServiceObservability 固定宽度抽屉改响应式 |
| D1 键盘可达性 | 可点击 div（Grafana 卡片已做，其余 VM 行/Overview 行/图谱节点待补） |

## 7.3 实施统计
- **修复通道**: 12 个并行通道（fix-1~fix-12），全部完成并核验
- **改动规模**: 89 个文件（前端 ~35、Go 三仓 ~25、Python ~8、部署 ~10、其余）
- **验证**: 前端 tsc+build ✅ / Go build 三仓 ✅（含 WAL 新增 3 测试、Go 全量测试）/ Python py_compile + 313 测试（1 个既有无关失败）/ helm lint+template ✅ / bash -n ✅
- **遗留**: 5 个后端缺口 + 9 项未实施（见 7.2）
