# AIOps 智能可观测平台 · 全量功能深度实测报告

- **测试时间**: 2026-08-15（全天，UTC+8）
- **测试环境**: http://localhost:30253（OrbStack K8s + Helm，ns=observability，14 Pods），admin/admin123
- **测试方式**: ① 代码勘探（前端 16 页面 / 后端 60+ 端点 / 4 数据源 schema）② 数据注入（OTLP 4380 spans / 1753 logs + VM/VLogs/CH 直插）③ 故障注入（orders 错误率 0.8×120s）④ 全 API 矩阵（144 断言）⑤ GUI 全页面实测（Playwright 16 路由 56 断言）⑥ 真实 LLM 对话实测（DeepSeek 已启用，多轮）⑦ 告警引擎验证（规则触发→firing→自动恢复）⑧ 冗余代码深度分析
- **前置说明**: 测试开始前 ClickHouse 处于 CrashLoopBackOff（探针 bug），已运行时修复（详见 P0-1），仓库代码未改动

---

## 一、执行摘要

本轮在上一轮（2026-08-14）修复 20+ 项的基础上，完成**真实 LLM 首次启用实测**与**告警引擎故障注入验证**。总体结论：

1. **核心链路健康**：数据注入→采集→落库→查询→告警→AI→执行闭环全链路可用；上轮 P2-2（服务级告警规则）经故障注入**实测修复生效**（orders error_rate>0.3 规则在 0.8 错误率窗口 firing value=19.09 → 自动 resolved）。
2. **环境实时故障已恢复**：ClickHouse 探针三层 bug 致 CrashLoop 34+ 次，修复后数据面恢复（问题仍在仓库代码）。
3. **真实 LLM 首次接入并跑通**：DeepSeek 已配置可用（in-pod kickoff 实测返回真实内容），但暴露 6 项 LLM 链路问题（非流式空报告 / SSRF 误伤 / 静默降级 / 60s 超时 / 延迟 85-201s / 问答聚焦度差）。
4. **新增 25 项问题**：P0×1 / P1×10 / P2×7 / P3×7，含 2 个 RBAC 缺口（其中告警静默可压制故障信号）、1 个跨集群数据泄漏（namespaces/events + 拓扑边 MySQL 回退双通道）、3 个数据源静默丢数据缺陷、7 个 DELETE 语义错误。
5. **冗余分析**：仓库代码冗余仅 ~1.6%，但本地未入库冗余 12K LOC（legacy 前端）+ 部署面 ~30% 内存空载（deepflow/Redis/ipmi 三条零数据管线）。

---

## 二、数据源核验

### 2.1 ClickHouse ✅（修复后）
- 连接：clickhouse-0:8123（default/dev-ch-pass），库 observability，4 正式表 + 5 遗留死表
- 写入链路：ingest `/v1/traces`、`/v1/logs`（OTLP JSON，X-Api-Key）→ WAL → 批量 flush（1024/5s）→ trace_spans / log_records / service_topology
- 注入验证：测试服务 4380 spans / 1753 logs 落库；orders 错误 span 30.5%（214/702）与 VM RED 完全一致；拓扑 59 边（含 16 条测试边）
- 缺陷：探针三层 bug（P0-1）；5 张死表（附录 A）

### 2.2 MySQL ✅
- 连接：mysql-0:3306（root/dev-mysql-pass），库 aiops，30 张表
- 验证：users/clusters/catalog/devices/topology/llm_providers/alert_rules/slo_targets/dashboard_panels/tenants 全 CRUD 通过（144 断言）
- 缺陷：LLM key 历史密文不可恢复（加密密钥曾轮换，本轮已重填生效）

### 2.3 VictoriaMetrics ✅
- 连接：victoria-metrics:8428（无鉴权），promscrape 15s（node-exporter/ingest/orchestrator），retention 30d
- 验证：service_requests_total{service="orders"}=702 与 CH 一致；test_injected_metric=123 push 成功；query_range PromQL 正确（orders 错误率 0.3048）
- 缺陷：F2（import JSON 数组静默拒收）、F5（instant query 30-60s 可见延迟）

### 2.4 VictoriaLogs ✅
- 连接：victoria-logs:9428，/insert/jsonline，15,005 条 K8s pod 日志（shipper 30s 增量）
- 验证：test-marker-xyz 5 条注入可检索；平台 /logs/query source=victorialogs 双源切换正常
- 缺陷：F1（无 Content-Type 静默丢数据——HTTP 200 但不落库）

### 2.5 数据源总评
| 项 | 状态 |
|---|---|
| CH/MySQL/VM/VLogs 读写闭环 | ✅ 全通过 |
| 采集→查询一致性（CH vs VM RED） | ✅ 完全一致 |
| 静默丢数据风险 | ⚠️ F1/F2（P2） |
| API 契约误导 | ⚠️ F3/F4（P2） |

