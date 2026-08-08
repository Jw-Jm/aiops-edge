"""AI Orchestrator v5 — FastAPI + LangGraph + arq + ChromaDB + Detector"""
import json
import os
import sys
import time
import uuid
import asyncio
from collections import defaultdict
from fastapi import FastAPI, Request, HTTPException
from fastapi.middleware.cors import CORSMiddleware
from fastapi.responses import StreamingResponse, JSONResponse, PlainTextResponse, Response
from sse_starlette.sse import EventSourceResponse

from shell_policy import ShellPolicy
from models import ChatRequest, ShellCheckRequest, MCPCallRequest, AlertRCARequest
from store import _task_store
import metrics  # noqa: F401 — 注册 Prometheus 指标
from skill_registry import SkillRegistry, ExpertRegistry
from skills import init_skills, init_experts
from orchestrator import describe_graph
from flow_api import router as flow_router

# 默认开启 LLM mock（本机部署联调用，不消耗真实模型）；生产设 LLM_MOCK=false 关闭
os.environ.setdefault("LLM_MOCK", os.getenv("LLM_MOCK", "true"))

app = FastAPI(title="AIOps Orchestrator", version="5.0")
app.add_middleware(CORSMiddleware, allow_origins=["*"], allow_methods=["*"], allow_headers=["*"])
shell_policy = ShellPolicy()
app.include_router(flow_router)

# 延迟导入：orchestrator 中的 ChromaDB 模型下载会阻塞启动
# 使用 startup event 在后台初始化
_brain = None

def _get_brain():
    global _brain
    if _brain is None:
        from orchestrator import brain
        _brain = brain
    return _brain

PROVIDER_BACKEND = {
    "openai": "openai", "deepseek": "deepseek",
    "kimi": "openai", "moonshot": "openai",
    "qwen": "openai", "dashscope": "openai",
    "xiaomi": "openai", "custom": "openai",
}


def _normalize_provider(provider: str) -> str:
    """把中文/别名 provider 映射到 backend。返回 (provider, backend)"""
    p = (provider or "openai").strip().lower()
    # 中文名映射
    alias = {"小米": "xiaomi", "小米mimo": "xiaomi", "通义千问": "qwen",
             "月之暗面": "kimi", "深度求索": "deepseek"}
    for k, v in alias.items():
        if k in p:
            return v, PROVIDER_BACKEND.get(v, "openai")
    return p, PROVIDER_BACKEND.get(p, "openai")


def _fetch_saved_llm_config() -> dict | None:
    """从 query-api 内部接口拉取已保存的启用 LLM 配置（含真实 API Key）作为回退。"""
    try:
        import urllib.request
        qa = os.environ.get("QUERY_API_URL", "http://query-api.observability.svc.cluster.local:8080/api/v1")
        token = os.environ.get("INTERNAL_TOKEN", "")
        req = urllib.request.Request(f"{qa}/settings/llm/internal", method="GET")
        req.add_header("X-Internal-Token", token)
        with urllib.request.urlopen(req, timeout=5) as resp:
            data = json.loads(resp.read().decode())
            cfg = data.get("data", data)
            api_key = cfg.get("api_key") or cfg.get("apiKey")
            if not api_key:
                return None
            provider = cfg.get("provider", "openai")
            norm, backend = _normalize_provider(provider)
            return {
                "api_key": api_key,
                "model": cfg.get("model", "gpt-4o"),
                "base_url": cfg.get("base_url", "https://api.openai.com/v1"),
                "provider": norm,
                "backend": backend,
            }
    except Exception:
        return None


def _parse_llm_config(request: Request):
    """提取 LLM 配置：优先请求头 (ProxyAI headers)，否则回退到数据库已保存配置。"""
    h = request.headers
    if h.get("X-LLM-API-Key"):
        provider = h.get("X-LLM-Provider", "openai")
        norm, backend = _normalize_provider(provider)
        _get_brain().set_llm_config({
            "api_key": h["X-LLM-API-Key"],
            "model": h.get("X-LLM-Model", "gpt-4o"),
            "base_url": h.get("X-LLM-Base-URL", "https://api.openai.com/v1"),
            "provider": norm,
            "backend": backend,
        })
    else:
        # 无请求头 → 回退到数据库已保存的启用配置
        saved = _fetch_saved_llm_config()
        _get_brain().set_llm_config(saved)


# ═══════════════════════════════════════════════════════════════
#  Rate Limiter (in-memory fallback, 100 req/min per IP)
# ═══════════════════════════════════════════════════════════════

_rate_limit_store: dict[str, list] = defaultdict(list)


@app.middleware("http")
async def rate_limit_middleware(request: Request, call_next):
    skip_paths = ("/health", "/api/v1/health", "/metrics", "/docs", "/openapi.json")
    if any(request.url.path.startswith(p) for p in skip_paths):
        return await call_next(request)

    client_ip = request.client.host if request.client else "unknown"
    now = time.time()
    window = [t for t in _rate_limit_store.get(client_ip, []) if now - t < 60]
    if len(window) >= 100:
        return JSONResponse({"error": "rate limit exceeded (100 req/min)"}, status_code=429)

    window.append(now)
    _rate_limit_store[client_ip] = window
    return await call_next(request)


# ═══════════════════════════════════════════════════════════════
#  Chat
# ═══════════════════════════════════════════════════════════════

@app.post("/api/v1/ai/chat")
async def ai_chat(req: ChatRequest, request: Request):
    _parse_llm_config(request)
    thread_id = req.session_id or uuid.uuid4().hex[:8]

    if req.stream:
        # 关键修复: SSE 生成器放在线程中执行，不占用 uvicorn 的 async worker pool
        # graph.stream() 是同步阻塞调用，在线程中运行不会阻塞事件循环
        import queue
        import threading
        import asyncio as _asyncio

        event_queue = queue.Queue()
        stop_event = threading.Event()

        def _run_stream():
            try:
                for event in _get_brain().stream_sync(req.intent, req.service or "", req.message, thread_id):
                    # 捕获操作建议 → 自动创建待审批任务到任务工作台
                    if event.get("type") == "suggestion":
                        _create_chat_suggestion_task(event, req, thread_id)
                    event_queue.put(event)
                event_queue.put(None)  # sentinel: done
            except Exception as e:
                event_queue.put({"type": "error", "text": str(e)[:200]})
                event_queue.put(None)
            finally:
                stop_event.set()

        thread = threading.Thread(target=_run_stream, daemon=True)
        thread.start()

        def _format_sse(ev: dict) -> str:
            """将内部事件 dict 序列化为标准 SSE 帧（event: + data:）。"""
            etype = ev.get("type", "message")
            data = json.dumps(ev, ensure_ascii=False)
            return f"event: {etype}\ndata: {data}\n\n"

        async def generate():
            while True:
                try:
                    event = await _asyncio.get_event_loop().run_in_executor(None, event_queue.get, True, 0.1)
                except queue.Empty:
                    if stop_event.is_set():
                        break
                    await _asyncio.sleep(0.1)
                    continue
                if event is None:
                    break
                # done/error 补结构化字段；其余透传
                if event.get("type") == "done":
                    yield _format_sse({
                        "type": "done",
                        "text": event.get("text", ""),
                        "assistant_message": {
                            "id": f"asst_{thread_id}",
                            "content": event.get("text", ""),
                            "created_at": time.strftime("%Y-%m-%dT%H:%M:%SZ"),
                        },
                    })
                elif event.get("type") == "approval_pending":
                    # 创建待审批任务并回填真实 task_id 供前端审批卡绑定
                    tid = _create_chat_suggestion_task(event, req, thread_id)
                    if tid:
                        event["task_id"] = tid
                    yield _format_sse(event)
                elif event.get("type") == "error":
                    yield _format_sse({"type": "error", "error": event.get("text", ""), "code": "dag_error"})
                else:
                    yield _format_sse(event)

        return StreamingResponse(generate(), media_type="text/event-stream",
                                 headers={"X-Session-Id": thread_id, "Cache-Control": "no-cache"})
    else:
        result = _get_brain().execute_sync(req.intent, req.service or "", req.message, thread_id)
        # 巡检/诊断报告落盘：持久化到 ClickHouse（历史趋势）并在 MinIO 留档
        try:
            if result and len(result.strip()) > 100:
                _upload_report(thread_id, result, service=req.service or "")
        except Exception as _e:
            print(f"[chat] 报告持久化失败: {_e}")
        return PlainTextResponse(result[:10000], media_type="text/markdown; charset=utf-8")


