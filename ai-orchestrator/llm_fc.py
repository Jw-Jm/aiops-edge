"""真实 LLM function-calling 决策器（解析 LLM 返回的 JSON 决策）。

纯逻辑：parse_llm_decision 不依赖 orchestrator/langgraph，可独立测试。
make_llm_decision_fn 在运行期 import orchestrator._llm（避免顶层重依赖链）。
"""
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