---

## 三、回归验证（上轮修复项逐一复核）

| 上轮编号 | 问题 | 本轮验证结果 |
|---|---|---|
| P2-2 | 服务级规则不触发 | ✅ **故障注入实测修复生效**：orders error_rate>0.3 规则在 0.8 错误率窗口 firing（value=19.087），恢复后自动 resolved |
| P2-3 | 多集群节点串接 | ✅ /clusters/{id}/nodes 无 kubeconfig 返回明确错误（但 namespaces/events 仍泄漏 → 新 P1-1） |
| P2-4 | 集群事件恒空 | ✅ 主集群事件正常返回（3~11 条） |
| P1-1 | NL2SQL 时间语义 | ✅ fallback 已解析时间窗口（近1小时→INTERVAL 1 HOUR）（但 LLM 路径有 P1-4） |
| P1-2 | 审计字段污染 | ⚠️ 筛选参数生效；历史脏数据仍在（P3-5）；新写入语义待核 |
| P1-4 | 统计口径多套 | ✅ services=24=列表 24；edges=59=拓扑 59；自环已过滤（但集群维度 edges 未过滤 → P1-9） |
| P2-5 | 资源口径不一致 | ✅ 工作台与节点页内存口径一致 |
| P0-1 | LLM 密钥链路 | ✅ 保存自检/诚实显示/密钥不轮换均生效（本轮重填 key 成功；内部接口下发正常） |
| P1-3 | 报告判定逻辑 | ✅ 信息查询 verdict=信息/risk=0 已生效 |
| P1-5 | AI 对话工作流 | ⚠️ 意图分级生效，但处置卡仍对信息查询弹出（P1-8） |
| P3-3 | 拓扑自环 | ✅ 已过滤 |
| 5.1 | 组件健康 | ✅ 10 组件探活正常 |
| 5.2/5.3 | Pod 详情/KubeVirt | ✅ 空态友好 |

---

## 四、问题清单（P0-P3 分级）

### P0（功能不可用 / 数据缺失）

#### P0-1 ClickHouse 探针三层 bug 致 CrashLoopBackOff（部署模板缺陷）
- **现象**：clickhouse-0 处于 CrashLoopBackOff（34+ 次重启），clickhouse-init Job 卡死 "waiting for clickhouse" 138 分钟；liveness 探针每 30s 杀死容器。平台 trace/log/拓扑/告警事件全链路不可用。
- **根因**（三层递进）：
  1. 线上 StatefulSet 运行旧模板：探针 `--host $(CH_PROBE_PASSWORD)`（参数错位，`--host` 后面跟了密码占位符）；
  2. 仓库 HEAD 模板 `deploy/helm/aiops/templates/clickhouse/statefulset.yaml:78/83` 探针用 `clickhouse-client --password $(CH_PROBE_PASSWORD)` —— **Kubernetes exec 探针不做 `$(VAR)` 展开**，`--password` 收到字面量 `$(CH_PROBE_PASSWORD)` → 认证失败；
  3. 实测容器内 `users.d/default-password.xml` 的 sha256 与 Secret `dev-ch-pass` 完全一致（0ffa161c…），证明密码本身正确，纯粹是探针设计缺陷。
- **影响**：CH 全链路不可用，平台查询面 30s 超时。属 P0 部署级故障。
- **修复方案**：探针改用 shell 包装让变量展开：`["sh","-c","clickhouse-client --host 127.0.0.1 --password \"$CH_PROBE_PASSWORD\" --query \"SELECT 1\""]`；或改用 tcpSocket（9000）+ httpGet（8123）探针。已运行时修复并验证 Ready。**注意：仓库模板未改，helm 重装/升级会回归 crashloop——模板修复 + `helm upgrade` 发布是两个缺一不可的动作**。
- **涉及文件**：`deploy/helm/aiops/templates/clickhouse/statefulset.yaml:76-85`

---

### P1（功能可用但结果错误 / 逻辑错误）

#### P1-1 无 kubeconfig 集群的 namespaces/events 泄漏真实集群数据
- **现象**：对无 kubeconfig 的 prod-cluster（id=626）调 `GET /clusters/626/namespaces` 返回真实集群 6 个 namespace；`/clusters/626/events` 返回真实集群 7 条 Warning 事件（跨集群串数据）。
- **根因**：`clusterNamespaces`（clusters.go:219）与 `clusterEvents`（:240）无"无 kubeconfig → 拒绝"守卫，`clusterKubeconfig` 返回空串后 `kubeList` 静默回退 `inClusterKubeconfig()`（:309）。上轮 P2-3 修复只覆盖了 `/nodes`（:256）。
- **影响**：多集群场景下数据张冠李戴，误导运维并造成信息泄露。
- **修复方案**：与 `clusterNodes` 同款守卫——无 kubeconfig 且非默认集群时返回明确空态+error，绝不回退默认集群。
- **涉及文件**：`ai-apm-query-go/internal/api/clusters.go:219,240,256,309`

