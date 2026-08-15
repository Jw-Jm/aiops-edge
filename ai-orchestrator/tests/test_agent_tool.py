"""spawn_worker 工具 + persona worker 运行时单元测试（B3）"""
from agent_tool import set_personas, spawn_worker, worker_tools, task_notification
from persona_registry import Persona, load_personas, PERSONAS_BUILTIN_DIR
from function_calling import WHITELIST_READONLY


def _ensure_tools():
    """注册测试用工具（幂等）。"""
    from skill_registry import ToolRegistry
    if not ToolRegistry.get("query_metrics"):
        ToolRegistry.register(name="query_metrics", description="指标查询", params={},
                              cls_="safe")(lambda service="x": "ok")
    if not ToolRegistry.get("probe_http"):
        ToolRegistry.register(name="probe_http", description="HTTP 探测", params={},
                              cls_="safe")(lambda url="": "ok")
    if not ToolRegistry.get("execute_shell"):
        ToolRegistry.register(name="execute_shell", description="执行命令", params={},
                              requires_approval=True, cls_="dangerous")(
            lambda command="": "shall not run")


def test_spawn_worker_sync_returns_persona_answer(monkeypatch):
    """spawn 一个 persona → 返回其 system_prompt 拼接任务文本的结果。"""
    personas = load_personas(PERSONAS_BUILTIN_DIR)
    set_personas(personas)

    def fake_make(system_prompt):
        def decision(messages, tools):
            instr = messages[0]["content"]
            return {"type": "final", "content": f"[worker] 受理: {instr}"}
        return decision

    monkeypatch.setattr("agent_tool._make_decision_fn", fake_make)
    out = spawn_worker("specialist-sre", "排查服务异常", "pod 一直 CrashLoopBackOff")
    assert out.startswith("[worker] 受理:")
    assert "pod 一直 CrashLoopBackOff" in out   # 任务文本透传
    assert "资深 SRE" in out                    # persona system_prompt 拼接


def test_spawn_worker_unknown_persona_returns_error():
    set_personas({})
    out = spawn_worker("no-such-specialist", "d", "p")
    assert "未知 specialist" in out
    assert "no-such-specialist" in out


def test_worker_tools_readonly_intersects_whitelist():
    """read-only persona 工具集 = persona.tools ∩ WHITELIST_READONLY。"""
    _ensure_tools()
    p = Persona(name="x", when_to_use="w",
                tools=["query_metrics", "execute_shell", "probe_http"],
                permission_mode="read-only", max_turns=5)
    tools = worker_tools(p)
    names = [t.name for t in tools]
    assert "query_metrics" in names
    assert "execute_shell" not in names   # 非只读被过滤
    assert "probe_http" not in names      # 不在只读白名单也被过滤
    assert names and all(n in WHITELIST_READONLY for n in names)


def test_worker_tools_readwrite_keeps_persona_tools():
    _ensure_tools()
    p = Persona(name="ops", when_to_use="w",
                tools=["query_metrics", "probe_http"],
                permission_mode="read-write", max_turns=5)
    names = [t.name for t in worker_tools(p)]
    assert set(names) == {"query_metrics", "probe_http"}


def test_worker_tools_excludes_disallowed():
    _ensure_tools()
    p = Persona(name="x", when_to_use="w",
                tools=["query_metrics", "probe_http"],
                disallowed_tools=["probe_http"],
                permission_mode="read-write", max_turns=5)
    names = [t.name for t in worker_tools(p)]
    assert names == ["query_metrics"]


def test_task_notification_frame_shape():
    ev = task_notification("specialist-sre", "completed", "调查完成")
    assert ev["type"] == "task_notification"
    assert ev["data"]["worker"] == "specialist-sre"
    assert ev["data"]["status"] == "completed"
    assert ev["data"]["summary"] == "调查完成"
