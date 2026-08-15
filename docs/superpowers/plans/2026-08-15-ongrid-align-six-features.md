# ongrid 对齐六项功能实施方案

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 补齐对齐 ongrid v0.12.0 的六项能力：工作流可视化前端（可编辑）、多智能体分工、K8s 生命周期动作、Skill 文件化+市场、内置运维知识库、Grafana 集成。

**Architecture:** 六个工作流（A–F）相互独立，各自交付自包含后端模块（Python 文件）+ 前端页面目录；对共享文件（`main.py`、`tools.py`、`App.tsx`、`api/client.ts`）的修改集中为每工作流末尾的"挂载（Mount）"小节，由集成者串行执行，避免并行写冲突。所有设计对齐 ongrid 的实现模式（触发器类型、persona 目录、preflight token、SKILL.md、playbook frontmatter、dashboard JSON 代理）。

**Tech Stack:** 后端 ai-orchestrator（Python/FastAPI + APScheduler + ChromaDB + SQLite/MySQL）；ai-apm-query-go（Go/Gin，仅 Grafana 代理一处改动）；前端 observability-frontend（React 18.3 + TS 5.6 + antd 5.22 + @xyflow/react 12.11 + zustand + axios + echarts 5）。

**Spec:** `AIOPS_AUDIT_REPORT_2026-08-15.md` 第八章（能力对比与差距分析）。侦察依据：`ongrid-ref/` 本地副本（v0.12.0）。

## Global Constraints

- 端点统一挂在 `/api/v1/ai/*` 或 `/api/v1/ops/*` 下，鉴权复用 `_require_internal_token` / `_require_admin` / `_require_approver`（main.py 现有装饰器）。
- 前端页面放 `observability-frontend/src/pages/<域>/<Page>.tsx`；路由注册在 `src/App.tsx`（lazy import 14-29 行 + `<Routes>` 290-309 行）；菜单加 `NAV_GROUPS`（35-85 行）；API 统一走 `src/api/client.ts` 的共享 axios 实例（baseURL `/api/v1`，自动 X-Tenant-ID/Bearer/cluster_id）；UI 用 `src/components/ui/PageKit.tsx` + `src/index.css` CSS 变量。
- 后端新模块一律 `ai-orchestrator/<name>.py` 单文件模块，不新建目录（跟随 flow_engine/ 单文件惯例）；SQLite 文件放 `AIOPS_DATA_DIR`（默认 `/data`）；MySQL 复用现有 db_* 模块。
- 测试：后端 pytest（`ai-orchestrator/tests/`）；前端 `npm run build` 通过 + playwright（`.playwright-cli.json`）人工验收。
- 提交：每任务完成即 commit，message 风格 `feat: <模块> <简述>`。
- 安全红线：外部安装的 skill 只能是"提示词 + 引用已有工具"，**禁止执行外部代码**；写操作必须过 `execution_gate`/审批 + 审计。
- 依赖红线：前端不新增 npm 依赖（@xyflow/react/echarts 已在栈内）；后端仅允许新增 `ecdsa`（或复用已有 `cryptography`，Task D4 验证后定）。

---

## 现状 → 目标速览