# ═══════════════════════════════════════════════════════════════
#  Sessions
# ═══════════════════════════════════════════════════════════════

# ═══════════════════════════════════════════════════════════════
#  AI Skills / Agents（只读 + 执行）
# ═══════════════════════════════════════════════════════════════

@app.get("/api/v1/ai/skills")
async def ai_skills():
    try:
        if not SkillRegistry.list_all():
            init_skills()
            init_experts()
    except Exception:
        pass
    return {"skills": [s.to_summary() for s in SkillRegistry.list_all()]}

@app.get("/api/v1/ai/skills/{key}")
async def ai_skill_detail(key: str):
    try:
        if not SkillRegistry.list_all():
            init_skills()
            init_experts()
    except Exception:
        pass
    skill = SkillRegistry.get(key)
    if not skill:
        raise HTTPException(404, "skill not found")
    return skill.to_summary()

@app.post("/api/v1/ai/skills/{key}/execute")
async def ai_skill_execute(key: str, body: dict = None):
    try:
        if not SkillRegistry.list_all():
            init_skills()
            init_experts()
    except Exception:
        pass
    params = (body or {}).get("params", {})
    try:
        return SkillRegistry.execute_skill(key, params)
    except KeyError:
        raise HTTPException(404, "skill not found")

@app.get("/api/v1/ai/agents")
async def ai_agents():
    try:
        if not ExpertRegistry.list_all():
            init_experts()
    except Exception:
        pass
    return {"agents": [
        {"name": e.name, "role": e.role, "goal": e.goal,
         "description": e.goal, "skills": e.skills, "tools": e.tools}
        for e in ExpertRegistry.list_all()
    ]}

@app.get("/api/v1/ai/agents/{name}")
async def ai_agent_detail(name: str):
    try:
        if not ExpertRegistry.list_all():
            init_experts()
    except Exception:
        pass
    e = ExpertRegistry.get(name)
    if not e:
        raise HTTPException(404, "agent not found")
    return {"name": e.name, "role": e.role, "goal": e.goal, "backstory": e.backstory,
            "skills": e.skills, "tools": e.tools}

@app.post("/api/v1/ai/agents")
async def ai_agent_create(body: dict = None):
    b = body or {}
    name = b.get("name", "")
    if not name:
        raise HTTPException(400, "name required")
    try:
        if not ExpertRegistry.list_all():
            init_experts()
    except Exception:
        pass
    ExpertRegistry.register(
        name=name, role=b.get("role", ""), goal=b.get("goal", ""), backstory=b.get("backstory", ""),
        intent_keywords=b.get("intent_keywords", []), skills=b.get("skills", []),
        tools=b.get("tools", []), system_prompt_template=b.get("system_prompt_template", ""),
    )
    ExpertRegistry.save_custom_store()
    # 同步到 MySQL AgentStore
    try:
        from db_agents import AgentStore
        AgentStore().upsert(name, b.get("role", ""), b.get("goal", ""), b.get("backstory", ""), True, False)
    except Exception:
        pass
    return ExpertRegistry.get(name).__dict__

@app.put("/api/v1/ai/agents/{name}")
async def ai_agent_update(name: str, body: dict = None):
    b = body or {}
    fields = {}
    for k in ("role", "goal", "backstory", "intent_keywords", "skills", "tools", "system_prompt_template"):
        if k in b:
            fields[k] = b[k]
    ok = ExpertRegistry.update(name, **fields)
    if not ok:
        raise HTTPException(404, "agent not found")
    # 同步到 MySQL AgentStore
    try:
        from db_agents import AgentStore
        e = ExpertRegistry.get(name)
        AgentStore().upsert(name, e.role, e.goal, e.backstory, True, False)
    except Exception:
        pass
    return ExpertRegistry.get(name).__dict__

@app.delete("/api/v1/ai/agents/{name}")
async def ai_agent_delete(name: str):
    if not ExpertRegistry.delete(name):
        raise HTTPException(400, "cannot delete built-in or not found")
    # 同步删除 MySQL AgentStore
    try:
        from db_agents import AgentStore
        AgentStore().delete(name)
    except Exception:
        pass
    return {"deleted": name}

# ═══════════════════════════════════════════════════════════════
#  Workflows（内置 DAG 只读 + 运行）
# ═══════════════════════════════════════════════════════════════

@app.get("/api/v1/ai/flows")
async def ai_flows():
    return {"flows": [describe_graph("full"), describe_graph("chat")]}

@app.get("/api/v1/ai/flows/{key}")
async def ai_flow_detail(key: str):
    mode = "chat" if key.endswith("chat_diagnosis") else "full"
    return describe_graph(mode)

@app.post("/api/v1/ai/flows/{key}/run-legacy")
async def ai_flow_run(key: str, body: dict = None):
    mode = "chat" if key.endswith("chat_diagnosis") else "full"
    service = (body or {}).get("service", "")
    message = (body or {}).get("message", "对服务进行完整诊断")
    intent = "chat" if mode == "chat" else "ops"
    brain = _get_brain()
    result = await asyncio.get_event_loop().run_in_executor(
        None, brain.execute_sync_full, intent, service, message, "workflow-run"
    )
    return {"run_id": f"run_{int(time.time()*1000)}", "result": result}

@app.get("/api/v1/ai/sessions")
async def list_sessions():
    try:
        rows = _get_brain()._conn.execute(
            "SELECT DISTINCT thread_id FROM checkpoints ORDER BY thread_id DESC LIMIT 50"
        ).fetchall()
        sessions, seen = [], set()
        for (tid,) in rows:
            if tid in seen: continue
            seen.add(tid)
            try:
                state = _get_brain().graph.get_state({"configurable": {"thread_id": tid}})
                vals = state.values if state else {}
                sessions.append({
                    "session_id": tid,
                    "preview": (vals.get("user_message", "") or vals.get("final_response", "") or tid)[:80],
                    "intent": vals.get("intent", ""),
                    "created_at": "",
                })
            except:
                sessions.append({"session_id": tid, "preview": tid, "intent": "", "created_at": ""})
        return {"sessions": sessions}
    except:
        return {"sessions": []}


@app.get("/api/v1/ai/session/{sid}")
async def get_session(sid: str):
    state = _get_brain().graph.get_state({"configurable": {"thread_id": sid}})
    if state and state.values:
        vals = state.values
        msgs = []
        if vals.get("user_message"):
            msgs.append({"role": "user", "content": vals["user_message"]})
        if vals.get("final_response"):
            msgs.append({"role": "assistant", "content": vals["final_response"]})
        return {
            "session_id": sid, "intent": vals.get("intent", ""),
            "service": vals.get("service", ""), "messages": msgs,
            "final_response": vals.get("final_response", ""),
        }
    raise HTTPException(404, "session not found")


