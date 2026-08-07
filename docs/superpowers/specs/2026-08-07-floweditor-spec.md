# FlowEditor 可编辑工作流引擎 立项 Spec

> 日期：2026-08-07 ｜ 状态：立项评估（基于代码审计）
> 定位：在现有 LangGraph 固定 DAG 基础上，实现"用户可定义/可编辑/可运行"的工作流引擎 + 可视化编辑器

---

## 〇、结论

**可行，且改造面极小（MVP 约 5-7 人日）**。现有 `ai-orchestrator` 已具备 80% 底座：
- 13 个节点函数签名完全统一 `node_x(state) -> dict`（无副作用参数，从 state 读输入）
- `GRAPH_DEFS` 已是结构化 `{nodes[], edges[]}` 编辑模板
- `build_graph` 节点注册已是声明式循环（仅边是硬编码 if/else）
- skill_registry 有 JSON 持久化成熟范式可复用

**核心改造**：① `NODE_REGISTRY` 节点注册表 ② `build_graph_from_def` 通用动态构建（替换硬编码边）③ `WorkflowRegistry` JSON 持久化 ④ API ⑤ 前端 React Flow 编辑器。

---

## 一、架构

```
┌─ 前端 /workflows/editor ─────────────┐
│  React Flow 画布（节点拖拽/连线）     │
│  → 生成 {nodes[], edges[]}            │
└──────────────┬───────────────────────┘
               │ POST /ai/flows (保存)
┌──────────────▼───────────────────────┐
│ WorkflowRegistry (workflow_registry) │
│  JSON 持久化 (/tmp/flow_store.json)  │
│  内置 GRAPH_DEFS(full/chat) + 用户    │
└──────────────┬───────────────────────┘
               │ get_graph(key)
┌──────────────▼───────────────────────┐
│ build_graph_from_def(workflow)       │
│  NODE_REGISTRY 查函数 → 动态 add_node │
│  edges → add_edge                    │
│  编译 StateGraph                     │
└──────────────┬───────────────────────┘
               │ graph.invoke(initial)
┌──────────────▼───────────────────────┐
│ 现有 node_* 函数（13 个，复用）       │
│  collect/clean/rca/rag/crewai/holmes │
│  plan/risk/wait_approval/execute/    │
│  verify/report/memorize/summarize    │
└──────────────────────────────────────┘
```

## 二、数据模型

### Workflow 定义（沿用 GRAPH_DEFS 结构）
```json
{
  "key": "workflow.custom_diagnosis",
  "name": "自定义诊断流程",
  "description": "用户自定义",
  "nodes": [
    {"id": "collect", "label": "数据采集", "desc": "采集服务指标"},
    {"id": "rca", "label": "RCA 根因分析", "desc": "定位根因"}
  ],
  "edges": [["collect", "rca"]],
  "entry": "collect"
}
```

### NodeRegistry（node id → 可执行函数）
```python
NODE_REGISTRY = {
    "collect": node_collect, "clean": node_clean, "rca": node_rca,
    "rag": node_rag, "crewai": node_crewai, "holmes": node_holmes,
    "plan": node_plan, "risk": node_risk, "wait_approval": node_wait_approval,
    "execute": node_execute, "verify": node_verify, "report": node_report,
    "memorize": node_memorize, "summarize": node_summarize,
}
```

## 三、后端改造（MVP）

### 1. build_graph_from_def（动态构建核心）
```python
def build_graph_from_def(workflow: dict, checkpointer=None):
    builder = StateGraph(AgentState)
    for n in workflow["nodes"]:
        fn = NODE_REGISTRY.get(n["id"])
        if not fn: raise ValueError(f"unknown node: {n['id']}")
        builder.add_node(n["id"], fn)
    entry = workflow.get("entry", workflow["nodes"][0]["id"])
    builder.set_entry_point(entry)
    for s, t in workflow["edges"]:
        builder.add_edge(s, t)
    out_nodes = {s for s, _ in workflow["edges"]}
    for n in workflow["nodes"]:
        if n["id"] not in out_nodes:
            builder.add_edge(n["id"], END)
    return builder.compile(checkpointer=checkpointer)
```
重构 `build_graph(mode)` 内部走此通用逻辑（full/chat 定义来自 GRAPH_DEFS）。

### 2. WorkflowRegistry（JSON 持久化，仿 ExpertRegistry）
- `save_custom_store()` → `/tmp/flow_store.json`（env `FLOWS_STORE` 可覆盖）
- `load_custom_store()` → 启动合并到 GRAPH_DEFS（用户 key 覆盖/追加）
- `get(key)`/`save(key, def)`/`delete(key)`/`list_all()`（内置+用户）
- `BrainOrchestrator` 加 `self.user_graphs` 缓存 + `get_graph(key)`（懒编译 + 保存时失效）

### 3. API
| 方法/路径 | 说明 |
|---|---|
| `GET /ai/flows` | 列表（内置 full/chat + 用户自定义）|
| `GET /ai/flows/{key}` | 单个 workflow 定义 |
| `POST /ai/flows` | 保存/更新用户 workflow |
| `DELETE /ai/flows/{key}` | 删除用户 workflow |
| `POST /ai/flows/{key}/run` | 按 key 运行（现有 run 改造，按 key 取 graph）|

## 四、前端改造（MVP）

### /workflows/editor 页（React Flow）
- 引入 `@xyflow/react`（需 npm 装）
- 画布：节点池（左侧 13 节点）/ 画布（拖拽放置、连线）/ 属性面板（名称/描述/entry）
- 编辑 → 生成 `{nodes, edges}` → `POST /ai/flows` 保存
- 现有 `/workflows` 列表页 → 加"编辑"入口 + 用户自定义 flow 展示
- 运行：复用现有 run 表单

## 五、MVP 边界

### 做（核心闭环）
1. `NODE_REGISTRY` + `build_graph_from_def`（顺序 DAG）
2. `WorkflowRegistry` JSON 持久化
3. API：列表/保存/运行/删除
4. 前端：React Flow 编辑器 + 保存 + 运行

### 不做（V2）
- 条件边/循环（wait_approval 审批分支 `route_approval`）——MVP 默认跳过中断或仅顺序边
- 自定义节点（用户新写函数）——只从现有 13 节点选
- 分支并行（多出边）——MVP 只支持纯链（单前驱/单后继）
- 版本控制/回滚/发布

## 六、风险与对策
1. **节点前置依赖**（execute 依赖 plan/approved）：前端做依赖校验，`build_graph_from_def` 对非法顺序抛错
2. **LangGraph 编译后不可改**：用户 workflow 变更需重新 compile 并失效 `user_graphs` 缓存
3. **wait_approval interrupt**：已有 SqliteSaver 支持，MVP 默认 `approved=True` 跳过

## 七、工作量
- 后端：NODE_REGISTRY + build_graph_from_def + WorkflowRegistry + API ≈ 3-4 人日
- 前端：React Flow 编辑器 + 列表集成 ≈ 2-3 人日
- **合计 MVP ≈ 5-7 人日**（可独立交付）

---

## 决策点（待确认）
1. **MVP 是否只做"顺序纯链"**（不做条件/分支/循环）？——建议是，V2 再扩
2. **前端用 @xyflow/react（React Flow）** 还是继续 G6？——建议 React Flow（与 ongrid FlowEditor 对齐，且节点编辑更成熟）
3. **持久化用 JSON 文件**（仿 skill_registry）还是 SQLite？——建议 JSON（改动最小、对齐现有范式）