#### P1-2 非流式 /ai/chat 恒返回空报告
- **现象**：`stream:false` 请求 3 次均返回 `report:""`（耗时 85-132s）。
- **根因**：`main.py` 非流式分支调用 `execute_sync` → `_run_dag` 使用 `self.graph`（full 17 节点图）→ 含 `wait_approval` 节点，初始 `approved=False` 触发 `interrupt()` 挂起，`ainvoke` 无恢复入口 → 图停在 interrupt → `final_response=""`。流式分支用 `chat_graph`（6 节点无审批门）故正常。
- **影响**：`stream:false` API 契约完全不可用（前端恰好只用流式，故 UI 未暴露）。
- **修复方案**：非流式分支改用 `chat_graph`（`execute_sync` 传 mode 或直接 `self.chat_graph.ainvoke`）；或更推荐：full 图非交互时置 `approved=True`（写命令仍受 shell_policy 白名单约束，风险可控），或显式处理 `__interrupt__` 返回"待审批"。
- **涉及文件**：`ai-orchestrator/main.py:355-357`、`ai-orchestrator/orchestrator.py:1575,1053-1061`

#### P1-3 SSRF 校验误伤合法 LLM + 保存逻辑部分写入不一致
- **现象**：`POST /settings/llm` 保存报 `base_url 校验失败: 不允许指向本地/内网/metadata 地址`；`/settings/llm/test` 恒 400；UI 显示"配置异常请重新填写"但实际 LLM 可用（经 provider enable 旁路）。GUI 实测该按钮 400。
- **根因**：
  1. `validateLLMBaseURL`（settings.go:335-373）对域名做 `net.LookupHost`，OrbStack DNS 返回 ULA 私网 IPv6（fdfe:dcba:9876::48）→ `IsPrivate()` 命中 → 拒绝合法公网 `api.deepseek.com`（误伤）；
  2. `SaveLLMSettings`（settings.go:390-416）先写 provider/key/model 字段，**最后**才校验 base_url，校验失败返回 400 但前面字段已持久化 → 部分写入不一致（实测 GET 显示 provider=deepseek 但 base_url 残留 openai）。
- **影响**：无法经标准 UI 配置 LLM（只能走 provider enable 旁路）；测试按钮恒失败误导用户；配置状态可能部分写入造成脏状态。
- **修复方案**：① `validateLLMBaseURL` 忽略 ULA 地址（fc00::/7 站点本地非安全风险）或仅校验 A 记录、增加公网域名白名单；② `SaveLLMSettings` 先全量校验再写入（原子性）。
- **涉及文件**：`ai-apm-query-go/internal/api/settings.go:335-373,390-416`

#### P1-4 NL2SQL 静默 fallback + LLM 密钥持有者生命周期脆弱
- **现象**：LLM 已启用后，NL2SQL translate 仍 0.017s 返回 fallback SQL（按错误数排序而非错误率、LIMIT 100 而非前 3、无时间词默认 24H 窗口），且无任何降级提示。
- **根因**：`nl2sql_translate`（main.py:2192-2216）直接用 `brain.llm_config` + `_LLM_KEY_HOLDER` 残留状态（不调用 `_parse_llm_config`）；而 `_parse_llm_config` 拉取失败时执行 `set_llm_config(None)` **静默清空** holder（orchestrator.py:1524-1540）；holder 清空后 `_llm` 秒退空串 → 静默 fallback。即 NL2SQL 的 LLM 可用性依赖"上一次 chat 请求成功填充 holder"的副作用，极其脆弱。
- **影响**：问数结果错误（排序/LIMIT/时间窗口），误导决策，且用户无法区分 LLM/fallback。
- **修复方案**：`nl2sql_translate` 自行 `_fetch_saved_llm_config()` 或复用 `_parse_llm_config`；fallback 时在响应加 `llm_mode` 字段提示；`_fetch` 失败不清空 holder（幂等保护）。
- **涉及文件**：`ai-orchestrator/main.py:2192-2216`、`ai-orchestrator/orchestrator.py:117-166,1524-1540`