@app.delete("/api/v1/ai/session/{sid}")
async def delete_session(sid: str):
    try:
        _get_brain()._conn.execute("DELETE FROM checkpoints WHERE thread_id = ?", (sid,))
        _get_brain()._conn.execute("DELETE FROM writes WHERE thread_id = ?", (sid,))
        _get_brain()._conn.commit()
        return {"message": "session deleted", "session_id": sid}
    except Exception as e:
        raise HTTPException(500, f"delete failed: {e}")


# ═══════════════════════════════════════════════════════════════
#  Shell + MCP
# ═══════════════════════════════════════════════════════════════

@app.post("/api/v1/ai/shell/check")
async def shell_check(req: ShellCheckRequest):
    reject = shell_policy.check(req.command)
    return {"allowed": reject is None, "reason": reject}


@app.get("/api/v1/mcp/tools")
async def mcp_tools():
    from mcp_server import mcp
    return {"tools": mcp.list_tools()}


@app.post("/api/v1/mcp/call")
async def mcp_call(req: MCPCallRequest):
    from mcp_server import mcp
    return {"result": mcp.call_tool(req.name, req.args)}


# ═══════════════════════════════════════════════════════════════
#  Ops Tasks
# ═══════════════════════════════════════════════════════════════

import hashlib
from models import TaskCreateRequest, WebhookPayload, CaseSearchRequest


def _task_id() -> str:
    return hashlib.md5(str(uuid.uuid4()).encode()).hexdigest()[:12]


def _create_chat_suggestion_task(event: dict, req, thread_id: str):
    """AI Chat 生成操作建议后，自动创建一条待审批任务到任务工作台。
    人工在任务工作台点"通过"后才真正执行 script。"""
    try:
        script = event.get("script", "")
        plan = event.get("plan", "")
        if not script and not plan:
            return None
        tid = _task_id()
        task = {
            "id": tid, "status": "waiting", "source": "ai_chat",
            "service": req.service or "unknown",
            "context": f"[AI Chat] {req.message[:80]}",
            "diagnosis": event.get("final_response", "")[:5000],
            "plan": plan,
            "script": script,
            "risk_score": int(event.get("risk_score", 0) or 0),
            "risk_reason": event.get("risk_reason", ""),
            "report": "",
            "chat_thread_id": thread_id,
            "created_at": time.strftime("%Y-%m-%dT%H:%M:%SZ"),
            "done_at": "",
        }
        _task_store[tid] = task
        print(f"[ai_chat] 已创建待审批任务 {tid} (含操作建议)")
        return tid
    except Exception as e:
        print(f"[ai_chat] 创建建议任务失败: {e}")
        return None


@app.post("/api/v1/ops/tasks")
async def create_task(req: TaskCreateRequest, request: Request):
    if not req.context:
        raise HTTPException(400, "context is required")
    tid = _task_id()
    task = {
        "id": tid, "status": "pending", "source": req.source,
        "service": req.service, "context": req.context,
        "diagnosis": "", "plan": "", "script": "", "risk_score": 0, "risk_reason": "",
        "report": "", "created_at": time.strftime("%Y-%m-%dT%H:%M:%SZ"),
        "done_at": "",
    }
    _task_store[tid] = task

    # Async: run DAG in background
    import threading
    _parse_llm_config(request)
    if not _get_brain().llm_config or not _get_brain().llm_config.get("api_key"):
        task["status"] = "failed"
        task["diagnosis"] = "LLM API Key 未配置, 请在设置页面配置"
        return {"task": task}

    task["status"] = "diagnosing"
    def _run():
        try:
            final_state = _get_brain().execute_sync_full("diagnosis", req.service, req.context, tid)
            _task_store[tid]["diagnosis"] = final_state.get("final_response", "")[:5000]
            _task_store[tid]["plan"] = final_state.get("plan", "")[:2000]
            _task_store[tid]["script"] = final_state.get("script", "")[:1000]
            _task_store[tid]["risk_score"] = final_state.get("risk_score", 0)
            _task_store[tid]["risk_reason"] = final_state.get("risk_reason", "")[:500]
            _task_store[tid]["report"] = final_state.get("report", "")[:2000]
            _task_store[tid]["done_at"] = time.strftime("%Y-%m-%dT%H:%M:%SZ")
            _task_store[tid]["status"] = "done"
            if final_state.get("final_response"):
                try: _upload_report(tid, final_state["final_response"], service=req.service or "")
                except Exception: pass
        except Exception as e:
            _task_store[tid]["status"] = "failed"
            _task_store[tid]["diagnosis"] = str(e)[:500]
    threading.Thread(target=_run, daemon=True).start()

    return {"task": task}


@app.post("/api/v1/ops/webhook")
async def ops_webhook(request: Request):
    """
    统一告警/异常事件 webhook 入口。
    兼容: vmalert (数组格式) / 自定义 WebhookPayload
    """
    tid = _task_id()
    _parse_llm_config(request)

    try:
        body = await request.json()
    except:
        body = {}

    # 兼容 vmalert 格式: [{"labels": {...}, "annotations": {...}}]
    if isinstance(body, list) and len(body) > 0:
        alert = body[0]
        labels = alert.get("labels", {})
        annotations = alert.get("annotations", {})
        source = annotations.get("ops_source", labels.get("alertname", "alert"))
        service = labels.get("service", labels.get("pod", ""))
        context = annotations.get("summary", f"{labels.get('alertname','')} 触发")
        severity = labels.get("severity", "warning")
    else:
        source = body.get("source", "webhook")
        service = body.get("service", "")
        context = body.get("summary", body.get("context", ""))
        severity = body.get("severity", "warning")

    task = {
        "id": tid, "status": "queued",
        "source": source,
        "service": service,
        "context": context,
        "diagnosis": "", "plan": "", "script": "", "risk_score": 0, "risk_reason": "",
        "report": "", "created_at": time.strftime("%Y-%m-%dT%H:%M:%SZ"),
        "done_at": "",
    }
    _task_store[tid] = task

    # 不再自动触发 LLM 诊断：只登记任务，等待人工在任务工作台手动触发
    # (避免每次告警都自动调用 LLM，造成大量开销；也避免 vmalert 15s 重复 webhook 反复诊断)
    return {"task_id": tid, "status": "queued", "message": "task queued, trigger diagnosis manually"}


@app.get("/api/v1/ops/tasks")
async def list_tasks(status: str = "", source: str = ""):
    result = list(_task_store.values())
    if status:
        result = [t for t in result if t["status"] == status]
    if source:
        result = [t for t in result if t["source"] == source]
    result.sort(key=lambda t: t["created_at"], reverse=True)
    return {"tasks": result}


@app.get("/api/v1/ops/tasks/{tid}")
async def get_task(tid: str):
    if tid not in _task_store:
        raise HTTPException(404, "task not found")
    return {"task": _task_store[tid]}


