# S1 缺陷 D15 — AI 运行证据采集为空（进行中，部分修复）

## 现象（真实环境复验）
- 通过 `POST /api/v1/ai/runs` 创建的调查运行，RCA 全部返回 `insufficient_evidence`、
  `missing_evidence=["graph_relation"]`、`warning_codes=["GRAPH_UNAVAILABLE"]`、0 证据。
- 复验运行：e202de1b / 58b4f7f2 / b1282352 / ad6b4c55（run1-6）、run7-10。
- 证据：`docs/acceptance/<RID>/api/ai-run*-detail.json`、`ai-run*-evidences.json`。

## 根因链（已定位的三层）
1. **工具注册表未初始化（已修复）**
   `query_graph.v1` 在 worker 冷启动时不在 `ToolRegistry` → `InternalQueryClient._resolve_tool`
   抛 `invalid_context`。只有 orchestrator 主应用与 kg.runtime 调用
   `init_default_tool_registry()`，rca_engine 路径没有。
   修复：`internal_query_client.py` 唯一汇聚点幂等初始化 + `rca_engine/runtime.py` 双保险。
   验证：容器内 probe `get_vertex` 返回 200 与完整实体；单元测试
   `tests/test_rca_runtime_tool_registry.py` 2/2 通过。

2. **异常被静默吞掉（已修复，可观测性）**
   引擎 `except Exception` 分支无日志，导致前两层根因完全不可见。
   修复：`rca_engine/engine.py` + `InvestigationGraphClient.__call__` 记录真实失败码。
   验证：日志现显示真实错误 `GRAPH_ENTITY_NOT_FOUND`。

3. **租约身份缺失（当前卡点，进行中）**
   真实运行中 `_execution_context` 抛
   `investigation Tool requires active lease identity`（线程内验证）。
   `InvestigationRuntime.execute` 在 `bind_execution_lease_token(work.lease._token)`
   内调用 brain.investigate，但真实运行的绑定令牌为空 → 推测：
   - HTTP accept 的 lease claim 未填充 `_token`，或
   - 走 recover/checkpoint 路径时令牌不入持久化态（设计如此），或
   - brain.investigate 实际经另一条未绑定租约的路径执行 RCA。
   下一步：跟踪 `InvestigationDispatcher.accept → lease claim → brain.investigate`，
   确认 `work.lease._token` 在执行时是否为空，修复绑定源头。

## 诚实结论
- **D15 未关闭**。注册表层修复已生效（容器内直调图谱成功），
  但端到端运行仍 0 证据，第三层（租约身份）未修复。
- 本轮改动文件：
  - `ai-orchestrator/internal_query_client.py`（注册表汇聚点初始化）
  - `ai-orchestrator/rca_engine/runtime.py`（双保险初始化、租约回退、失败日志）
  - `ai-orchestrator/rca_engine/engine.py`（异常日志）
  - `ai-orchestrator/tests/test_rca_runtime_tool_registry.py`（回归测试 2/2 通过）
- 部署镜像：v1.4.0-d15b/c/d/e（`127.0.0.1:5050/aiops/ai-orchestrator`）。
- 相邻测试失败说明：`test_investigation_runtime.py` 6 个失败为本地缺
  pytest-asyncio；`test_action_proposal.py` 收集错误为本地 Python 3.9 不支持
  `str | None`（容器 3.12 正常）。均为环境差异，非本次改动引入。

---

## 更新（2026-09-14 10:30）— D15 已闭环

### 最终根因（第 4 层，也是最初误判的第 0 层）
`POST /api/v1/ai/runs` 的请求字段名是 **`resource_id` / `service`**（runs_public.go:86-87），
验收脚本误用了 `target_resource_id` → 字段被忽略 → Run 持久化 TargetResourceID="" →
派发 resource_id 空 → 实体解析无输入 → GRAPH_ENTITY_NOT_FOUND。
前次记录的"租约身份缺失"为测试探针构造不完整（request_context=None）所致，非产品缺陷。

### 修复验证（真实环境）
- 用 `resource_id: k8s-deployment:v1:...:74f7e6f4...` 创建 Run `56a6145a`：
  - 证据采集 **6 类全部入库**（metrics/traces/logs/alerts/changes/events），
    其中 logs/alerts/events 含真实数据（pod 日志、告警、K8s 事件）。
  - RCA 结论 `partial`/未知 —— 目标实体无真实故障（负样本），证据不足拒答，
    符合"不得证据不足输出 Confirmed"的设计要求。
- UI（真实环境 1600×1000）：
  - 八页导航顺序正确，AI 智能运维为默认首页。
  - AI 运维工作台完整渲染：范围冻结表、任务列表（64 个真实 Run）、
    任务阶段（证据采集 · 已入库 6 条 / 候选原因 · 0 个 / 结论 · 未知 / 动作 · 0 项 /
    恢复验证 · 尚未验证）、结论等级、候选原因与反证、受控调查入口。
  - 集群 scope 切换生效（告警徽标随集群变化 4→3）。
  - 证据明细表（evidence_id/类型/来源/观察时间/质量/事实摘要）已实现；
    集群切换后需刷新重载（loadRuns 依赖已含 selectedRunId，evidences 依赖
    activeClusterId）。
- 截图：screenshots/aiops-home-1600x1000.png、aiops-task-6-evidences-1600.png

### 遗留（转入下一轮）
1. 集群切换后证据明细表需要自动重载的时序复核（浏览器 reload 后回到 scope
   未选态 —— 需验证 preferredClusterId 恢复链路）。
