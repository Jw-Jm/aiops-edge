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
    """获取某子任务类型对应专家的工具列表。

    无 expert_registry（或专家未匹配到工具）时，退化为白名单内全部已注册只读工具，
    保证真实 LLM 也能看到可用工具 schema，而不只是硬编码的 mock。
    """
    name = None
    if expert_registry:
        matched = expert_registry.match_intent(task_type)
        name = matched.name if matched else None
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
    resolved = [ToolRegistry.get(t) for t in dedup if ToolRegistry.get(t)]
    # 退化为白名单内全部已注册只读工具（当无专家工具或全部为空时）
    if not resolved:
        resolved = [t for t in ToolRegistry.list_all() if t.name in WHITELIST_READONLY and t.cls == "safe"]
    return resolved


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
            parts.append(f"## {tid}({r.get('task_type', '')})\n{r.get('conclusion', '')}")
        return "\n\n".join(parts) if parts else "(无子结论)"
    return reviewer_decision(sub_results)
