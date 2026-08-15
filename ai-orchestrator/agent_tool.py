"""spawn_worker 工具: coordinator 派活给 specialist persona + worker 运行时（B3）。

worker 运行时复用 function_calling.run_tool_loop：工具集 = persona.tools - disallowed，
read-only 再 ∩ WHITELIST_READONLY；max_turns 作为工具调用轮数上限。
B4 的后台执行 + task_notification SSE 通知在 spawn_worker(background=True) 提供。
"""
import queue
from concurrent.futures import ThreadPoolExecutor

from function_calling import run_tool_loop, WHITELIST_READONLY
from skill_registry import ToolRegistry

# ── personas 注入钩子（main.py 挂载时 set_personas；未 set 时懒加载 builtin 目录）──
_PERSONAS = None

# ── 后台 worker 执行器与终态通知（B4）──
_BG_EXECUTOR = ThreadPoolExecutor(max_workers=4, thread_name_prefix="persona-bg")
_NOTIFICATION_QUEUE = queue.Queue()
_ON_DONE = None


def set_personas(personas: dict):
    """注入 persona 注册表（main.py 启动挂载时调用）。"""
    global _PERSONAS
    _PERSONAS = personas or {}


def _personas() -> dict:
    global _PERSONAS
    if _PERSONAS is None:
        from persona_registry import load_personas, PERSONAS_BUILTIN_DIR
        _PERSONAS = load_personas(PERSONAS_BUILTIN_DIR)
    return _PERSONAS


def task_notification(worker: str, status: str, summary: str) -> dict:
    """后台 worker 终态 SSE frame：{"type":"task_notification","data":{...}}。"""
    return {"type": "task_notification",
            "data": {"worker": worker, "status": status, "summary": summary}}


def set_on_done(cb=None):
    """注册 worker 完成回调；不传时用默认（写入通知队列供 chat SSE 轮询）。"""
    global _ON_DONE
    _ON_DONE = cb if cb is not None else _default_on_done


def _default_on_done(worker: str, status: str, summary: str):
    _NOTIFICATION_QUEUE.put(task_notification(worker, status, summary))


def drain_notifications() -> list:
    """返回并清空通知队列（chat SSE 生成器轮询 task_notification frame）。"""
    items = []
    while True:
        try:
            items.append(_NOTIFICATION_QUEUE.get_nowait())
        except queue.Empty:
            break
    return items


def _notify(worker: str, status: str, summary: str):
    if _ON_DONE:
        try:
            _ON_DONE(worker, status, summary)
        except Exception:  # noqa: BLE001
            pass


# ── worker 运行时 ──

def worker_tools(persona):
    """persona 工具集 = persona.tools - disallowed；read-only 再 ∩ 只读白名单。"""
    by_name = {t.name: t for t in ToolRegistry.list_all()}
    tools = [by_name[n] for n in persona.tools if n in by_name and n not in persona.disallowed_tools]
    if persona.permission_mode == "read-only":
        tools = [t for t in tools if t.name in WHITELIST_READONLY]
    return tools


def _brain_llm_config() -> dict:
    """取进程内 brain.llm_config（真实 LLM 决策用）；失败返回空 dict。"""
    try:
        from orchestrator import brain
        return getattr(brain, "llm_config", None) or {}
    except Exception:  # noqa: BLE001
        return {}


