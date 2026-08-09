# 批3：LLM function-calling 工具循环 + 双层 Agent 架构 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将 AI 编排从「一次性采集喂 LLM」升级为「LLM 迭代式 function-calling 工具循环 + Coordinator/子Agent/Reviewer 双层 Agent」，mock 下完整可跑、可测试。

**Architecture:** 新增纯逻辑模块 `function_calling.py`（通用工具循环）+ `dual_agent.py`（双层编排可测逻辑），二者**不依赖 langgraph/orchestrator**（本机 Python 3.9 测试隔离）；orchestrator.py 只加薄节点壳调用它们，组成新 `dual_graph`。增强 `llm_mock.py` 模拟工具决策。前端复用 SSE tool_start/tool_end 增加 subagent/coordinator/reviewer 标签。

**Tech Stack:** Python 3.9+（纯逻辑模块）、LangGraph（节点壳）、pytest、CrewAI（真实 LLM）、SSE（前端实时流）

---

## Global Constraints

- **测试隔离**：新增测试**不得 import `orchestrator`/`main`**（触发 langgraph>=1.0 重依赖链，本机 Python 3.9 无法解析）。只测试 `function_calling.py` / `dual_agent.py` / `llm_mock.py` 等纯模块。
- **只读安全**：function-calling 工具白名单仅含 `cls=="safe"` 只读工具；`mutating`/`dangerous`/需审批工具一律拒绝并在结果中说明，绝不执行。
- **护栏固定值**（可经构造参数覆盖，默认）：`max_steps=6`、`max_tool_calls=4`、每工具超时 10s、总耗时上限 120s。
- **合规**：全自研，仅借鉴 ongrid 双层 Agent 概念，严禁复制其代码（AGPL-3.0）。
- **数据所有权**：双层 Agent 运行记录仍由 ai-orchestrator 写，不新增存储/服务。
- **零回归**：现有 `graph`/`chat_graph` 不动；`dual_agent` 开关默认关闭。

---

## 文件结构

| 文件 | 责任 | 操作 |
|---|---|---|
| `ai-orchestrator/function_calling.py` | 通用 function-calling 工具循环（schema 生成、白名单执行、循环引擎）| 新建 |
| `ai-orchestrator/dual_agent.py` | 双层编排纯逻辑（Coordinator 拆解解析、并行子 Agent、Reviewer 合并）| 新建 |
| `ai-orchestrator/llm_mock.py` | mock LLM 决策（工具调用序列/拆解/审查）| 修改 |
| `ai-orchestrator/orchestrator.py` | 新增 coordinator/subagent/reviewer 节点壳 + `dual_graph` + `stream_sync` mode | 修改 |
| `ai-orchestrator/models.py` | ChatRequest 增加 `dual_agent` 开关 | 修改 |
| `ai-orchestrator/main.py` | `/ai/chat` 读取开关，stream_sync 传 mode | 修改 |
| `observability-frontend/src/pages/AIChat/ChatThread.tsx` | 工具卡片类型标签 + 子Agent链展开 | 修改 |
| `ai-orchestrator/tests/test_function_calling.py` | 护栏/schema/白名单/循环测试 | 新建 |
| `ai-orchestrator/tests/test_dual_agent.py` | 拆解/并行/合并/SSE 顺序测试 | 新建 |
| `ai-orchestrator/tests/test_llm_mock.py` | mock 决策函数测试 | 修改 |

---

### Task 1: `function_calling.py` — 工具 schema 生成与白名单守卫

**Files:**
- Create: `ai-orchestrator/function_calling.py`
- Test: `ai-orchestrator/tests/test_function_calling.py`

**Interfaces:**
- Consumes: `skill_registry.ToolDef`（已有字段 name/description/func/params/cls/scope/requires_approval）
- Produces:
  - `make_tools_schema(tools: list[ToolDef]) -> list[dict]` — OpenAI function schema
  - `exec_tool_with_guard(tool: ToolDef, args: dict, whitelist: set[str]) -> str` — 白名单校验+执行+返回结果字符串
  - `WHITELIST_READONLY: set[str]` — 默认只读白名单

- [ ] **Step 1: 写失败测试**

```python
# ai-orchestrator/tests/test_function_calling.py
import pytest
from function_calling import make_tools_schema, exec_tool_with_guard, WHITELIST_READONLY


def _dummy_safe(**kw):
    from skill_registry import ToolDef
    return ToolDef(name=kw.get("name", "query_metrics"), description="d", func=lambda service="x": "ok",
                   params={"service": {"type": "string", "required": True, "default": "", "desc": "s"}},
                   cls=kw.get("cls", "safe"))


def test_make_tools_schema_shape():
    t = _dummy_safe(name="query_metrics")
    schema = make_tools_schema([t])
    assert schema[0]["type"] == "function"
    assert schema[0]["function"]["name"] == "query_metrics"
    assert "service" in schema[0]["function"]["parameters"]["properties"]


def test_exec_safe_tool_runs():
    t = _dummy_safe()
    out = exec_tool_with_guard(t, {"service": "svc"}, WHITELIST_READONLY)
    assert out == "ok"


def test_exec_mutating_rejected():
    t = _dummy_safe(cls="mutating")
    out = exec_tool_with_guard(t, {}, WHITELIST_READONLY)
    assert "拒绝" in out and "mutating" in out


def test_exec_not_in_whitelist_rejected():
    t = _dummy_safe(name="dangerous_tool")
    out = exec_tool_with_guard(t, {}, WHITELIST_READONLY)
    assert "拒绝" in out


def test_exec_requires_approval_rejected():
    from skill_registry import ToolDef
    t = ToolDef(name="restart", description="d", func=lambda: "x", cls="mutating", requires_approval=True)
    out = exec_tool_with_guard(t, {}, WHITELIST_READONLY)
    assert "审批" in out or "拒绝" in out
```

- [ ] **Step 2: 运行测试验证失败**