def _run_diagnosis(tid: str, svc: str, ctx: str):
    """在后台线程执行一次 LLM 诊断，并回填任务结果。"""
    import threading
    def _run():
        try:
            final_state = _get_brain().execute_sync_full("diagnosis", svc, ctx, tid)
            _task_store[tid]["status"] = "done"
            _task_store[tid]["diagnosis"] = final_state.get("final_response", "")[:5000]
            _task_store[tid]["plan"] = final_state.get("plan", "")[:2000]
            _task_store[tid]["script"] = final_state.get("script", "")[:1000]
            _task_store[tid]["risk_score"] = final_state.get("risk_score", 0)
            _task_store[tid]["risk_reason"] = final_state.get("risk_reason", "")[:500]
            _task_store[tid]["report"] = final_state.get("report", "")[:2000]
            _task_store[tid]["done_at"] = time.strftime("%Y-%m-%dT%H:%M:%SZ")
            if final_state.get("final_response"):
                try: _upload_report(tid, final_state["final_response"], service=svc)
                except Exception: pass
        except Exception as e:
            _task_store[tid]["status"] = "failed"
            _task_store[tid]["diagnosis"] = str(e)[:500]
    threading.Thread(target=_run, daemon=True).start()


@app.post("/api/v1/ops/tasks/{tid}/run")
async def run_task(tid: str):
    """手动触发任务诊断（LLM）— 前端任务工作台"手动诊断"按钮调用。
    告警 webhook 只登记 queued 任务，由人工在此手动触发，避免每次告警都自动调 LLM。"""
    if tid not in _task_store:
        raise HTTPException(404, "task not found")
    task = _task_store[tid]
    if task.get("status") in ("diagnosing", "running", "done", "approved"):
        return {"task_id": tid, "status": task.get("status"), "message": "task already processed"}
    if not _get_brain().llm_config or not _get_brain().llm_config.get("api_key"):
        task["status"] = "failed"
        task["diagnosis"] = "LLM API Key 未配置"
        return {"task_id": tid, "status": "failed"}
    task["status"] = "diagnosing"
    _run_diagnosis(tid, task.get("service", "") or "", task.get("context", "") or "")
    return {"task_id": tid, "status": "diagnosing"}


@app.post("/api/v1/ops/tasks/{tid}/approve")
async def approve_task(tid: str):
    if tid not in _task_store:
        raise HTTPException(404, "task not found")
    task = _task_store[tid]
    task["status"] = "approved"
    try:
        # AI Chat 建议任务: 通过后执行 script (若有)
        if task.get("source") == "ai_chat":
            exec_result = _get_brain().execute_suggestion(
                task.get("service", ""), task.get("script", ""), task.get("context", ""))
            task["status"] = "done"
            task["report"] = f"已人工确认并执行建议。\n操作结果:\n{exec_result}"
            task["done_at"] = time.strftime("%Y-%m-%dT%H:%M:%SZ")
            try:
                _upload_report(tid, task["report"], service=task.get("service", ""))
            except Exception: pass
        else:
            # LangGraph 任务: Resume with approval
            final = _get_brain().approve_and_resume(tid, approved=True)
            task["status"] = "done"
            task["report"] = final.get("final_response", "")[:500]
            task["done_at"] = time.strftime("%Y-%m-%dT%H:%M:%SZ")
            if final.get("final_response"):
                try:
                    _upload_report(tid, final["final_response"], service=task.get("service", ""))
                except Exception: pass
    except Exception as e:
        task["status"] = "failed"
        task["diagnosis"] = str(e)
    _task_store.persist(tid)  # 审批结果落 MySQL
    return {"task": task}


@app.post("/api/v1/ops/tasks/{tid}/reject")
async def reject_task(tid: str):
    if tid not in _task_store:
        raise HTTPException(404, "task not found")
    task = _task_store[tid]
    task["status"] = "rejected"
    task["done_at"] = time.strftime("%Y-%m-%dT%H:%M:%SZ")
    try:
        # 仅 LangGraph 任务需要 resume 拒绝
        if task.get("source") != "ai_chat":
            _get_brain().approve_and_resume(tid, approved=False)
    except: pass
    _task_store.persist(tid)  # 拒绝结果落 MySQL
    return {"task": task}


# ═══════════════════════════════════════════════════════════════
#  Case Library (ChromaDB RAG)
# ═══════════════════════════════════════════════════════════════

@app.get("/api/v1/ops/cases")
async def list_cases():
    from rag import rag
    try:
        count = rag.count()
        results = rag.search("", limit=20) if count > 0 else []
        return {"cases": results, "total": count}
    except:
        return {"cases": [], "total": 0}


@app.get("/api/v1/ops/cases/search")
async def search_cases(q: str = "", limit: int = 5):
    from rag import rag
    try:
        results = rag.search(q, limit=limit)
        return {"cases": results, "query": q}
    except:
        return {"cases": [], "query": q}


@app.get("/api/v1/ops/cases/list")
async def list_all_cases():
    """列出所有案例（管理页面）"""
    from rag import rag
    cases = rag.list_all()
    return {"cases": cases, "total": len(cases)}


@app.post("/api/v1/ops/cases/{case_id}/feedback")
async def case_feedback(case_id: str, outcome: str = "success"):
    """人工反馈案例有效性: outcome = success | failed"""
    from rag import rag
    rag.validate_case(case_id, outcome)
    return {"message": "feedback recorded", "case_id": case_id, "outcome": outcome}


@app.post("/api/v1/ops/cases/decay")
async def trigger_decay():
    """触发案例衰减 (或由 CronJob 定时调用)"""
    from rag import rag
    rag.decay_scores()
    return {"message": "decay triggered"}


# ═══════════════════════════════════════════════════════════════
#  RCA Engine
# ═══════════════════════════════════════════════════════════════

@app.post("/api/v1/ops/rca")
async def rca_analysis(req: TaskCreateRequest):
    """独立 RCA 分析 — 不依赖 LLM (确定性模式)"""
    from rca import diagnose_root_cause
    if not req.service:
        raise HTTPException(400, "service is required")
    return diagnose_root_cause(req.service)


@app.post("/api/v1/ops/rca/deep")
async def rca_deep_analysis(req: TaskCreateRequest, request: Request):
    """深度 RCA — 包含假设引擎 (需要 LLM)"""
    _parse_llm_config(request)
    from rca import full_rca_analysis
    if not req.service:
        raise HTTPException(400, "service is required")
    return full_rca_analysis(req.service)


@app.post("/api/v1/ops/rca/alert")
async def rca_alert_analysis(req: AlertRCARequest, request: Request):
    """告警事件 → RCA 假设引擎联动。

    前端告警事件页"根因分析"按钮调用。将告警上下文（rule_name/message/count/severity）
    注入假设引擎，对 K8s 集群告警直接走 cluster_check 证伪循环，返回根因报告。
    """
    _parse_llm_config(request)
    from rca import full_rca_analysis

    if not req.service and not req.rule_id:
        raise HTTPException(400, "service or rule_id is required")

    # 组装告警上下文，注入 RCA 假设引擎
    anomaly_event = {
        "service": req.service,
        "rule_id": req.rule_id,
        "rule_name": req.rule_name,
        "severity": req.severity,
        "message": req.message,
        "count": req.count,
        "first_timestamp": req.first_timestamp,
        "last_timestamp": req.last_timestamp,
    }
    result = full_rca_analysis(req.service or "kubernetes", anomaly_event=anomaly_event)
    result["alert"] = {
        "rule_id": req.rule_id,
        "rule_name": req.rule_name,
        "severity": req.severity,
        "message": req.message,
        "count": req.count,
        "last_timestamp": req.last_timestamp,
    }
    return result


# ── RCA 报告导出（Markdown）──────────────────────────────────

