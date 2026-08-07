# 自研 FlowEditor 工作流引擎 — 设计文档

> 日期：2026-08-07 ｜ 状态：设计确认
> 定位：**严格参照 ongrid 架构**，在自研 Python 栈上实现可定义/可编辑/可运行/可审批的工作流引擎 + React Flow 可视化编辑器

---

## 〇、结论

采用 **自研 Python DAG 执行器**（方案 A，最贴合 ongrid），替换现有 LangGraph 硬编码图执行层。

- 核心抽象：`NodeSpec` 自描述节点注册表（引擎/校验/前端三端数据驱动）
- 数据流：**边只表达控制流**，数据走共享 `RunContext` + 模板引用 `{{nodes.<id>.output.<path>}}`
- 能力：`condition` 节点（true/false）、并行扇出（并发上限 4）、DAG 校验（无循环）、`error` 端口
- 审批：`wait_approval` 节点 = **暂停 + 恢复**（FlowRun `waiting_approval` 状态）
- 持久化：SQLite 三表（核心闭环），V2 迁 MySQL

**核心闭环约 12-15 人日（约 2 周）**。V2 后置：cron/alert 触发器、AI 生成工作流、test-node、MySQL、版本控制。

---

## 一、架构

```
┌─ 前端 observability-frontend ──────────────────────────┐
│  /workflows 列表页（改造：编辑入口 + 用户自定义 CRUD）    │
│  /workflows/editor 编辑器（新增，React Flow v12）        │
│  → 数据驱动：GET /ai/flows/node-types 渲染调色板          │
└──────────────┬─────────────────────────────────────────┘
               │ /api/v1/ai/flows/* (CRUD + run + resume)
┌──────────────▼─────────────────────────────────────────┐
│ flow_engine/  (自研，对齐 ongrid biz/flow)               │
│  noderegistry.py   NodeSpec 注册表（引擎/校验/前端三端）   │
│  graph.py          wire 格式 + DAG 校验（Kahn 环检测）    │
│  engine.py         DAG 执行器（并发扇出 + 暂停/恢复）      │
│  expr.py           模板引用 + 条件求值                     │
│  usercase.py       WorkflowRegistry + FlowRun 调度       │
│  store.py          SQLite 持久化                          │
└──────────────┬─────────────────────────────────────────┘
               │ 复用现有能力（节点改造适配）
┌──────────────▼─────────────────────────────────────────┐
│ orchestrator.py 现有 15 节点 → 包装为 NodeSpec + NodeFn   │
│ rag.py / rca.py / tools.py / skills.py / llm_mock        │
└─────────────────────────────────────────────────────────┘
```

**与现有系统关系**：
- 现有 `/api/v1/ai/chat`（chat_graph）、`/api/v1/ops/tasks`（任务工作台）**保持不动**
- 新增 flow 引擎作为独立能力层，可复用现有节点执行逻辑
- 现有 `/api/v1/ai/flows`（只读 + 同步 run）**扩展为完整 CRUD + 异步 run 轮询**，向后兼容

---

## 二、数据模型（对齐 ongrid Graph wire 格式）

### Graph
```json
{
  "id": "flow_xxxx",
  "name": "我的诊断流程",
  "description": "",
  "enabled": true,
  "version": 1,
  "nodes": [
    {"id": "n1", "type": "collect", "name": "数据采集", "config": {"service": "{{trigger.service}}"}, "position": {"x": 0, "y": 0}},
    {"id": "n2", "type": "condition", "name": "有告警?", "config": {"expr": "{{nodes.n3.output.alert_count}} > 0"}}
  ],
  "edges": [
    {"id": "e1", "source": "n1", "sourcePort": "next", "target": "n3"},
    {"id": "e2", "source": "n2", "sourcePort": "true", "target": "n4"},
    {"id": "e3", "source": "n2", "sourcePort": "false", "target": "n5"}
  ]
}
```

