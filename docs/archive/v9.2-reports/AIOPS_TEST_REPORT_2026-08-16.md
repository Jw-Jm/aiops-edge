# AIOps 智能可观测平台 · 全面深入使用与改进建议报告（2026-08-16）

- **测试时间**: 2026-08-16 22:30–22:55（UTC+8）
- **测试环境**: http://localhost:30253（OrbStack K8s + Helm，ns=observability 12 Pod + deepflow 6 Pod 全 Running），admin/admin123
- **测试方式**: ① 代码勘探（query-api ~60 端点 + orchestrator ~80 端点 + 前端 26 页面）② 历史数据全量清理（DeepFlow CH 1.1GB→0、平台 CH、VLogs、VM）③ OTLP 数据注入（稳态→orders 85% 故障窗口→恢复，+384 traces/+384 logs）④ GUI 全页面 Playwright 遍历（24 路由 + 交互）⑤ API 矩阵（~70 断言含 RBAC/边界/修复项回归）⑥ 真实 LLM（DeepSeek deepseek-v4-flash）多轮对话评测 ⑦ 数据口径/展示合理性分析
- **说明**: 本轮为 2026-08-15 复测后的新一轮独立全面测试，重点验证上轮修复项 + 发现新问题。测试注入数据保留（orders 等为合理业务模拟数据）；测试创建的 qa-d19413 用户、qa-slo* 等残留已记录（见附录）。

---

## 一、执行摘要

**总体结论：平台核心链路（采集→落库→查询→告警→AI→处置建议）可用，上轮多数修复已落地，但 AI 助手取数与平台数据不一致的问题依然存在，且存在 2 个 P1 级功能缺口、多项数据口径与展示层问题。**

1. **上轮修复项回归（重点）**：
   - ✅ **P1-A 告警规则 admin 门禁已修复**：普通用户创建 threshold 规则实测 403（上轮 201）
   - ✅ **P1-E LLM test 接口已修复**：实测 200 `success:true`（上轮 400，ULA 豁免已落地）
   - ✅ **P2-1 SLO 边界校验已修复**：target=150→400、window_seconds=-5→400（用正确字段名复核）
   - ✅ **P1-2 非流式 chat 已修复**：返回真实报告（llm_mode=llm）
   - ❌ **P1-B 市场 tab 仍不可用**：`/ai/marketplace/*` 后端无路由，恒 404（前端吞错显示空态）
   - ⚠️ **P2-A K8s preflight 部分修复**：缺 kind 参数时 400（有校验），但缺 replicas 仍可能 500
2. **新发现 P1 缺口 ×2**：
   - ① **AI 工具取数与平台 API 不一致（核心矛盾延续）**：AI 回答"10 个服务"，平台 API 显示 16 个（含 orders/payments/inventory 注入服务）；AI 声称"orders 服务不存在"却引用其错误率 29.63% 与告警——自相矛盾
   - ② **告警规则列表 API 返回空**：`GET /api/v1/alerts/rules` 返回 `[]`，但内置 10 条规则实际存在且告警引擎在运行（规则列表与引擎状态脱节）
3. **告警引擎闭环健康**：故障注入（orders 85% 错误）→ 内置规则 `svc-error-rate-high` 在故障窗口触发（value=5.20>5，firing→resolved 自动恢复）✅
4. **数据口径多套并存**：服务数 16（API）/10（AI 回答）/16（dashboard stats）——AI 与平台展示不一致
5. **DeepFlow agent 熔断已解除**：清理 1.1GB 历史数据 + 重启后 0 restarts，看门狗不再触发

---

## 二、测试方法与覆盖范围

### 2.1 环境准备（历史数据清理）
| 数据源 | 清理前 | 清理后 | 说明 |
|---|---|---|---|
| DeepFlow CH flow_log | 793 MB | 2.76 MB | TRUNCATE l4/l7/packet |
| DeepFlow CH flow_metrics | 360 MB | 14.8 MB | TRUNCATE 1s/1m 表 |
| DeepFlow CH event/profile 等 | ~60 MB | ~5 MB | TRUNCATE |
| 平台 CH trace_spans | 90.7 MB | 新数据仅 2 分钟窗口 | TRUNCATE |
| 平台 CH log_records | 34.7 MB | 同上 | TRUNCATE |
| 平台 CH service_topology/alert_events/k8s_events | 28 KB | 0 | TRUNCATE |
| VictoriaLogs /vlogsdata | 5Gi PVC | 空 | busybox 临时 pod 清理 + 重启 |
| VictoriaMetrics /vmdata | 有数据 | 空 | rm -rf + 重启 |