2. RCA 仍 0 候选原因：需构造有真实故障信号的场景（如真实 Pending pod /
   ImagePullBackOff）验证 Candidate→Supported 链路与 40 场景评测。
3. 本轮测试产生的运行记录（run1-run11）带"验收运行"前缀，验收结束后清理。

---

## 更新 2（2026-09-14 11:10）— D16 关联缺陷闭环（未建模对象 0 证据）

### 根因（D16，两个组件各一层）
1. **query-api `searchGraphAliases`**：alias 未命中/不可用时只有 MemoryRepository
   回退 SearchEntities；HugeGraph 仓库按安全设计拒绝全名扫描
   （GRAPH_FEATURE_UNAVAILABLE_LEGACY）→ 503。修复：能力限制降级为空命中，
   resolve_entity 返回 404 GRAPH_ENTITY_NOT_FOUND（而非 503）。
2. **rca_engine**：`GRAPH_ENTITY_NOT_FOUND` 与图谱系统故障共用 fail-closed
   分支 → 跳过全部本地证据采集；且 InvestigationEvidenceProvider 以图谱候选
   构造查询目标，未建模对象恒 0 证据。修复：
   - entity_resolver：区分 not_found（返回 None）与系统故障（上抛）。
   - engine：`GRAPH_ENTITY_UNRESOLVED` 分支继续本地证据采集，missing 申报
     graph_relation；系统故障仍 fail-closed（GRAPH_UNAVAILABLE）。
   - evidence provider：候选为空时回退 request 目标名采集。

### 真实环境验证
- `regression-svc`（真实 critical firing 告警对象，未建模进图谱）调查运行
  run17：**6 类证据全部入库**（metrics/traces/logs/alerts/changes/events），
  RCA 结论 partial/insufficient_evidence（诚实降级，未伪装 Confirmed）。
- 语义区分验证：warning=GRAPH_ENTITY_UNRESOLVED（数据事实）≠
  GRAPH_UNAVAILABLE（系统故障）。
- 回归测试：test_d16b_entity_unresolved.py 2/2、test_engine.py、
  test_rca_runtime_tool_registry.py、test_p16_12_rca_scenarios.py 全通过。
- 部署镜像：query-api v1.4.0-d16b / orchestrator+worker v1.4.0-d16e。

### D15/D16 总状态
- 调查链路端到端打通：创建 → 派发 → 实体解析（UID/名称双路径）→
  6 类证据采集 → 确定性评分 → 诚实结论 → 证据查询 API → UI 渲染。
- 负样本（无故障实体、未建模对象）正确拒答，符合第 11 章"证据不足不得
  输出 Confirmed"。
- 剩余：Candidate→Confirmed 正样本场景（需有完整图谱关联+真实故障信号的
  对象）与 40 场景 × 5 次评测（T11 阶段执行）。

---

## 更新 3（2026-09-14 11:30）— T6 AI 运维报告链路打通

### 实现
- 后端 `POST /api/v1/ops/reports/ai-operations?cluster_id=`（ai_operations_report.go）：
  从已完成/终止 Run 聚合真实事实（scope/时间窗/结论等级/证据清单/假设与反证/
  动作与预检/graph generation/版本）写入 reports 表（report_type=ai_operations）。
  租户/集群隔离与非终态拒绝（RUN_NOT_TERMINAL）。
- 前端 Reports 页：AI 桶"从任务生成报告"卡片（runId 输入预填深链参数）；
  AI 运维任务页"由本任务生成报告"深链 /reports?type=ai-operations&runId=。
- 页面/下载/API 同一 content 投影（Markdown），下载端点复用现有
  /ops/reports/{id}/download。

### 真实环境验证
- 正样本 run18（deepflow-grafana，图谱实体 + 真实 Liveness 503 故障）：
  6 类证据（traces 100 条真实数据），候选根因形成（4 类独立证据，
  score 0.36，graph_generation 467）；因图谱无传播路径边，系统诚实拒绝
  Confirmed（防自根因，符合"Confirmed 精确率 100%"要求）。
- 报告 #3 生成 200：verdict Unknown（诚实）、6 条证据；列表 AI 桶可见；
  下载 200，Markdown 内容完整。
- UI：报告页深链自动切 AI 桶、生成卡片渲染、"AI 运维报告 1 份"。
- 截图：reports-ai-bucket-1600.png

---

## 更新 4（2026-09-14 12:30）— T7 知识库验证与 D17 闭环

### D17（新发现并修复）
`ProxyAI` 在 production 对 /ai/runs 之外的全部 legacy 代理一刀切 410，
导致知识库核心数据源失效（页面恒空态 0 条）。而 legacyProxyAllowed 白名单
（settings.go）与 orchestrator production_surface kept 路由表都显式保留
knowledge 配置面 —— 属代理入口分发不一致。修复：production 下白名单内路由
继续代理（双重防线仍在：orchestrator kept 表 + X-Internal-Token），白名单外
保持 410。部署 v1.4.0-d17；curl 验证 /ai/knowledge?type=all 返回真实条目
（ChromaDB case，含此前沉淀的报告）。

### 知识库验证状态
- 数据层：canonical 端点 /clusters/{clusterId}/knowledge 返回真实条目
  （T7 生命周期验证 incident），列表/详情/版本/检索/索引状态端点可用
- 页面：检索/筛选/类型分桶/引用定位提示/草稿与审核分离 UI 均渲染
- 遗留：浏览器会话 authScope.activeClusterId 在部分 reload 场景未恢复
  （页面显示"未选择"→ 列表请求不发起）；API 与服务端均正常，属前端
  会话恢复时序，需专项排查（T10 遗留清单）
