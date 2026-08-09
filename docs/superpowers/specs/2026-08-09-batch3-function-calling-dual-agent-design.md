# 批3：LLM function-calling 工具循环 + 双层 Agent 架构（设计）

**日期**: 2026-08-09
**批次**: 批 3（总纲 Phase A：A2 + A3）
**性质**: 设计文档（已与用户对齐 4 项关键决策）
**目标**: 将 AI 编排从「一次性采集喂 LLM」升级为「LLM 迭代式 function-calling 工具循环 + Coordinator/子Agent/Reviewer 双层 Agent 架构」

---

## 0. 已对齐的关键决策

| # | 决策 | 选择 |
|---|---|---|
| 1 | 双层 Agent 落法 | **完整双层**：Coordinator 独立拆解多子任务 → 子 Agent 独立（可并行）→ Reviewer 合并审查 |
| 2 | function-calling 与 mock | **mock 也模拟工具循环**：mock 下 LLM 返回预设"调工具→拿结果→继续"序列，循环/护栏/工具执行全部真实运行 |
| 3 | 循环护栏与前端展示 | **完整闭环**：max_steps / 工具超时 / 白名单 / 总耗时；前端展示双层全过程 |
| 4 | 实现载体 | **方案 A：扩展现有 LangGraph**，新增 coordinator/subagent循环/reviewer 节点，复用状态机/SSE/mock/审计 |

---

## 1. 现状与差距（代码实际）

| 项 | 现状 | 差距 |
|---|---|---|
| LLM 调用 | `_llm()`（orchestrator.py:82）用 CrewAI 单次 kickoff，system prompt 明确"禁止调用工具" | 无 function-calling 循环 |
| 数据采集 | `node_collect` 一次性采集全量数据喂 LLM | 无法按需迭代取新证据 |
| Agent 体系 | `ExpertRegistry`（skills/experts.py）4 专家：inspection/diagnosis/ops/query，按 intent 关键词路由 | 单层，无 Coordinator/Reviewer |
| 工具体系 | `ToolRegistry`（skill_registry.py）已含 name/description/func/params/cls/scope，`describe_for_llm()` 可生成描述 | 仅文本描述，未暴露为 function-calling schema |
| 图调度 | `build_graph()` 固定 15/6 节点 DAG（orchestrator.py:646） | 单层链式，无并行子 Agent |
| SSE | `server.py` 已含 tool_start/tool_end/chunk/suggestion 事件，前端 ChatThread.tsx 已渲染工具卡片 | 无 subagent/coordinator/reviewer 类型标签 |
| mock | `llm_mock.py` 仅返回固定文本 | 不模拟工具决策/拆解/审查 |

---

## 2. 总体架构

在现有 LangGraph 中新增**双层编排图 `dual_graph`**（作为 chat_graph 的升级版），复用全部基础设施（状态机、SSE、mock、审计、审批）。核心改动：`orchestrator.py` + 新增 `function_calling.py` + 增强 `llm_mock.py` + 前端 `ChatThread.tsx`。

```
用户消息
  │
  ▼
[coordinator]  Coordinator Agent ──拆解──▶ 子任务列表（诊断/巡检/运维/问数）
  │
  ├─▶ [subagent: 子任务1] ──function-calling 循环──▶ 子结论1
  ├─▶ [subagent: 子任务2] ──function-calling 循环──▶ 子结论2   (并行，线程池)
  └─▶ [subagent: 子任务3] ──function-calling 循环──▶ 子结论3
  │
  ▼
[reviewer]  Reviewer Agent ──合并+审查──▶ 最终报告（SOP校验/质量打分/冲突消解）
  │
  ▼
[summarize] 输出最终结论（复用现有）
```

## 3. 模块设计

### 3.1 新增 `function_calling.py` — 通用工具循环（核心）

供每个子 Agent 复用的迭代式 function-calling 循环：

- **循环语义**：LLM 决策 → 若要求调工具 → 校验白名单 → 执行工具 → 结果回填 → 再交 LLM 决策 → 直到 LLM 输出最终结论或触发护栏。
- **工具 schema**：复用 `ToolRegistry`（name/description/params/cls/scope），转换为 OpenAI 格式 `tools` 定义。
- **护栏（循环体内强校验）**：
  - `max_steps=6`（LLM 最多决策次数）
  - `max_tool_calls=4`（最多工具调用数）
  - 每工具超时 10s
  - 总耗时上限 120s
  - **工具白名单**：仅允许 `cls=="safe"` 工具 + 显式白名单（query_metrics/query_traces/query_topology/get_service_list/get_infrastructure 等只读）；`mutating`/`dangerous` 或需审批工具直接拒绝并在结果中说明。
  - mock 模式同样执行白名单。