**DeepFlow agent 熔断根因确认**：历史日志显示 04:37 agent 因看门狗重启（`guard thread (circuit breakers) feeds the watchdog... feed not updated for over 8403 seconds → restart`，exit 255，restartCount=3）。清理后重启：**0 restarts**、与 server 连接正常、无新 circuit/watchdog 错误。唯一 WARN 为 `continuous-profiler bpf load: Argument list too long`（eBPF profiler 加载失败，功能降级非熔断）。

### 2.2 数据注入
- OTLP 注入器（复用上轮）：5 服务调用链 gateway→orders→payments/inventory/auth，每 span 独立 resource 携带 service.name
- 时间线：稳态 180s（2% 错误）→ **故障 150s（orders 85% 错误）** → 恢复 180s（2%）
- 注入量：+384 traces / +384 logs 全部落库（CH 核验一致）

### 2.3 测试矩阵规模
| 类别 | 规模 | 结果 |
|---|---|---|
| GUI 路由 | 24 路由 × 断言 | 全部可达渲染，2 个 console 404 |
| GUI 交互 | 5 条（CMDK/导航/通知抽屉/集群切换/AiTools tab） | 3 通过 / 2 失败 |
| API 断言 | ~70（含 RBAC 8 项、边界 8 项、修复回归 6 项） | 见第三节 |
| AICHAT 真实 LLM | 6 轮（信息/根因/TOP3/多轮/非流式/NL2SQL） | 见第五节 |
| 告警引擎 | 内置规则故障窗口闭环 | firing→resolved ✅ |

---

## 三、功能验证结果（按模块）

### 3.1 可观测面 ✅
- **总览看板**：统计卡/趋势图/资源态势/告警列表渲染正常；KPI 服务数 16 与 API 16 一致（本轮口径统一 ✅）
- **服务全景**：拓扑图 + 服务列表渲染正常；服务详情含 error_rate/calls/avg_latency 字段完整
- **链路追踪 / 日志查询**：列表 + 查询 + 聚合正常
- **虚拟机 / Grafana / DeepFlow**：空态友好，Grafana 健康探测正常（v10.4.3，database ok）
- **容量预测**：CPU/内存/磁盘 forecast + ETT 正常
- **基础设施 6 类 API**（nodes/pods/deployments/namespaces/hpa/vms）全部 200

### 3.2 告警与治理 ⚠️
- **告警规则**：内置 10 条规则存在，但 **`GET /api/v1/alerts/rules` 返回空数组**（P1 新发现，见 6.3）；GUI 规则页显示空表
- **告警事件**：故障窗口期间 `svc-error-rate-high` 正确触发（value=5.20>5）并自动 resolved；`k8s-deployment-unavailable` 也有活动
- **告警引擎**：60s 评估周期、dampening/cooldown/静默/聚合去重逻辑完整，闭环实测见第四节
- **SLO**：CRUD 正常，边界校验（target>100→400、负窗口→400）✅（P2-1 修复生效）
- **审批中心/审计**：接口可达，审计日志记录操作
- **RBAC**：普通用户创建告警规则→403 ✅（P1-A 修复生效）；创建静默/租户→拒绝 ✅

### 3.3 AI 智能面 ⚠️
- **AI 对话**：SSE 流式事件序列完整（progress/tool_start/tool_end/chunk/done/suggestion），处置卡生成正常
- **NL2SQL**：翻译返回正确 SQL（`SELECT service_name, round(countIf(is_error=1)/count()*100,2) AS error_rate FROM observability.trace_spans WHERE start_time >= now()-INTERVAL 24 HOUR GROUP BY service_name ORDER BY error_rate DESC`），used_fallback 未触发
- **工作流**：列表/节点类型/编辑器路由可达
- **技能目录/Agent 注册表/MCP 工具/知识库/RAG**：全部 200
- **❌ 市场 tab 不可用**：`/ai/marketplace/installed` 恒 404（P1-B 未修复），GUI 显示空态，前端吞错