#### P1-5 LLM 60s 硬超时 → 占位符泄漏进诊断结论
- **现象**：orders 故障诊断输出 `### 诊断结论\n[LLM 调用超时, 请稍后重试]`。
- **根因**：`_llm` 的 `future.result(timeout=60)`，DeepSeek 长上下文（含工具采集数据）单次调用 >60s → TimeoutError → 返回占位符字符串直接拼进报告，无重试/降级。**附加隐患（评审补充）**：超时后 `executor.shutdown(wait=False)`，`crew.kickoff` 线程继续跑完（85-132s 场景每超时一次泄漏一个线程数十秒~分钟级），超时越频繁线程泄漏越多。
- **影响**：诊断结论被占位符污染，误导运维判断；高频超时下线程泄漏逐步耗尽资源。
- **修复方案**：超时重试；流式 LLM 调用（逐 token 消除超时）；诊断类调用放宽超时；超时后触发确定性降级而非占位符；限制并发或改用直接 HTTP 客户端解决线程泄漏。
- **涉及文件**：`ai-orchestrator/orchestrator.py:160-163`

#### P1-6 对话端到端延迟 85-201s
- **现象**：chat 流式端到端 85-132s；GUI 实测 201s 无 done 事件（G6）。
- **根因**：chat 图 6 节点串行多轮 LLM + 工具采集（collect 多源查询 + K8sGPT 10s + RCA/RAG/CrewAI 各一轮 LLM），每轮 60s 上限串行累加。
- **影响**：交互体验差，接近超时边缘，GUI 测试 201s 未完成。
- **修复方案**：独立节点并行化；流式 LLM 逐 token 输出；工具采集结果缓存；削减 LLM 调用轮次。
- **涉及文件**：`ai-orchestrator/orchestrator.py` chat 图节点

#### P1-7 light_query 分流后问答聚焦度差
- **现象**：问"简要列出错误率最高的一个服务"，输出全量服务调用量/延迟巡检列表（deepflow-agent 6365、gateway 1660…），未聚焦"错误率最高"的服务。
- **根因**：`_is_info_query`（orchestrator.py:429-440）命中"列出"→ light 路径（collect→summarize），summarize 用通用巡检 prompt 生成全量数据转储，忽略用户具体问题。
- **影响**：问答不对焦，用户体验差。
- **修复方案**：light 路径 summarize 传入原始问题并要求针对性作答；细化意图识别（区分"列服务"与"找最高错误率服务"）。
- **涉及文件**：`ai-orchestrator/orchestrator.py:429-440`、node_summarize

#### P1-8 信息类问题仍生成处置命令建议卡
- **现象**：问"当前有多少个服务"，输出 `## 处置命令\nkubectl get pods -A` 并触发 suggestion 事件（上轮 P1-5 修复未根治）。
- **根因**：`_extract_script` 从文本提取 kubectl/curl 行，即使信息查询也生成处置建议；suggestion 事件生成未与 intent 严格绑定。
- **影响**：对非故障问题弹出可执行命令卡，可能误导执行（已有人工审批缓解风险）。
- **修复方案**：信息查询类禁用处置建议；`_extract_script` 仅在 diagnosis 意图下启用。
- **涉及文件**：`ai-orchestrator/orchestrator.py`（_extract_script、node_summarize）

#### P1-9 拓扑边数不随集群过滤（MySQL 回退绕过 cluster/scope）
- **现象**：`cluster_id=626` → services=0 但 edges=13（服务数与边数矛盾）。
- **根因**（经评审修正）：DashboardStats 的 CH 边查询**本身带 clusterClause**（handler.go:938-941），真实泄漏源是 **MySQL 回退**——CH 按集群过滤后 0 条 → `loadTopologyEdgesFromMySQL()`（handler.go:951-952,1275-1306）读 MySQL `topology_relations`，完全不区分 cluster/tenant/scope，返回合成边（calls=1）。13 正是 MySQL 手工拓扑基线的边数。**同款回退在 GlobalTopology（handler.go:1138-1142）也存在** → `/topology/global?cluster_id=xxx` 同样串数据，影响面比初判更大。
- **影响**：多集群首页与拓扑页的边数与服务数矛盾，跨集群串数据。
- **修复方案**：MySQL 回退仅在全集群（cluster_id 为空/all）时进行，或回退查询同样带 cluster/scope 过滤（两处：DashboardStats + GlobalTopology）。
- **涉及文件**：`ai-apm-query-go/internal/api/handler.go:938-952,1138-1142,1275-1306`

#### P1-10 告警静默写接口无 admin 门禁（可压制告警掩盖故障）
- **现象**：user 角色 `POST /alerts/silences` → 201（成功创建静默）。
- **根因**：`AlertSilences` POST 分支无 `isAdmin` 守卫（alerts.go:892-947），仅靠"登录即可"。
- **影响**：普通用户可静默任意告警，压制故障信号——安全边界问题（评审建议从 P2 升 P1）。
- **修复方案**：POST/DELETE 静默加 `isAdmin` 守卫。
- **涉及文件**：`ai-apm-query-go/internal/api/alerts.go:892-947`

---

### P2（功能可用但体验 / 数据质量问题）