核心函数：
- `make_tools_schema(tools: list[ToolDef]) -> list[dict]`：转 OpenAI function schema
- `exec_tool_with_guard(tool: ToolDef, args: dict, whitelist: set) -> str`：白名单校验 + 执行 + 返回结果
- `run_tool_loop(llm_decision, tools, user_prompt, whitelist, on_tool) -> str`：通用循环引擎，`llm_decision` 是可注入的（真实 LLM 或 mock 决策器），`on_tool` 回调用于 SSE 事件

### 3.2 `orchestrator.py` — 三个新 LangGraph 节点

**node_coordinator**：
- 输入：user_message + 已采集数据
- 调 `_llm`（role=Coordinator），prompt 要求输出子任务拆解 JSON `[{"task_id","task_type","target_service","query"}]`
- mock：`mock_coordinator_plan()` 返回预设 2-3 子任务
- 输出 `subtasks` 到 state

**node_subagent（循环节点）**：
- 对每个子任务实例化子 Agent（复用 ExpertRegistry 的 4 专家 role/goal 作系统提示）
- 每个子 Agent 跑一遍 `run_tool_loop`（并行用 ThreadPoolExecutor）
- 工具集 = 该专家关联 Skill 的 tools（`ExpertRegistry.skills_of`）
- 输出 `sub_results: {task_id: {conclusion, tool_trace, cost}}`
- SSE：每完成一个子 Agent，yield tool_start/tool_end（name=子Agent名，type=subagent）

**node_reviewer**：
- 输入：全部 sub_results
- 调 `_llm`（role=Reviewer），要求合并审查：SOP 校验（结论是否有依据/矛盾）、打分、消解冲突、输出最终报告
- mock：`mock_reviewer_result()` 返回预设合并文本
- 输出 `review_result` → summarize

### 3.3 `llm_mock.py` — mock 模拟工具循环

- `mock_llm_decision(messages, tools)`：按预设序列返回决策（第 1 次调 query_metrics，第 2 次调 get_service_list，第 3 次返回 final），循环体真实执行工具、真实回填、真实推进
- `mock_coordinator_plan()`：返回预设拆解 JSON
- `mock_reviewer_result()`：返回预设合并审查文本

### 3.4 前端 `ChatThread.tsx`

- 工具卡片增加类型标签：`coordinator`/`subagent`/`reviewer`/`tool`
- 子 Agent 卡片可展开显示底层工具调用链
- `progress` 文本展示阶段（Coordinator 拆解→子Agent 执行中→Reviewer 审查中）
- 复用现有 SSE 协议，无新事件类型

### 3.5 入口分流

- `BrainOrchestrator` 新增 `dual_graph`
- `stream_sync` 支持 `mode` 参数（chat/dual）
- main.py `/api/v1/ai/chat` 增加 `dual_agent` 开关（默认关闭，不破坏现有 chat）
- 现有 graph/chat_graph 不动，零回归

## 4. 测试（TDD）

新增 `tests/test_function_calling.py` + `tests/test_dual_agent.py`：
- 循环护栏：max_steps 截断、白名单拒绝 mutating、超时
- mock 决策序列执行：真实调用 query_metrics 并回填
- Coordinator 拆解 JSON 解析、并行子 Agent 结果收集
- Reviewer 合并输出
- SSE 事件顺序（coordinator→subagent→reviewer→done）

## 5. 数据与合规

- 全自研，仅借鉴 ongrid 双层 Agent 概念，不复制代码（AGPL 红线）
- 数据所有权不变：双层 Agent 运行记录仍由 orchestrator 写（现有 agents/审计表）
- 组件最小化：不引入新存储/服务/镜像

## 6. 自审

- [x] 覆盖总纲 A2（function-calling 循环）+ A3（双层 Agent）
- [x] 基于当前代码实际（非旧文档）
- [x] 复用现有引擎（LangGraph/ToolRegistry/ExpertRegistry/SSE/mock），组件最小化
- [x] 4 项关键决策已与用户对齐
- [x] mock 下端到端可跑，测试可覆盖