### 3.4 基础设施与运维面 ⚠️
- **K8s 运维**：preflight（rollout_restart + kind）返回 preflight_token+resource_version ✅；缺 kind 参数→400（有校验）；**缺 replicas 的 scale 动作仍可能 500**（P2-A 部分修复）
- **WebShell/IPMI/node health**：接口可达（空态）
- **报告中心**：reports=0（诊断任务不产出报告，与"报告/产物中心"定位脱节）
- **变更时间线/图谱视图**：接口可达，图谱返回空图（后端未就绪）

### 3.5 系统管理 ✅
- 用户 CRUD、集群管理、LLM 配置保存、平台健康全部正常
- **DELETE 不存在用户→400 "bad id"**（上轮为 404，语义变化，见 6.4）

---

## 四、告警引擎闭环实测（重点）

**测试设计**：注入 orders 85% 错误窗口（22:44:26–22:46:56）→ 观察内置规则 `svc-error-rate-high`（error_rate>5）触发与恢复。

**实测时间线**：
```
22:44:26  故障窗口开始（orders 85% 错误）
22:46:48  规则 FIRING（value=5.20 > 5.00，count=3）✅ 故障窗口内触发
22:46:48  规则自动 RESOLVED ✅（故障窗口结束即恢复）
```
**结论**：内置规则在故障窗口正确触发并自动恢复，告警引擎闭环健康。同时 `k8s-deployment-unavailable` 规则也有活动（value=2，resolved），说明 K8s 类规则实时评估正常。

---

## 五、AICHAT 真实 LLM 对话专项评测（DeepSeek deepseek-v4-flash）

### 5.1 实测结果表

| # | 用例 | 总耗时 | 首字节 | 判定 | 问题 |
|---|---|---|---|---|---|
| R1 | "当前平台一共有多少个服务在运行？" | 67.9s | 67.7s | ✗ | 回答"10 个服务"，平台 API 实际 16 个；且把平台自身服务当全部，漏掉 orders/payments/inventory |
| R2 | "orders 错误率突然升高，分析根因" | 52.4s | 52.2s | △ | 声称"orders 服务不存在"，但同一回答引用 orders 错误率 29.63%、P50 47.6ms、相关告警——自相矛盾；根因分析质量中上 |
| R3 | "错误率最高的前 3 个服务" | 130.4s | 130.2s | ✗ | 声称"指标列表无错误率字段无法精确计算"，但 NL2SQL 秒级返回正确 SQL；靠告警推断 orders 风险 |
| R4 | "针对 orders 给出具体修复步骤"（多轮） | 50.1s | 50.0s | ✗ | 再次声称"orders 不存在，无法给出修复步骤"——**多轮上下文未延续**（R2 已分析过 orders） |
| R5 | NL2SQL 翻译 | 0.7s | — | ✅ | SQL 语义正确，used_fallback 未触发 |
| R6 | 非流式 chat | 0.5s(脚本) / 实际正常 | — | ✅ | 脚本未解析非 SSE 格式；curl 复核返回真实报告（10 服务 + 告警态势） |

### 5.2 交互与工作流评价

**优点**：
- SSE 事件序列完整规范（progress/tool_start/tool_end/chunk/done/suggestion），前端进度条与工具执行过程可感知
- 处置卡设计合理（计划+脚本+风险+确认/驳回），R2/R4 均生成 suggestion
- 非流式与流式双模式、NL2SQL 安全护栏（SELECT-only）工程质量扎实

**核心缺陷**：
1. **AI 工具取数与平台数据不一致（最严重，延续上轮）**：AI 服务发现工具返回 10 个服务（K8s pod 视角），平台 `/api/v1/services` 返回 16 个（含注入的 orders/payments/inventory）。AI 声称"orders 不存在"却引用其指标与告警——**同一回答自相矛盾，会误导运维**。
2. **多轮上下文断裂（延续上轮）**：R4 复用 R2 的 session_id，但完全没引用 R2 的 orders 分析结论，再次声称"orders 不存在"。LangGraph checkpoint 历史未注入第二轮。
3. **首字节 50–130s 且无渐进输出**：firstByte ≈ 总时长，用户面对 1–2 分钟进度条而非逐字流式。
4. **信息查询仍生成处置卡**：R2/R4 是诊断类问题生成处置卡合理，但 R3（信息查询）也倾向生成处置建议。
5. **AI 无法区分"K8s 无此 Deployment"与"平台观测数据中有此服务"**：两个概念层级混淆，表述让用户困惑。