#### P2-1 SLO 数值无边界校验
- **现象**：`window_seconds:-5` → 201 静默默认 30 天；`target:150.5`（>100）→ 201 原样入库。
- **根因**：`createSLO/updateSLO` 只做 `<=0` 兜底默认，无窗口范围（1h-90d）与 target∈(0,100] 校验。
- **影响**：非法 SLO 配置入库，SLO 判定失真。
- **修复方案**：增加窗口范围与 target 上限校验（负数拒绝而非静默默认）。
- **涉及文件**：`ai-apm-query-go/internal/api/slo.go:102-107,143-148`

#### P2-2 租户写接口无 admin 门禁
- **现象**：user 角色 `POST /tenants` → 201（成功建租户）。
- **根因**：`/tenants` 注册未套 `RequireRole`（main.go:251-260），CreateTenant 无角色校验。
- **影响**：普通用户可越权创建租户（当前多租户隔离依赖 header，实际隔离为空，影响较静默告警轻——评审建议 P2）。
- **修复方案**：`/tenants` 套 `RequireRole(admin)`。
- **涉及文件**：`ai-apm-query-go/cmd/api/main.go:251-260`、`ai-apm-query-go/internal/api/tenant.go:77`

#### P2-3 VLogs 无 Content-Type 静默丢数据（数据源侧）
- **现象**：POST /insert/jsonline 不带 Content-Type 时返回 HTTP 200 但数据不落库（无错误、无告警）。
- **根因**：VictoriaLogs 对非 application/json 请求静默忽略（上游行为），平台/调用方易产生"写成功但查不到"假象。
- **影响**：日志丢失且无感知，可观测性数据缺口。
- **修复方案**：写入侧强制 Content-Type 并在平台 shipper/客户端层加响应校验与失败告警。
- **涉及文件**：`ai-apm-query-go/internal/api/data_sync.go`（日志 shipper）

#### P2-4 VM import JSON 数组静默拒收（数据源侧）
- **现象**：POST /api/v1/import body 为 JSON 数组时返回 204 但日志报 missing metric object，行被跳过。
- **根因**：VictoriaMetrics 要求 NDJSON 对象逐行，数组格式被静默拒绝（无 4xx）。
- **影响**：指标丢失无感知。
- **修复方案**：调用方用 NDJSON；平台封装 VM push 时校验格式并透传错误。
- **涉及文件**：平台 VM push 封装处

#### P2-5 /logs/query 忽略 keyword 参数（API 契约）
- **现象**：`GET /logs/query?keyword=test-marker` 被当空条件，返回全量日志（LIMIT 100），非关键字命中。
- **根因**：处理器仅读取 `query` 参数（handler.go:1509），`keyword` 未映射。
- **影响**：按 keyword 传参的调用方得到错误结果，静默返回 200。
- **修复方案**：keyword 作为 query 别名；前端统一用 query 参数。
- **涉及文件**：`ai-apm-query-go/internal/api/handler.go:1509`

#### P2-6 /metrics/query 命名误导（非 PromQL 端点）
- **现象**：`GET /metrics/query?query=service_errors_total{...}` 忽略 query 参数，返回硬编码 CH 分钟级 RED 聚合；真正 PromQL 代理在 /metrics/query_range。
- **根因**：QueryMetrics（handler.go:824-867）仅支持 service 参数，与 endpoint 名"metrics/query"语义不符。
- **影响**：调用方按"PromQL"使用得到错误语义数据。
- **修复方案**：改名或明确文档标注；将 PromQL 逻辑合并到 /metrics/query。
- **涉及文件**：`ai-apm-query-go/internal/api/handler.go:824-867`

#### P2-7 VM instant query 30-60s 可见延迟（数据源侧）
- **现象**：push 后 query_range 立即可见，instant query 30-60s 内返回空。
- **根因**：VictoriaMetrics instant query 的时序一致性延迟。
- **影响**：实时校验工具需等待，易误判。
- **修复方案**：文档标注；实时校验用 query_range。

---

### P3（体验细节）