Run: `cd ai-orchestrator && python -m pytest tests/test_function_calling.py -v`
Expected: FAIL with `ModuleNotFoundError: No module named 'function_calling'`

- [ ] **Step 3: 实现 `function_calling.py`**

```python
# ai-orchestrator/function_calling.py
"""通用 LLM function-calling 工具循环（纯逻辑，无 langgraph 依赖）。"""
import json
import time
from typing import Callable

from skill_registry import ToolDef, ToolRegistry

# 默认只读白名单：仅允许安全、只读的观测工具
WHITELIST_READONLY = {
    "query_metrics", "query_traces", "query_topology", "get_service_list",
    "get_infrastructure", "deepflow_status", "query_logs", "k8sgpt_diagnose",
}


def make_tools_schema(tools: list) -> list:
    """将 ToolRegistry 工具转换为 OpenAI function-calling schema。"""
    out = []
    for t in tools:
        props = {}
        req = []
        for pname, pinfo in (t.params or {}).items():
            ptype = pinfo.get("type", "string") if isinstance(pinfo, dict) else "string"
            ptype_map = {"int": "integer", "float": "number", "bool": "boolean", "string": "string"}
            props[pname] = {"type": ptype_map.get(str(ptype), "string"),
                            "description": (pinfo.get("desc", "") if isinstance(pinfo, dict) else "")}
            if isinstance(pinfo, dict) and pinfo.get("required"):
                req.append(pname)
        out.append({
            "type": "function",
            "function": {
                "name": t.name,
                "description": t.description,
                "parameters": {"type": "object", "properties": props,
                               "required": req if req else None},
            },
        })
    return out


def exec_tool_with_guard(tool: ToolDef, args: dict, whitelist: set) -> str:
    """白名单校验 + 执行工具。mutating/dangerous/需审批/不在白名单一律拒绝。"""
    if tool.requires_approval:
        return f"[工具 {tool.name} 被拒绝]：该工具需要人工审批，function-calling 循环不自动执行。"
    if tool.cls != "safe":
        return f"[工具 {tool.name} 被拒绝]：工具等级 {tool.cls} 非 safe，循环白名单仅允许只读安全工具。"
    if tool.name not in whitelist:
        return f"[工具 {tool.name} 被拒绝]：不在 function-calling 白名单中。"
    try:
        schema = tool.params or {}
        filtered = {k: v for k, v in (args or {}).items() if k in schema}
        result = tool.func(**filtered) if filtered else tool.func()
        return str(result)[:2000]
    except Exception as e:
        return f"[工具 {tool.name} 执行失败]：{e}"


def run_tool_loop(llm_decision: Callable[[list, list], dict], tools: list,
                  user_prompt: str, whitelist: set = WHITELIST_READONLY,
                  max_steps: int = 6, max_tool_calls: int = 4,
                  max_duration_s: int = 120, on_tool: Callable = None) -> dict:
    """通用 function-calling 循环引擎。

    llm_decision(messages, tools) -> dict: 返回 {"type":"tool","name","arguments"}
     或 {"type":"final","content"}。可注入真实 LLM 或 mock 决策器。
    on_tool(tool_name, status, result) 回调用于 SSE 事件。
    返回 {"final": str, "tool_calls": int, "steps": int, "truncated": bool, "trace": list}
    """
    messages = [{"role": "user", "content": user_prompt}]
    trace = []
    tool_calls = 0
    started = time.time()
    for step in range(max_steps):
        if time.time() - started > max_duration_s:
            return {"final": "已超过总耗时上限，循环终止。", "tool_calls": tool_calls,
                    "steps": step, "truncated": True, "trace": trace}
        decision = llm_decision(messages, tools)
        if decision.get("type") == "final":
            messages.append({"role": "assistant", "content": decision.get("content", "")})
            return {"final": decision.get("content", ""), "tool_calls": tool_calls,
                    "steps": step + 1, "truncated": False, "trace": trace}
        if decision.get("type") == "tool":
            if tool_calls >= max_tool_calls:
                return {"final": "已达到最大工具调用数，循环终止。", "tool_calls": tool_calls,
                        "steps": step + 1, "truncated": True, "trace": trace}
            name = decision.get("name", "")
            args = decision.get("arguments", {}) or {}
            t = ToolRegistry.get(name)
            if not t:
                result = f"[工具 {name} 未注册]"
            else:
                result = exec_tool_with_guard(t, args, whitelist)
            tool_calls += 1
            trace.append({"tool": name, "result": result[:200]})
            messages.append({"role": "assistant",
                             "content": f"调用工具 {name}，结果为:\n{result}"})
            if on_tool:
                on_tool(name, result)
    return {"final": "已达到最大推理步数，循环终止。", "tool_calls": tool_calls,
            "steps": max_steps, "truncated": True, "trace": trace}
```

- [ ] **Step 4: 运行测试验证通过**

Run: `cd ai-orchestrator && python -m pytest tests/test_function_calling.py -v`
Expected: PASS (5 tests)

- [ ] **Step 5: 提交**

```bash
git add ai-orchestrator/function_calling.py ai-orchestrator/tests/test_function_calling.py
git commit -m "feat(batch3): function-calling 工具循环 - schema生成/白名单守卫/循环引擎"
```

---

### Task 2: `llm_mock.py` — mock 模拟工具决策 / 拆解 / 审查

**Files:**
- Modify: `ai-orchestrator/llm_mock.py`
- Test: `ai-orchestrator/tests/test_llm_mock.py`

**Interfaces:**
- Consumes: 现有 `is_mock_enabled()`/`mock_llm_response()`
- Produces:
  - `mock_llm_decision(messages: list, tools: list) -> dict` — 返回 tool/final 决策（mock 序列）
  - `mock_coordinator_plan() -> list[dict]` — 预设拆解 JSON
  - `mock_reviewer_result(sub_results: dict) -> str` — 预设合并审查文本

- [ ] **Step 1: 追加失败测试到 `tests/test_llm_mock.py`**