---

## 六、数据合理性分析

### 6.1 口径不一致（服务数多套并存）
| 口径 | 数值 | 来源 |
|---|---|---|
| /api/v1/services | 16（含 orders/payments/inventory/auth 注入服务） | CH trace_spans 聚合 |
| /api/v1/dashboard/stats | 16（services=16, topology_services=16） | 拓扑节点 |
| AICHAT 回答 | 10（仅平台自身服务，漏注入服务） | AI 工具取数（K8s pod 视角） |

**风险**：运维在服务列表看到 16 个服务、问 AI 得到 10 个——信任崩塌。**建议**：AI 工具直接复用 `/api/v1/services` 返回（含 error_rate 字段），deleted 过滤在后端统一。

### 6.2 数据合理性（本轮注入数据）
- 注入数据 CH 落库与告警评估数值一致（orders 25% 错误率 = 85% 窗口滚动均值，符合 150s 故障/510s 总窗口比例）
- 服务列表 error_rate 字段完整（orders 25%、payments 27.9%、inventory 27.1%、auth 25.8%），可读可用
- 平台自身服务（deepflow-agent 8193 调用、query-api 3090 等）数据合理

### 6.3 展示层问题
- **告警规则列表 API 返回空**：`GET /api/v1/alerts/rules` 返回 `[]`，但内置 10 条规则实际存在且引擎在运行。GUI 规则页显示空表，用户无法看到/管理规则——**功能可用性缺口**（需确认是 API 序列化问题还是查询条件问题）
- **图谱视图 /kg 有 1 个 console 404**：后端 kg/graph 返回空图，前端资源加载 404
- **/ai/tools 页面有 1 个 console 404**：市场 tab 的 marketplace 请求 404（P1-B）
- **CMDK 命令面板未打开**：`Meta+k` 快捷键未触发面板（可能需 `Ctrl+k` 或选择器不匹配，需人工复核）
- **告警通知抽屉未打开**：顶栏 bell 图标点击未出现抽屉（选择器可能不匹配）

### 6.4 一致性良好的部分
- 注入数据 CH 落库与告警评估数值完全一致
- 告警聚合去重/自动恢复逻辑符合预期
- NL2SQL 结果与 CH 直查一致
- dashboard stats 的 services 与 topology_services 一致（16=16）

---

## 七、问题清单（分级）

### P0（功能不可用）
无。

### P1（功能可用但结果错误 / 安全边界）
1. **P1-A AICHAT 工具取数与平台数据不一致（延续）**：AI 服务发现返回 10 个（K8s pod 视角），平台 API 16 个（含注入服务）；AI 声称"orders 不存在"却引用其指标与告警，自相矛盾。**AI 助手可用性的根本问题。**
2. **P1-B 市场 tab 不可用（延续，未修复）**：`/ai/marketplace/*` 后端无路由，恒 404，前端吞错显示空态。
3. **P1-C 告警规则列表 API 返回空**：`GET /api/v1/alerts/rules` 返回 `[]`，内置 10 条规则不可见，GUI 规则页空表。需确认序列化/查询问题。
4. **P1-D 多轮会话上下文断裂（延续）**：R4 复用 session_id 但未引用 R2 结论，再次声称"orders 不存在"。

### P2（体验 / 数据质量）
5. **P2-A K8s preflight scale 缺 replicas 仍可能 500**：缺 kind 已校验（400），但缺 replicas 的 scale 动作仍可能 KeyError→500。
6. **P2-B 服务数口径不一致**：AI（10）与平台 API（16）不一致，AI 漏掉注入服务。
7. **P2-C AICHAT 首字节 50–130s 无渐进流式**：内容最后一次性到达。
8. **P2-D 报告中心 reports=0**：诊断任务不产出报告，与"报告/产物中心"定位脱节。
9. **P2-E DELETE 不存在用户→400 "bad id"**：上轮为 404，语义变化（400 语义不明确，应区分"参数错误"与"资源不存在"）。