### 核心语义
- **边只表达控制流**，数据不沿边走
- 数据通过共享 `RunContext` 传递：`context = {trigger: {...}, nodes: {id: {output, status}}, vars: {...}}`
- 节点 config 里的 `{{...}}` 在运行时解析为上游输出（模板引用）
- 输出端口：普通节点 `next`；`condition` 节点 `true`/`false`；所有节点隐式 `error`
- **无循环**：`graph.py` 用 Kahn 拓扑排序检测环
- **并行扇出**：多出边 → 线程并发执行（`ThreadPoolExecutor(max_workers=4)`）
- **OR-join + execute-once**：节点首次被任何入边激活才运行，之后 no-op，菱形结构不死锁
- **无并行 join/merge 节点**（对齐 ongrid，V2 计划）

---

## 三、NodeSpec 注册表（noderegistry.py）

```python
@dataclass
class NodeSpec:
    type: str            # "collect" / "rca" / "condition" / "wait_approval" ...
    kind: str            # trigger | action | control | data
    category: str        # 采集 / 分析 / 执行 / 控制
    label: str           # 中文名
    ports: list          # 控制输出端口，默认 ["next"]；condition=["true","false"]
    config_fields: list  # 配置表单字段，前端据此渲染
    output_shape: list   # 静态输出字段
    execute: Callable    # 执行器: fn(ctx, config) -> dict(output)
```

**注册表操作**：`register_node(spec)` / `lookup_node(type)` / `all_node_specs()`

**三端数据驱动**：新增节点 = 注册一个 spec，引擎/校验器/前端（`/node-types` API）自动识别，无 per-type switch。

### 现有 15 节点改造适配
把 `node_x(state) -> dict` 改为 `node_x(ctx, config) -> dict`，**复用函数体逻辑**，只改输入输出适配：

| 现有节点 | 现有强类型输出 | 新 NodeSpec 输出（`ctx.nodes.<id>.output`） |
|---|---|---|
| collect | services_data / infra_data / alert_data / red_metrics / trace_data / k8sgpt_raw | `{"services", "infra", "alerts", "red", "traces", "k8sgpt"}` |
| clean | （无）| `{}` |
| rca | rca_mode / rca_root_cause / rca_evidence / rca_confidence | `{"mode", "root_cause", "evidence", "confidence"}` |
| rag | similar_cases | `{"cases"}` |
| crewai | crewai_result | `{"result"}` |
| holmes | holmesgpt_result | `{"result"}` |
| plan | plan / script | `{"plan", "script"}` |
| risk | risk_score / risk_reason | `{"score", "reason"}` |
| execute | execute_output | `{"output"}` |
| verify | verify_pass / after_metrics | `{"pass", "after_metrics"}` |
| report | report | `{"report"}` |
| memorize | （无）| `{"stored"}` |
| summarize | final_response | `{"final_response"}` |

**审批节点** `wait_approval`（扩展，非 ongrid 原生）：
- KindControl，输出端口 `["approved", "rejected"]`
- 执行时使 run 置 `waiting_approval` 并暂停，持久化 RunContext
- `resume(approved=True/False)` 后走 `approved`/`rejected` 端口

**condition 节点** `condition`（对齐 ongrid）：
- KindControl，输出端口 `["true", "false"]`
- `config.expr` 如 `{{nodes.n3.output.alert_count}} > 0`，`expr.py` 求值决定走 true/false

---

## 四、执行器与审批（engine.py）

- `Engine.execute(graph, trigger)`：从 entry 启动调度
- 调度语义：
  - 节点激活 → 执行 `spec.execute(ctx, config)`
  - 输出写 `ctx.nodes.<id>.output`，同时决定 `fired_port`（next/true/false/error）
  - 按 `fired_port` 沿边激活下游
  - 多出边并行（`ThreadPoolExecutor`）
  - 每个节点 execute-once（已执行节点再次激活 no-op）