def _rca_result_to_markdown(result: dict) -> str:
    """将 RCA 结构化结果格式化为 Markdown 报告。"""
    mode = result.get("mode", "deterministic")
    alert = result.get("alert", {})
    det = result.get("result", {})
    hyp = det.get("hypothesis_result")

    md = ["# AI 根因分析报告\n"]
    # 告警信息
    if alert.get("rule_name"):
        md.append("## 告警信息")
        md.append(f"- **告警规则**: {alert.get('rule_name', '-')}")
        md.append(f"- **严重级别**: {alert.get('severity', '-')}")
        md.append(f"- **触发次数**: {alert.get('count', 0)}")
        md.append(f"- **最近触发**: {alert.get('last_timestamp', '-')}")
        if alert.get("message"):
            md.append(f"- **告警消息**: {alert.get('message', '')}")
        md.append("")

    # 模式
    md.append(f"## 分析模式: {'假设引擎（深度分析）' if mode == 'hypothesis_engine' else '确定性分析'}\n")

    # 根因结论
    md.append("## 根因结论")
    md.append(f"- **根因服务**: {det.get('root_cause_service', '未知')}")
    if det.get("causality_direction"):
        md.append(f"- **因果方向**: {det.get('causality_direction')}")
    md.append(f"- **置信度**: {det.get('confidence', 0):.2f}")
    if det.get("recommendation"):
        md.append(f"- **建议**: {det.get('recommendation')}")
    if det.get("message"):
        md.append(f"- **说明**: {det.get('message')}")
    md.append("")

    # 证据链
    evidence = det.get("evidence_chain") or []
    if evidence:
        md.append("## 证据链")
        for ev in evidence:
            md.append(f"- **{ev.get('layer', '?')}**: {ev.get('finding', '')}")
        md.append("")

    # 假设引擎详情
    if hyp:
        md.append("## 假设引擎证伪结论")
        best = hyp.get("best_hypothesis")
        if best:
            md.append(f"- **最佳假设**: {best.get('hypothesis', '')}")
            md.append(f"- **置信度**: {best.get('confidence', 0):.2f}")
            md.append(f"- **集群检查**: `{best.get('proposed_check', '')}`")
        else:
            md.append("- 未确认任何假设（所有假设被证伪或不确定）")
        md.append("")

        evidence_log = hyp.get("evidence_log") or []
        if evidence_log:
            md.append("### 检查证据")
            md.append("| 假设 | 集群检查 | 判定 | 置信度 |")
            md.append("|------|---------|------|--------|")
            for ev in evidence_log:
                v = ev.get("verdict", "")
                verdict_label = {"confirm": "支持", "falsify": "证伪", "inconclusive": "不确定"}.get(v, v)
                md.append(f"| {ev.get('hypothesis', '')} | `{ev.get('check', '')}` | {verdict_label} | {ev.get('confidence', 0):.2f} |")
            md.append("")

    md.append("---")
    md.append(f"*报告生成时间: {time.strftime('%Y-%m-%d %H:%M:%S')}*")
    return "\n".join(md)


@app.post("/api/v1/ops/rca/alert/export")
async def rca_alert_export(req: AlertRCARequest, request: Request, format: str = "markdown"):
    """告警 RCA 报告导出为 Markdown / PDF（PDF 经浏览器打印）。

    format=markdown 返回 .md 下载；format=pdf 返回可供打印的 HTML。
    """
    _parse_llm_config(request)
    from rca import full_rca_analysis

    anomaly_event = {
        "service": req.service,
        "rule_id": req.rule_id,
        "rule_name": req.rule_name,
        "severity": req.severity,
        "message": req.message,
        "count": req.count,
        "first_timestamp": req.first_timestamp,
        "last_timestamp": req.last_timestamp,
    }
    result = full_rca_analysis(req.service or "kubernetes", anomaly_event=anomaly_event)
    result["alert"] = {
        "rule_id": req.rule_id,
        "rule_name": req.rule_name,
        "severity": req.severity,
        "message": req.message,
        "count": req.count,
        "last_timestamp": req.last_timestamp,
    }

    md = _rca_result_to_markdown(result)
    fname = f"rca-report-{req.rule_id or req.service or 'unknown'}"

    if format == "pdf":
        # 生成简单 HTML 供浏览器打印为 PDF
        md_html = md.replace(chr(10), "<br/>").replace("## ", "<h2>").replace("# ", "<h1>")
        html = (
            "<!DOCTYPE html><html lang='zh'><head><meta charset='utf-8'>"
            f"<title>{fname}</title><style>"
            "body{font-family:-apple-system,'PingFang SC',sans-serif;margin:40px;line-height:1.7;color:#222}"
            "h1{border-bottom:2px solid #722ed1;padding-bottom:8px}"
            "h2{margin-top:24px;color:#722ed1}"
            "table{border-collapse:collapse;width:100%;margin:12px 0}"
            "th,td{border:1px solid #ddd;padding:6px 10px;text-align:left;font-size:14px}"
            "th{background:#f5f0ff}"
            "code{background:#f5f5f5;padding:2px 4px;border-radius:3px}"
            "</style></head><body>"
            f"{md_html}</body></html>"
        )
        from fastapi.responses import HTMLResponse
        return HTMLResponse(html)

    # markdown
    import urllib.parse
    filename = f"{fname}.md"
    encoded = urllib.parse.quote(filename)
    from fastapi.responses import Response
    return Response(content=md.encode("utf-8"), media_type="text/markdown; charset=utf-8",
                    headers={"Content-Disposition": f"attachment; filename*=UTF-8''{encoded}"})


# ═══════════════════════════════════════════════════════════════
#  WebSocket (任务状态实时推送)
# ═══════════════════════════════════════════════════════════════

from fastapi import WebSocket, WebSocketDisconnect

_ws_clients: set = set()


@app.websocket("/api/v1/ops/ws")
async def ops_websocket(ws: WebSocket):
    await ws.accept()
    _ws_clients.add(ws)
    try:
        while True:
            await ws.receive_text()  # keep-alive
    except WebSocketDisconnect:
        pass
    finally:
        _ws_clients.discard(ws)


# ═══════════════════════════════════════════════════════════════
#  Anomaly Detection
# ═══════════════════════════════════════════════════════════════

@app.post("/api/v1/ops/anomalies/scan")
async def scan_anomalies(request: Request):
    """手动触发全量异常扫描 (3算法投票)"""
    from detector import detector
    from tools import get_service_list
    import json as _json

    anomalies = []
    raw = get_service_list()
    try:
        svc_data = _json.loads(raw)
    except Exception:
        svc_data = []
    for svc in (svc_data if isinstance(svc_data, list) else []):
        name = svc.get("service_name", "")
        if not name:
            continue
        # 对关键指标做检测
        metrics_vals = {
            "error_rate": float(svc.get("max_ms", 0)) / 100.0 if float(svc.get("avg_ms", 0)) > 0 else 0,
            "p99_latency": float(svc.get("max_ms", 0)),
            "request_rate": float(svc.get("traces", 0)),
        }
        for metric, val in metrics_vals.items():
            # feed 历史数据 (从 ClickHouse 历史查询或使用当前值填充)
            detector.feed(name, metric, val)
            results = detector.detect(name, metric, val)
            if results:
                confirmed = detector.vote(results)
                if confirmed:
                    anomalies.append({
                        "service": confirmed.service, "metric": confirmed.metric,
                        "value": confirmed.current_value,
                        "method": confirmed.method, "severity": confirmed.severity,
                        "score": round(confirmed.score, 3),
                    })

    # 异常扫描只登记任务，不再自动触发 LLM 诊断
    # (由人工在任务工作台点击"手动诊断"按钮触发，避免每次扫描都自动调用 LLM)
    queued = 0
    for a in anomalies[:3]:  # 最多登记 3 个
        svc = a["service"]
        ctx = f"{svc} {a['metric']} 异常 (score={a['score']:.2f})"
        tid = _task_id()
        _task_store[tid] = {"id": tid, "status": "queued", "source": "anomaly_scan",
                            "service": svc, "context": ctx,
                            "diagnosis": "", "plan": "", "script": "", "risk_score": 0,
                            "report": "", "created_at": time.strftime("%Y-%m-%dT%H:%M:%SZ"), "done_at": ""}
        queued += 1

    return {"anomalies": anomalies, "count": len(anomalies),
            "auto_diagnose_triggered": 0, "diagnose_queued": queued}