| 工作流 | 现状（已核实） | 目标 |
|---|---|---|
| A 工作流 | flow_engine 后端完备（15 节点/11 端点），触发器硬编码 manual，无 cron/告警触发；v2 有 Editor.tsx(731 行,@xyflow/react) 可复用；v3 无页面 | 触发器三类型（manual/cron/alert_fired）+ NL 生成 + test-node；v3 可编辑画布页 |
| B 多智能体 | 8 skills + 4 Expert（keyword 匹配）+ dual_agent.py（未启用）+ CrewAI factory；无 persona 文件 | persona 文件注册表 + coordinator LLM 路由 + spawn_worker 工具 + 告警自动调查 |
| C K8s 动作 | ShellPolicy 白名单已含 restart/scale/undo/delete pod；无结构化动作/preflight/资源校验；query-api 侧 kubectl 全只读 | execute_k8s_action 结构化动作 + preflight token + 乐观锁 + 审批 + 前端页面 |
| D Skill 市场 | 8 个 skill 硬编码 Python（skills/*.py） | SKILL.md 文件化 + 签名校验 + marketplace 安装/卸载/热载 |
| E 知识库 | 77 案例 + ChromaDB 检索/反馈闭环；无 playbook | 5 分类 playbook + ops_playbooks 向量集合 + query_knowledge 工具 + 浏览页 |
| F Grafana | deepflow 自带 grafana + nginx /grafana/ 代理；v3 无页面 | query-api 代理 dashboard JSON + v3 Grafana 页（iframe + 原生面板渲染） |

---

## 工作流 A：工作流可视化前端（可编辑）+ 触发器

**Goal:** v3 前端拥有可编辑的 React Flow 画布（节点拖拽/配置/连线/运行/历史），后端支持 manual/cron/alert_fired 三种触发器 + NL 生成 + 单节点试跑。

**Architecture:** 触发器节点按 ongrid 模式实现（`trigger.manual|trigger.cron|trigger.alert_fired`，`alertMatches` 规则名子串 + min_severity）。cron 用现有 APScheduler（main.py:36-59 已有实例）管理 job；告警触发挂到现有 `/api/v1/ops/webhook` 处理器上。前端把 v2 Editor.tsx 移植到 v3 约定（技术栈同源：@xyflow/react 已是依赖）。

### Task A1: 触发器节点类型 + 校验

**Files:**
- Create: `ai-orchestrator/flow_engine/nodes_trigger.py`
- Modify: `ai-orchestrator/flow_engine/noderegistry.py`（register 时并入 `nodes_trigger.TRIGGER_NODES`）
- Modify: `ai-orchestrator/flow_engine/graph.py`（`validate_graph` 增加 trigger 规则）
- Test: `ai-orchestrator/tests/test_nodes_trigger.py`

**Interfaces:**
- Produces: `TRIGGER_NODES: dict[str, dict]`；`exec_trigger(ctx: RunContext, node_id, node_type, config) -> dict`；`alert_matches(config: dict, rule: str, severity: str) -> bool`（A3/B6 复用）

- [ ] **Step 1: 写失败测试**

```python
# tests/test_nodes_trigger.py
from flow_engine.nodes_trigger import TRIGGER_NODES, alert_matches

def test_trigger_node_types_registered():
    assert set(TRIGGER_NODES) == {"trigger.manual", "trigger.cron", "trigger.alert_fired"}
    for spec in TRIGGER_NODES.values():
        assert spec["kind"] == "trigger" and "next" in spec["ports"]

def test_alert_matches_rule_and_severity():
    cfg = {"rule": "high-cpu", "min_severity": "warning"}
    assert alert_matches(cfg, "high-cpu", "critical") is True
    assert alert_matches(cfg, "high-cpu", "info") is False      # 低于最低级别
    assert alert_matches(cfg, "high-mem", "critical") is False  # 规则名不匹配

def test_alert_matches_empty_rule_matches_any():
    assert alert_matches({"rule": "", "min_severity": "warning"}, "any-rule", "critical") is True
```

- [ ] **Step 2: 运行确认失败** `cd ai-orchestrator && python -m pytest tests/test_nodes_trigger.py -v` → FAIL（模块不存在）

- [ ] **Step 3: 实现**

```python
# ai-orchestrator/flow_engine/nodes_trigger.py
"""触发器节点: trigger.manual / trigger.cron / trigger.alert_fired"""
from flow_engine.expr import RunContext

TRIGGER_NODES = {
    "trigger.manual": {
        "label": "手动触发", "kind": "trigger", "ports": ["next"],
        "config_fields": {"description": {"type": "text", "label": "说明", "default": ""}},
    },
    "trigger.cron": {
        "label": "定时触发", "kind": "trigger", "ports": ["next"],
        "config_fields": {"cron": {"type": "text", "label": "Cron 表达式(5 段, UTC)", "default": "0 * * * *"}},
    },
    "trigger.alert_fired": {
        "label": "告警触发", "kind": "trigger", "ports": ["next"],
        "config_fields": {
            "rule": {"type": "text", "label": "告警规则名(子串匹配, 空=全部)", "default": ""},
            "min_severity": {"type": "select", "label": "最低级别",
                             "options": ["info", "warning", "critical"], "default": "warning"},
        },
    },
}

SEVERITY_ORDER = {"info": 0, "warning": 1, "critical": 2}


def exec_trigger(ctx: RunContext, node_id: str, node_type: str, config: dict) -> dict:
    """trigger 节点把触发信息写入 ctx.vars, 下游节点用 {{vars.<node_id>.*}} 引用"""
    t = ctx.trigger or {}
    ctx.vars.setdefault(node_id, {})
    ctx.vars[node_id].update({
        "type": node_type,
        "fired_at": t.get("fired_at", ""),
        "payload": t.get("payload", {}),
        "cron": config.get("cron", ""),
        "rule": config.get("rule", ""),
    })
    return {"ok": True, "node_id": node_id}


def alert_matches(config: dict, rule: str, severity: str) -> bool:
    rule_cfg = (config.get("rule") or "").strip()
    if rule_cfg and rule_cfg not in (rule or ""):
        return False
    min_sev = config.get("min_severity", "warning")
    return SEVERITY_ORDER.get(severity, 0) >= SEVERITY_ORDER.get(min_sev, 0)
```

同时在 `noderegistry.py` 的注册列表并入 `TRIGGER_NODES`（NodeSpec 构造与现有节点一致），`graph.py` 的 `validate_graph` 增加两条规则：① 一张图有且只有一个 `trigger.*` 节点；② trigger 节点不允许有入边（现有"无入边节点=入口"逻辑不变）。

- [ ] **Step 4: 运行确认通过** `python -m pytest tests/test_nodes_trigger.py -v` → PASS
- [ ] **Step 5: Commit** `git add ai-orchestrator/flow_engine/nodes_trigger.py ai-orchestrator/flow_engine/noderegistry.py ai-orchestrator/flow_engine/graph.py ai-orchestrator/tests/test_nodes_trigger.py && git commit -m "feat: flow 触发器节点类型 trigger.manual/cron/alert_fired"`

### Task A2: cron 触发器调度

**Files:**
- Create: `ai-orchestrator/flow_engine/trigger_scheduler.py`
- Modify: `ai-orchestrator/flow_engine/usecase.py`（`run_flow(flow_id, trigger=None, message=None)` 签名与 `trigger_type` 透传，替换硬编码 `"manual"`）
- Test: `ai-orchestrator/tests/test_trigger_scheduler.py`

**Interfaces:**
- Consumes: `WorkflowService.list_flows()` / `run_flow(...)`（usecase.py 现有）
- Produces: `CronTriggerManager(scheduler, list_enabled_flows, run_flow)`，`.sync()`（幂等全量对齐 job）

- [ ] **Step 1: 写失败测试**

```python
# tests/test_trigger_scheduler.py
from flow_engine.trigger_scheduler import CronTriggerManager

class FakeSched:
    def __init__(self): self.jobs = {}
    def add_job(self, fn, trigger, args, id, replace_existing, misfire_grace_time):
        self.jobs[id] = (fn, args); return type("J", (), {"id": id})()
    def remove_job(self, job_id): self.jobs.pop(job_id, None)

def test_sync_adds_and_removes_cron_jobs():
    flows = [
        {"id": "f1", "graph": {"nodes": [{"type": "trigger.cron", "config": {"cron": "0 * * * *"}}]}},
        {"id": "f2", "graph": {"nodes": [{"type": "trigger.manual"}]}},
    ]
    ran = []
    mgr = CronTriggerManager(FakeSched(), lambda: flows, lambda fid, trigger: ran.append(fid))
    mgr.sync()
    assert "flow-cron-f1" in mgr._sched.jobs          # 有 cron 的加 job
    mgr._sched.jobs["flow-cron-f1"][0]()             # 触发
    assert ran == ["f1"]
    flows.pop(0)                                      # f1 删除
    mgr.sync()
    assert "flow-cron-f1" not in mgr._sched.jobs      # job 同步移除
```

- [ ] **Step 2: 运行确认失败** → FAIL
- [ ] **Step 3: 实现**

```python
# ai-orchestrator/flow_engine/trigger_scheduler.py
"""cron 触发器: 30s 扫描启用的 workflow, 与 APScheduler job 幂等对齐"""
import logging
from datetime import datetime, timezone

from apscheduler.triggers.cron import CronTrigger

log = logging.getLogger(__name__)
SCAN_INTERVAL_SECONDS = 30


def _now_utc() -> str:
    return datetime.now(timezone.utc).isoformat()


class CronTriggerManager:
    def __init__(self, scheduler, list_enabled_flows, run_flow):
        self._sched = scheduler
        self._list = list_enabled_flows
        self._run = run_flow
        self._jobs = {}  # flow_id -> (job_id, cron_expr)

    def sync(self):
        desired = {}
        for f in self._list():
            for node in (f.get("graph") or {}).get("nodes", []):
                if node.get("type") == "trigger.cron":
                    desired[f["id"]] = (node.get("config") or {}).get("cron") or "0 * * * *"
        for flow_id, cron_expr in desired.items():
            if flow_id in self._jobs and self._jobs[flow_id][1] == cron_expr:
                continue
            if flow_id in self._jobs:
                try:
                    self._sched.remove_job(self._jobs[flow_id][0])
                except Exception:
                    pass
            job = self._sched.add_job(
                self._fire, CronTrigger.from_crontab(cron_expr), args=[flow_id],
                id=f"flow-cron-{flow_id}", replace_existing=True, misfire_grace_time=60)
            self._jobs[flow_id] = (job.id, cron_expr)
        for flow_id in list(self._jobs):
            if flow_id not in desired:
                try:
                    self._sched.remove_job(self._jobs.pop(flow_id)[0])
                except Exception:
                    pass

    def _fire(self, flow_id: str):
        log.info("cron 触发 workflow %s", flow_id)
        self._run(flow_id, trigger={"type": "cron", "fired_at": _now_utc(), "payload": {}})
```

`usecase.py` 的 `run_flow` 改为接收 `trigger: dict | None`，`trigger_type = trigger.get("type", "manual") if trigger else "manual"`，其余逻辑不变（引擎已支持 trigger 传参）。

- [ ] **Step 4: 运行确认通过** → PASS；另跑既有 flow 测试 `python -m pytest tests/ -k flow` 确认无回归
- [ ] **Step 5: Commit** `git add ... && git commit -m "feat: workflow cron 触发器调度(30s 对齐 APScheduler job)"`

### Task A3: 告警触发（alert_fired）

**Files:**
- Create: `ai-orchestrator/flow_engine/flow_alert_dispatch.py`
- Modify: `ai-orchestrator/main.py`（`/api/v1/ops/webhook` 处理器尾部调用 dispatch；Mount A3）
- Test: `ai-orchestrator/tests/test_flow_alert_dispatch.py`

**Interfaces:**
- Consumes: `nodes_trigger.alert_matches`（A1）
- Produces: `dispatch_alert(list_enabled_flows, run_flow, rule, severity, payload) -> list[{"flow_id","run_id"}]`（B6 复用）

- [ ] **Step 1: 写失败测试**（仿 A2：两条 flow，一条 alert_fired 规则命中、一条 cron 不命中，断言只触发命中者且 payload 透传）
- [ ] **Step 2: 运行确认失败** → FAIL
- [ ] **Step 3: 实现**

```python
# ai-orchestrator/flow_engine/flow_alert_dispatch.py
"""告警触发: 告警事件匹配 trigger.alert_fired 节点并运行 workflow"""
import logging

from flow_engine.nodes_trigger import alert_matches
from flow_engine.trigger_scheduler import _now_utc

log = logging.getLogger(__name__)


def dispatch_alert(list_enabled_flows, run_flow, rule: str, severity: str, payload: dict) -> list:
    fired = []
    for f in list_enabled_flows():
        for node in (f.get("graph") or {}).get("nodes", []):
            if node.get("type") != "trigger.alert_fired":
                continue
            if not alert_matches(node.get("config") or {}, rule, severity):
                continue
            run_id = run_flow(f["id"], trigger={
                "type": "alert_fired", "fired_at": _now_utc(),
                "payload": payload or {}})
            fired.append({"flow_id": f["id"], "run_id": run_id})
            break  # 每个 flow 最多触发一次
    return fired
```

**Mount A3**（main.py，`/api/v1/ops/webhook` 处理器末尾，告警已落库后）：
```python
try:
    fired = dispatch_alert(workflow_service.list_enabled_flows,
                           workflow_service.run_flow, rule, severity, payload)
    if fired:
        logger.info("告警触发 workflow: %s", fired)
except Exception:
    logger.exception("告警→workflow 派发失败(不影响告警入库)")
```

- [ ] **Step 4: 运行确认通过** → PASS
- [ ] **Step 5: Commit** `git commit -m "feat: 告警触发 workflow(alert_fired 匹配规则名+级别)"`

### Task A4: NL 生成工作流端点

**Files:**
- Modify: `ai-orchestrator/flow_api.py`（`POST /api/v1/ai/workflows/generate`）
- Test: `ai-orchestrator/tests/test_flow_generate.py`

**Interfaces:**
- Consumes: `main.py` 的 LLM 配置解析（`_parse_llm_config` / orchestrator `_LLM_KEY_HOLDER`，按现文件内获取 LLM 的方式复用）；`noderegistry` 节点类型清单；`graph.validate_graph`
- Produces: `POST /generate` body `{"prompt": str}` → `{"name","description","graph"}`（graph 已通过校验）

- [ ] **Step 1: 写失败测试**（mock LLM 返回固定 JSON 字符串：含 `\`\`\`json` 围栏与尾注释，断言 strip 后校验通过返回；非法 graph（环）断言 400）
- [ ] **Step 2: 运行确认失败** → FAIL
- [ ] **Step 3: 实现**

```python
# flow_api.py 追加
GENERATE_SYSTEM_PROMPT = """你是工作流设计器。根据用户需求生成 JSON 对象(不要 markdown 围栏):
{"name":"<短名>","description":"<说明>","graph":{"nodes":[{"id":"n1","type":"trigger.manual","name":"手动触发","config":{},"position":{"x":0,"y":0}}],"edges":[{"id":"e1","source":"n1","sourcePort":"next","target":"n2"}]}}
可用节点类型: {node_types}
规则: 有且仅有一个 trigger.* 节点; 边 sourcePort ∈ next/true/false/approved/rejected/error; 只输出 JSON。"""

@router.post("/generate")
def generate_flow(body: GenerateBody):
    _require_admin()
    llm = _resolve_llm()                      # 复用现有 LLM 解析
    node_types = ", ".join(sorted(registry.node_types()))
    raw = llm.chat(system=GENERATE_SYSTEM_PROMPT.format(node_types=node_types), user=body.prompt)
    raw = _strip_code_fences(raw)
    graph = json.loads(raw)
    err = validate_graph(graph.get("graph", {}))
    if err:
        raise HTTPException(400, f"生成结果非法: {err}")
    return {"name": graph.get("name", "生成工作流"),
            "description": graph.get("description", ""),
            "graph": graph["graph"]}
```

- [ ] **Step 4: 运行确认通过** → PASS
- [ ] **Step 5: Commit** `git commit -m "feat: NL 一句话生成工作流 /ai/workflows/generate"`

### Task A5: 单节点试跑（test-node）

**Files:**
- Modify: `ai-orchestrator/flow_api.py`（`POST /api/v1/ai/workflows/test-node`）
- Test: `ai-orchestrator/tests/test_flow_testnode.py`

**Interfaces:**
- Consumes: `Engine.execute`（engine.py 现有）
- Produces: `POST /test-node` body `{"type","config","trigger"}` → `{"ok","output"}`

- [ ] **Step 1: 写失败测试**（构造单节点图 `{nodes:[{id:"n1",type:"collect",config:{...},position:{}}],edges:[]}`，mock 运行后断言 output 返回）
- [ ] **Step 2: 运行确认失败** → FAIL
- [ ] **Step 3: 实现**：构造 1 节点临时图 → `Engine.execute(graph, trigger, resume_hook=None)` → 返回该节点 output；节点类型不合法 → 400。
- [ ] **Step 4: 运行确认通过** → PASS
- [ ] **Step 5: Commit** `git commit -m "feat: workflow 单节点试跑 test-node 端点"`

### Task A6: v3 前端 Workflows 页面（列表 + 可编辑画布 + 运行历史）

**Files:**
- Create: `observability-frontend/src/pages/ai/Workflows/index.tsx`（列表 + NL 生成入口 + 新建）
- Create: `observability-frontend/src/pages/ai/Workflows/Editor.tsx`（画布）
- Create: `observability-frontend/src/pages/ai/Workflows/Detail.tsx`（运行历史 + 审批恢复）
- Modify: `observability-frontend/src/api/client.ts`（追加 workflow API 函数）
- Modify: `observability-frontend/src/App.tsx`（Mount A6：lazy import + Route `/ai/workflows`、`/ai/workflows/editor`、`/ai/workflows/:id` + NAV_GROUPS 菜单项 `{path:'/ai/workflows', label:'工作流', icon:'workflow'}`）

**Interfaces:**
- Consumes: `/ai/workflows` 全部 11 端点 + `/generate` + `/test-node`；`GET /ai/workflows/node-types` 数据驱动节点面板

- [ ] **Step 1: 移植清单核对**。读 `src_legacy_v2/pages/Workflows/`（index.tsx:212 / Editor.tsx:731 / Detail.tsx:162）与 `src_legacy_v2/api/client.ts:64-164`，将调用的 legacy API 函数签名对应到 v3 client 追加（路径不变，仅改为共享 `api` 实例）。
- [ ] **Step 2: client.ts 追加**。按 v3 约定（`src/api/client.ts` 现有 `export const xxx = ...` 风格）追加 `listWorkflows/createWorkflow/getWorkflow/updateWorkflow/deleteWorkflow/toggleWorkflow/runWorkflow/listFlowRuns/getFlowRun/resumeFlowRun/listWorkflowNodeTypes/generateWorkflow/testFlowNode`。
- [ ] **Step 3: Editor.tsx 移植**。将 v2 Editor.tsx 的画布逻辑移植到 v3：React Flow 画布（`@xyflow/react` 12.x API）、节点面板数据来自 `GET node-types`（按 `config_fields` 渲染表单，含 trigger 三类型的 cron/rule/min_severity 配置）、边 `sourcePort` 下拉（next/true/false/approved/rejected/error）、保存 PUT、运行 Drawer（trigger payload JSON 编辑 + 手动运行）、`/generate` NL 生成按钮（结果预览确认后落画布）、单节点 test-node 试跑按钮。UI 外层套 `PageKit.tsx` PageHeader/Breadcrumb，样式沿用 CSS 变量。
- [ ] **Step 4: index.tsx / Detail.tsx 移植**。列表卡片（名称/启用开关/最近运行状态）+ 运行历史表格（GET runs + run detail 节点明细），waiting_approval 状态行显示"批准/拒绝"按钮（仅 approver 角色，调 resume 端点）。
- [ ] **Step 5: Mount A6**（App.tsx 路由 + 菜单，见 Files）。
- [ ] **Step 6: 验证**：`cd observability-frontend && npm run build` 通过；playwright 手工验收：新建 cron 流程（每分钟）→ 观察 1 分钟内自动出现 run 记录 → 编辑连线/保存 → 手动 run → 历史可见。
- [ ] **Step 7: Commit** `git commit -m "feat: v3 工作流可视化前端(列表/编辑器/运行历史/NL 生成)"`

---

## 工作流 B：多智能体分工

**Goal:** persona 文件化注册表 + coordinator LLM 自主路由 + `spawn_worker` 工具（同步/后台）+ 告警自动调查闭环（incident-investigator）。

**Architecture:** 对齐 ongrid：persona = markdown frontmatter 文件（`name/when_to_use/tools/permission_mode/max_turns`）；coordinator system prompt 注入"可用 specialist 目录"由 LLM 选择；worker 复用现有 `function_calling.run_tool_loop` 执行；后台 worker 终态经 chat SSE `task_notification` frame 投递。

### Task B1: 共享 frontmatter 解析工具 + persona 文件

**Files:**
- Create: `ai-orchestrator/md_meta.py`
- Create: `ai-orchestrator/personas/specialist-sre.md`、`specialist-ops.md`、`specialist-compute.md`、`specialist-disk.md`、`specialist-network.md`、`incident-investigator.md`、`reporter.md`、`reviewer.md`（8 个文件）
- Test: `ai-orchestrator/tests/test_md_meta.py`

**Interfaces:**
- Produces: `md_meta.split_frontmatter(text) -> (meta: dict, body: str)`（D2/E2 复用）

- [ ] **Step 1: 写失败测试**（合法 frontmatter 解析出字段；无 `---` 抛 ValueError；未闭合抛 ValueError）
- [ ] **Step 2: 运行确认失败** → FAIL（依赖 yaml，若 `requirements.txt` 无 `PyYAML` 则本步先加依赖）
- [ ] **Step 3: 实现**

```python
# ai-orchestrator/md_meta.py
"""markdown frontmatter 解析共享工具 (persona / SKILL.md / playbook 共用)"""
import yaml


def split_frontmatter(text: str):
    lines = text.splitlines()
    if not lines or lines[0].strip() != "---":
        raise ValueError("缺少 YAML frontmatter (--- 开头)")
    end = None
    for i in range(1, len(lines)):
        if lines[i].strip() == "---":
            end = i
            break
    if end is None:
        raise ValueError("frontmatter 未闭合 (缺少结尾 ---)")
    meta = yaml.safe_load("\n".join(lines[1:end])) or {}
    return meta, "\n".join(lines[end + 1:])
```

- [ ] **Step 4: 运行确认通过** → PASS
- [ ] **Step 5: 写 persona 文件**。示例（其余 7 个照此，tools 从现有 ToolRegistry 工具名取，如 query_metrics/query_traces/get_service_list/probe_http/probe_tcp/read_journal/tail_file/case_search/rca_analyze/execute_shell 等）：

```markdown
---
name: specialist-sre
description: 资深 SRE，负责 Kubernetes 与容器平台故障
when_to_use: 涉及 pod/deployment/service/节点调度、资源配额、容器启动失败的诊断
tools: [query_metrics, query_traces, get_service_list, probe_http, probe_tcp, read_journal, tail_file]
permission_mode: read-only
max_turns: 20
---
你是资深 SRE。基于观测数据定位 K8s 层问题，优先看 pod 状态/事件/资源水位，输出结论与证据链。
```

  - `incident-investigator.md`：`permission_mode: read-only`、`max_turns: 40`、`when_to_use: 告警触发后的根因调查`
  - `reviewer.md`：`permission_mode: read-only`、`max_turns: 5`、body 规定输出 `Decision: approve|reject`（供 C 工作流二审用）
  - `reporter.md`：`when_to_use: 报告生成`
- [ ] **Step 6: Commit** `git commit -m "feat: persona 文件(sre/ops/compute/disk/network/investigator/reporter/reviewer) + md_meta 工具"`

### Task B2: persona 注册表

**Files:**
- Create: `ai-orchestrator/persona_registry.py`
- Test: `ai-orchestrator/tests/test_persona_registry.py`

**Interfaces:**
- Consumes: `md_meta.split_frontmatter`（B1）
- Produces: `Persona` dataclass；`load_personas(*dirs) -> dict[str, Persona]`（builtin `personas/` 目录 + `AIOPS_DATA_DIR/personas` 用户目录）；`build_catalog(personas) -> str`（coordinator 目录注入用，B5 复用）

- [ ] **Step 1: 写失败测试**（加载 fixtures 目录：合法文件入表；缺 `when_to_use` 抛 ValueError；非法 permission_mode 抛 ValueError；builtin+user 同名时 user 覆盖）
- [ ] **Step 2: 运行确认失败** → FAIL
- [ ] **Step 3: 实现**（结构见本方案设计：`Persona` 字段 `name/description/when_to_use/system_prompt/tools/disallowed_tools/permission_mode/max_turns/background/model/source`；`build_catalog` 排除 reviewer/reporter，输出 `- name: description | when_to_use 首行` 列表）
- [ ] **Step 4: 运行确认通过** → PASS
- [ ] **Step 5: Mount B2**（main.py 启动时 `PERSONAS = load_personas(PERSONAS_BUILTIN_DIR, USER_PERSONAS_DIR)`，注入 chat 处理与 `/ai/agents` 列表端点合并返回）
- [ ] **Step 6: Commit** `git commit -m "feat: persona 注册表(启动加载 builtin+用户目录)"`

### Task B3: spawn_worker 工具 + worker 运行时

**Files:**
- Create: `ai-orchestrator/agent_tool.py`
- Modify: `ai-orchestrator/tools.py`（注册 `spawn_worker`，Class=write）
- Test: `ai-orchestrator/tests/test_agent_tool.py`

**Interfaces:**
- Consumes: `persona_registry`（B2）；`function_calling.run_tool_loop`（现有）；`execution_gate.check_tool_executable`（现有）
- Produces: `spawn_worker(subagent_type: str, description: str, prompt: str) -> str`（同步阻塞返回 worker 结果）；`run_worker(persona, prompt) -> str`（B6 复用）

- [ ] **Step 1: 写失败测试**（mock LLM：spawn 一个 persona 返回其 system_prompt 拼接任务文本；未注册 persona 返回错误串；permission_mode=read-only 时工具集 = persona.tools 白名单 ∩ 现有只读白名单）
- [ ] **Step 2: 运行确认失败** → FAIL
- [ ] **Step 3: 实现**

```python
# ai-orchestrator/agent_tool.py
"""spawn_worker 工具: coordinator 派活给 specialist persona"""
import hashlib
import time

from function_calling import run_tool_loop

_DEDUPE_TTL = 90


def run_worker(persona, prompt: str) -> str:
    """worker 运行时: persona 工具白名单 + max_turns 上限"""
    from skill_registry import ToolRegistry
    registry = ToolRegistry.get_instance() if hasattr(ToolRegistry, "get_instance") else None
    # 工具集 = persona.tools 白名单 - disallowed; read-only 时再 ∩ 只读白名单
    tools = [t for t in (registry.all_tools() if registry else [])
             if t.name in persona.tools and t.name not in persona.disallowed_tools]
    if persona.permission_mode == "read-only":
        tools = [t for t in tools if t.name in WHITELIST_READONLY_NAMES]
    system = persona.system_prompt + "\n\n## 任务\n" + prompt + \
        f"\n(最多 {persona.max_turns} 轮工具调用; 结束时输出结论)"
    return run_tool_loop(llm_decision, tools, prompt, whitelist=...)


def spawn_worker(subagent_type: str, description: str, prompt: str) -> str:
    personas = _personas()  # 由 main.py 注入
    p = personas.get(subagent_type)
    if not p:
        return f"错误: 未知 specialist {subagent_type}, 可用: {', '.join(personas)}"
    return run_worker(p, prompt)
```

  - `_personas` 由 Mount 注入（main.py 挂载时 set）。
- [ ] **Step 4: 运行确认通过** → PASS
- [ ] **Step 5: Commit** `git commit -m "feat: spawn_worker 工具 + persona worker 运行时(白名单+max_turns)"`

### Task B4: 后台 worker + SSE task_notification

**Files:**
- Modify: `ai-orchestrator/agent_tool.py`（`spawn_worker` 增加 `background` 参数）
- Modify: `ai-orchestrator/main.py`（chat SSE 流支持 `task_notification` frame；Mount B4）
- Test: `ai-orchestrator/tests/test_agent_tool_bg.py`

**Interfaces:**
- Produces: `spawn_worker(..., background=True) -> str`（立即返回 "已后台启动"）；worker 终态发 SSE frame `{"type":"task_notification","data":{"worker":name,"status":"completed|failed","summary":...}}`

- [ ] **Step 1: 写失败测试**（background=True 不阻塞；完成回调被调用且携带 worker 名）
- [ ] **Step 2: 运行确认失败** → FAIL
- [ ] **Step 3: 实现**：ThreadPoolExecutor 异步执行 + `on_done` 回调写入 chat 会话的消息队列；Mount B4 在 `/api/v1/ai/chat` SSE 生成器中轮询该队列并 yield `task_notification` frame（格式同上）。
- [ ] **Step 4: 运行确认通过** → PASS
- [ ] **Step 5: Commit** `git commit -m "feat: 后台 worker + chat SSE task_notification 通知"`

### Task B5: coordinator 路由（dual_agent 启用）

**Files:**
- Modify: `ai-orchestrator/dual_agent.py`
- Modify: `ai-orchestrator/main.py`（Mount B5：chat 开关打开时 coordinator system prompt 注入 `build_catalog(PERSONAS)`）

**Interfaces:**
- Consumes: `persona_registry.build_catalog`（B2）；`spawn_worker`（B3）
- Produces: coordinator 在 tool 循环中可调 `spawn_worker`；保留现有 `ExpertRegistry.match_intent` keyword 兜底（LLM 未选 agent 时回退现有专家逻辑）

- [ ] **Step 1: 写失败测试**（coordinator prompt 含 catalog 中 persona 名与 when_to_use 首行；不含 reviewer/reporter）
- [ ] **Step 2: 运行确认失败** → FAIL
- [ ] **Step 3: 实现**：`dual_agent.py` Coordinator 构造处拼接 `build_catalog` 输出；`_expert_tools` 增加 spawn_worker；Reviewer 角色保持现有合并逻辑。
- [ ] **Step 4: 运行确认通过** → PASS
- [ ] **Step 5: Commit** `git commit -m "feat: coordinator LLM 路由 specialist(目录注入+keyword 兜底)"`

### Task B6: 告警 → incident-investigator 自动调查

**Files:**
- Create: `ai-orchestrator/investigator.py`
- Modify: `ai-orchestrator/main.py`（Mount B6：`/api/v1/ops/webhook` 处理器与 A3 dispatch 并列调用）
- Test: `ai-orchestrator/tests/test_investigator.py`

**Interfaces:**
- Consumes: `run_worker`（B3）+ `incident-investigator` persona（B1）；`rag.add_knowledge`（现有，调查报告入库）
- Produces: `maybe_investigate(rule, severity, payload, run_worker) -> str | None`（门控：`INVESTIGATOR_ENABLED` env 默认 true、`min_severity=warning`、`dedup_window=300s`、`max_concurrent=5`、超时 5min）

- [ ] **Step 1: 写失败测试**（低级别告警返回 None；去重窗口内重复告警返回 None；正常告警调用 run_worker 且把结果 add_knowledge）
- [ ] **Step 2: 运行确认失败** → FAIL
- [ ] **Step 3: 实现**：仿 ongrid `biz/alert/investigator` 配置门控；worker 输出写 `rag.add_knowledge(type="investigation", ...)`；Mount B6 在 webhook 处理器中 `background=True` 异步 spawn（不阻塞告警入库）。
- [ ] **Step 4: 运行确认通过** → PASS
- [ ] **Step 5: Commit** `git commit -m "feat: 告警自动调查(incident-investigator 门控+去重+入库)"`

---

## 工作流 C：K8s 生命周期动作

**Goal:** 结构化 K8s 动作（rollout_restart/scale/delete_pod/evict_pod/cordon/uncordon/drain）+ 两阶段 preflight token + 审批 + 审计 + v3 操作页面。

**Architecture:** 对齐 ongrid `execute_k8s_action`：dry-run 预检生成一次性 HMAC token（TTL 5min，绑定参数 sha256 + resourceVersion 乐观锁），真实写必须带 token；命令生成严格落在现有 `shell_policy.EXEC_WRITE` 白名单内（cordon/uncordon/drain 需先补白名单）；审批复用现有 `_require_approver` + `ApprovalStore`，审计复用 `_audit_log`。

### Task C0: 执行环境验证（前置）

- [ ] **Step 1**：检查 `ai-orchestrator/deployment.yaml` 是否挂载 kubeconfig（`kubectl` 可用性）。执行 `kubectl get pods -n <部署 ns>` 找到 ai-orchestrator pod → `kubectl exec <pod> -- kubectl get nodes` 验证。
- [ ] **Step 2**：若不可用 → 在 helm values 给 ai-orchestrator 挂 kubeconfig secret（参照 query-api 的挂载方式）；可用则记录结论。
- [ ] **Step 3: Commit**（仅 helm 变更时）`git commit -m "fix: ai-orchestrator 挂载 kubeconfig 支持 K8s 写操作"`

### Task C1: 动作 schema + 命令构建 + 白名单扩展

**Files:**
- Create: `ai-orchestrator/k8s_actions.py`
- Modify: `ai-orchestrator/shell_policy.py`（EXEC_WRITE 增补 `kubectl cordon node /`, `kubectl uncordon node /`, `kubectl drain node / --ignore-daemonsets`）
- Test: `ai-orchestrator/tests/test_k8s_actions.py`

**Interfaces:**
- Produces: `ACTIONS` / `ACTION_KINDS` 常量；`build_command(action, kind, namespace, name, **kw) -> str`；`make_preflight_token(...) -> str`；`verify_preflight_token(token, ...) -> bool`；`preflight(action, kind, namespace, name, **kw) -> dict`

- [ ] **Step 1: 写失败测试**（7 个动作各生成期望命令串；scale 非整数 replicas 抛错；生成命令全部通过 `is_whitelisted_for_execute`；token 校验：篡改参数/过期/错签名均 False）
- [ ] **Step 2: 运行确认失败** → FAIL
- [ ] **Step 3: 实现**（核心代码）：

```python
# ai-orchestrator/k8s_actions.py
"""结构化 K8s 生命周期动作: 命令生成 + preflight token + 乐观锁"""
import hashlib
import hmac
import json
import time

from shell_policy import is_whitelisted_for_execute

ACTIONS = ["rollout_restart", "scale", "delete_pod", "evict_pod", "cordon", "uncordon", "drain"]
ACTION_KINDS = {"rollout_restart": ("deployment", "statefulset", "daemonset"),
                "scale": ("deployment", "statefulset"),
                "delete_pod": ("pod",), "evict_pod": ("pod",),
                "cordon": ("node",), "uncordon": ("node",), "drain": ("node",)}
PREFLIGHT_TTL = 300  # 秒

_secret = b""


def set_secret(s: str):
    global _secret
    _secret = s.encode()


def build_command(action: str, *, kind: str, namespace: str, name: str, **kw) -> str:
    if kind not in ACTION_KINDS[action]:
        raise ValueError(f"动作 {action} 不支持 kind={kind}")
    if action == "rollout_restart":
        return f"kubectl rollout restart {kind}/{name} -n {namespace}"
    if action == "scale":
        return f"kubectl scale {kind}/{name} --replicas={int(kw['replicas'])} -n {namespace}"
    if action in ("delete_pod", "evict_pod"):
        return f"kubectl delete pod {name} --grace-period={int(kw.get('grace_period_seconds', 30))} -n {namespace}"
    if action == "cordon":
        return f"kubectl cordon node {name}"
    if action == "uncordon":
        return f"kubectl uncordon node {name}"
    if action == "drain":
        t = int(kw.get("drain_timeout", 300))
        return f"kubectl drain node {name} --ignore-daemonsets --delete-emptydir-data --timeout={t}s"
    raise ValueError(f"未知动作 {action}")


def _args_sha(action, kind, namespace, name, **kw) -> str:
    payload = json.dumps({"a": action, "k": kind, "ns": namespace, "n": name, **kw}, sort_keys=True)
    return hashlib.sha256(payload.encode()).hexdigest()


def make_preflight_token(action, kind, namespace, name, **kw) -> str:
    body = {"sha": _args_sha(action, kind, namespace, name, **kw),
            "exp": int(time.time()) + PREFLIGHT_TTL}
    sig = hmac.new(_secret, json.dumps(body, sort_keys=True).encode(), hashlib.sha256).hexdigest()[:16]
    return json.dumps(body) + "." + sig


def verify_preflight_token(token: str, action, kind, namespace, name, **kw) -> bool:
    body_s, _, sig = token.rpartition(".")
    try:
        body = json.loads(body_s)
    except ValueError:
        return False
    if body.get("exp", 0) < time.time() or body.get("sha") != _args_sha(action, kind, namespace, name, **kw):
        return False
    expect = hmac.new(_secret, json.dumps(body, sort_keys=True).encode(), hashlib.sha256).hexdigest()[:16]
    return hmac.compare_digest(expect, sig)
```

`preflight()`：白名单校验 → `kubectl get {kind}/{name} -n {ns} -o jsonpath='{.metadata.resourceVersion}'`（subprocess，复用 `tools.execute_shell` 的只读通道）校验资源存在并取 resourceVersion → 返回 `{"ok": True, "preflight_token": ..., "resource_version": ..., "command": ...}`；资源不存在 → `{"ok": False, "error": "资源不存在"}`。
- [ ] **Step 4: 运行确认通过** → PASS
- [ ] **Step 5: Commit** `git commit -m "feat: K8s 结构化动作 schema + preflight token + 白名单扩展(cordon/drain)"`

### Task C2: preflight / execute 端点 + 审批 + 审计

**Files:**
- Modify: `ai-orchestrator/main.py`（Mount C2：`POST /api/v1/ops/k8s/preflight`、`POST /api/v1/ops/k8s/execute`）
- Test: `ai-orchestrator/tests/test_k8s_endpoints.py`

**Interfaces:**
- Consumes: `k8s_actions`（C1）；`_require_approver` / `_audit_log`（main.py 现有）；`db_approval.ApprovalStore`（现有）

- [ ] **Step 1: 写失败测试**（非 approver 403；execute 无 token/篡改 token 400；destructive 动作（delete_pod/drain/evict_pod/cordon）未走审批单拒绝；通过路径 mock subprocess 断言审计写入）
- [ ] **Step 2: 运行确认失败** → FAIL
- [ ] **Step 3: 实现**（Mount C2 代码要点）：

```python
@router.post("/ops/k8s/preflight")
def k8s_preflight(body: K8sActionBody):
    _require_approver()
    result = k8s_actions.preflight(body.action, kind=body.kind, namespace=body.namespace,
                                   name=body.name, **(body.extra or {}))
    if not result["ok"]:
        raise HTTPException(400, result["error"])
    _audit_log("k8s_preflight", body.action, f"{body.kind}/{body.name}")
    return result

@router.post("/ops/k8s/execute")
def k8s_execute(body: K8sActionBody):
    _require_approver()
    destructive = body.action in ("delete_pod", "evict_pod", "cordon", "drain")
    if destructive:
        _require_approved_task(body.approval_task_id)  # ApprovalStore 状态=approved 且参数匹配
    if not k8s_actions.verify_preflight_token(body.preflight_token, body.action,
                                              kind=body.kind, namespace=body.namespace,
                                              name=body.name, **(body.extra or {})):
        raise HTTPException(400, "preflight_token 无效或已过期")
    current = k8s_actions.current_resource_version(body.kind, body.namespace, body.name)
    if body.expected_resource_version and current != body.expected_resource_version:
        raise HTTPException(409, "资源版本已变化, 请重新预检")
    out = k8s_actions.execute(body)          # subprocess + 输出截断 + 审计
    _audit_log("k8s_execute", body.action, f"{body.kind}/{body.name}", operator=_audit_operator())
    return {"ok": True, "output": out}
```

- [ ] **Step 4: 运行确认通过** → PASS
- [ ] **Step 5: Commit** `git commit -m "feat: K8s 动作 preflight/execute 端点(审批+乐观锁+审计)"`

### Task C3: chat 工具注册（execute_k8s_action / describe_k8s_resource）

**Files:**
- Modify: `ai-orchestrator/tools.py`
- Test: `ai-orchestrator/tests/test_k8s_tools.py`

**Interfaces:**
- Produces: 注册 `execute_k8s_action`（cls=dangerous，requires_approval=True → 走 execution_gate + 内部调 C2 逻辑）与 `describe_k8s_resource`（cls=safe，`kubectl describe/get` 只读）

- [ ] **Step 1: 写失败测试**（describe 直接可用；execute 未经审批被 execution_gate 拒绝）
- [ ] **Step 2: 运行确认失败** → FAIL
- [ ] **Step 3: 实现**：`execute_k8s_action` 内部复用 C1 `build_command` + 现有 `execute_shell` 双策略校验；`describe_k8s_resource(kind, name, namespace)` 走 `kubectl describe`。
- [ ] **Step 4: 运行确认通过** → PASS
- [ ] **Step 5: Commit** `git commit -m "feat: chat 工具 execute_k8s_action/describe_k8s_resource"`

### Task C4: v3 K8s 运维页面

**Files:**
- Create: `observability-frontend/src/pages/infra/K8sActions.tsx`
- Modify: `observability-frontend/src/api/client.ts`（追加 k8s 端点函数）
- Modify: `observability-frontend/src/App.tsx`（Mount C4：路由 `/infra/k8s` + NAV_GROUPS 菜单）

**Interfaces:**
- Consumes: C2 端点 + query-api 现有只读端点（`clusterNodes/clusterNamespaces` 及 pods 相关，如缺 workload 列表端点则本任务在 query-api 补 `GET /api/v1/k8s/workloads`）

- [ ] **Step 1**：query-api 核对只读端点是否覆盖 workload 列表（kind/name/namespace/replicas/status）；缺失则新增 `GET /api/v1/k8s/workloads`（复用 `kubeList()` 机制）
- [ ] **Step 2**：页面实现。左侧 workload 列表（antd Table，可筛选 ns/kind）→ 选中行操作按钮（重启/扩容/删除 Pod/驱逐/节点 cordon/drain 按资源类型显隐）→ 动作 Drawer（参数表单）→ "预检"按钮调 preflight 显示命令与 resourceVersion → destructive 动作弹审批确认（提交 approval task）→ "执行"带 token 调 execute → 结果面板 + 执行记录（audit）。
- [ ] **Step 3: 验证**：`npm run build` 通过；playwright 验收：对测试命名空间的测试 deployment 走一遍 重启→扩容→缩回 全流程。
- [ ] **Step 4: Commit** `git commit -m "feat: v3 K8s 运维页面(结构化动作+预检+审批执行)"`

### Task C5（可选）: reviewer persona 自动二审接入

- 仅当 B1/B3 完成后：destructive 动作在 `_require_approved_task` 之前先过 reviewer persona（`run_worker(reviewer, 动作上下文)`，输出含 `Decision: approve` 才继续），保持人类审批为最终门。Commit: `feat: K8s destructive 动作 reviewer 自动二审`

---

## 工作流 D：Skill 文件化 + 市场

**Goal:** 8 个内置 skill 迁移为 SKILL.md 文件；marketplace 支持从 local/tarball/git 安装 pack（ECDSA 签名校验，verified/unsigned/failed 三态）、卸载、热重载。

**Architecture:** 对齐 ongrid：`skills/<name>/SKILL.md` 为 skill 单元（frontmatter `name/description/when_to_use/activation/tools` + body=system prompt）；安装流程 staging→逃逸检查→唯一性→签名校验→落盘→`reloadRegistries()`。**安全决策：外部 skill 的 tools 只能引用已有注册工具名，不执行外部代码。**

### Task D1: 8 个内置 skill 迁移为 SKILL.md

**Files:**
- Create: `ai-orchestrator/skills/{observability,infra,rca_skill,rag_skill,vm_ops,alert_ops,automation,diagnose}/SKILL.md`（8 个）
- Modify: `ai-orchestrator/skills/__init__.py`（`init_skills()` 暂改为空壳占位，加载逻辑由 D3 接管）

**Interfaces:**
- Produces: SKILL.md 文件集（`name` 保持 `skill.xxx` 现有名以兼容前端列表；`tools[].impl: builtin` 引用现有 ToolRegistry 工具）

- [ ] **Step 1**：逐个从现有 `skills/*.py` 迁移（`description/intent_keywords/system_prompt/tools` 字段一一对应）。示例：

```markdown
---
name: skill.rca
version: "1.0"
description: 根因分析(RCA)
when_to_use: 用户报告故障、错误率升高、响应变慢、需要定位根因时
activation:
  mode: keyword
  keywords: [根因, 故障, 为什么, 出错, rca, 崩溃, 错误率]
tools:
  - name: rca_analyze
    impl: builtin
    class: read
---
（正文 = 原 rca_skill.py 的 system_prompt）
```

- [ ] **Step 2**：`python -c "from skills import init_skills"` 可导入（占位）；`python -m pytest tests/ -k skill` 若现有测试校验 `init_skills()` 返回数则同步更新断言为"从文件加载"（D3 后恢复）。
- [ ] **Step 3: Commit** `git commit -m "feat: 8 个内置 skill 迁移为 SKILL.md"`

### Task D2: SKILL.md 加载器

**Files:**
- Create: `ai-orchestrator/skill_loader.py`
- Test: `ai-orchestrator/tests/test_skill_loader.py`

**Interfaces:**
- Consumes: `md_meta.split_frontmatter`（B1）
- Produces: `SkillFile` dataclass；`load_skills(*dirs) -> dict[str, SkillFile]`（builtin `skills/` + `AIOPS_DATA_DIR/skills`）

- [ ] **Step 1: 写失败测试**（合法目录全部加载；缺必填字段抛 ValueError；tools 引用未注册工具名抛 ValueError；user 目录覆盖 builtin 同名）
- [ ] **Step 2: 运行确认失败** → FAIL
- [ ] **Step 3: 实现**（必填校验 `name/description/when_to_use`；工具名对 `ToolRegistry` 现有集合校验）
- [ ] **Step 4: 运行确认通过** → PASS
- [ ] **Step 5: Commit** `git commit -m "feat: SKILL.md 加载器(必填校验+工具引用校验)"`

### Task D3: SkillRegistry 改造 + 热重载

**Files:**
- Modify: `ai-orchestrator/skill_registry.py`（`init_skills()` 改为调 `skill_loader.load_skills`；`activation.keyword` 与现有 `intent_keywords` 合并；新增 `reload()`）
- Modify: `ai-orchestrator/skills/__init__.py`（恢复真实加载）
- Test: `ai-orchestrator/tests/test_skill_registry.py`（更新/新增）

- [ ] **Step 1: 写失败测试**（`init_skills()` 后 8 个 skill 可 match；`reload()` 后新文件生效）
- [ ] **Step 2: 运行确认失败** → FAIL
- [ ] **Step 3: 实现**：`SkillDef` 字段由 `SkillFile` 映射填充；`match()` 优先 activation.keyword 再退 description 关键词。
- [ ] **Step 4: 运行确认通过** → PASS（回归：`/ai/skills` 列表端点与 AiTools 页不破）
- [ ] **Step 5: Commit** `git commit -m "feat: SkillRegistry 文件化加载 + reload 热重载"`

### Task D4: marketplace 安装/卸载 + 签名校验

**Files:**
- Create: `ai-orchestrator/marketplace.py`
- Test: `ai-orchestrator/tests/test_marketplace.py`

**Interfaces:**
- Consumes: `skill_loader.load_skills`（D2）；`skill_registry.reload`（D3）
- Produces: `install(source: str) -> dict`（SourceType: `local` 目录 / `tarball` / `git` URL）；`uninstall(pack_id)`；`list_installed()`；`verify_signature(pack_dir) -> "verified"|"unsigned"|"failed"`

- [ ] **Step 1: 写失败测试**（fixtures：合法签名 pack 安装成功且 reload 生效；无 signature.json → unsigned；签名被篡改 → failed；路径逃逸（`../`）拒绝；重复 pack_id 拒绝；uninstall 后文件消失）
- [ ] **Step 2: 运行确认失败** → FAIL（签名实现先检查依赖：`python -c "import ecdsa"` 失败则 `pip install ecdsa` 并加 `requirements.txt`）
- [ ] **Step 3: 实现**（流程骨架）：

```python
# ai-orchestrator/marketplace.py
"""marketplace: 安装来源 local|tarball|git; ECDSA 签名三态; 热重载"""
import hashlib
import os
import shutil
import subprocess
import tempfile

REQUIRE_SIGNED = os.environ.get("MARKETPLACE_REQUIRE_SIGNED", "0") == "1"


def install(source: str, as_admin: bool = False) -> dict:
    if not as_admin:
        raise PermissionError("仅管理员可安装")
    staging = _fetch_to_staging(source)          # local: copytree; tarball: tar -xzf; git: clone --depth=1
    skills = _scan_skills(staging)               # 递归找 SKILL.md (支持 pack 的 skills/ 子目录布局)
    if not skills:
        raise ValueError("未找到 SKILL.md")
    for s in skills:
        if not _is_within(staging, s):           # realpath 必须仍在 staging 下
            raise ValueError(f"路径逃逸: {s}")
    state = verify_signature(staging)
    if state == "failed":
        raise ValueError("签名校验失败")
    if REQUIRE_SIGNED and state != "verified":
        raise ValueError("市场要求签名包")
    pack_id = os.path.basename(staging.rstrip("/"))
    dest = os.path.join(USER_SKILLS_DIR, pack_id)
    if os.path.exists(dest):
        raise ValueError(f"pack 已安装: {pack_id}")
    os.rename(staging, dest)
    _record_installed(pack_id, source, state)    # SQLite market.db (installed_packs 表)
    _reload_registries()                         # skill_registry.reload()
    return {"pack_id": pack_id, "signature_state": state, "skills": [os.path.basename(s) for s in skills]}
```

签名校验：`signature.json` 含 `{sig: <base64 ASN.1 ECDSA>, pub_key: <pem>}`；对 pack 内 `*.md/*.json`（排除 signature.json 自身）按相对路径排序做 SHA-256 拼接 → `ecdsa.VerifyASN1`。
- [ ] **Step 4: 运行确认通过** → PASS
- [ ] **Step 5: Commit** `git commit -m "feat: marketplace 安装/卸载/ECDSA 签名三态校验"`

### Task D5: marketplace 端点 + 前端市场页

**Files:**
- Modify: `ai-orchestrator/main.py`（Mount D5：`POST /api/v1/ai/marketplace/install`、`GET /api/v1/ai/marketplace/installed`、`DELETE /api/v1/ai/marketplace/installed/{pack_id}`，均 `_require_admin`）
- Modify: `observability-frontend/src/pages/ai/AiTools.tsx`（增加"市场"Tab：安装表单 source 类型选择 + 已安装列表含 signature_state 徽标 + 卸载按钮）
- Modify: `observability-frontend/src/api/client.ts`（追加 marketplace 函数）

- [ ] **Step 1**：端点挂载（包校验走 D4 实现，错误映射 400/403）
- [ ] **Step 2**：前端 Tab 实现（antd Tabs + Form + Table，状态徽标 verified 绿/unsigned 灰/failed 红）
- [ ] **Step 3: 验证**：`npm run build`；`python -m pytest tests/ -k marketplace`；playwright 验收安装一个本地示例 pack → skill 列表出现新 skill。
- [ ] **Step 4: Commit** `git commit -m "feat: marketplace 端点 + AiTools 市场 Tab"`

---

## 工作流 E：内置运维知识库

**Goal:** 5 分类 playbook 文件库 + 向量检索（ChromaDB ops_playbooks 集合）+ `query_knowledge` chat 工具 + v3 浏览页。

**Architecture:** 对齐 ongrid `builtin_vault`：playbook = YAML frontmatter（title/tags/alert_keys/applies_to）+ 正文四段结构（What this means / Immediate checks / Likely causes / Escalation criteria）；复用现有 RAGStore 嵌入器（bge-small-zh-v1.5）新增独立 collection；检索 score ≥ 0.6 才按 playbook 作答（persona 约定）。

### Task E1: playbook 首批内容（模板 + 20 篇）

**Files:**
- Create: `ai-orchestrator/data/playbooks/diagnostics/oom-killed.md`、`disk-pressure.md`、`dns-resolution-failure.md`、`conntrack-table-full.md`、`node-not-ready.md`、`crashloopbackoff.md`、`pod-pending-insufficient-resources.md`、`kubelet-down.md`、`redis-latency-spike.md`、`redis-oom-eviction.md`、`mysql-slow-query.md`、`mysql-connection-exhausted.md`、`clickhouse-merge-slow.md`、`nfs-stale-hang.md`、`network-packet-loss.md`
- Create: `ai-orchestrator/data/playbooks/alerts/high-cpu.md`、`high-memory.md`
- Create: `ai-orchestrator/data/playbooks/concepts/alerting.md`、`incident-response.md`
- Create: `ai-orchestrator/data/playbooks/reference/kubectl-cheatsheet.md`

**Interfaces:**
- Produces: 20 篇 playbook（E2 的输入）

- [ ] **Step 1**：写 2 篇完整样例（以下为诊断类模板，其余 18 篇同构填写）：

```markdown
---
title: OOMKilled 容器内存溢出
tags: [k8s, oom, memory]
alert_keys: [ContainerOOMKilled, PodOOMKilled, KubePodCrashLooping]
applies_to: [k8s]
---
# OOMKilled 容器内存溢出

## What this means
容器内存超过 limit 被内核 OOM killer 杀掉, pod 状态 OOMKilled / CrashLoopBackOff。

## Immediate checks
1. 平台查询: 该 pod 内存使用率曲线(近 30 分钟)与 limit 对比
2. kubectl get pod <pod> -n <ns> -o yaml 看 lastState.terminated.reason
3. kubectl describe pod 看 OOM 事件与 exit code 137

## Likely causes
- 应用内存泄漏
- limit 设置过低
- 流量突增瞬时峰值

## Escalation criteria
- 同一 pod 30 分钟内 OOM ≥ 3 次
- 内存曲线单调增长无回落(疑似泄漏, 建议 dump heap)
```

- [ ] **Step 2**：按模板完成其余 18 篇（标题/标签/告警键引用平台已有告警规则名；`immediate checks` 优先引用平台查询能力而非纯 kubectl）
- [ ] **Step 3: Commit** `git commit -m "feat: 内置运维 playbook 首批 20 篇(5 分类)"`

### Task E2: playbook 加载器（chunk → embed → ops_playbooks）

**Files:**
- Create: `ai-orchestrator/playbook_loader.py`
- Modify: `ai-orchestrator/rag.py`（`RAGStore` 增加 `ops_playbooks` collection 管理）
- Test: `ai-orchestrator/tests/test_playbook_loader.py`

**Interfaces:**
- Consumes: `md_meta.split_frontmatter`（B1）；`RAGStore` 嵌入器（现有 `bge-small-zh-v1.5`）
- Produces: `load_playbooks(store: RAGStore) -> int`（返回 chunk 数，幂等）

- [ ] **Step 1: 写失败测试**（fixtures 目录 2 篇 → chunk 数 ≥ 2；frontmatter 字段（category/tags/alert_keys）进入 metadata；重复加载幂等）
- [ ] **Step 2: 运行确认失败** → FAIL
- [ ] **Step 3: 实现**：按 `## ` 标题切 chunk（≤600 字符），metadata 含 `path/category/title/tags/alert_keys/applies_to`，doc_id=`{relpath}#{i}`。
- [ ] **Step 4: 运行确认通过** → PASS
- [ ] **Step 5: Commit** `git commit -m "feat: playbook 加载器(标题切块+向量化入 ops_playbooks)"`

### Task E3: 检索集成 + query_knowledge 工具

**Files:**
- Modify: `ai-orchestrator/rag.py`（`search()` 分层检索合并 playbooks；新增 `search_playbooks(query, limit, path_prefix, tags)`）
- Modify: `ai-orchestrator/tools.py`（注册 `query_knowledge`，cls=safe）
- Test: `ai-orchestrator/tests/test_knowledge_search.py`

**Interfaces:**
- Produces: `query_knowledge(query, path_prefix=None, tags=None, max_results=5) -> {"items":[{"title","path","category","tags","score","preview"}]}`（preview ≤ 800 字符）

- [ ] **Step 1: 写失败测试**（查询 "容器内存溢出" 命中 oom-killed 且 score 排序；path_prefix=diagnostics 过滤；score<0.6 的条目默认仍返回但带 score 供 persona 判断）
- [ ] **Step 2: 运行确认失败** → FAIL
- [ ] **Step 3: 实现**：`search()` 保留现有案例权重逻辑，playbook 结果以独立分组返回；`query_knowledge` 工具拼接两个分组输出。
- [ ] **Step 4: 运行确认通过** → PASS
- [ ] **Step 5: Commit** `git commit -m "feat: query_knowledge 工具 + playbook 分层检索"`

### Task E4: playbook 浏览端点 + v3 知识库页

**Files:**
- Modify: `ai-orchestrator/main.py`（Mount E4：`GET /api/v1/ai/knowledge/playbooks`（列表, 支持 category/tags 过滤）、`GET /api/v1/ai/knowledge/playbooks/{path}`（原文））
- Modify: `observability-frontend/src/pages/ai/Knowledge.tsx`（增加"运维 Playbook"Tab：左侧分类树 + 列表 + 右侧原文渲染）
- Modify: `observability-frontend/src/api/client.ts`（追加函数）

- [ ] **Step 1**：端点挂载（读文件目录，路径参数做 `..` 逃逸校验）
- [ ] **Step 2**：前端 Tab（antd Tree + List + react-markdown 渲染原文）
- [ ] **Step 3: 验证**：`npm run build`；pytest 端点测试；playwright 浏览一篇 playbook。
- [ ] **Step 4: Commit** `git commit -m "feat: playbook 浏览端点 + 知识库页 Playbook Tab"`

---

## 工作流 F：Grafana 集成

**Goal:** v3 拥有 Grafana 页面：dashboard 搜索/列表 + 嵌入浏览 + 深链；面板级原生渲染（phase 2）。

**Architecture:** 对齐 ongrid：管理器代理 dashboard JSON（避免 CORS/cookie）→ 前端原生渲染面板。Phase 1（低风险先落地）：query-api 新增 Grafana 代理端点 + iframe 嵌入（nginx `/grafana/` 已有 + deepflow grafana 打开 `allow_embedding`）。Phase 2：`PromQLPanel` 组件用 echarts 5 渲染 timeseries/stat/gauge。

### Task F1: query-api Grafana 代理端点

**Files:**
- Create: `ai-apm-query-go/internal/api/grafana.go`
- Modify: `ai-apm-query-go/internal/api/router.go`（注册路由，按现有路由注册方式）
- Test: `ai-apm-query-go/internal/api/grafana_test.go`（httptest 模拟上游）

**Interfaces:**
- Consumes: 配置 `GRAFANA_ROOT_URL`（默认 `http://deepflow-grafana.deepflow.svc.cluster.local`）、`GRAFANA_API_TOKEN`（可选）、`GRAFANA_TLS_INSECURE`（默认 true）
- Produces: `GET /api/v1/grafana/health`；`GET /api/v1/grafana/search?query=`（转发 `/api/search`）；`GET /api/v1/grafana/dashboards/{uid}`（转发 `/api/dashboards/uid/{uid}`）

- [ ] **Step 1: 写失败测试**（mock 上游：search 返回数组透传；dashboards/uid 返回 JSON 透传；上游 404 透传 404；有 token 时带 Authorization header）
- [ ] **Step 2: 运行确认失败** → FAIL（`go test ./internal/api/ -run Grafana -v`）
- [ ] **Step 3: 实现**：

```go
// internal/api/grafana.go
type GrafanaConfig struct {
	RootURL    string
	APIToken   string
	TLSInsecure bool
}

func (h *grafanaHandler) ProxyDashboard(c *gin.Context) {
	uid := c.Param("uid")
	target := fmt.Sprintf("%s/api/dashboards/uid/%s", h.cfg.RootURL, url.PathEscape(uid))
	req, _ := http.NewRequestWithContext(c.Request.Context(), http.MethodGet, target, nil)
	if h.cfg.APIToken != "" {
		req.Header.Set("Authorization", "Bearer "+h.cfg.APIToken)
	}
	resp, err := h.client.Do(req)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "grafana 不可达"})
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	c.Data(resp.StatusCode, "application/json", body)
}
```

  （`http.Client` 在 `TLSInsecure` 时 `InsecureSkipVerify: true`；handler 在现有结构体组装处注入配置）
- [ ] **Step 4: 运行确认通过** → PASS
- [ ] **Step 5: Commit** `git commit -m "feat: query-api Grafana 代理端点(health/search/dashboards)"`

### Task F2: deepflow grafana 嵌入配置

**Files:**
- Modify: `deploy/helm/aiops/values-deepflow.yaml`

- [ ] **Step 1**：在 deepflow grafana 配置段追加（chart 支持性以 values 文档为准，等价项替换）：
```yaml
grafana:
  grafana.ini:
    security:
      allow_embedding: true
      cookie_samesite: none
    auth.anonymous:
      enabled: true
      org_role: Viewer
```
- [ ] **Step 2**：`helm upgrade --install deepflow deepflow/deepflow -n deepflow -f deploy/helm/aiops/values-deepflow.yaml` 后 curl 验证 `http://deepflow-grafana.deepflow.svc.cluster.local/api/health` 通。
- [ ] **Step 3: Commit** `git commit -m "feat: deepflow grafana 开启 allow_embedding + 匿名只读"`

### Task F3: v3 Grafana 页面（Phase 1 iframe 嵌入 + 深链）

**Files:**
- Create: `observability-frontend/src/pages/observability/Grafana.tsx`
- Modify: `observability-frontend/src/api/client.ts`（追加 grafana 函数）
- Modify: `observability-frontend/src/App.tsx`（Mount F3：路由 `/observability/grafana` + NAV_GROUPS 菜单）

**Interfaces:**
- Consumes: F1 端点；nginx `/grafana/` 代理（现有）

- [ ] **Step 1**：页面实现：搜索框（调 `/grafana/search`）→ dashboard 卡片列表（title/tags/uid）→ 点击进入浏览：iframe `src=/grafana/d/{uid}?theme=light&kiosk`（经现有 nginx 代理）+ 右上"在新窗口打开"深链按钮（`/grafana/d/{uid}`）。
- [ ] **Step 2: 验证**：`npm run build`；playwright：搜索到 deepflow 内置 dashboard → iframe 正常渲染（无 X-Frame-Options 报错）→ 深链新窗口可开。
- [ ] **Step 3: Commit** `git commit -m "feat: v3 Grafana 页(dashboard 搜索+iframe 嵌入+深链)"`

### Task F4（Phase 2）: PromQLPanel 原生面板渲染

**Files:**
- Create: `observability-frontend/src/components/PromQLPanel.tsx`
- Modify: `observability-frontend/src/pages/observability/Grafana.tsx`（切换"原生渲染"视图）

**Interfaces:**
- Consumes: F1 `GET /grafana/dashboards/{uid}`（dashboard JSON）；echarts 5（现有依赖）

- [ ] **Step 1**：组件实现：解析 dashboard JSON `panels[]` 中 `type ∈ timeseries|stat|gauge` 的面板 → 提取 targets（expr/datasource）、thresholds、unit、legend → echarts 渲染（timeseries→line；stat→单值卡；gauge→仪表盘）；数据源：targets 指向 prometheus 的走 query-api 现有 PromQL 透传端点（`/metrics/query` 现有 P2-6 已加透传）取数。
- [ ] **Step 2**：Grafana.tsx 加"iframe / 原生"切换开关（zustand 本地状态）。
- [ ] **Step 3: 验证**：`npm run build`；playwright：选一个含 timeseries 面板的 dashboard 原生渲染曲线与 iframe 版一致（数据一致即通过）。
- [ ] **Step 4: Commit** `git commit -m "feat: PromQLPanel 原生渲染(timeseries/stat/gauge)"`

---

## 执行顺序与并行车道

六个工作流模块级相互独立（A–F 各自创建独立模块文件与页面目录），共享文件挂载点集中在每工作流末尾的 Mount 小节。建议：

**Phase 1（并行，模块开发）**：A1–A5 / B1–B5 / C0–C3 / D1–D4 / E1–E3 / F1–F2 可同时开工（写冲突表已核查：各工作流只写自己新建的文件；唯一交叉是 `md_meta.py` 由 B 先建，D/E 复用其接口即可）。

**Phase 2（串行，挂载整合，单集成者）**：按 Mount 清单逐条落地——main.py（A3/B2/B4/B5/B6/C2/D5/E4）、tools.py（B3/C3/E3）、shell_policy.py（C1）、App.tsx + NAV_GROUPS + api/client.ts（A6/C4/D5/E4/F3）。每条 ≤ 15 分钟，冲突面小。

**Phase 3（并行，前端页面）**：A6 / C4 / D5 / E4 / F3 页面实现（各自独立目录，client.ts 追加按"每工作流只加自己的函数"约定，冲突由集成者 Phase 2 已消化）。

**Phase 4（验收）**：全量 pytest + `npm run build` + playwright 手工回归（沿用既有 13 项回归清单 + 本轮新增页面）。

推荐顺序（若单车道执行）：A → B → C → D → E → F（B 依赖 A 的告警派发点，C5 依赖 B 的 reviewer；其余无强依赖）。

## 风险与开放决策

1. **C0：ai-orchestrator pod 内 kubectl 可用性未证实**——若不可用且不便挂 kubeconfig，备选：写动作改由 query-api 执行（Go 侧已有 kubeconfig 处理），审批/审计仍在 orchestrator。C0 先验证再定。
2. **E2：嵌入模型可用性**——bge-small-zh-v1.5 已在案例检索中工作，playbook 复用同一嵌入器，风险低。
3. **F2：deepflow chart 对 `allow_embedding` 的支持**——若不支持，Phase 1 退化为"深链新窗口"（v2 现状），Phase 2 原生渲染不受影响。
4. **D4：签名依赖**——优先复用现有 `cryptography`（若已在 requirements），否则新增 `ecdsa`。
5. **B5：LLM 路由质量**——保留 `ExpertRegistry.match_intent` keyword 兜底；`dual_agent` 开关默认关闭，灰度打开。
6. **外部 skill 不执行代码**——marketplace 安装的 skill 仅提示词 + 引用已有工具；需在 SKILL.md 校验中固化（D2 已含工具名校验）。
7. **A2：cron 时区**——统一 UTC（对齐 ongrid），前端表单标注"UTC"。