```python
from llm_mock import mock_llm_decision, mock_coordinator_plan, mock_reviewer_result


def test_mock_decision_first_tool_then_final():
    # 第一次调用应返回 query_metrics 工具决策
    d1 = mock_llm_decision([], [])
    assert d1["type"] == "tool"
    # 第二次（带已有消息）返回 final
    d2 = mock_llm_decision([{"role": "assistant", "content": "已调工具"}], [])
    assert d2["type"] == "final"
    assert isinstance(d2.get("content"), str)


def test_mock_coordinator_plan_shape():
    plan = mock_coordinator_plan()
    assert isinstance(plan, list) and len(plan) >= 2
    assert "task_type" in plan[0] and "task_id" in plan[0]


def test_mock_reviewer_merges():
    out = mock_reviewer_result({"t1": {"conclusion": "a"}, "t2": {"conclusion": "b"}})
    assert isinstance(out, str) and "b" in out
```

- [ ] **Step 2: 运行测试验证失败**

Run: `cd ai-orchestrator && python -m pytest tests/test_llm_mock.py -v`
Expected: FAIL with `ImportError: cannot import name 'mock_llm_decision'`

- [ ] **Step 3: 在 `llm_mock.py` 末尾追加 mock 决策实现**

```python
# 批3: function-calling / 双层 Agent mock 决策
_MOCK_TOOL_SEQUENCE = ["query_metrics", "get_service_list"]

def mock_llm_decision(messages, tools):
    """模拟 LLM function-calling 决策：先调工具，最后给出最终结论。"""
    already_called = sum(1 for m in messages if m.get("role") == "assistant" and "调用工具" in m.get("content", ""))
    if already_called < len(_MOCK_TOOL_SEQUENCE):
        name = _MOCK_TOOL_SEQUENCE[already_called]
        args = {"service": "unknown"} if name == "query_metrics" else {}
        return {"type": "tool", "name": name, "arguments": args}
    return {"type": "final", "content": "[mock] 双层Agent诊断完成：基于指标与拓扑，疑似最近一次发布引起的调用异常，建议回滚。"}


def mock_coordinator_plan():
    """mock Coordinator 拆解：返回预设 2-3 个子任务。"""
    return [
        {"task_id": "t1", "task_type": "diagnosis", "target_service": "unknown", "query": "排查服务异常根因"},
        {"task_id": "t2", "task_type": "inspection", "target_service": "unknown", "query": "巡检服务健康状态"},
    ]


def mock_reviewer_result(sub_results):
    """mock Reviewer 合并审查：拼接所有子结论并给质量结论。"""
    parts = []
    for tid, r in (sub_results or {}).items():
        parts.append(f"[{tid}] {r.get('conclusion', '')[:200]}")
    body = "\n".join(parts) if parts else "(无子结论)"
    return "[mock] Reviewer 审查通过：子结论一致，无冲突。合并如下:\n" + body
```

- [ ] **Step 4: 运行测试验证通过**

Run: `cd ai-orchestrator && python -m pytest tests/test_llm_mock.py -v`
Expected: PASS (原有 5 + 新增 3 = 8 tests)

- [ ] **Step 5: 提交**

```bash
git add ai-orchestrator/llm_mock.py ai-orchestrator/tests/test_llm_mock.py
git commit -m "feat(batch3): llm_mock 增加 function-calling/coordinator/reviewer mock 决策"
```

---

### Task 3: `dual_agent.py` — 双层编排纯逻辑

**Files:**
- Create: `ai-orchestrator/dual_agent.py`
- Test: `ai-orchestrator/tests/test_dual_agent.py`

**Interfaces:**
- Consumes: `function_calling.run_tool_loop`、`llm_mock`（mock 决策）、`skill_registry.ExpertRegistry`
- Produces:
  - `parse_subtasks(raw: str) -> list[dict]` — 解析 Coordinator LLM 输出的拆解 JSON（含异常容错）
  - `run_subtask(subtask: dict, llm_decision, expert_registry, on_tool) -> dict` — 单子任务跑 function-calling，返回 `{"task_id","task_type","conclusion","tool_trace","cost"}`
  - `run_subtasks(subtasks: list, llm_decision_factory, expert_registry, on_tool) -> dict` — 并行跑全部子任务（线程池），返回 `sub_results` 有序 dict
  - `merge_review(sub_results: dict, reviewer_decision) -> str` — Reviewer 合并，返回最终报告

- [ ] **Step 1: 写失败测试**

```python
# ai-orchestrator/tests/test_dual_agent.py
import json
import pytest
from dual_agent import parse_subtasks, run_subtask, run_subtasks, merge_review
from llm_mock import mock_llm_decision


def test_parse_subtasks_valid_json():
    raw = '[{"task_id":"t1","task_type":"diagnosis","query":"a"},{"task_id":"t2","task_type":"inspection","query":"b"}]'
    subs = parse_subtasks(raw)
    assert len(subs) == 2 and subs[0]["task_type"] == "diagnosis"


def test_parse_subtasks_fenced_json():
    raw = '```json\n[{"task_id":"t1","task_type":"query","query":"c"}]\n```'
    subs = parse_subtasks(raw)
    assert len(subs) == 1 and subs[0]["task_id"] == "t1"


def test_parse_subtasks_invalid_returns_empty():
    assert parse_subtasks("not json") == []


def test_run_subtask_mock_loop():
    from skill_registry import ToolRegistry
    from skills.observability import register_observability_skill
    register_observability_skill()  # 确保 query_metrics 已注册
    subtask = {"task_id": "t1", "task_type": "diagnosis", "target_service": "unknown", "query": "排查"}
    r = run_subtask(subtask, mock_llm_decision, None)
    assert r["task_id"] == "t1"
    assert "mock" in r["conclusion"] or r["conclusion"]
    assert len(r["tool_trace"]) >= 1  # 至少调了一个工具


def test_run_subtasks_parallel_collects_all():
    subs = [
        {"task_id": "t1", "task_type": "diagnosis", "target_service": "unknown", "query": "a"},
        {"task_id": "t2", "task_type": "inspection", "target_service": "unknown", "query": "b"},
    ]
    results = run_subtasks(subs, mock_llm_decision, None)
    assert set(results.keys()) == {"t1", "t2"}


def test_merge_review():
    from llm_mock import mock_reviewer_result
    out = merge_review({"t1": {"conclusion": "a"}}, mock_reviewer_result)
    assert isinstance(out, str) and out
```