def _make_decision_fn(system_prompt: str):
    """构造 worker 的 LLM 决策器：mock 优先，否则真实 LLM（复用 llm_fc 模式）。"""
    from llm_mock import is_mock_enabled, mock_llm_decision
    if is_mock_enabled():
        return mock_llm_decision

    def decision(messages, tools):
        from orchestrator import _llm, _llm_key_ready
        from llm_fc import parse_llm_decision
        if not _llm_key_ready():
            return {"type": "final", "content": "(LLM 未配置，worker 无法执行调查)"}
        tools_desc = "\n".join(f"- {t.name}: {t.description}" for t in (tools or []))
        recent = messages[-1].get("content", "") if messages else ""
        prompt = (f"可用工具:\n{tools_desc}\n\n最近上下文:\n{recent}\n\n"
                  f"请决定下一步：需要调用工具则输出 "
                  f'{{"type":"tool","name":"工具名","arguments":{{...}}}}；'
                  f"否则输出 {{\"type\":\"final\",\"content\":\"最终结论\"}}。只输出 JSON。")
        raw = _llm(_brain_llm_config(), system_prompt, prompt, "specialist-worker")
        return parse_llm_decision(raw)
    return decision


def run_worker(persona, prompt: str) -> str:
    """worker 运行时：persona 工具白名单 + max_turns 上限，返回结论文本。"""
    tools = worker_tools(persona)
    system = persona.system_prompt or "你是运维 specialist。"
    instruction = (f"{system}\n\n## 任务\n{prompt}\n\n"
                   f"(最多 {persona.max_turns} 轮工具调用; 结束时输出结论)")
    if persona.permission_mode == "read-only":
        whitelist = WHITELIST_READONLY
    else:
        whitelist = {t.name for t in tools}
    res = run_tool_loop(_make_decision_fn(system), tools, instruction,
                        whitelist=whitelist,
                        max_steps=persona.max_turns + 1,
                        max_tool_calls=persona.max_turns,
                        max_duration_s=max(persona.max_turns * 15, 60))
    final = (res or {}).get("final", "") or ""
    return final or "(worker 无结论)"


def spawn_worker(subagent_type: str, description: str, prompt: str,
                 background: bool = False) -> str:
    """把任务派发给 specialist worker。background=True 立即返回，终态经通知队列投递。"""
    personas = _personas()
    p = personas.get(subagent_type)
    if not p:
        return f"错误: 未知 specialist {subagent_type}, 可用: {', '.join(sorted(personas))}"
    if background:
        _BG_EXECUTOR.submit(_run_bg, subagent_type, p, prompt)
        return f"已后台启动 {subagent_type} worker，完成后将通过 SSE 通知。"
    return run_worker(p, prompt)


def _run_bg(worker_name: str, persona, prompt: str):
    try:
        summary = run_worker(persona, prompt)
        status = "completed"
    except Exception as e:  # noqa: BLE001
        summary = f"worker {worker_name} 执行失败: {e}"
        status = "failed"
    _notify(worker_name, status, summary)


def make_spawn_worker_tool():
    """构造 spawn_worker 的 ToolDef（协调器 / 工具注册挂载用）。"""
    from skill_registry import ToolDef
    return ToolDef(
        name="spawn_worker",
        description="把子任务派发给指定 specialist（persona）worker 执行，返回其调查/分析结论",
        func=spawn_worker,
        category="agent",
        params={
            "subagent_type": {"type": "string", "required": True, "desc": "specialist 名（persona）"},
            "description": {"type": "string", "required": True, "desc": "子任务说明"},
            "prompt": {"type": "string", "required": True, "desc": "要执行的任务指令"},
            "background": {"type": "bool", "required": False, "default": False,
                           "desc": "是否后台执行并走 SSE 通知"},
        },
        cls="safe",
        when_to_use="协调器需要把子任务派给 specialist worker 时",
    )


def register_spawn_worker_tool(registry=None):
    """把 spawn_worker 注册进 ToolRegistry（供 main.py 挂载用；不落 tools.py）。"""
    registry = registry or ToolRegistry
    if not registry.get("spawn_worker"):
        t = make_spawn_worker_tool()
        registry.register(name=t.name, description=t.description, category=t.category,
                          requires_approval=t.requires_approval, params=t.params,
                          cls_=t.cls, scope=t.scope, when_to_use=t.when_to_use,
                          origin=t.origin)(t.func)
    return registry.get("spawn_worker")