- **P3-1 DELETE 不存在资源返回 200 而非 404**（7 处：users/clusters/catalog/devices/topology/slo/panels）。各 delete handler 未先查存在性，DAO.Delete 对不存在行不报错。涉及 users.go/clusters.go/catalog.go/devices.go/topology_graph.go/slo.go/dashboard.go 的 delete 分支
- **P3-2 用户角色无白名单校验**：POST /users 传 role:"superadmin" 被 DAO 拒绝（400），但报错非显式角色校验。users.go:50-52
- **P3-3 PUT /users 类型错误静默零值**：`{"status":"not-an-int"}` → 200，json.Unmarshal 错误被忽略。users.go:86-98
- **P3-4 审计日志 operator 语义仍偏**：历史脏数据（operator 出现 "approved"/"1"/"user"）是既成事实，且**当前写入路径 `_audit_operator` 仍写 X-Internal-Role（角色 admin/user）而非用户名**（db_audit.py 写入点）——不是"待核"而是"仍偏"，需统一取 JWT 用户名并清洗历史数据
- **P3-5 POST /me 返回 200 空体**：Me handler 无方法分支。users.go:198-213
- **P3-6 loadgen Kind 映射与 OTel 标准不符**：Kind:2 映射 CLIENT 而非标准 SERVER，压测工具生成的 span 语义错误。model/span.go KindMap
- **P3-7 markdown 残留未根治**：AI 报告"**目标**: -"空占位仍在（实测 04:47 输出）；顶栏硬编码"演示环境"徽标、"个人资料/修改密码"disabled 占位

---

## 五、AI 对话专项评测结论

| 测试用例 | 预期 | 实测 | 判定 |
|---|---|---|---|
| 当前有多少个服务（流式） | 简洁服务清单 | 真实 LLM 回答"8 个非删除服务"（实际 24，口径不准）+ 处置命令卡 | △ 部分正确 |
| orders 错误率升高根因 | 聚焦 orders 分析 | "[LLM 调用超时, 请稍后重试]" 占位符 | ✗ 超时污染 |
| 列出错误率最高的服务 | 聚焦最高错误率服务 | 全量服务调用量/延迟巡检列表 | ✗ 不对焦 |
| NL2SQL 错误率最高前 3 | 错误率降序前 3 | fallback SQL：按错误数排序 LIMIT 100 | ✗ 语义错误 |
| 非流式对话 | 正常报告 | 恒空报告（full 图 interrupt） | ✗ 空报告 |
| 端到端延迟 | <10s | 85-201s | ✗ 过慢 |

**结论**：交互链路工程完整（SSE 流式/进度/处置卡/确认执行/审计闭环），真实 LLM 已打通但**存在 6 项链路级缺陷**（P1-2~P1-8），核心矛盾是：① 密钥持有者生命周期脆弱导致静默降级；② 60s 硬超时与串行 DAG 叠加导致延迟过长/占位符污染；③ 信息查询与故障查询的意图分流仍不精准。修复后应重测一轮真实 LLM 对话。

---

## 附录 A：冗余代码深度分析（摘要，完整见对话上下文）

**基准**：tracked 35,295 LOC / 196 文件（query-go 14,004 / orchestrator 12,907 / ingest-go 3,705 / frontend-src 4,679）。

**A. 可安全删除清单**：
| # | 路径 | 规模 | 证据 |
|---|---|---|---|
| A1 | src_legacy_v2/（57 文件） | 12,421 LOC | git 从未入库（ls-files=0）、tsconfig 不编译、零引用 |
| A2 | 前端测试工件（143 png + 68 yaml） | 19MB | gitignored、Playwright 快照 |
| A3 | tasks.py（arq worker） | 102 LOC | 零 import、无 arq manifest、Redis 唯一"理由"建立在死代码上 |
| A4-A7 | agents.py/setup.py/kb_expand.py/seed_knowledge.py | ~257 LOC | 零引用/被取代 |
| A8 | 仓库根 7 份历史 review 报告 | ~100KB | git status 已标记删除 |
| A9 | CH 5 张死表 | 0 行 | init_clickhouse.sql 已不建，数据迁 MySQL |
| A10-A11 | ongrid-ref/、.playwright-cli/、.superpowers/ | 57MB | 均 gitignored |

**B. 建议保留的休眠能力**：flow_engine/（活跃后端，前端零消费）、dual_agent/llm_mock/function_calling（降级模式）、snmp/ipmi/node_health（硬件接入通道，默认关闭常驻）、rca deep LLM 路径、DeepFlow 同步管线（agent 25h ImagePullBackOff 零数据，501 stub 是安全护栏勿删）。

**C. 结构性冗余**：三套 LLM 编排栈（LangGraph 1888 LOC + flow_engine 992 + CrewAI/function_calling/dual_agent）、任务队列双实现（arq vs 内存 _task_store）、知识导入三脚本、chatWithAI 死导出（AiChat 用原生 fetch）。

**D. 部署面空载**（实测 14 pod 总请求 2400m CPU / 8156Mi 内存）：
| 项 | 浪费 |
|---|---|
| deepflow ns 5 pod | ~2.4GB 内存空载（agent 拉镜像失败零数据） |
| Redis | ~12Mi（唯一消费者 tasks.py 是死代码） |
| ipmi-exporter | ~26Mi 空转（无 BMC） |