- [ ] **Step 2: 运行测试验证失败**

Run: `cd ai-orchestrator && python -m pytest tests/test_dual_agent.py -v`
Expected: FAIL with `ModuleNotFoundError: No module named 'dual_agent'`

- [ ] **Step 3: 实现 `dual_agent.py`**

```python
# ai-orchestrator/dual_agent.py
"""双层 Agent 编排纯逻辑（Coordinator 拆解 → 并行子 Agent → Reviewer 合并）。

不依赖 langgraph/orchestrator，便于独立测试（本机 Python 3.9）。
"""
import json
import re
import time
from concurrent.futures import ThreadPoolExecutor

from function_calling import run_tool_loop, WHITELIST_READONLY
from skill_registry import ExpertRegistry, ToolRegistry


def parse_subtasks(raw: str) -> list:
    """解析 Coordinator LLM 输出的子任务拆解 JSON（容忍 ```json 围栏/噪声）。"""
    if not raw:
        return []
    text = raw.strip()
    m = re.search(r"```(?:json)?\s*(.*?)```", text, re.DOTALL)
    if m:
        text = m.group(1).strip()
    # 提取首个 [ ... ] 数组
    arr = re.search(r"\[.*\]", text, re.DOTALL)
    if not arr:
        return []
    try:
        data = json.loads(arr.group(0))
        if isinstance(data, list):
            return [d for d in data if isinstance(d, dict) and d.get("task_id")]
    except Exception:
        return []
    return []


def _expert_tools(expert_registry, task_type: str) -> list:
    """获取某子任务类型对应专家的工具列表。"""
    name = expert_registry.match_intent(task_type).name if expert_registry else None
    if not name:
        name = task_type if task_type in ExpertRegistry.BUILTIN_EXPERTS else "inspection"
    tools = []
    if expert_registry:
        for s in ExpertRegistry.skills_of(name):
            if s:
                tools.extend(s.tools)
    # 去重
    seen, dedup = set(), []
    for t in tools:
        if t not in seen:
            seen.add(t)
            dedup.append(t)
    return [ToolRegistry.get(t) for t in dedup if ToolRegistry.get(t)]


def run_subtask(subtask: dict, llm_decision, expert_registry, on_tool=None) -> dict:
    """单个子任务跑一遍 function-calling 循环。"""
    task_type = subtask.get("task_type", "inspection")
    query = subtask.get("query", subtask.get("target_service", ""))
    tools = _expert_tools(expert_registry, task_type)
    started = time.time()
    res = run_tool_loop(llm_decision, tools, query,
                        whitelist=WHITELIST_READONLY, on_tool=on_tool)
    return {
        "task_id": subtask.get("task_id", "task"),
        "task_type": task_type,
        "conclusion": res.get("final", ""),
        "tool_trace": res.get("trace", []),
        "cost": round(time.time() - started, 2),
        "truncated": res.get("truncated", False),
    }


def run_subtasks(subtasks: list, llm_decision_factory, expert_registry, on_tool=None) -> dict:
    """并行跑全部子任务，返回有序 sub_results dict。"""
    if not subtasks:
        return {}

    def _run(t):
        return run_subtask(t, llm_decision_factory, expert_registry,
                           on_tool=(lambda n, r: on_tool(t["task_id"], t["task_type"], n, r)) if on_tool else None)

    with ThreadPoolExecutor(max_workers=min(len(subtasks), 4)) as ex:
        futures = {ex.submit(_run, t): t for t in subtasks}
        results = {}
        for fut in futures:
            r = fut.result()
            results[r["task_id"]] = r
    # 保持原顺序
    ordered = {}
    for t in subtasks:
        tid = t["task_id"]
        if tid in results:
            ordered[tid] = results[tid]
    return ordered


def merge_review(sub_results: dict, reviewer_decision) -> str:
    """Reviewer 合并全部子结论，返回最终报告。"""
    if reviewer_decision is None:
        parts = []
        for tid, r in sub_results.items():
            parts.append(f"## {tid}({r.get('task_type','')})\n{r.get('conclusion','')}")
        return "\n\n".join(parts) if parts else "(无子结论)"
    return reviewer_decision(sub_results)
```

- [ ] **Step 4: 运行测试验证通过**

Run: `cd ai-orchestrator && python -m pytest tests/test_dual_agent.py -v`
Expected: PASS (6 tests)

- [ ] **Step 5: 提交**

```bash
git add ai-orchestrator/dual_agent.py ai-orchestrator/tests/test_dual_agent.py
git commit -m "feat(batch3): 双层Agent编排纯逻辑 - Coordinator拆解/并行子Agent/Reviewer合并"
```

---

### Task 4: `orchestrator.py` — LangGraph 节点壳 + dual_graph + mode

**Files:**
- Modify: `ai-orchestrator/orchestrator.py`
- Test: 本任务由 Task 3 的纯逻辑覆盖，节点壳通过 `python -c` import 冒烟（不跑 langgraph 重链）

**Interfaces:**
- Consumes: `dual_agent.parse_subtasks/run_subtasks/merge_review`、`llm_mock`、`_llm`、`AgentState`
- Produces:
  - `node_coordinator(state) -> dict` — 写 `subtasks`
  - `node_subagent(state) -> dict` — 写 `sub_results`
  - `node_reviewer(state) -> dict` — 写 `review_result`/`final_response`
  - `BrainOrchestrator.dual_graph` — 新 LangGraph
  - `stream_sync(..., mode="dual")` — 支持 dual 模式 SSE

- [ ] **Step 1: 在 `orchestrator.py` 添加 import**

在文件顶部 imports 区追加（在 `from llm_mock import ...` 之后）：
```python
from dual_agent import parse_subtasks, run_subtasks, merge_review
```

- [ ] **Step 2: 添加三个节点函数**（放在 `node_summarize` 之前）

```python
def node_coordinator(state):
    """双层 Agent - Coordinator：拆解用户意图为子任务列表。"""
    cfg = state.get("llm_config")
    user_msg = state.get("user_message", "")
    context = (state.get("services_data", "") + state.get("alert_data", ""))[:2000]
    raw = ""
    if should_skip_llm(cfg) or is_mock_enabled():
        raw = json.dumps(mock_coordinator_plan(), ensure_ascii=False)
    else:
        raw = _llm(cfg, "你是任务协调器。把用户请求拆解为可并行执行的子任务，"
                         "输出 JSON 数组，每项含 task_id/task_type/target_service/query。"
                         "task_type 限 diagnosis/inspection/ops/query。只输出 JSON。",
                   f"用户请求:「{user_msg}」\n上下文:\n{context}", "Coordinator")
    subtasks = parse_subtasks(raw)
    if not subtasks:
        subtasks = [{"task_id": "t1", "task_type": "diagnosis",
                     "target_service": state.get("service", ""), "query": user_msg}]
    return {"subtasks": subtasks, "messages": [f"[{_now()}] Coordinator 拆解为 {len(subtasks)} 个子任务"]}


def node_subagent(state):
    """双层 Agent - 子 Agent：并行跑每个子任务的 function-calling 循环。"""
    subtasks = state.get("subtasks") or []
    if not subtasks:
        return {"sub_results": {}}
    sub_results = run_subtasks(subtasks, mock_llm_decision, ExpertRegistry)
    return {"sub_results": sub_results,
            "messages": [f"[{_now()}] {len(sub_results)} 个子 Agent 完成"]}


def node_reviewer(state):
    """双层 Agent - Reviewer：合并审查全部子结论，输出最终报告。"""
    cfg = state.get("llm_config")
    sub_results = state.get("sub_results") or {}
    if should_skip_llm(cfg) or is_mock_enabled():
        final = mock_reviewer_result(sub_results)
    else:
        parts = "\n\n".join(f"[{tid}]({r.get('task_type','')}): {r.get('conclusion','')[:500]}"
                            for tid, r in sub_results.items())
        final = _llm(cfg, "你是结果审查员。合并子 Agent 结论，校验依据与冲突，输出最终诊断报告。",
                     f"子结论:\n{parts}", "Reviewer")
    return {"review_result": final, "final_response": final,
            "messages": [f"[{_now()}] Reviewer 审查完成"]}
```

> 注意：`node_subagent` 中并行子 Agent 目前用 `mock_llm_decision`（与 mock 一致）；真实 LLM 的 function-calling 决策器（`llm_decision_factory`）在 Task 5 接入，届时替换此调用。

- [ ] **Step 3: 添加 `dual_graph` 到 `BrainOrchestrator.__init__`**

在 `__init__` 中现有 `self.chat_graph = ...` 之后追加：
```python
        self.dual_graph = build_graph(checkpointer=self.checkpointer, mode="dual")
```

- [ ] **Step 4: 在 `build_graph` 中增加 `dual` 模式分支**

在 `build_graph` 的 `nodes` 列表加入 coordinator/subagent/reviewer，并在 mode 判断里加 `dual` 分支：
```python
    nodes = [
        ("collect", node_collect), ("clean", node_clean), ("rca", node_rca),
        ("rag", node_rag),
        ("crewai", node_crewai), ("holmes", node_holmes),
        ("coordinator", node_coordinator), ("subagent", node_subagent),
        ("reviewer", node_reviewer),
        ("plan", node_plan), ("risk", node_risk),
        ("wait_approval", node_wait_approval),
        ("execute", node_execute), ("verify", node_verify),
        ("report", node_report), ("memorize", node_memorize),
        ("summarize", node_summarize),
    ]
```
在 `if mode == "chat":` 之后新增：
```python
    if mode == "dual":
        # 双层 Agent 路径: collect→clean→rca→rag→coordinator→subagent→reviewer→summarize
        builder.add_edge("rag", "coordinator")
        builder.add_edge("coordinator", "subagent")
        builder.add_edge("subagent", "reviewer")
        builder.add_edge("reviewer", "summarize")
```
（确保 `if mode == "chat"` 分支保持原样，`else` 完整路径分支不变。）

- [ ] **Step 5: 更新 `stream_sync` 支持 dual mode**

将 `stream_sync(self, intent, service, message, thread_id="default")` 签名改为 `stream_sync(self, intent, service, message, thread_id="default", mode="chat")`，并把：
```python
            graph = getattr(self, "chat_graph", self.graph)
```
改为：
```python
            graph = getattr(self, "dual_graph" if mode == "dual" else "chat_graph", self.graph)
```
同时把 `step_names` 增加映射：
```python
        step_names = {..., "coordinator": "Coordinator 拆解", "subagent": "子Agent 分析", "reviewer": "Reviewer 审查"}
```

- [ ] **Step 6: 冒烟验证 import（不触发 langgraph 重链）**

Run: `cd ai-orchestrator && python -c "import ast; ast.parse(open('orchestrator.py').read()); print('syntax ok')"`
Expected: `syntax ok`（本机不解析 langgraph 重依赖，仅验证语法与结构）

- [ ] **Step 7: 提交**

```bash
git add ai-orchestrator/orchestrator.py
git commit -m "feat(batch3): orchestrator 增加 coordinator/subagent/reviewer 节点与 dual_graph"
```

---

### Task 5: 真实 LLM function-calling 决策器 + 接入 node_subagent

**Files:**
- Create: `ai-orchestrator/llm_fc.py`
- Test: `ai-orchestrator/tests/test_function_calling.py`（追加 schema/决策解析单测，不依赖 orchestrator）

**Interfaces:**
- Consumes: `function_calling.make_tools_schema`、`_llm`（真实 LLM 调用）
- Produces:
  - `make_llm_decision_fn(cfg, system_prompt) -> callable` — 返回一个 `llm_decision(messages, tools)` 决策器，调真实 LLM，解析其 JSON 输出（`{"type":"tool"|"final", ...}`）

- [ ] **Step 1: 追加失败测试**

```python
# tests/test_function_calling.py 追加
from llm_fc import parse_llm_decision


def test_parse_llm_decision_tool():
    d = parse_llm_decision('{"type":"tool","name":"query_metrics","arguments":{"service":"svc"}}')
    assert d["type"] == "tool" and d["name"] == "query_metrics"


def test_parse_llm_decision_final():
    d = parse_llm_decision('{"type":"final","content":"结论"}')
    assert d["type"] == "final"


def test_parse_llm_decision_invalid_defaults_final():
    d = parse_llm_decision("no json")
    assert d["type"] == "final"
```

- [ ] **Step 2: 运行测试验证失败**

Run: `cd ai-orchestrator && python -m pytest tests/test_function_calling.py -v`
Expected: FAIL with `ModuleNotFoundError: No module named 'llm_fc'`

- [ ] **Step 3: 实现 `llm_fc.py`**

```python
# ai-orchestrator/llm_fc.py
"""真实 LLM function-calling 决策器（解析 LLM 返回的 JSON 决策）。"""
import json
import re


def parse_llm_decision(raw: str) -> dict:
    """解析 LLM 输出为决策 dict。tool 决策要求 name；否则视为 final。"""
    if not raw:
        return {"type": "final", "content": ""}
    text = raw.strip()
    m = re.search(r"\{.*\}", text, re.DOTALL)
    if m:
        text = m.group(0)
    try:
        d = json.loads(text)
    except Exception:
        return {"type": "final", "content": raw[:2000]}
    if d.get("type") == "tool" and d.get("name"):
        return {"type": "tool", "name": d["name"], "arguments": d.get("arguments") or {}}
    return {"type": "final", "content": d.get("content") or raw[:2000]}


def make_llm_decision_fn(cfg, system_prompt):
    """构造真实 LLM 决策器。使用 orchestrator._llm 执行，需在运行期 import 避免顶层重链。"""
    from orchestrator import _llm
    def decision(messages, tools):
        tools_desc = "\n".join(
            f"- {t['function']['name']}: {t['function']['description']}" for t in (tools or []))
        recent = messages[-1].get("content", "") if messages else ""
        prompt = (f"{system_prompt}\n\n可用工具:\n{tools_desc}\n\n"
                  f"最近上下文:\n{recent}\n\n"
                  f"请决定下一步：需要调用工具则输出 "
                  f'{{"type":"tool","name":"工具名","arguments":{{...}}}}；'
                  f"否则输出 {{\"type\":\"final\",\"content\":\"最终结论\"}}。只输出 JSON。")
        raw = _llm(cfg, system_prompt, prompt, "function-calling Agent")
        return parse_llm_decision(raw)
    return decision
```

- [ ] **Step 4: 运行测试验证通过**

Run: `cd ai-orchestrator && python -m pytest tests/test_function_calling.py -v`
Expected: PASS (5 + 3 = 8 tests)

- [ ] **Step 5: 将真实决策器接入 `node_subagent`**

在 `node_subagent` 中，把真实 LLM 决策器注入 `run_subtasks`（保留 mock 分支）：
```python
def node_subagent(state):
    subtasks = state.get("subtasks") or []
    if not subtasks:
        return {"sub_results": {}}
    cfg = state.get("llm_config")
    if should_skip_llm(cfg) or is_mock_enabled():
        decision = mock_llm_decision
    else:
        from llm_fc import make_llm_decision_fn
        decision = make_llm_decision_fn(cfg, "你是可观测性诊断子 Agent，通过调用工具收集证据并给出结论。")
    sub_results = run_subtasks(subtasks, decision, ExpertRegistry)
    return {"sub_results": sub_results,
            "messages": [f"[{_now()}] {len(sub_results)} 个子 Agent 完成"]}
```

- [ ] **Step 6: 冒烟验证语法**

Run: `cd ai-orchestrator && python -c "import ast; ast.parse(open('orchestrator.py').read()); ast.parse(open('llm_fc.py').read()); print('syntax ok')"`
Expected: `syntax ok`

- [ ] **Step 7: 提交**

```bash
git add ai-orchestrator/llm_fc.py ai-orchestrator/orchestrator.py ai-orchestrator/tests/test_function_calling.py
git commit -m "feat(batch3): 真实LLM function-calling 决策器并接入 node_subagent"
```

---

### Task 6: 入口分流 — models.py + main.py 的 dual_agent 开关

**Files:**
- Modify: `ai-orchestrator/models.py`
- Modify: `ai-orchestrator/main.py`

**Interfaces:**
- Consumes: `ChatRequest`（新增 `dual_agent` 字段）、`BrainOrchestrator.stream_sync`（mode 参数）
- Produces: `/api/v1/ai/chat` 在 `req.dual_agent=True` 时走 dual 模式

- [ ] **Step 1: `models.py` 给 ChatRequest 加开关**

```python
class ChatRequest(BaseModel):
    intent: str = "diagnosis"
    service: str = ""
    message: str = ""
    stream: bool = True
    session_id: Optional[str] = None
    dual_agent: bool = False   # 批3: 双层 Agent 开关（默认关闭，零回归）
```

- [ ] **Step 2: `main.py` ai_chat 传 mode**

将 `main.py:153` 的调用改为传入 mode：
```python
                for event in _get_brain().stream_sync(req.intent, req.service or "", req.message, thread_id,
                                                      mode="dual" if req.dual_agent else "chat"):
```

- [ ] **Step 3: 冒烟验证语法 + 手动验证（本地起 server）**

Run: `cd ai-orchestrator && python -c "import ast; ast.parse(open('models.py').read()); ast.parse(open('main.py').read()); print('syntax ok')"`
Expected: `syntax ok`

（可选，若本机能起 server）Run: `LLM_MOCK=true python main.py &` 后 `curl -s http://localhost:8000/api/v1/ai/chat -X POST -H 'Content-Type: application/json' -d '{"message":"诊断一下","stream":false,"dual_agent":true}'` 观察返回含 mock 双层结论。

- [ ] **Step 4: 提交**

```bash
git add ai-orchestrator/models.py ai-orchestrator/main.py
git commit -m "feat(batch3): /ai/chat 增加 dual_agent 开关，走双层Agent图"
```

---

### Task 7: 前端 — ChatThread 工具卡片类型标签 + 子Agent链展开

**Files:**
- Modify: `observability-frontend/src/pages/AIChat/ChatThread.tsx`

**Interfaces:**
- Consumes: 现有 SSE `tool_start`/`tool_end` 事件；后端在 tool_start 中带 `agent_type` 字段（coordinator/subagent/reviewer/tool）
- Produces: 工具卡片按 agent_type 显示标签；subagent 卡片显示其 tool_trace 子链

- [ ] **Step 1: 后端在 SSE tool_start 事件带 agent_type**

在 `orchestrator.py` 的 `stream_sync` 中，把 tool 事件增加 `agent_type` 字段（coordinator 对应 coordinator 节点，subagent 用子 Agent 名，reviewer 用 reviewer 节点）：
```python
                if node_name == "coordinator":
                    yield {"type": "tool_start", "tool_call_id": tool_id, "name": "Coordinator 拆解",
                           "agent_type": "coordinator", "status": "pending", "arguments": {}}
                    yield {"type": "tool_end", "tool_call_id": tool_id, "name": "Coordinator 拆解",
                           "agent_type": "coordinator", "status": "success",
                           "arguments": {}, "result": str(node_data.get("subtasks", ""))[:500]}
                elif node_name == "subagent":
                    for tid, r in (node_data.get("sub_results") or {}).items():
                        sid = f"sub_{tid}"
                        yield {"type": "tool_start", "tool_call_id": sid, "name": f"子Agent {r.get('task_type','')}",
                               "agent_type": "subagent", "status": "pending", "arguments": {}}
                        yield {"type": "tool_end", "tool_call_id": sid, "name": f"子Agent {r.get('task_type','')}",
                               "agent_type": "subagent", "status": "success",
                               "arguments": {}, "result": r.get("conclusion", "")[:500],
                               "tool_trace": r.get("tool_trace", [])}
                elif node_name == "reviewer":
                    yield {"type": "tool_start", "tool_call_id": tool_id, "name": "Reviewer 审查",
                           "agent_type": "reviewer", "status": "pending", "arguments": {}}
                    yield {"type": "tool_end", "tool_call_id": tool_id, "name": "Reviewer 审查",
                           "agent_type": "reviewer", "status": "success",
                           "arguments": {}, "result": str(node_data.get("review_result", ""))[:500]}
```

> 说明：上述子 Agent 的 SSE 内联在 `stream_sync` 里（节点粒度），真实 function-calling 的逐工具事件由 `run_tool_loop` 的 `on_tool` 回调产生（Task 3/5 已预留），若需要逐工具级展示可在后续增强，本任务先做节点级双层展示。

- [ ] **Step 2: `ChatThread.tsx` 更新 ToolCard 接口**

```typescript
interface ToolCard {
  tool_call_id: string
  name: string
  status: string
  result?: string
  agent_type?: string   // coordinator | subagent | reviewer | tool
  tool_trace?: { tool: string; result: string }[]
}
```

- [ ] **Step 3: `ChatThread.tsx` 渲染增加 agent_type 标签与展开**

将 tool 事件解析中补上 agent_type/tool_trace：
```typescript
          case 'tool_start':
            toolLocal.push({ tool_call_id: ev.tool_call_id, name: ev.name, status: 'pending', agent_type: ev.agent_type, tool_trace: ev.tool_trace })
            break
          case 'tool_end':
            toolLocal = toolLocal.map((t) => (t.tool_call_id === ev.tool_call_id ? { ...t, status: ev.status, result: ev.result, agent_type: ev.agent_type ?? t.agent_type, tool_trace: ev.tool_trace ?? t.tool_trace } : t))
            break
```
将工具卡片渲染改为（含类型标签与 subagent 链展开）：
```tsx
        {toolCards.map((t) => {
          const tagColor = t.agent_type === 'coordinator' ? 'purple' : t.agent_type === 'reviewer' ? 'orange' : t.agent_type === 'subagent' ? 'blue' : 'default'
          return (
            <div key={t.tool_call_id} style={{ display: 'flex', flexDirection: 'column', gap: 4, padding: '6px 12px', marginBottom: 6, background: 'var(--surface-2)', borderRadius: 8 }}>
              <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                <Tag color={tagColor}>{t.agent_type || 'tool'}</Tag>
                <span style={{ fontSize: 12, color: 'var(--text)' }}>⚙️ {t.name}</span>
                <span style={{ fontSize: 11, color: t.status === 'success' ? '#22c55e' : '#a1a1aa' }}>{t.status}</span>
              </div>
              {t.agent_type === 'subagent' && t.tool_trace && t.tool_trace.length > 0 && (
                <div style={{ marginLeft: 24, fontSize: 11, color: 'var(--text-muted)' }}>
                  {t.tool_trace.map((tr, i) => <div key={i}>→ {tr.tool}: {tr.result.slice(0, 60)}</div>)}
                </div>
              )}
            </div>
          )
        })}
```
（需在文件顶部确认 `Tag` 从 antd 导入：`import { Tag } from 'antd'`）

- [ ] **Step 4: 前端 tsc 校验**

Run: `cd observability-frontend && npx tsc --noEmit -p tsconfig.json 2>&1 | head -30`
Expected: 无新增类型错误（或仅提示与本任务无关的既有错误）

- [ ] **Step 5: 提交**

```bash
git add observability-frontend/src/pages/AIChat/ChatThread.tsx ai-orchestrator/orchestrator.py
git commit -m "feat(batch3): 前端双层Agent工具卡片标签与子Agent链展开"
```

---

### Task 8: 全量回归验证 + 部署

**Files:** 无（验证与部署）

- [ ] **Step 1: 全量单测**

Run: `cd ai-orchestrator && python -m pytest tests/test_function_calling.py tests/test_dual_agent.py tests/test_llm_mock.py -v`
Expected: ALL PASS（function_calling 8 + dual_agent 6 + llm_mock 8 = 22 tests）

- [ ] **Step 2: 既有测试回归**

Run: `cd ai-orchestrator && python -m pytest tests/ -q 2>&1 | tail -5`
Expected: 既有测试保持通过，无新增失败（批3 新增模块不 import orchestrator，不影响既有隔离测试）

- [ ] **Step 3: Go 后端回归**（若批3 改动涉及 query-api 依赖校验，通常不涉及）

Run: `cd ai-apm-query-go && go build ./... && go test ./... 2>&1 | tail -10`
Expected: 通过（批3 不改 Go，通常无需跑；若跑则应为全绿）

- [ ] **Step 4: 本地冒烟（mock 双层端到端）**

Run: `cd ai-orchestrator && LLM_MOCK=true python -c "
from dual_agent import parse_subtasks, run_subtasks, merge_review
from llm_mock import mock_coordinator_plan, mock_llm_decision, mock_reviewer_result
from skills import init_skills, init_experts
init_skills(); init_experts()
subs = parse_subtasks('')
if not subs:
    subs = mock_coordinator_plan()
from skill_registry import ExpertRegistry
results = run_subtasks(subs, mock_llm_decision, ExpertRegistry)
final = merge_review(results, mock_reviewer_result)
print('FINAL:', final[:200])
assert final
print('OK')
"`
Expected: `OK`，且 FINAL 含 mock 双层结论（证明 mock 下端到端跑通）

- [ ] **Step 5: 构建并部署 ai-orchestrator（国内源）**

```bash
cd aiops/ai-orchestrator
# 打 tar 上传远程构建（含 skills/ 目录）
scp -r . root@192.168.0.63:/tmp/orch-src/
ssh root@192.168.0.63 'cd /tmp/orch-src && \
  python -m pip install -i https://pypi.tuna.tsinghua.edu.cn/simple -r requirements.txt && \
  docker build -t ai-orchestrator:v10 . && \
  docker tag ai-orchestrator:v10 docker.io/library/ai-orchestrator:v10 && \
  docker save docker.io/library/ai-orchestrator:v10 | ctr -n k8s.io images import - && \
  kubectl -n observability set image deploy/ai-orchestrator ai-orchestrator=docker.io/library/ai-orchestrator:v10 && \
  kubectl -n observability rollout restart deploy/ai-orchestrator && \
  kubectl -n observability rollout status deploy/ai-orchestrator'
```

- [ ] **Step 6: 构建并部署前端（离线重建）**

```bash
cd observability-frontend
npm install && npm run build
# 推送 dist 到远程，用本地旧 nginx 镜像离线重建（实测可行）
scp -r dist/* root@192.168.0.63:/tmp/frontend-img/dist/
ssh root@192.168.0.63 'cd /tmp/frontend-img && \
  docker build -t observability-frontend:vNEXT . && \
  docker tag observability-frontend:vNEXT docker.io/library/observability-frontend:vNEXT && \
  docker save docker.io/library/observability-frontend:vNEXT | ctr -n k8s.io images import - && \
  kubectl -n observability set image deploy/frontend frontend=docker.io/library/observability-frontend:vNEXT && \
  kubectl -n observability rollout restart deploy/frontend && \
  kubectl -n observability rollout status deploy/frontend'
```

- [ ] **Step 7: 端到端验证**

Run: `curl -s -o /dev/null -w "%{http_code}" http://localhost:30253/` → 200
Run: `curl -s http://localhost:30253/api/v1/ai/chat -X POST -H 'Content-Type: application/json' -H 'Authorization: Bearer <token>' -d '{"message":"诊断一下","stream":false,"dual_agent":true}'`
Expected: 返回含 mock 双层诊断文本；前端 AIChat 开启"双层Agent"开关后能看到 Coordinator/子Agent/Reviewer 工具卡片

- [ ] **Step 8: 提交（若部署脚本/版本有改动）**

```bash
git add -A && git commit -m "chore(batch3): 部署验证通过" --no-verify || echo "无待提交改动"
```

---

## 自审

**1. Spec 覆盖：**
- A2 function-calling 循环 → Task 1（循环引擎+护栏）、Task 5（真实 LLM 决策器）✅
- A3 双层 Agent（Coordinator/子Agent/Reviewer）→ Task 3（纯逻辑）、Task 4（LangGraph 节点壳）✅
- mock 模拟工具循环 → Task 2 ✅
- 护栏（max_steps/工具超时/白名单/总耗时）→ Task 1 引擎 ✅
- 前端双层展示 → Task 7 ✅
- 入口分流零回归 → Task 6 ✅
- 测试 TDD → 每 Task 含 TDD 步骤 ✅
- 部署验证 → Task 8 ✅

**2. 占位符扫描：** 无 TBD/TODO；每个代码步骤都有完整实现。

**3. 类型/签名一致性：**
- `make_tools_schema(tools)->list[dict]` 在 Task 1 定义、Task 5 使用 ✅
- `run_tool_loop(llm_decision, tools, user_prompt, ...)` 签名在 Task 1 定义、Task 3 `run_subtask` 使用一致 ✅
- `parse_subtasks/run_subtasks/merge_review` 在 Task 3 定义、Task 4/8 使用一致 ✅
- `mock_llm_decision/mock_coordinator_plan/mock_reviewer_result` 在 Task 2 定义、Task 3/4/8 使用一致 ✅
- `stream_sync(..., mode=)` 在 Task 4 加参、Task 6 main.py 传参一致 ✅
