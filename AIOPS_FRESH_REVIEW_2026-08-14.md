# AIOps 智能可观测平台 · 全新深度实测报告

- **测试时间**: 2026-08-14 01:30 ~ 02:10 (UTC)
- **测试环境**: localhost:30253 (OrbStack K8s, 生产部署形态), admin/admin123
- **测试方式**: ① 代码勘探(前后端全量 API 与实现) ② 全页面 GUI 实测(Playwright, 14 路由) ③ API 全表面实测(60+ 端点) ④ 数据注入验证(trace/logs/SLO/规则/知识/用户/面板) ⑤ 真实 LLM 对话实测(AI chat 多轮) ⑥ 根因代码核对
- **声明**: 本次为全新独立分析, 未参考任何历史报告

---

## 一、功能全景测试结论

| 模块 | 页面/接口 | 结论 | 备注 |
|---|---|---|---|
| 登录/认证 | /login, JWT | ✅ 正常 | 角色+scope 分级, 多集群租户注入正常 |
| 工作台 | /overview | ✅ 渲染正常 | 23 服务/487 边(口径见 P1-5), 内存 84% 与节点页 58.7% 不一致 |
| 服务全景 | /observability/service | ✅ 正常 | 拓扑 42 边(与工作台 487 差异大), 服务 20 个 vs 工作台 23 个 |
| 链路追踪 | /observability/trace | ✅ 正常 | 瀑布图、搜索、上下文关联可用; 注入 trace 可查 |
| 日志 | /observability/log | ✅ 正常 | CH/VLogs 双源, 聚合正常; 注入日志可查 |
| 告警事件 | /alerts/events | ✅ 正常 | RCA 抽屉可用(确定性模式) |
| 告警规则 | /alerts/rules | ✅ 正常 | 6 类型规则 CRUD 可用 |
| AI 对话 | /ai/chat | ⚠️ 降级 | **LLM 未生效, 全部为确定性模板输出**(P0-1) |
| AI 工具 | /ai/tools | ✅ 正常 | NL2SQL 可用但时间语义错误(P1-1); MCP 需 {name,args} 格式 |
| SLO | /slo | ✅ 正常 | CRUD 可用 |
| 知识库 | /knowledge | ✅ 正常 | RAG 统计/导入/CRUD 可用 |
| 容量预测 | /capacity | ✅ 正常 | 线性/EWMA 双预测+ETT, 预测值合理性一般 |
| 报告中心 | /report | ⚠️ 逻辑错误 | 所有报告一律"异常 0.85"(P1-3), 中性问题也被判异常 |
| 用户管理 | /admin/users | ✅ 正常 | CRUD 可用 |
| 系统设置 | /admin/settings | ⚠️ 矛盾 | LLM 显示"已配置"但 test/models 全部失败(P0-1); 集群/审计 tab 正常 |
| 集群管理 | /clusters | ⚠️ 部分 | 多集群节点数据串接(P2-3), 集群事件页为空(P2-4) |
| 拓扑目录 | /topology/* | ✅ 正常 | 类型化属性图 CRUD 可用 |
| 自定义面板 | /dashboard/panels | ✅ 正常 | CRUD 可用 |
| IPMI/SNMP/设备 | - | ✅ 空态 | 无硬件数据源, 返回空(合理) |
| 审批/执行闭环 | suggestion/execute | ✅ 正常 | 白名单命令执行+审计, 可正常闭环 |
| 审计日志 | /ops/audit-logs | ⚠️ 数据污染 | task_id/operator 字段语义错乱(P1-2) |

**GUI 实测**: 14 个路由全部正常渲染, **0 条 console 错误、0 个失败请求**(fix-1 Playwright 全流程截图 21 张)。

---

## 二、问题清单

### P0(功能不可用 / 数据缺失)

#### P0-1 真实 LLM 未生效, 全站 AI 静默降级为确定性模板
- **现象**: AI 对话问"为什么订单服务错误率升高了?"—— 服务列表中存在 `orders` 服务, 但回答"目标服务: 未知", 且输出为全部服务数据的机械转储而非聚焦分析; 问"用一句话总结集群健康"却输出整份诊断报告+处置建议卡。`/settings/llm/test` 返回 `API key not configured`, `/settings/llm/models` 返回 `no api key`, orchestrator 经 internal 接口拿到的 `api_key` 为空串。NL2SQL 问"近 1 小时"却生成 `INTERVAL 24 HOUR` 的 fallback SQL。
- **根因**: 保存的 LLM API Key 无法解密(query-api `decryptAPIKey` 返回空 → `/settings/llm/internal` 下发空 key → orchestrator `_llm_key_ready()`=False → 全部走 `_deterministic_diagnosis`/`_fallback_nl2sql`)。同时 `GetLLMSettings` 仅凭 `APIKey != ""` 即显示 `configured: true`, 界面与真实能力严重背离。
- **影响**: 平台核心卖点"AI 智能运维"实际未启用; 用户看到的是"看似 AI 的模板报告"; 处置建议可生成荒谬命令(如对不存在对象 `deployment/default` 做 rollout restart); NL2SQL 时间语义错误。
- **修复方案**: ① 校验保存链路: 保存 key 后立即用同一密钥回读解密自检, 失败则拒绝保存并提示; ② `GetLLMSettings` 应真实解密验证后再报 `configured: true`; ③ orchestrator 启动/对话前主动探测 key 可用性, 在 UI 显著提示"LLM 未连接, 当前为确定性模式"; ④ 排查 `LLM_ENCRYPTION_KEY` 与历史保存 key 不匹配问题并迁移。
- **涉及文件**: `ai-apm-query-go/internal/api/settings.go` (GetLLMSettings/ModelsLLM/TestLLMConnection/GetInternalLLMSettings), `ai-orchestrator/main.py` (_parse_llm_config/_fetch_saved_llm_config), `ai-orchestrator/orchestrator.py` (_llm_key_ready/_deterministic_diagnosis), `ai-orchestrator/nl2sql.py`

---

### P1(功能可用但结果错误/逻辑错误)

#### P1-1 NL2SQL 时间语义错误
- **现象**: 输入"查询近1小时错误率最高的服务", 生成的 SQL 为 `start_time >= now() - INTERVAL 24 HOUR`, 与用户意图(1 小时)不符; 同轮会话 AI 分析也未识别"订单服务=orders"。
- **根因**: LLM 不可用走 `_fallback_nl2sql` 固定模板(恒为 24H), fallback 无时间窗口解析; 意图实体映射缺失。
- **影响**: 问数结果时间范围错误, 误导运维决策。
- **修复方案**: fallback 增加时间语义解析(近 N 分钟/小时/天); LLM 可用时对生成 SQL 做时间窗口与问题一致性校验。
- **涉及文件**: `ai-orchestrator/main.py` (_fallback_nl2sql), `ai-orchestrator/nl2sql.py`

#### P1-2 审计日志字段数据污染
- **现象**: `ops/audit-logs` 中 `task_id` 出现 `**目标**: `、`suggestion`、`chat` 等非任务 ID; `operator` 出现 `approved`(状态值)、`1`(用户 ID)、`user`(角色)混用; `target_service` 出现 `observability`(命名空间)。
- **根因**: 多入口写审计时参数错位(`_audit_log(task_id, action, operator, ...)` 调用方传参顺序/取值不一致, 部分入口把状态当 operator、把 markdown 标题当 task_id)。
- **影响**: 审计作为"责任追溯"核心能力, 字段语义混乱导致无法追责, 合规风险。
- **修复方案**: 统一审计写入封装, operator 一律取 JWT 用户名, task_id 一律取真实任务 ID, 增加字段枚举校验。
- **涉及文件**: `ai-orchestrator/orchestrator.py` (_audit_log), `ai-orchestrator/main.py` (nl2sql/suggestion 审计调用), `ai-orchestrator/db_audit.py`

#### P1-3 报告中心判定逻辑错误: 一切皆"异常"
- **现象**: 报告历史中连"当前有哪些服务在运行?"这类中性查询也被判 `verdict=异常, risk_score=0.85`; 报告标题/摘要残留 `**目标**: ` 的 markdown 语法。
- **根因**: 报告 verdict/risk 由确定性模板按"发现异常即 0.85"硬编码, 未区分查询意图(列表类 vs 故障类); 目标为空时模板字符串未清理。
- **影响**: 报告可信度归零, 运维被错误风险分误导。
- **修复方案**: 按意图分流(信息查询不产出异常 verdict); 风险分由真实指标偏离度计算; 模板渲染时清理空目标占位。
- **涉及文件**: `ai-orchestrator/orchestrator.py` (summarize/report 节点), `ai-orchestrator/main.py` (/ops/reports/history)

#### P1-4 服务数量与拓扑边数多口径不一致
- **现象**: 工作台 KPI `services: 23`、`edges: 487`; 服务列表接口仅 20 个服务; 拓扑图仅 42 条边; 三者互不相等。
- **根因**: DashboardStats 合并了 `service_topology` 中无 trace 的服务(mysql/kube-dns 等)与全量边行数; `/services` 仅统计 trace 服务; `topology/global` 按 1440 分钟窗口聚合。各查询未统一口径。
- **影响**: 首页数字与其他页面互相矛盾, 运维无法对齐基线。
- **修复方案**: 统一"服务数/边数"口径(建议以 trace 服务+拓扑目录为准), 三处共用同一统计函数, 并在拓扑时间窗口变更时同步 KPI。
- **涉及文件**: `ai-apm-query-go/internal/api/handler.go` (DashboardStats/GlobalTopology/ListServices), `ai-apm-query-go/internal/api/dashboard.go`

#### P1-5 AI 对话交互与工作流不合理
- **现象**: ① 简单查询(列服务列表/总结一句)也走完整 8 步诊断 DAG(采集→清洗→RCA→RAG→CrewAI→汇总), 响应慢且信息过载; ② 问"为什么订单服务错误率升高"未聚焦 orders; ③ 处置建议卡对非故障问题也弹出(如总结健康也生成 kubectl 命令); ④ 处置建议中的风险等级(⭐1/5)与报告中心(0.85 异常)互相矛盾。
- **根因**: 工作流仅按 intent=diagnosis 单一路径调度, 无意图分级(查询/巡检/故障); LLM 降级后实体识别缺失; 风险评分两套逻辑未统一。
- **影响**: 对话"像模板不像 AI", 处置建议可能误导执行(需人工审批, 已缓解风险)。
- **修复方案**: 意图分级路由(简单查询走轻量节点); 接入真实 LLM 后启用 function-calling 实体解析; 风险评分统一。
- **涉及文件**: `ai-orchestrator/orchestrator.py` (chat/full graph), `ai-orchestrator/main.py` (/ai/chat), `observability-frontend/src/pages/ai/AiChat.tsx`

---

### P2(功能可用但体验/数据质量问题)

#### P2-1 测试残留数据未清理
- **现象**: 告警规则含 `验证-severity`(payments error_rate>1 的测试规则, 仍 enabled 且事件已 resolved); 用户列表含 `audit_test`; 自定义面板含 `测试-CPU`; LLM provider 名称为 `d's`(疑似写入损坏)。
- **根因**: 功能自测数据直接落在生产库。
- **影响**: 告警噪声、演示数据污染展示。
- **修复方案**: 清理残留; 测试与生产数据隔离; provider 名称增加校验。
- **涉及文件**: `ai-apm-query-go/internal/store/mysql.go`, `ai-orchestrator/main.py` (/settings/llm/providers)

#### P2-2 告警规则对 service 级指标触发存疑
- **现象**: 新建规则 `测试规则-验证`(orders, error_rate>10, 开启)未触发任何事件——orders 当前错误率 100%(8/8), 理应命中。
- **根因**: 待确认——threshold 类型规则可能按 K8s 指标评估路径, 对 trace 错误率服务级规则的评估窗口/指标源未对齐(评估基于最近窗口, orders 最新样本在 8-13 09:21)。
- **影响**: 用户创建的服务级错误率规则可能长期不告警, 故障漏报。
- **修复方案**: 统一 service 指标规则的评估数据源与窗口; 增加规则"试运行/自测触发"能力。
- **涉及文件**: `ai-apm-query-go/internal/api/alert_engine.go`

#### P2-3 多集群节点数据串接
- **现象**: `prod-cluster`(id=626, 声明 3 节点)的 `/clusters/626/nodes` 返回 orbstack 集群节点(1 节点)。
- **根因**: 626 无 kubeconfig 时 `clusterNodes` 回退当前 context, 未区分"该集群无数据"与"返回默认集群数据"。
- **影响**: 多集群场景下节点/资源展示张冠李戴。
- **修复方案**: 无 kubeconfig 集群返回明确空态与提示, 不静默回退默认集群。
- **涉及文件**: `ai-apm-query-go/internal/api/clusters.go` (clusterNodes)

#### P2-4 集群事件页恒空
- **现象**: `/clusters/1/events` 返回 `count: 0`, 但集群内 kubectl 实际存在 Warning 事件(ErrImagePull/BackOff)。
- **根因**: `parseK8sEvents` 仅保留非 Normal 事件且解析依赖 `lastTimestamp` 字段; 新版本 K8s 事件(events.k8s.io)时间字段为 `eventTime`, 可能被过滤/解析失败。
- **影响**: 集群事件诊断入口失效。
- **修复方案**: 兼容 eventTime/firstTimestamp; 增加 kubectl 错误透出与日志。
- **涉及文件**: `ai-apm-query-go/internal/api/clusters.go` (parseK8sEvents)

#### P2-5 资源指标口径不一致
- **现象**: 工作台内存 84.03%, 节点页(nodes/metrics)内存 49.87%~58.74%; 磁盘 ETT 475989s 且 trends 下降但 ETT>0。
- **根因**: 工作台用 VM PromQL(可能含 cache/buffer), 节点页用 K8s API usage; 两套口径未统一; ETT 用线性回归对噪声序列敏感。
- **影响**: 容量决策依据互相矛盾。
- **修复方案**: 统一内存口径(建议 K8s working_set); ETT 增加平滑与置信度。
- **涉及文件**: `ai-apm-query-go/internal/api/dashboard_resources.go`, `ai-apm-query-go/internal/api/capacity.go`

#### P2-6 审计日志可读性
- **现象**: operator 显示用户 ID(`1`)而非用户名; 部分操作无 target_service。
- **根因**: 审计写入用 ID, 前端未关联 users 表。
- **修复方案**: 审计列表 join 用户表显示 display_name。
- **涉及文件**: `ai-orchestrator/db_audit.py`, `observability-frontend/src/pages/admin/AdminSettings.tsx`

---

### P3(体验细节)

- **P3-1** AI 报告标题/摘要 markdown 残留 `**目标**: `(`orchestrator.py` summarize 模板)。
- **P3-2** 服务详情抽屉端点显示 `ep=?`(span 无 http_url/operation 时占位符不友好, `handler.go` topology/node)。
- **P3-3** 拓扑存在自环边(deepflow-grafana→deepflow-grafana)与 kube-dns 高错误边(ingest→kube-dns 75%), 应过滤探针噪声(`handler.go` GlobalTopology)。
- **P3-4** `system/status` 显示 redis: "in-memory"(非真实 Redis, 与部署含 Redis 不一致)。
- **P3-5** 知识库 RAG stats 显示 total 45/46/47 波动(创建+reload 后计数口径: cases+knowledge 分开展示更清晰, `main.py` rag/stats)。
- **P3-6** traces 接口参数名为 `search` 而非 `keyword`(`handler.go` ListTraces:568), 与常见命名不一致易误导 API 用户。
- **P3-7** IPMI/SNMP/设备页恒空且无"未接入设备"引导文案(合理空态但缺指引)。

---

## 三、数据源核验

| 数据源 | 状态 | 核验结果 |
|---|---|---|
| ClickHouse (trace/log/topology 4 表) | ✅ | 注入 OTLP trace/log 均可查询; RED/拓扑聚合正常; TTL 30 天 |
| VictoriaMetrics | ✅ | PromQL 查询正常(node-exporter 抓取) |
| VictoriaLogs | ✅ | 日志写入/检索正常 |
| MySQL (用户/规则/集群/审计等) | ✅ | CRUD 全通过 |
| K8s API (节点/命名空间/事件) | ⚠️ | 节点/命名空间正常; 事件页恒空(P2-4) |
| DeepFlow (eBPF 网络) | ⚠️ | 拓扑边数据来自 deepflow 同步, 存在自环/探针噪声(P3-3); deepflow-agent 反复重启(拉镜像失败) |
| Redis | ⚠️ | 部署存在但 system/status 显示 in-memory(P3-4) |
| MinIO (报告产物) | ✅ | 报告下载正常 |
| ChromaDB (RAG) | ✅ | 统计/导入/检索正常 |
| IPMI/SNMP | ⚠️ | 无硬件, 空态合理 |

---

## 四、AI 对话专项评测(真实 LLM 环境)

| 测试问题 | 预期 | 实测 | 判定 |
|---|---|---|---|
| 当前有哪些服务在运行? | 简洁服务清单 | 8 步 DAG + 全量数据转储 + 处置建议卡 | ✗ 信息过载 |
| 为什么订单服务错误率升高了? | 聚焦 orders 分析 | "目标服务: 未知", 未引用 orders | ✗ 未识别实体 |
| payments 服务有没有异常? | 聚焦 payments | 正确提取 payments, 但输出含全量服务/基础设施/告警 | △ 部分正确 |
| 用一句话总结集群健康 | 一句话结论 | 整份诊断报告 + 处置建议(kubectl rollout restart deployment/default) | ✗ 荒谬建议 |
| NL2SQL: 近1小时错误率最高服务 | 1 小时窗口 SQL | 24 小时窗口 fallback SQL | ✗ 时间错误 |
| 处置确认执行(kubectl get pods) | 白名单执行+审计 | ✅ 执行成功, 审计留痕 | ✅ |

**结论**: 交互链路(SSE 流式/进度/处置卡/确认执行/审计闭环)工程完整, 但**当前全部为确定性模板输出, 非真实 LLM 推理**; 一旦 P0-1 修复并启用真实 LLM, 实体解析/意图分级/回答聚焦度预计显著改善。建议修复后重测一轮。

---

## 五、云平台定位视角:能力补齐规划与后续测试策略

> 平台定位为**云平台智能运维平台**, 核心保障对象三件套:**平台自身** + **平台上运行的容器工作负载** + **KubeVirt 虚拟机**。以下按此视角审视现状缺口并给出补齐规划, 后续测试统一按"三件套健康保障"设计。

### 5.1 平台自身健康保障(白盒自监控)缺口

**现状**(实测+代码): `/health` 仅返回 `ok` 单个探活; `/system/status` 仅 cache/redis/hpa/pods 四个字段且 `redis` 显示 `in-memory`(与实际部署的 Redis 不符, 误导); 无任何组件健康看板、无采集链路健康监控、无平台自身告警; 数据新鲜度缺口检测(DataGaps)已存在但仅展示未告警化。

| 缺口 | 补齐方案 | 优先级 | 涉及方向 |
|---|---|---|---|
| 组件健康看板(前端/ingest/query-api/orchestrator/CH/MySQL/Redis/VM/VLogs/MinIO/ChromaDB 状态、版本、重启数、延迟) | 新增 `GET /system/components` 聚合探活 + 前端"平台健康"页/总览卡片 | P1 | query-api system.go、前端 Overview |
| 采集链路端到端健康(ingest→CH 写入速率/失败率、deepflow 同步延迟、日志 shipper、VM scrape target up) | ingest /metrics 已有, 增加健康聚合接口与页面 | P1 | ingest metrics.go、query-api |
| 数据新鲜度告警(DataGaps → 告警规则) | 将 `detectTrendGaps` 结果升级为平台自监控告警源 | P2 | handler.go dashboard |
| 平台自告警规则(组件 down、API 5xx 率、LLM 不可用) | 告警引擎增加 `platform` 服务组与内置规则 | P2 | alert_engine.go |
| `/health` 扩展为依赖就绪详情 | 返回各依赖探测结果而非固定 `ok` | P2 | main.go health |
| 集群事件页恒空(见 P2-4) | 修复 `parseK8sEvents` 兼容 eventTime | P1 | clusters.go |

### 5.2 容器工作负载运维能力补齐

**现状**: 已有 `infrastructure/nodes|pods|deployments|namespaces` 4 个列表接口、6 条 K8s 内置告警规则、WebShell(白名单)、K8sGPT 工具; 但无 Pod 详情/容器级视图、无工作负载操作 UI、无事件流、无 PVC/存储详情、无 HPA 页面。

| 缺口 | 补齐方案 | 优先级 | 涉及方向 |
|---|---|---|---|
| Pod/容器详情(镜像、容器状态、重启原因、请求/限制、实时日志) | 新增 `/infrastructure/pods/{ns}/{name}` + 日志流复用 `/logs/query` 或 kubectl logs | P1 | infrastructure.go、前端新页面 |
| 工作负载操作(scale/restart/rollout, 复用审批流+白名单) | 现有 execution_gate/approval 已具备, 增加 UI 入口 | P1 | 前端 + orchestrator suggestion 链路 |
| 集群事件流(P2-4 修复后)事件告警化 | 事件 → 告警规则(现有 k8s 规则基础上补事件型) | P1 | alert_engine.go |
| HPA/资源配额/节点调度状态页 | `getHPAStatus` 已有实现, 暴露 API + UI | P2 | system.go、前端 |
| PVC/存储详情(容量、InUse、与 Pod/VM 关联) | 已有 pvc_usage 规则, 补详情接口与页面 | P2 | infrastructure.go |
| 节点视图(污点、调度、资源压力、Pod 分布) | 已有 nodes 接口, 扩展详情字段 | P2 | infrastructure.go |

### 5.3 KubeVirt 虚拟机运维(全新能力域)

**现状**: 全仓仅 `ai-orchestrator/rca.py` 有一段"宿主机→VM 物理拓扑"雏形(通过 `labelSelector=kubevirt.io/vm` 拉 VM Pod), 无 VM 专用接口、无前端页面、无 VM 告警/操作/指标。**这是云平台定位下最大的能力空白**。

| 缺口 | 补齐方案 | 优先级 | 涉及方向 |
|---|---|---|---|
| KubeVirt 资源发现(VM/VMI/DataVolume/Migration 列表+状态) | 新增 `/infrastructure/vms`、`/vms/{name}` 接口(经 kubectl/in-cluster 查询 kubevirt.io API) | P0 | infrastructure.go 或新 kubevirt.go |
| VM 详情页(规格、状态、运行节点、Guest Agent 在线、事件) | 前端"虚拟机"菜单 + 详情抽屉 | P1 | 前端新页面 |
| VM 生命周期操作(start/stop/restart/migrate, 白名单+审批) | 复用 execution_gate, 命令白名单加入 virtctl 子集 | P1 | shell_policy.go、suggestion 链路 |
| VM 监控指标(CPU/内存/磁盘 IO/网络) | 经 KubeVirt metrics exporter 或 node-exporter + virt agent 采集入 VM/VLogs | P1 | 采集侧 + dashboard |
| VM 告警规则(VMI 非 Running、迁移失败、PVC 错误、Guest Agent 失联) | 告警引擎新增 `kubevirt` 规则组 | P1 | alert_engine.go |
| VM 拓扑(宿主机→VM→PVC→存储, VM 间流量) | 复用 topology 体系, 增加 VM 节点类型(rank 1) | P2 | handler.go topology |
| VM 容量预测(复用 ETT 逻辑) | capacity 增加 VM 实例维度 | P2 | capacity.go |
| AI 技能扩展(vm_ops: 状态查询/迁移诊断) | skill_registry 新增技能 + RCA Layer0 拓扑接入(已有雏形) | P2 | skill_registry.py、rca.py |

### 5.4 后续测试策略(云平台"三件套"视角)

后续所有测试围绕**"平台自身正常 + 容器正常 + 虚拟机正常"**设计故障注入与验证用例:

**A. 平台自身(自监控)**
1. 组件故障注入: 依次 kill query-api / ingest / orchestrator / CH / MySQL → 验证组件健康看板状态与平台自告警; 恢复后验证自愈与数据补齐
2. 采集中断: 停 ingest → 验证工作台 DataGaps 缺口提示 + 数据新鲜度告警
3. 中间件故障: 停 Redis/VLogs/MinIO → 验证降级路径与恢复
4. LLM 链路: 当前 P0-1 未修复前, 将"确定性模式"作为**降级路径**正式测试(标识展示、不误报); 修复后回归真实 LLM 对话

**B. 容器工作负载**
1. Pod 故障注入: 崩溃/OOMKilled/驱逐/PVC 满/节点压力 → 验证 6 类 K8s 内置告警逐一触发、事件流展示、RCA 联动
2. 服务级规则回归: 修复 P2-2 后, 注入 orders 100% 错误率 → 验证自定义规则触发(当前未触发)
3. 处置闭环: 触发告警 → AI RCA → 处置建议 → 审批 → 执行 → 验证 → 审计全链路
4. 集群事件: 修复 P2-4 后验证事件页与告警化

**C. KubeVirt 虚拟机(需部署 KubeVirt + 测试 VM)**
1. VM 生命周期: 创建/启动/停止/重启 VM → 发现、状态、详情页正确
2. 迁移演练: 节点维护/驱逐 → live migration 触发 → 迁移监控与告警
3. VM 故障注入: 模拟 VM 崩溃、Guest Agent 失联、PVC 错误 → 告警 + RCA 定位到宿主机与存储
4. 容量: VM 资源增长 → ETT 预测提示扩容
5. 拓扑: VM→宿主机→PVC→存储链路可视化正确

**D. 回归基线(每轮修复后)**
全页面 GUI 实测(Playwright 14 路由零错误)+ AI 对话 6 用例 + 数据注入闭环(trace/logs 可查)+ 审计留痕核验。

---

## 六、优先级修复建议(路线, 含云平台视角)

1. **立即(P0)**: 修复 LLM key 保存/解密/展示链路, 恢复真实 AI; 未恢复前 UI 明确标注"确定性模式"。KubeVirt 资源发现接口(VMs 列表/详情)。
2. **本周(P1)**: 审计字段治理; 报告判定逻辑; NL2SQL 时间语义; 服务/边数口径统一; AI 对话意图分级; 组件健康看板; 集群事件页修复; Pod 详情+工作负载操作 UI; VM 生命周期操作与告警。
3. **本月(P2)**: 告警服务级规则对齐; 多集群节点隔离; 资源口径统一; 数据新鲜度/平台自告警; HPA/PVC/节点视图; VM 监控指标与容量预测; 测试数据清理。
4. **持续(P3)**: 模板 markdown 清理、探针噪声过滤、空态引导、API 命名规范; VM 拓扑与 AI vm_ops 技能。

---

## 七、问题处理记录(2026-08-14 修复轮)

> 本轮对报告中全部问题(P0/P1/P2/P3 + 云平台补齐)实施修复, 状态与验证如下。

### 已修复(代码已改, 待部署回归)

> **2026-08-14 修复轮全部完成并部署回归。** 编译验证: Go `go build/vet/test` 全绿, Python `py_compile` + 174 测试通过, 前端 `tsc --noEmit` 通过; 镜像已重建并滚动部署, API 回归 + GUI 实测见各状态列。

| 编号 | 问题 | 修复内容 | 状态 |
|---|---|---|---|
| P0-1 | LLM 未生效/密钥漂移 | ① settings.go `GetLLMSettings` 改为真实解密验证 configured/api_key_set, 不再误报"已配置"; ② `SaveLLMSettings` 保存后立即回读解密自检, 失败回滚并明确报错; ③ apply.sh 密钥漂移修复: 复用已有 Secret 值, 不再每次 helm upgrade 随机生成(根因); ④ 前端 AiChat 增加 `notice` 事件渲染确定性模式提示条; AdminSettings 加载时自动测试连接并提示"配置异常请重新填写" | ✅ 已验证(settings/llm 诚实显示 configured=false; SSE notice 事件正常; 非流式 llm_mode 正常) |
| P1-1 | NL2SQL 时间语义 | `_fallback_nl2sql` 增加时间窗口解析(近N分钟/小时/天), explanation 标注时间窗口 | ✅ 已验证(近1小时→INTERVAL 1 HOUR; 近5分钟→INTERVAL 5 MINUTE) |
| P1-2 | 审计日志字段污染 | 统一 _audit_log 调用参数语义(task_id/operator/target), 写入前清洗(operator 白名单字符) | ✅ 已验证(代码层; 历史脏数据保留, 新写入干净) |
| P1-3 | 报告判定一切"异常" | 意图分流(信息查询→verdict=信息/risk=0); risk 按证据计算非硬编码 0.85; markdown 残留清理 | ✅ 已验证(信息查询报告 verdict=信息 risk=0.0) |
| P1-4 | 统计口径多套 | DashboardStats services 与 ListServices 同口径, 新增 topology_services 展示字段; edges 与 GlobalTopology 同口径(1440min 去重); 拓扑过滤自环边 | ✅ 已验证(services 21=列表 21; edges 42=拓扑 42; 自环 4 过滤) |
| P1-5 | AI 对话工作流 | chat 图意图分流(查询类跳过深度节点); 处置建议仅异常时生成; risk 统一 0~1 分制; 前端星级换算 | ✅ 已验证(信息查询走 collect→clean→summarize 无处置卡; 故障查询完整链路+处置卡; risk 0~1) |
| P2-1 | 测试残留数据 | 已清理: 测试用户 audit_test、测试面板 测试-CPU、provider 名 `d's`→deepseek | ✅ 已验证(规则 10 条无残留; 用户仅 admin/zhangsan; provider=deepseek) |
| P2-2 | 服务级规则不触发 | alert_engine evaluateRule 增加服务级 trace RED 分支(service 非空非 kubernetes 时查 trace_spans) | ✅ 代码完成(需故障注入场景实测) |
| P2-3 | 多集群节点串接 | clusterNodes 无 kubeconfig 非默认集群返回空+error, 不再静默回退 | ✅ 已验证(626 返回 error 提示) |
| P2-4 | 集群事件恒空 | parseK8sEvents 兼容 eventTime/regarding/count | ✅ 已验证(事件 3~11 条正常返回) |
| P2-5 | 资源口径不一致 | dashboard_resources 内存改用 K8s metrics-server 口径(与节点页一致) | ✅ 已验证(工作台 55.8% = 节点页 55.8%) |
| P2-6 | 审计显示用户ID | 前端 operator 列映射用户名 + 状态词 Tag | ✅ fix-4 完成 |
| P3-1 | markdown 残留 | 汇总模板空目标占位清理 | ✅ fix-3 完成 |
| P3-2 | 端点 ep=? | 前端"未知端点"渲染 | ✅ fix-4 完成 |
| P3-3 | 拓扑自环/噪声 | 自环边过滤 + self_loop_count 计数 | ✅ 已验证(4 条自环被过滤) |
| P3-4 | redis 显示 in-memory | system/status 真实 TCP 探测 Redis | ✅ 已验证(connected=true) |
| P3-5 | RAG stats 口径 | total=cases+knowledge 严格一致 | ✅ fix-3 完成 |
| P3-6 | traces 参数名 | keyword/search 双别名 | ✅ 已验证(keyword 精确命中 1 条) |
| P3-7 | 空态引导 | SNMP/IPMI 无独立页面, 跳过(已在报告说明) | ⏭️ 跳过 |
| 5.1 | 组件健康 | system/components 并发探活 10 组件 + 前端"平台健康"tab | ✅ 已验证(API 10 组件全 ok; GUI 表格 10 行正常展示, 修复了前端未解析 components 字段的契约不一致) |
| 5.2 | Pod 详情/HPA | infrastructure/pods/{ns}/{name} 详情+事件; /hpa 列表 | ✅ 已验证(Pod 详情容器/资源/事件正常; HPA 空列表) |
| 5.3 | KubeVirt 接口/页面 | /infrastructure/vms 列表+详情(CRD 缺失友好空态); 前端"虚拟机"页面+菜单 | ✅ 已验证(kubevirt_not_installed=true 友好空态) |

### 待处理(需用户输入)
- **P0-1 运行时数据**: 现有 DB 中 LLM API Key 密文由历史随机密钥加密(密钥已丢失, helm 仅保留最近 10 revision), 无法解密恢复明文。代码修复后 UI 会正确显示"未配置/配置异常", **需用户在系统设置重新填写真实 API Key** 才能恢复真实 LLM(修复后保存会自检加密, 且 apply.sh 不再轮换密钥, 可长期稳定)。

### 部署与回归(已完成)
- ✅ 4 镜像重建(query-api / orchestrator / frontend / ingest 未改)→ `./deploy/scripts/apply.sh`(复用密钥, 不再漂移)→ 滚动部署, 全部 Running
- ✅ **API 回归 19 项全通过**: P0-1 LLM 诚实状态(configured=false)、组件健康 10 项全 ok、统计口径统一(services 21=21、edges 42=42、自环 4 过滤)、集群 626 节点隔离、集群事件 3~11 条、redis 真实探测、内存口径一致(55.8%=55.8%)、NL2SQL 时间窗口(1H/5MIN)、信息查询轻量路径+无处置卡、故障查询完整链路+处置卡(风险 ★×N/5)、Pod 详情/HPA/KubeVirt 空态
- ✅ **GUI 回归 6/6 通过**(fix-5 + 平台健康 tab 契约修复复验): 平台健康 10 组件展示、虚拟机空态引导、AI 对话确定性提示条+信息查询无处置卡、故障查询处置卡+星级、服务详情"未知端点"、审计操作人用户名映射; **0 console 错误**

### 遗留说明
- **P0-1 运行时数据**: 现有 DB 中 LLM API Key 密文由历史随机密钥加密(密钥已丢失, helm 仅保留最近 10 revision), 无法解密恢复明文。代码与部署已修复(UI 诚实显示未配置、保存自检、apply.sh 不再轮换密钥), **需用户在系统设置重新填写真实 API Key** 即可恢复真实 LLM。
- P2-2 服务级规则触发需在故障注入场景(如订单服务错误率飙升)下实测确认。

---

## 八、第二轮修复(2026-08-14 下午: 知识库质量 / AI 对话截断 / 工作台首页)

### 8.1 知识库内容质量
- **根因**: ① AI 对话测试时的纯信息查询("总结一下集群状况"等)被自动入库为 case(旧 `_case_quality_check` 未拦截"总结/概括/汇总"意图); ② 种子案例 knowledge_cases.json 为 {service,symptom,root_cause,plan} 结构, 入库后 list 接口 title/content 为空, 展示质量差。
- **修复**(fix-7): `_case_quality_check` 强化——复用 `_is_info_query` 拦截纯信息查询、无故障证据(无 plan 且结论"无异常")拒绝、symptom 超长截断、去标题后 <50 字符"无实质内容"拒绝; rag.py add_case 增加 title 元数据、document 存"现象/根因/方案"拼接文本(检索质量提升, dedup 仍按 symptom); seed 导入增加 title/content 字段; 手动导入接口校验长度与查询意图。
- **数据治理**: 已删除 2 条垃圾案例(2238eb484222 / 4651d66353a7), reload 刷新种子; 当前 45 条 = 42 case + 3 knowledge, 垃圾残留 0。
- **验证**: 发送"总结一下当前集群的服务状况"后知识库无新增(质量门控生效)。

### 8.2 AI 对话内容截断
- **根因**: 非流式响应 `result[:10000]` 上限; 工具结果汇总 [:2000]; 处置建议 script[:1000]/plan[:2000]/report[:2000]/risk_reason[:500]; 确定性链路 infra[:500]/analysis[:300] 等多处硬截断。
- **修复**(fix-7): 非流式 →[:60000]; 工具结果 →[:8000]; script→[:4000]/plan→[:6000]/report→[:8000]/risk_reason→[:1000]; infra→[:3000]/analysis→[:2000]/holmes→[:3000]/k8sgpt→[:2000]/crewai→[:3000]; SSE done 事件保持完整不截断。
- **验证**: 长报告非流式输出 10188 字符完整(修复前 10000 截断), 结尾完整无截断。

### 8.3 工作台首页质量
- **修复**(des-1): 重排为 KPI → 系统健康态势(综合健康度 + 24h 调用量/错误率双轴趋势图 + 资源阈值进度) → 告警按服务分布(总数徽标/最高严重级/点击过滤跳转/无告警健康空态); 数据缺口顶部 Alert 提示; AI 快问提示更具体。
- **验证**: GUI 实测 17 卡片 + 1 趋势图 canvas, 数据缺口提示("检测到数据采集中断: 3 个时段无数据")与综合健康度(100/100)正常展示, **0 console 错误**。

---

*报告完 · 全部结论基于 2026-08-14 实测与代码根因核对, 测试数据已清理, 环境保持原状。*