- **暂停 + 恢复（审批）**：
  - `wait_approval` 执行时 run 状态 → `waiting_approval`
  - 引擎把 `context_json` + 未执行子图 + 激活状态持久化到 `flow_runs`，run 返回
  - `POST /runs/{run_id}/resume`（approve/reject）→ 载入上下文继续执行
  - 与现有 `ops/tasks/{tid}/approve|reject` 审批流对接
- 错误处理：节点异常 → `error` 端口；无 error 边连出 → 整个 run `failed`

---

## 五、持久化（SQLite，核心闭环）

数据库：`/data/aiops-flows.db`（env `FLOWS_DB` 覆盖）

### flows 表（Workflow 定义）
| 字段 | 类型 | 说明 |
|---|---|---|
| id | TEXT PK | 流程 ID |
| name | TEXT | 名称 |
| description | TEXT | 描述 |
| enabled | INTEGER | 启停 |
| version | INTEGER | 版本（每次保存自增）|
| graph_json | TEXT | 整个画布 DAG JSON |
| created_at / updated_at | TEXT | 时间戳 |

### flow_runs 表（一次运行）
| 字段 | 类型 | 说明 |
|---|---|---|
| run_id | TEXT PK (UUID) | 运行 ID |
| flow_id | TEXT | 关联流程 |
| flow_version | INTEGER | 版本快照 |
| status | TEXT | pending/running/waiting_approval/succeeded/failed/canceled |
| trigger_type / trigger_json | TEXT | 触发器载荷 |
| context_json | TEXT | RunContext 快照（审批恢复用）|
| error | TEXT | 错误信息 |
| created_at | TEXT | 时间戳 |

### flow_run_nodes 表（单节点运行行）
| 字段 | 类型 | 说明 |
|---|---|---|
| run_id | TEXT | 关联运行 |
| node_id / node_type / node_name | TEXT | 节点快照 |
| status | TEXT | 节点状态 |
| input_json / output_json | TEXT | 解析模板后配置 + 输出 |
| fired_port | TEXT | next/true/false/error |
| error | TEXT | 错误 |

V2 迁 MySQL（对齐 ongrid 三表 + 保留策略 `PruneRuns` + `SweepStaleRunning`）。

---

## 六、API（FastAPI，对齐 ongrid 路由）

| 方法/路径 | 说明 |
|---|---|
| `GET /api/v1/ai/flows` | 列表（内置 full/chat + 用户自定义）|
| `GET /api/v1/ai/flows/{id}` | 详情（含 graph）|
| `POST /api/v1/ai/flows` | 创建用户 workflow |
| `PUT /api/v1/ai/flows/{id}` | 保存（更新，版本自增）|
| `DELETE /api/v1/ai/flows/{id}` | 删除 |
| `POST /api/v1/ai/flows/{id}/toggle` | 启停 |
| `POST /api/v1/ai/flows/{id}/run` | 手动运行（异步，返回 run_id）|
| `GET /api/v1/ai/flows/{id}/runs` | 运行列表 |
| `GET /api/v1/ai/flows/{id}/runs/{run_id}` | 运行详情（run + nodes 回放）|
| `POST /api/v1/ai/flows/{id}/runs/{run_id}/resume` | 审批恢复（approve/reject）|
| `GET /api/v1/ai/flows/node-types` | 节点注册表（数据驱动前端）|
| `GET /api/v1/ai/flows/tools` | 工具目录（tool 节点拖拽源）|

**兼容**：现有 `POST /api/v1/ai/flows/{key}/run`（同步返回）保留，与新的 `/runs` 异步轮询并存。

---

## 七、前端（React Flow，对齐 ongrid FlowEditor）

新增依赖：`@xyflow/react`（React Flow v12）。