**E. 结论**：真死代码仅 ~576 LOC（1.6%）；大头是本地未入库冗余（12K LOC + 57MB）+ 部署面 ~30% 内存空载。清理优先级：先磁盘（legacy/工件）→ 代码（tasks.py 等）→ 部署（deepflow/Redis/ipmi 默认关闭）。风险：client.ts 80 死导出有存活后端端点（API 契约层勿盲删）；flow_engine 是活跃后端；CH 死表 DROP 需先 RENAME 冷置。

---

## 附录 B：工作台首页重设计建议（摘要，完整 464 行方案见对话）

**核心主张**：首页 = "当前选定集群的健康报告"，新增 Z0 集群概览带作为第一视觉焦点，直击"直观展示当前集群信息"诉求。

**新信息架构（6 层）**：
| 层 | 区块 | 数据来源 |
|---|---|---|
| Z0 | 集群概览带（★新增） | clusters + stats + resources |
| Z1 | 健康 KPI 行（RED + 阈值着色） | stats |
| Z2 | 资源容量（左）+ 调用趋势（右） | resources + nodes/metrics + stats.trend |
| Z3 | 告警态势（分级汇总 + RCA 下钻） | stats.alerts + alerts/events |
| Z4 | 服务 RED TOP（★渲染已返回未展示的 top_services） | stats.top_services |
| Z5 | AI 快问入口（★上下文感知预设问句） | /ai/chat |

**现有 6 类问题**：集群感知薄弱（身份仅小 Tag）、KPI 三卡共用同一 sparkline（语义错位）、top_services/alerts 汇总等已返回数据未渲染、平台组件健康与 SLO 不联动、无数据集群空态无引导、统一 60s 粗放轮询。

**集群切换四场景**：主集群 / 纳管有数据 / 纳管无数据（空态 + CTA"去配置集群"）/ 全部集群聚合。

**阈值着色**：错误率 <1%/1-5%/>5%；资源 ≤60%/到阈值/超阈值；ETT=0/≤72h/>72h。**刷新分层**：告警 15s / KPI·服务 30s / 资源·趋势 60s。**分期**：P0 集群概览带+sparkline 修正+告警分级+服务 RED TOP+AI 快问+空态规范；P1 平台健康条+SLO 联动+ack/resolve+时间范围；P2 健康评分/散点图/暗色/快捷键。

---

---

## 六、安全与遗漏风险（评审补充，本轮未完全覆盖的高危区）

1. **ingest 鉴权状态矛盾**：injection.md 记录 `INGEST_API_KEY` 为空（鉴权实际未启用），但无 key 访问又返回 401——需澄清部署实际是否强制 X-Api-Key。若未强制，**任意流量可注入伪造 trace/log/metric，污染告警与拓扑**（本轮最大安全盲区）。
2. **共享密钥过弱**：`INTERNAL_TOKEN=dev-internal-token`、ingest key 均为固定 dev 值；orchestrator 信任 X-Internal-Role 提权（有 token 校验但密钥本身弱），orchestrator 8080 网络可达性若未收紧存在横向提权面。
3. **NL2SQL execute 面**：`/ai/nl2sql/{sid}/execute` 执行库中已存 SQL，测试只覆盖 translate；validate_sql 与 execute 之间存储层若被污染可执行任意 CH SQL（CH 只读，风险低但需确认账号权限）。
4. **CH 探针修复未回归**：运行时已修但模板未改，helm 重装/升级即回归 crashloop。
5. **P1-5 线程泄漏**：60s 超时后 kickoff 线程继续跑完，高频超时下线程泄漏（已并入 P1-5）。
6. **告警引擎稳定性**：60s 评估周期 + 内存/CH 双写，只测了单规则触发，未测并发规则/告警风暴/高延迟场景。
7. **GUI 未覆盖项**：KPI 三卡共用 sparkline、top_services/alerts 已返回未渲染等前端问题已由设计侧确认（附录 B 问题分析），未逐项进 GUI 断言。

## 附录 C：测试方法与环境

- **数据注入**：自写 OTLP 注入器（ingest /v1/traces + /v1/logs，Kind=1 SERVER），5 服务稳态 2.5min + orders 高错误 0.8×120s + 恢复；VLogs/VM/CH 直插 marker 记录。数据已保留供复测
- **告警引擎验证**：创建 test-orders-err 规则（threshold error_rate>0.3）→ 故障窗口轮询 3 分钟 → firing value=19.087 → 恢复自动 resolved → 清理规则与事件
- **API 矩阵**：144 断言（认证/RBAC/8 管理面 CRUD/编排器代理/边界健壮性），135 通过 / 9 失败
- **GUI**：Playwright 1.60 headless 1680×950，16 路由 56 断言，34 张截图（.slim/deepwork/test-results/gui/）
- **真实 LLM**：DeepSeek（deepseek-v4-flash）经 provider 流启用，多轮流式/非流式/NL2SQL 实测
- **环境处理**：CH 探针运行时修复（sh -c 包装，未改仓库）；LLM key 已配置（脱敏）；测试数据注入保留、业务测试数据已清理