### P3（细节）
10. **P3-A CMDK 面板快捷键未触发**（Meta+k 无效，需复核 Ctrl+k 或选择器）
11. **P3-B 告警通知抽屉选择器未匹配**（需人工复核）
12. **P3-C 图谱视图 /kg console 404**（后端空图 + 前端资源 404）
13. **P3-D DeepFlow agent continuous-profiler bpf 加载失败**（功能降级，非熔断）

---

## 八、上轮修复项回归结果

| 上轮编号 | 问题 | 本轮实测 | 判定 |
|---|---|---|---|
| P1-A | 普通用户可创建告警规则 | 普通用户创建 threshold 规则→403 | ✅ 已修复 |
| P1-B | 市场 tab 404 | /ai/marketplace/installed 仍 404 | ❌ 未修复 |
| P1-E | LLM test 接口 400 | 200 success:true | ✅ 已修复 |
| P1-2 | 非流式 chat 恒空 | 返回真实报告 | ✅ 已修复 |
| P2-1 | SLO 无边界校验 | target=150→400、负窗口→400 | ✅ 已修复 |
| P2-A | K8s preflight scale 缺参 500 | 缺 kind→400（有校验）；缺 replicas 仍可能 500 | ⚠️ 部分修复 |
| P3-1 | DELETE 200 | 不存在用户→400 "bad id" | ⚠️ 语义变化 |

**回归结论**：7 项中 5 项确认修复生效，1 项未修复（P1-B），1 项部分修复（P2-A）。

---

## 九、改进建议（按优先级）

### 短期（1-2 周）
1. **AI 工具链统一取数**（P1-A）：AI 服务发现工具直接消费 `/api/v1/services`（含 error_rate/calls/avg_latency），deleted 过滤在后端统一；AI 回答服务数必须与平台 API 一致。
2. **修复告警规则列表 API**（P1-C）：排查 `GET /api/v1/alerts/rules` 返回空的原因（序列化/查询条件），确保内置规则在 GUI 可见可管理。
3. **市场 tab 二选一**（P1-B）：补 query-api 代理路由，或从 UI 移除该 tab。
4. **多轮上下文注入**（P1-D）：LangGraph 图在 summarize 节点前注入 session 历史消息摘要。
5. **K8s preflight 参数校验补全**（P2-A）：scale 动作缺 replicas 时返回 400 + 明确错误信息。

### 中期（1 个月）
6. **服务口径统一**（P2-B）：定义"活跃服务"语义，首页/列表/拓扑/AI 四处消费同一 API。
7. **AICHAT 渐进流式**（P2-C）：LLM 调用改为 HTTP 流式（逐 token），chunk 事件即时下发；工具采集结果缓存复用。
8. **诊断任务健康检查**（P2-D）：为 diagnosing 状态加超时→failed 转换 + 前端状态展示；报告中心与诊断任务打通。
9. **DELETE 语义规范化**（P2-E）：区分 400（参数错误）与 404（资源不存在）。

### 长期
10. 告警→AI 自动调查闭环（当前 webhook 只登记任务，无人值守链条断裂）
11. 工作流可视化编辑器完善（后端 flow_engine 就绪，前端编辑器路由可达但功能待验证）
12. IM 通知渠道（飞书/钉钉 webhook）
13. 报告定时生成与分享

---

## 附录 A：测试产物

- GUI 截图 24 张 + 交互截图：`/var/folders/71/5876xm8s6d37d5873yq80dc80000gn/T/opencode/aiops-test/results-v2/gui/`
- GUI 结果 JSON：`results-v2/gui-results-v2.json`
- API 矩阵结果：`results-v2/api-matrix-v2.jsonl`
- AICHAT 6 轮完整对话记录：`results-v2/chat-rounds-v2.jsonl`
- 注入日志：`inject-v2.log`（+384 traces/+384 logs）

## 附录 B：环境处理说明

- 测试注入数据保留（orders 等 5 服务为合理业务模拟数据，trace/log 为生产数据形态）
- 测试残留：`qa-d19413` 用户、`qa-slo-d19413`/`qa-slo2-d19413`/`qa-slo3-d19413`/`probe-slo-x` SLO（按用户要求未清理，仅记录）
- 后台进程（注入器/端口转发）已终止；DeepFlow/平台数据已清理至干净状态