### /workflows/editor 页
- **调色板**：`GET /ai/flows/node-types` 数据驱动渲染（按 category 分组）
- **画布**：React Flow，节点拖拽/连线，`sourceHandle` 即端口名（next/true/false/error），边 label 显示端口名并着色
- **condition 节点**：特殊渲染（true/false 双出口 + error 底部端口）
- **配置抽屉**：`config_fields` 驱动渲染表单
- **保存**：`PUT /ai/flows/{id}`（`fromCanvas(nodes, edges)` 序列化，`sourceHandle`→`sourcePort`）
- **运行 + 轮询**：每 1.5s 拉 `GET /runs/{run_id}`，把节点状态着色到画布，运行记录抽屉
- **审批**：run 到 `waiting_approval` → 显示审批卡（plan/script/风险 + 批准/拒绝调 `/resume`）

### /workflows 列表页改造
- 加"编辑"入口 → `/workflows/editor`
- 用户自定义 flow 展示 + CRUD（新建/删除/启停）
- 保留运行入口

### 技术栈并存
- 编辑器用 React Flow；拓扑页仍用 G6，两者并存不冲突
- 前端沿用现有 antd 5 + zustand + vite 体系

---

## 八、错误处理与测试（TDD）

### 错误处理
- 节点异常 → `error` 端口；无 error 边 → run `failed`
- 非法图（环/未知节点类型/孤儿节点/端口不匹配）→ 校验报错返回 400
- run 失败落库 `flow_runs.error` + `flow_run_nodes.error`

### 测试（先写失败测试 → 实现 → GREEN）
- **单元**：
  - `graph.py`：DAG 校验（环检测 / 未知节点类型 / 孤儿节点 / 端口合法性）
  - `expr.py`：模板引用解析（点路径 + 数组下标）+ 条件求值（== != > >= < <= contains）
  - `noderegistry.py`：注册/查询/重复注册
- **引擎**：
  - 顺序链执行顺序
  - condition 分支（true/false 各自走对）
  - 并行扇出 + OR-join 钻石不死锁（execute-once）
  - error 分支（有 error 边 vs 无 error 边）
  - wait_approval 暂停 + resume 恢复
- **API**：CRUD、run、resume、node-types、runs 列表/详情
- **前端**：编辑器拖拽/连线/保存/回读

---

## 九、工作量（核心闭环，约 12-15 人日 / 2 周）

| 模块 | 人日 |
|---|---|
| 后端引擎（noderegistry + graph + engine + expr）| 4-5 |
| 15 节点改造适配 | 2-3 |
| 持久化 + API | 2-3 |
| 前端 React Flow 编辑器 + 列表集成 | 3-4 |
| **合计** | **12-15** |

---

## 十、V2 后置（不在本次范围）

- MySQL 三表持久化（含 PruneRuns / SweepStaleRunning）
- cron/alert 触发器（scheduler + dispatcher）
- AI 生成工作流（`/generate`，LLM 自然语言 → 图 JSON）
- test-node 单节点试跑
- 工具目录完备（tool 节点拖拽）
- 并行 join/merge 节点
- 版本控制 / 回滚 / 发布

---

## 决策记录

1. **引擎**：自研 Python DAG 执行器（方案 A，最贴合 ongrid），替换 LangGraph 硬编码执行层
2. **交付范围**：核心闭环先行（NodeSpec + DAG 引擎 + 15 节点改造 + SQLite + API + React Flow），V2 后置完整能力
3. **审批机制**：暂停 + 恢复（FlowRun `waiting_approval` + `context_json` 快照 + resume API）
4. **节点命名**：沿用现有命名（collect/rca/rag/crewai/...），output 用统一 dict
5. **前端**：编辑器用 React Flow v12，拓扑页仍 G6 并存
6. **运行**：新增 `/runs` 异步轮询，与现有同步 `/run` 兼容并存
7. **授权合规**：Clean-room 独立实现，仅借鉴 ongrid 架构理念（NodeSpec/RunContext/模板引用），不复制其代码