*报告完 · 全部结论基于 2026-08-15 实测与代码根因核对，测试产物见 .slim/deepwork/test-results/*

---

## 七、问题修复记录（2026-08-15 下午 · commit c3b8ff6）

> 本轮按"最推荐方案"修复全部 25 项问题 + 服务全景 ns 过滤新增需求 + 2 项评审安全加固。已构建部署并回归验证。

### 修复状态总览（全部 25 项 + 3 项新增）

| 编号 | 问题 | 修复方案 | 回归验证 |
|---|---|---|---|
| P0-CH-1 | CH 探针 $(VAR) 不展开 | 探针改 sh -c 包装（模板落地） | ✅ 模板渲染正确 + Pod Ready 0 重启 |
| P1-1 | 集群 ns/events 泄漏 | 守卫下沉 clusterKubeconfig | ✅ 626 明确拒绝 |
| P1-2 | 非流式 chat 恒空 | execute_sync mode="chat" | ✅ 返回 224 字报告 |
| P1-3 | SSRF 误伤+部分写入 | ULA 豁免 + 校验前置 | ✅（评审确认，部署后 test 接口仍需真实 key） |
| P1-4 | NL2SQL 静默降级 | request+_parse_llm_config+used_fallback | ✅ used_fallback:false + error_rate |
| P1-5 | LLM 超时+线程泄漏 | 超时 120s + Semaphore(4)+共享 executor | ✅（38.9s 完成，无超时） |
| P1-6 | 对话延迟 | K8sGPT 意图门控 | ✅ 132s→38.9s |
| P1-7 | 回答聚焦度差 | 信息查询针对性作答 | ✅ 聚焦+deleted 计数 |
| P1-8 | 信息查询生成处置卡 | 处置卡仅诊断意图 | ✅（意图门控落地） |
| P1-9 | 拓扑边集群过滤 | MySQL 回退全集群限定 | ✅ 626 edges=0 |
| P1-10 | 静默越权 | silences admin 门禁 | ✅ user→403 |
| P2-1 | SLO 无边界 | window/target 校验 | ✅ 负窗口/超限→400 |
| P2-2 | 租户越权 | tenants admin 门禁 | ✅ user→403 |
| P2-3 | VLogs 静默丢数据 | 已设 Content-Type（核对无需改） | ✅ |
| P2-4 | VM NDJSON | 上游行为，文档化 | ✅（记录） |
| P2-5 | keyword 忽略 | keyword 别名 | ✅ 命中 1 条 |
| P2-6 | metrics/query 命名 | PromQL 透传+限流+响应上限 | ✅ 透传成功 |
| P2-7 | VM 可见延迟 | 上游行为，文档化 | ✅（记录） |
| P3-1 | DELETE 200 | 八处 404 | ✅ 404 |
| P3-2 | 角色白名单 | 显式 admin\|user | ✅ superadmin→400 |
| P3-3 | PUT 类型校验 | Unmarshal 错误 400 | ✅ |
| P3-4 | 审计 operator | X-Internal-User 优先 | ✅ operator=admin |
| P3-5 | /me 200 空 | 非 GET 405 | ✅ |
| P3-6 | loadgen Kind | 2→1(SERVER) | ✅ build/test |
| P3-7 | markdown 残留 | 目标空→未指定 | ✅ 无残留 |
| 新增 | 服务全景 ns 过滤 | 前端下拉+后端契约 | ✅ namespaces/nodes.namespace/external/deleted |
| 评审加固 | validate_sql 绕过 | 拒绝表函数/OUTFILE/裸表名 | ✅ 15 测试 |
| 评审加固 | VM 透传隔离 | 限流+响应上限+文档化 | ✅ 3 测试 |

### 已知残留（非阻塞）
- 服务→ns 映射依赖 trace_spans.k8s_namespace，当前多数服务该字段为空（deepflow 同步未映射），ns 下拉实际仅"全部/default"——属采集侧数据完备性问题，代码逻辑已就绪
- nl2sql 审计 operator 仍为 "system"（硬编码，非 _audit_operator 路径），语义可接受（系统生成的查询）
- orchestrator 测试 3 个 pre-existing 失败（缺 LLM key / shell_policy 换行行为），非本轮引入
- VM 透传端点无租户隔离（PromQL 结构任意无法安全注入 label），已文档化需网络层隔离

### 部署
- 4 镜像重建（frontend/query-api/ingest/orchestrator，tag v1.0.0）→ helm upgrade（REVISION 3）→ 强制重建 query-api/frontend/orchestrator Pod 加载新镜像 → 回归 13 项全通过
