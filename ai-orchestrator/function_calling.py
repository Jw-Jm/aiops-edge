"""通用 LLM function-calling 工具循环（纯逻辑，无 langgraph 依赖）。

供子 Agent / 双层 Agent 复用：工具 schema 生成、白名单守卫、迭代式循环引擎。
"""
import json
import time
from typing import Callable, List, Dict, Set, Optional

from skill_registry import ToolDef, ToolRegistry

# 默认只读白名单：仅允许安全、只读的观测工具
WHITELIST_READONLY: Set[str] = {
    "query_metrics", "query_traces", "query_topology", "get_service_list",
    "get_infrastructure", "deepflow_status", "query_logs", "k8sgpt_diagnose",
}


def make_tools_schema(tools: List[ToolDef]) -> List[dict]:
    """将 ToolRegistry 工具转换为 OpenAI function-calling schema。"""
    out = []
    for t in tools:
        props = {}
        req = []
        for pname, pinfo in (t.params or {}).items():
            ptype = pinfo.get("type", "string") if isinstance(pinfo, dict) else "string"
            ptype_map = {"int": "integer", "float": "number", "bool": "boolean", "string": "string"}
            props[pname] = {
                "type": ptype_map.get(str(ptype), "string"),
                "description": (pinfo.get("desc", "") if isinstance(pinfo, dict) else ""),
            }
            if isinstance(pinfo, dict) and pinfo.get("required"):
                req.append(pname)
        out.append({
            "type": "function",
            "function": {
                "name": t.name,
                "description": t.description,
                "parameters": {
                    "type": "object",
                    "properties": props,
                    "required": req if req else None,
                },
            },
        })
    return out


def exec_tool_with_guard(tool: ToolDef, args: dict, whitelist: Set[str]) -> str:
    """白名单校验 + 统一审批闸门 + 执行工具。mutating/dangerous/需审批/不在白名单一律拒绝。"""
    from execution_gate import check_tool_executable
    # 统一审批闸门：safe 直接执行；mutating/dangerous/需审批 → 拒绝（function-calling 循环永不自动审批）
    allowed, reason = check_tool_executable(tool, approved=False)
    if not allowed:
        return f"[工具 {tool.name} 被拒绝]：{reason}，function-calling 循环不自动执行。"
    if tool.name not in whitelist:
        return f"[工具 {tool.name} 被拒绝]：不在 function-calling 白名单中。"
    try:
        schema = tool.params or {}
        filtered = {k: v for k, v in (args or {}).items() if k in schema}
        result = tool.func(**filtered) if filtered else tool.func()
        return str(result)[:2000]
    except Exception as e:
        return f"[工具 {tool.name} 执行失败]：{e}"


def run_tool_loop(llm_decision: Callable[[List[dict], List[dict]], dict],
                  tools: List[ToolDef],
                  user_prompt: str,
                  whitelist: Set[str] = WHITELIST_READONLY,
                  max_steps: int = 6,
                  max_tool_calls: int = 4,
                  max_duration_s: int = 120,
                  on_tool: Optional[Callable] = None) -> dict:
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