@app.get("/api/v1/ops/anomalies")
async def list_anomalies(service: str = "", metric: str = ""):
    """查询历史异常事件 (从 detector 当前窗口)"""
    from detector import detector
    result = []
    for key, values in detector.history.items():
        svc_mt = key.split(":", 1)
        if len(svc_mt) < 2:
            continue
        s, m = svc_mt
        if service and s != service:
            continue
        if metric and m != metric:
            continue
        vals = list(values)
        if len(vals) >= 2:
            result.append({"service": s, "metric": m, "latest": vals[-1],
                           "window_size": len(vals), "trend": "up" if vals[-1] > vals[-2] else "down"})
    return {"anomaly_trends": result[:50]}


# ═══════════════════════════════════════════════════════════════
#  MinIO Object Storage
# ═══════════════════════════════════════════════════════════════

from minio import Minio
from minio.error import S3Error

_minio = Minio(
    os.environ.get("MINIO_ENDPOINT", "minio.observability.svc.cluster.local:9000"),
    access_key=os.environ.get("MINIO_ACCESS_KEY", "minioadmin"),
    secret_key=os.environ.get("MINIO_SECRET_KEY", "minioadmin123"),
    secure=os.environ.get("MINIO_SECURE", "false").lower() == "true",
)
BUCKET = "ops-reports"
if not _minio.bucket_exists(BUCKET):
    _minio.make_bucket(BUCKET)


def _upload_report(task_id: str, content: str, filename: str = "report.md", service: str = ""):
    """Upload task report to MinIO, return object name. Also persist to ClickHouse."""
    import io
    data = content.encode("utf-8")
    obj_name = f"{task_id}/{filename}"
    _minio.put_object(BUCKET, obj_name, io.BytesIO(data), len(data), content_type="text/markdown")
    # 同步持久化到 ClickHouse（巡检报告历史）
    try:
        _persist_inspection_report(task_id, service or "", content, filename)
    except Exception as e:
        print(f"[reports] ClickHouse 持久化失败: {e}")
    return obj_name


# ═══════════════════════════════════════════════════════════════
#  ClickHouse 巡检报告持久化
# ═══════════════════════════════════════════════════════════════

_CH_HOST = os.environ.get("CLICKHOUSE_HOST", "clickhouse-0.clickhouse.observability.svc.cluster.local")
_CH_PORT = os.environ.get("CLICKHOUSE_PORT", "8123")

_REPORT_DDL = """
CREATE TABLE IF NOT EXISTS observability.inspection_reports (
    task_id String,
    service_name LowCardinality(String),
    report_type LowCardinality(String),
    verdict LowCardinality(String),
    risk_score Float32,
    summary String,
    content String,
    created_at DateTime
) ENGINE = MergeTree
PARTITION BY toYYYYMMDD(created_at)
ORDER BY (service_name, created_at)
TTL created_at + INTERVAL 90 DAY DELETE
"""


def _ch_query_json(sql: str) -> list:
    """执行 SELECT 并以 JSONEachRow 返回 dict 列表（供 NL→SQL 结果展示）。"""
    import urllib.parse
    import urllib.request
    import json as _json
    url = (f"http://{_CH_HOST}:{_CH_PORT}/?query="
           + urllib.parse.quote(sql) + "&default_format=JSONEachRow")
    with urllib.request.urlopen(urllib.request.Request(url), timeout=20) as resp:
        raw = resp.read().decode("utf-8", errors="replace")
    rows = []
    for line in raw.splitlines():
        if line.strip():
            try:
                rows.append(_json.loads(line))
            except Exception:
                pass
    return rows


def _ch_query(sql: str, data: bytes = None) -> str:
    """通过 ClickHouse HTTP 8123 执行 SQL。

    - SELECT / DDL: GET ?query=...&default_format=TabSeparated
    - INSERT: POST ?query=...&default_format=TabSeparated，body 为 TabSeparated 数据
    """
    import urllib.parse
    import urllib.request
    base = f"http://{_CH_HOST}:{_CH_PORT}/"
    query = urllib.parse.quote(sql) + "&default_format=TabSeparated"
    url = base + "?query=" + query
    if data is not None:
        req = urllib.request.Request(url, data=data,
                                     headers={"Content-Type": "text/tab-separated-values"})
    else:
        req = urllib.request.Request(url)
    with urllib.request.urlopen(req, timeout=15) as resp:
        return resp.read().decode("utf-8", errors="replace")


def _ensure_report_table():
    """幂等建表。"""
    try:
        # 用 POST 执行 DDL（GET 方式在某些场景对 CREATE 返回 500）
        import urllib.parse
        import urllib.request
        url = (f"http://{_CH_HOST}:{_CH_PORT}/?query="
               + urllib.parse.quote(_REPORT_DDL) + "&default_format=TabSeparated")
        req = urllib.request.Request(url, data=b"")
        with urllib.request.urlopen(req, timeout=15) as resp:
            resp.read()
    except Exception as e:
        print(f"[reports] 建表失败: {e}")


def _extract_report_fields(content: str, service: str, filename: str) -> dict:
    """从自由文本报告中启发式抽取结构化字段（verdict / risk_score / summary / report_type）。"""
    low = content.lower()
    # 健康判定
    verdict = "unknown"
    if any(k in low for k in ["异常", "高危", "严重", "中高风险", "高风险", "critical", "异常告警"]):
        verdict = "异常"
    elif any(k in low for k in ["关注", "中风险", "注意", "warning", "潜在风险"]):
        verdict = "关注"
    elif any(k in low for k in ["健康", "正常", "良好", "无异常", "ok"]):
        verdict = "健康"

    # 风险分 0~1
    risk = 0.3
    if verdict == "异常":
        risk = 0.8
    elif verdict == "关注":
        risk = 0.5
    elif verdict == "健康":
        risk = 0.1
    # 提取显式风险词增强
    if any(k in low for k in ["中高风险", "高风险", "严重", "critical"]):
        risk = max(risk, 0.85)
    elif any(k in low for k in ["中风险"]):
        risk = max(risk, 0.6)

    # 报告类型
    rtype = "inspection" if ("巡检" in content or "inspection" in low or "检查" in content) else "report"

    # 摘要：截取报告开头若干行作为 summary
    summary = ""
    for line in content.splitlines():
        line = line.strip()
        if not line:
            continue
        # 跳过标题/分隔
        if line.startswith("#") or line.startswith("---") or line.startswith("|") or line.startswith("**时间**"):
            continue
        if len(line) > 20:
            summary = line[:200]
            break
    return {"verdict": verdict, "risk_score": risk, "report_type": rtype, "summary": summary}


def _persist_inspection_report(task_id: str, service: str, content: str, filename: str = "report.md"):
    """将报告写入 MySQL reports 表（ReportStore）。"""
    from db_agents import ReportStore
    fields = _extract_report_fields(content, service, filename)
    ReportStore().save({
        "task_id": task_id, "service_name": service or "-",
        "report_type": fields["report_type"], "verdict": fields["verdict"],
        "risk_score": fields["risk_score"], "summary": fields["summary"],
        "content": content,
    })
    return task_id


# ── 巡检报告查询接口 ────────────────────────────────────────────

@app.get("/api/v1/ops/reports/history")
async def list_inspection_reports(service: str = "", limit: int = 50, offset: int = 0, report_type: str = ""):
    """巡检报告历史列表（按时间倒序）。数据来自 MySQL reports 表。"""
    from db_agents import ReportStore
    try:
        page = (offset // limit) + 1 if limit else 1
        result = ReportStore().list(service=service or None, page=page, size=limit)
    except Exception as e:
        return {"reports": [], "error": str(e)}
    reports = []
    for r in result["items"]:
        reports.append({
            "task_id": r.get("task_id", ""), "service_name": r.get("service_name", ""),
            "report_type": r.get("report_type", ""), "verdict": r.get("verdict", ""),
            "risk_score": float(r.get("risk_score") or 0), "summary": r.get("summary", ""),
            "created_at": r.get("created_at", ""),
        })
    return {"reports": reports, "count": len(reports)}


@app.get("/api/v1/ops/reports/trend")
async def inspection_report_trend(days: int = 14, report_type: str = "inspection"):
    """巡检报告历史趋势：按天统计报告数与平均风险分。数据来自 MySQL reports 表。"""
    from db_agents import ReportStore
    try:
        result = ReportStore().list(page=1, size=10000)
    except Exception as e:
        return {"trend": [], "error": str(e)}
    import datetime
    cutoff = datetime.datetime.utcnow() - datetime.timedelta(days=int(days))
    daily: dict[str, dict] = {}
    for r in result["items"]:
        if r.get("report_type", "") != report_type:
            continue
        created = r.get("created_at")
        if not created:
            continue
        try:
            if isinstance(created, datetime.datetime):
                dt = created
            elif isinstance(created, str):
                dt = datetime.datetime.strptime(str(created)[:19], "%Y-%m-%d %H:%M:%S")
            else:
                continue
        except Exception:
            continue
        if dt < cutoff:
            continue
        day = dt.strftime("%Y-%m-%d")
        d = daily.setdefault(day, {"count": 0, "sum_risk": 0.0})
        d["count"] += 1
        d["sum_risk"] += float(r.get("risk_score") or 0)
    trend = [{"date": d, "count": v["count"], "avg_risk": round(v["sum_risk"] / v["count"], 2) if v["count"] else 0}
             for d, v in sorted(daily.items())]
    return {"trend": trend}


@app.get("/api/v1/ops/reports/{task_id}/download")
async def download_report(task_id: str):
    """Download task report from MinIO."""
    from fastapi.responses import StreamingResponse
    try:
        obj = _minio.get_object(BUCKET, f"{task_id}/report.md")
        return StreamingResponse(obj.stream(), media_type="text/markdown",
                                 headers={"Content-Disposition": f"attachment; filename={task_id}-report.md"})
    except S3Error:
        raise HTTPException(404, "report not found")


@app.get("/api/v1/ops/reports")
async def list_reports():
    """List all stored reports."""
    try:
        objects = list(_minio.list_objects(BUCKET, recursive=True))
        return {"reports": [{"name": o.object_name, "size": o.size, "last_modified": str(o.last_modified)} for o in objects]}
    except Exception as e:
        return {"reports": [], "error": str(e)}


# ═══════════════════════════════════════════════════════════════
#  审计日志 / 知识库 / 规则（MySQL 持久化）
# ═══════════════════════════════════════════════════════════════

@app.get("/api/v1/ops/audit-logs")
async def list_audit_logs(action: str = "", operator: str = "", service: str = "",
                          page: int = 1, size: int = 50):
    from db_audit import AuditStore
    return AuditStore().query(page=page, size=size,
                              action=action or None, operator=operator or None, service=service or None)


@app.get("/api/v1/ai/knowledge")
async def list_knowledge(q: str = "", page: int = 1, size: int = 50):
    from db_agents import KnowledgeStore
    ks = KnowledgeStore()
    if q:
        return ks.search(q)
    return ks.list(page=page, size=size)


@app.post("/api/v1/ai/knowledge")
async def add_knowledge(body: dict = None):
    from db_agents import KnowledgeStore
    b = body or {}
    kid = KnowledgeStore().add(b.get("title", ""), b.get("content", ""),
                               b.get("source", "manual"), b.get("tags", ""), b.get("code_ref"))
    return {"ok": True, "id": kid}


@app.delete("/api/v1/ai/knowledge/{kid}")
async def delete_knowledge(kid: int):
    from db_agents import KnowledgeStore
    KnowledgeStore().delete(kid)
    return {"ok": True}


@app.get("/api/v1/ai/rules")
async def list_rules():
    from db_agents import RuleStore
    return {"rules": RuleStore().list()}


@app.post("/api/v1/ai/rules")
async def save_rule(body: dict = None):
    from db_agents import RuleStore
    b = body or {}
    RuleStore().save(b.get("rule_key"), b.get("name", ""), b.get("kind", "metric"),
                     b.get("severity", "warning"), b.get("enabled", True),
                     b.get("scope_type", "global"), b.get("join_mode", "all"),
                     b.get("conditions_json", {}), b.get("source_type", "custom"))
    return {"ok": True}


@app.delete("/api/v1/ai/rules/{rule_key}")
async def delete_rule(rule_key: str):
    from db_agents import RuleStore
    RuleStore().delete(rule_key)
    return {"ok": True}


@app.post("/api/v1/ai/rules/{rule_key}/toggle")
async def toggle_rule(rule_key: str):
    from db_agents import RuleStore
    RuleStore().toggle(rule_key)
    return {"ok": True}


# ═══════════════════════════════════════════════════════════════
#  NL→ClickHouse SQL（生成-确认-执行 + 安全护栏）
# ═══════════════════════════════════════════════════════════════

from nl2sql import validate_sql, normalize_sql, extract_sql_from_markdown, Nl2SqlStore, new_item
from shell_ws import shell_ws
from db_snmp import SNMPDeviceStore
from snmp_collector import SNMPCollector
import hardware_tools  # noqa: F401  注册 SNMP/IPMI/部件查询工具

_nl2sql_store = Nl2SqlStore()
_NL2SQL_SYSTEM = (
    "你是 ClickHouse SQL 专家。根据用户的中文查询意图，生成一条查询 AIOps 可观测性数据的 SQL。"
    "只能 SELECT，禁止 INSERT/UPDATE/DELETE/DROP/ALTER/CREATE。"
    "可用表：observability.trace_spans(span_id,parent_span_id,trace_id,service_name,start_time,"
    "duration_ns,is_error,response_status,peer_service), "
    "observability.service_topology(source_service,destination_service,calls,error_rate,p95_latency_ns,window), "
    "observability.log_records(service_name,log_time,level,message,digest), "
    "observability.inspection_reports(task_id,service_name,report_type,verdict,risk_score,summary,created_at). "
    "只返回 SQL 本体，不要任何解释、注释或 markdown 代码块。"
)


@app.post("/api/v1/ai/nl2sql/translate")
async def nl2sql_translate(body: dict = None):
    b = body or {}
    question = (b.get("question") or "").strip()
    if not question:
        raise HTTPException(400, "question is required")
    try:
        cfg = _get_brain().llm_config
        sql_raw = _llm(cfg, _NL2SQL_SYSTEM, question, role="ClickHouse SQL 专家")
    except Exception:
        sql_raw = ""
    sql_raw = extract_sql_from_markdown(sql_raw or "")
    if not validate_sql(sql_raw):
        return {"error": "生成的 SQL 未通过安全校验，请重试或简化查询",
                "sql": sql_raw, "id": None, "pending": False}
    sql = normalize_sql(sql_raw)
    item = new_item(sql, question)
    sid = _nl2sql_store.save(item)
    return {"id": sid, "sql": sql, "explanation": question, "pending": True}


@app.post("/api/v1/ai/nl2sql/{sid}/execute")
async def nl2sql_execute(sid: str):
    item = _nl2sql_store.get(sid)
    if not item:
        raise HTTPException(404, "not found")
    if item.get("status") == "executed":
        raise HTTPException(409, "already executed")
    try:
        rows = _ch_query_json(item["sql"])
    except Exception as e:
        return {"error": str(e), "columns": [], "rows": [], "count": 0}
    columns = list(rows[0].keys()) if rows else []
    try:
        _audit_log(item.get("id", ""), "nl2sql", "user", "", item["sql"],
                   "ok" if rows is not None else "fail", {"rows": len(rows)})
    except Exception:
        pass
    _nl2sql_store.mark_executed(sid)
    return {"columns": columns, "rows": rows, "count": len(rows)}


@app.get("/api/v1/ai/nl2sql/{sid}")
async def nl2sql_get(sid: str):
    item = _nl2sql_store.get(sid)
    if not item:
        raise HTTPException(404, "not found")
    return item


@app.get("/api/v1/ops/export/chat/{sid}")
async def export_chat(sid: str):
    """Export AI Chat session as Markdown file."""
    state = _get_brain().graph.get_state({"configurable": {"thread_id": sid}})
    if not state or not state.values:
        raise HTTPException(404, "session not found")
    vals = state.values
    md = f"# AI Chat Session: {sid}\n\n"
    md += f"**Intent**: {vals.get('intent','?')} | **Service**: {vals.get('service','?')}\n\n"
    md += f"## Question\n{vals.get('user_message','')}\n\n"
    md += f"## Analysis\n{vals.get('final_response','')[:5000]}\n"
    _upload_report(sid, md, "chat-export.md")
    return {"message": "exported", "session_id": sid, "download": f"/api/v1/ops/reports/{sid}/download"}


# ═══════════════════════════════════════════════════════════════
#  Health
# ═══════════════════════════════════════════════════════════════

@app.get("/health")
@app.get("/api/v1/health")
async def health():
    return {"status": "ok", "version": "5.0", "langgraph": True, "sse": True,
            "fastapi": True, "detector": True, "webhook": True}


@app.get("/metrics")
async def metrics():
    """Prometheus 格式指标导出"""
    try:
        from prometheus_client import generate_latest, REGISTRY
        from metrics import update_task_metrics, update_rag_metrics
        update_rag_metrics()
        update_task_metrics(_task_store)
        return Response(content=generate_latest(REGISTRY), media_type="text/plain; version=0.0.4")
    except ImportError:
        return Response(content="# aio_health 1\n", media_type="text/plain")


# ═══════════════════════════════════════════════════════════════
#  Entry point (replaces server.py)
# ═══════════════════════════════════════════════════════════════

# ═══════════════════════════════════════════════════════════════
#  SNMP 设备与采集
# ═══════════════════════════════════════════════════════════════

_snmp_collector = SNMPCollector()


@app.on_event("startup")
async def _start_snmp_collector():
    """后台启动 SNMP 采集调度（可降级，失败不阻塞）。"""
    try:
        asyncio.create_task(_snmp_collector.run_forever())
    except Exception:
        pass


@app.get("/api/v1/snmp/devices")
async def list_snmp_devices(active_only: bool = True):
    return {"devices": SNMPDeviceStore().list(active_only=active_only)}


@app.post("/api/v1/snmp/devices")
async def add_snmp_device(body: dict = None):
    b = body or {}
    if not b.get("ip"):
        raise HTTPException(400, "ip is required")
    dev_id = SNMPDeviceStore().create({
        "hostname": b.get("hostname", ""), "ip": b.get("ip"),
        "community": b.get("community", "public"), "snmp_version": b.get("snmp_version", "v2c"),
        "vendor": b.get("vendor", ""), "model": b.get("model", ""),
        "location": b.get("location", ""), "status": b.get("status", "active"),
    })
    return {"ok": True, "id": dev_id}


@app.delete("/api/v1/snmp/devices/{dev_id}")
async def delete_snmp_device(dev_id: int):
    SNMPDeviceStore().delete(dev_id)
    return {"ok": True}


@app.get("/api/v1/snmp/devices/{dev_id}/interfaces")
async def list_snmp_interfaces(dev_id: int):
    return {"interfaces": SNMPDeviceStore().list_interfaces(dev_id)}


@app.post("/api/v1/snmp/devices/{dev_id}/collect")
async def collect_snmp_device(dev_id: int):
    """手动立即采集某设备接口。"""
    devs = SNMPDeviceStore().list(active_only=False)
    dev = next((d for d in devs if d.get("id") == dev_id), None)
    if not dev:
        raise HTTPException(404, "device not found")
    data = _snmp_collector.collect_device(dev)
    SNMPDeviceStore().save_interfaces(dev_id, data["interfaces"])
    SNMPDeviceStore().touch_collect(dev_id)
    return {"ok": True, "interfaces": len(data["interfaces"]), "sys_descr": data["sys_descr"]}


# ═══════════════════════════════════════════════════════════════
#  IPMI（本地 /dev/ipmi0 上报）+ 部件可用性
# ═══════════════════════════════════════════════════════════════

from ipmi_ingest import IPMIStore
from node_health import NodeHealthAggregator


@app.post("/api/v1/ipmi/ingest")
async def ipmi_ingest(body: dict = None):
    """ipmi-exporter 上报节点传感器。可降级。"""
    b = body or {}
    node = b.get("node") or b.get("node_name")
    if not node:
        raise HTTPException(400, "node required")
    sensors = b.get("sensors") or []
    IPMIStore().ingest(node, sensors)
    return {"ok": True, "count": len(sensors)}


@app.get("/api/v1/ipmi/sensors")
async def list_ipmi_sensors(node: str = "", sensor_type: str = ""):
    return {"sensors": IPMIStore().query(node=node or None, sensor_type=sensor_type or None)}


@app.get("/api/v1/node/health")
async def list_node_health(node: str = ""):
    return {"health": NodeHealthAggregator().query(node=node or None)}


@app.post("/api/v1/node/health/aggregate")
async def aggregate_node_health(body: dict = None):
    """手动触发部件可用性聚合（mock node_exporter+IPMI 数据）。"""
    b = body or {}
    node = b.get("node") or b.get("node_name")
    if not node:
        raise HTTPException(400, "node required")
    metrics = b.get("metrics") or {}
    status = NodeHealthAggregator().aggregate(node, metrics)
    return {"ok": True, "health": status}


# WebShell WebSocket 端点
app.add_api_websocket_route("/api/v1/shell/ws", shell_ws)


if __name__ == "__main__":
    import uvicorn
    port = int(os.environ.get("PORT", "8080"))
    # 彻底解决并发耗尽: 使用多个 worker (1个主 + gunicorn风格)
    # health probe 用独立线程池的 uvicorn 配置
    uvicorn.run(app, host="0.0.0.0", port=port,
                timeout_keep_alive=300,
                limit_concurrency=50,  # 恢复并发限制但提高上限
                backlog=100,
                log_level="info")
