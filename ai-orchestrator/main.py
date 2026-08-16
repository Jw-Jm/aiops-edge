"""AI Orchestrator v5 — FastAPI + LangGraph + arq + ChromaDB + Detector"""
import json
import os
import time
import re
import uuid
import asyncio
from collections import defaultdict
from contextlib import asynccontextmanager
from fastapi import FastAPI, Request, HTTPException, Header
from fastapi.middleware.cors import CORSMiddleware
from fastapi.responses import StreamingResponse, JSONResponse, PlainTextResponse, Response
from pydantic import BaseModel
from sse_starlette.sse import EventSourceResponse
from apscheduler.schedulers.asyncio import AsyncIOScheduler

from shell_policy import ShellPolicy
from models import ChatRequest, ShellCheckRequest, MCPCallRequest, AlertRCARequest
from store import _task_store
import metrics  # noqa: F401 — 注册 Prometheus 指标
from skill_registry import SkillRegistry, ExpertRegistry
from skills import init_skills, init_experts
from orchestrator import describe_graph, _audit_log, _is_info_query, _risk_from_evidence, _case_quality_check, _llm_async
from flow_api import router as flow_router
from kg_api import router as kg_router
import agent_tool  # B4: 后台 persona worker 终态通知队列 (drain_notifications)

# 默认开启 LLM mock（本机部署联调用，不消耗真实模型）；生产设 LLM_MOCK=false 关闭。
# 注意：mock 模式下 NL2SQL/RCA 深度/AI 诊断返回的是模拟内容，生产环境必须关闭。
os.environ.setdefault("LLM_MOCK", os.getenv("LLM_MOCK", "true"))
if os.environ.get("LLM_MOCK", "").lower() in ("true", "1", "yes"):
    print("[WARN] LLM_MOCK=true：AI 诊断/RCA/NL2SQL 将返回模拟内容，仅适用于本地演示；生产必须设 LLM_MOCK=false", flush=True)

# ═══════════════════════════════════════════════════════════════
#  APScheduler — 定时异常扫描（每 5 分钟，只检测+持久化，不触发 LLM）
# ═══════════════════════════════════════════════════════════════
scheduler = AsyncIOScheduler()


@asynccontextmanager
async def lifespan(app: FastAPI):
    """统一生命周期：startup + shutdown（替代废弃的 @app.on_event）。

    管理：APScheduler 定时扫描、SNMP 采集/MySQL 迁移/知识库加载、AsyncSqliteSaver 连接。
    各启动项均可降级，失败不阻塞服务启动。
    """
    # === startup ===
    # 1. APScheduler 定时异常扫描
    try:
        scheduler.add_job(_scheduled_anomaly_scan, 'interval', minutes=5,
                          id='anomaly_scan', replace_existing=True)
        scheduler.start()
    except Exception as e:  # noqa: BLE001
        print(f"[startup] scheduler error: {e}", flush=True)
    # 1b. APScheduler 定时：告警事件自动入库为 case 草稿（每 15 分钟，失败仅打日志不抛错）
    try:
        scheduler.add_job(_scheduled_alert_to_case, 'interval', minutes=15,
                          id='alert_to_case', replace_existing=True)
    except Exception as e:  # noqa: BLE001
        print(f"[startup] scheduler add_job(alert_to_case) error: {e}", flush=True)
    # 1c. APScheduler 定时：节点部件健康真实聚合（P1-3，每 60s，失败仅打日志不抛错）
    try:
        scheduler.add_job(_scheduled_node_health_aggregate, 'interval', seconds=60,
                          id='node_health_aggregate', replace_existing=True)
    except Exception as e:  # noqa: BLE001
        print(f"[startup] scheduler add_job(node_health_aggregate) error: {e}", flush=True)
    # 2. SNMP 采集 + MySQL 迁移 + 知识库自动加载（均可降级，失败不阻塞）
    try:
        from db import migrate
        migrate()
    except Exception:
        pass
    try:
        asyncio.create_task(_snmp_collector.run_forever())
    except Exception:
        pass
    try:
        import threading
        threading.Thread(target=_seed_knowledge_bg, daemon=True).start()
    except Exception:
        pass
    # B2/B3: 加载 persona 注册表（builtin + 用户目录），注入 agent_tool 并注册 spawn_worker 工具
    try:
        from persona_registry import load_personas, PERSONAS_BUILTIN_DIR, USER_PERSONAS_DIR
        from agent_tool import set_personas, register_spawn_worker_tool
        PERSONAS = load_personas(PERSONAS_BUILTIN_DIR, USER_PERSONAS_DIR)
        set_personas(PERSONAS)
        register_spawn_worker_tool()
        print(f"[startup] personas loaded: {len(PERSONAS)}", flush=True)
    except Exception as e:  # noqa: BLE001
        print(f"[startup] persona 加载失败(不阻塞): {e}", flush=True)
    # C3: K8s chat 工具注册 + preflight token 密钥注入
    try:
        from k8s_actions import register_k8s_tools, set_secret
        from skill_registry import ToolRegistry
        register_k8s_tools(ToolRegistry)
        set_secret(os.environ.get("INTERNAL_TOKEN", ""))
    except Exception as e:  # noqa: BLE001
        print(f"[startup] k8s tools register error: {e}", flush=True)
    # A2: workflow cron 触发器调度（独立 BackgroundScheduler，30s 对齐 job）
    try:
        from apscheduler.schedulers.background import BackgroundScheduler as _BgSched
        from flow_engine.trigger_scheduler import CronTriggerManager
        from flow_api import get_flow_service as _get_flow_service
        _flow_wsvc = _get_flow_service()
        _flow_sched = _BgSched(daemon=True)
        _flow_cron_mgr = CronTriggerManager(
            _flow_sched,
            lambda: [f for f in _flow_wsvc.list_flows() if f.get("enabled")],
            _flow_wsvc.run_flow)
        _flow_cron_mgr.sync()
        _flow_sched.add_job(_flow_cron_mgr.sync, 'interval', seconds=30,
                            id='flow_cron_sync', replace_existing=True)
        _flow_sched.start()
    except Exception as e:  # noqa: BLE001
        print(f"[startup] flow cron scheduler error: {e}", flush=True)
    # E2: 内置运维 playbook 向量化加载（重试直到 ChromaDB 就绪，幂等，失败不阻塞）
    # 注意: ChromaDB 首次初始化可能失败(与 knowledge_seed 并行竞争 tenant 创建),
    # 一次性线程会静默丢 playbook, 因此带重试循环 (10 次 × 15s)。
    try:
        import threading as _th
        import time as _time
        from playbook_loader import load_playbooks as _load_playbooks
        from rag import rag as _rag_store

        def _load_playbooks_bg():
            for _attempt in range(10):
                try:
                    n = _load_playbooks(_rag_store)
                    if n > 0:
                        print(f"[startup] playbooks loaded: {n} chunks", flush=True)
                        return
                except Exception:
                    pass
                _time.sleep(15)
            print("[startup] playbooks 加载失败(重试耗尽), 可稍后手动触发", flush=True)

        _th.Thread(target=_load_playbooks_bg, daemon=True).start()
    except Exception:
        pass

    yield

    # === shutdown ===
    try:
        scheduler.shutdown(wait=False)
    except Exception:  # noqa: BLE001
        pass
    # AsyncSqliteSaver 连接关闭（如已初始化，避免进程退出资源泄漏）
    try:
        from orchestrator import brain
        if getattr(brain, "_async_conn", None) is not None:
            await brain._async_conn.close()
            brain._async_conn = None
            brain._async_saver_initialized = False
    except Exception:  # noqa: BLE001
        pass


app = FastAPI(title="AIOps Orchestrator", version="5.0", lifespan=lifespan)
app.add_middleware(CORSMiddleware, allow_origins=["*"], allow_methods=["*"], allow_headers=["*"])
shell_policy = ShellPolicy()
app.include_router(flow_router)
app.include_router(kg_router)


async def _scheduled_anomaly_scan():
    """定时异常扫描（只检测+持久化，不触发 LLM 诊断）

    从 query-api 拉服务列表，对 error_rate/p99_latency/request_rate 三指标做
    3 算法投票检测。detect() 内部确认异常时会调 _persist_anomaly 写入 MySQL
    anomaly_events 表（best-effort）。此处不调 LLM，避免定时任务产生模型开销。
    """
    try:
        from detector import detector
        from tools import get_service_list
        import json as _json
        raw = await asyncio.to_thread(get_service_list)
        try:
            svc_data = _json.loads(raw)
        except Exception:
            svc_data = []
        for svc in (svc_data if isinstance(svc_data, list) else []):
            name = svc.get("service_name", "")
            if not name:
                continue
            metrics_vals = {
                "error_rate": float(svc.get("error_rate", 0) or 0),
                "p99_latency": float(svc.get("max_ms", 0) or 0),
                "request_rate": float(svc.get("traces", 0) or 0),
            }
            for metric, val in metrics_vals.items():
                # detect() 内部已完成 vote + _persist_anomaly，无需重复调用 vote
                detector.detect(name, metric, val)
    except Exception as e:
        print(f"[scheduler] anomaly scan error: {e}")


async def _scheduled_node_health_aggregate():
    """P1-3: 每 60s 自动聚合节点部件健康（VM OS 层 + IPMI 硬件层 → node_component_health）。

    阻塞的 HTTP/MySQL 调用放线程池，失败仅打日志不阻塞主流程。
    """
    try:
        from node_health import NodeHealthAggregator
        await asyncio.to_thread(NodeHealthAggregator().aggregate_all)
    except Exception as e:
        print(f"[scheduler] node health aggregate error: {e}", flush=True)

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
        # P1-4: 拉取失败(None)时**不清空**现有配置，保留 holder 上次有效值——
        # 避免 NL2SQL 等端点因一次拉取抖动（网络/query-api 不可用）丢 key 而静默降级。
        saved = _fetch_saved_llm_config()
        if saved is not None:
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
        # stream_sync() 现在是 async generator (节点 async def, LLM 走 asyncio.to_thread
        # 不阻塞 event loop)。线程内无 event loop，用 asyncio.run 驱动 async generator。
        # 保留 thread + queue 模型: 让 SSE generate() 仍可独立检查 is_disconnected。
        import queue
        import threading
        import asyncio as _asyncio

        event_queue = queue.Queue()
        stop_event = threading.Event()

        def _run_stream():
            async def _astream():
                async for event in _get_brain().stream_sync(
                        req.intent, req.service or "", req.message, thread_id,
                        mode="dual" if req.dual_agent else "chat",
                        exec_context=req.exec_result, iteration=(req.exec_result and 2 or 1),
                        cluster_id=req.cluster_id or "all"):
                    # 操作建议不再落任务工作台，直接以 thread_id 作为确认标识发往前端内联审批
                    if event.get("type") == "suggestion":
                        event["thread_id"] = thread_id
                        event["exec_context"] = req.exec_result
                    event_queue.put(event)
            try:
                asyncio.run(_astream())
                event_queue.put(None)  # sentinel: done
                # Issue4: 会话完成后更新 session_store（提供 updated_at 供历史会话按时间倒序）
                try:
                    from session_store import session_store
                    session_store.save(thread_id, req.intent, req.service or "", [])
                except Exception:
                    pass
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
            # 检查客户端断开: 浏览器关闭/取消时主动退出, 避免流悬挂
            _is_disconnected = getattr(request, "is_disconnected", None)
            while True:
                # 客户端断开 → 立即停止 (uvicorn 才能关闭连接, 前端 reader.read() 才会 done)
                if _is_disconnected is not None:
                    try:
                        if await _is_disconnected():
                            break
                    except Exception:
                        pass
                # B4: 轮询后台 persona worker 终态，投递 task_notification frame
                try:
                    for _ntf in agent_tool.drain_notifications():
                        yield _format_sse(_ntf)
                except Exception:
                    pass
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
                    done_text = event.get("text", "")
                    # Issue7 修复: 流式对话结束持久化报告到报告中心（MinIO + MySQL）。
                    # req.service 可能为空（如"巡检所有 K8s 集群"），尝试从消息/报告内容中提取目标服务，
                    # 使报告中心能展示明确的服务归属，而非匿名 "-"。
                    try:
                        if done_text and len(done_text.strip()) > 100:
                            svc = (req.service or "").strip()
                            if not svc:
                                # 优先从消息里的"分析/诊断/巡检 XXX"提取；再退化从报告首行"**目标**"提取
                                svc = _extract_service_from_text(req.message or "") or _extract_service_from_text(done_text)
                            _upload_report(thread_id, done_text, service=svc, question=req.message or "")
                    except Exception as _e:
                        print(f"[chat] 流式报告持久化失败: {_e}")
                    yield _format_sse({
                        "type": "done",
                        "text": done_text,
                        "assistant_message": {
                            "id": f"asst_{thread_id}",
                            "content": done_text,
                            "created_at": time.strftime("%Y-%m-%dT%H:%M:%SZ"),
                        },
                    })
                    # 关键修复: yield done 后立即结束流, 避免前端 reader.read() 永远等不到 done
                    break
                elif event.get("type") == "approval_pending":
                    # 不再创建任务工作台任务；以内联 thread_id 为确认标识，前端审批卡直接使用
                    event["thread_id"] = thread_id
                    yield _format_sse(event)
                elif event.get("type") == "error":
                    yield _format_sse({"type": "error", "error": event.get("text", ""), "code": "dag_error"})
                    # error 后也立即结束, 避免流悬挂
                    break
                else:
                    yield _format_sse(event)

        return StreamingResponse(generate(), media_type="text/event-stream",
                                 headers={"X-Session-Id": thread_id, "Cache-Control": "no-cache"})
    else:
        result = await _get_brain().execute_sync(req.intent, req.service or "", req.message, thread_id,
                                                cluster_id=req.cluster_id or "all")
        # 巡检/诊断报告落盘：持久化到 ClickHouse（历史趋势）并在 MinIO 留档
        try:
            if result and len(result.strip()) > 100:
                _upload_report(thread_id, result, service=req.service or "", question=req.message or "")
        except Exception as _e:
            print(f"[chat] 报告持久化失败: {_e}")
        # P0-1: 非流式响应显式返回 llm_mode（deterministic/llm），不加在报告内容开头
        from orchestrator import _llm_key_ready
        # B1: 非流式响应放宽到 60000 字符，完整保留长巡检/诊断报告（原 10000 会截断超长报告）
        return JSONResponse({
            "report": result[:60000],
            "llm_mode": "llm" if _llm_key_ready() else "deterministic",
        })


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

# ═══════════════════════════════════════════════════════════════
#  Marketplace（D5）— 安装 / 已安装列表 / 卸载（均 admin）
# ═══════════════════════════════════════════════════════════════


def _require_admin(request: Request):
    """仅 admin 可操作（内部 token + X-Internal-Role=admin）。"""
    expected = os.environ.get("INTERNAL_TOKEN", "")
    got = request.headers.get("X-Internal-Token", "")
    if not expected or got != expected:
        raise HTTPException(403, "请求来源不可信（内部 token 校验失败）")
    if request.headers.get("X-Internal-Role", "") != "admin":
        raise HTTPException(403, "仅管理员可操作")


@app.post("/api/v1/ai/marketplace/install")
async def marketplace_install(request: Request, body: dict = None):
    _require_admin(request)
    import marketplace
    src = (body or {}).get("source", "")
    try:
        result = marketplace.install(src, as_admin=True)
        try:
            _audit_log("marketplace", "install", _audit_operator(request),
                       result["pack_id"], src, "ok")
        except Exception:
            pass
        return result
    except PermissionError:
        raise HTTPException(403, "仅管理员可安装")
    except ValueError as e:
        raise HTTPException(400, str(e))


@app.get("/api/v1/ai/marketplace/installed")
async def marketplace_installed(request: Request):
    _require_admin(request)
    import marketplace
    return {"installed": marketplace.list_installed()}


@app.delete("/api/v1/ai/marketplace/installed/{pack_id}")
async def marketplace_uninstall(request: Request, pack_id: str):
    _require_admin(request)
    import marketplace
    try:
        result = marketplace.uninstall(pack_id)
        try:
            _audit_log("marketplace", "uninstall", _audit_operator(request),
                       pack_id, "", "ok")
        except Exception:
            pass
        return result
    except ValueError as e:
        raise HTTPException(404, str(e))

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
    # execute_sync_full 已改为 async（节点 async def），直接 await（不再 run_in_executor）
    result = await brain.execute_sync_full(intent, service, message, "workflow-run")
    return {"run_id": f"run_{int(time.time()*1000)}", "result": result}

class SuggestionRequest(BaseModel):
    thread_id: str = ""
    script: str = ""        # 要执行的命令（AI 建议或用户自定义）
    service: str = ""
    context: str = ""       # 分析上下文（诊断文本），供 execute_suggestion 用
    approved: bool = False  # 安全(P0-4): 必须显式 true 才执行。默认 False=驳回，
                            # 杜绝漏传字段/前端缺陷导致"未确认即执行"环境操作


@app.post("/api/v1/ai/suggestion/execute")
def execute_suggestion_command(req: SuggestionRequest, request: Request):
    """需求2/3: aichat 内嵌审批——确认后执行处置命令（AI 建议或用户自定义）。

    安全：复用 execute_suggestion 的 ShellPolicy 黑名单 + 白名单强制（读写动作分级），
    用户确认后才会执行。返回执行结果，前端据此发起下一轮深入分析。
    """
    _require_approver(request)  # 复用审批人校验（与任务工作台一致）
    script = (req.script or "").strip()
    if not script:
        raise HTTPException(400, "script is required")
    if not req.approved:
        return {"thread_id": req.thread_id, "approved": False, "exec_result": "已驳回，未执行"}
    try:
        exec_result = _get_brain().execute_suggestion(
            req.service or "", script, req.context or "", task_id=req.thread_id or "manual")
    except Exception as e:
        return {"thread_id": req.thread_id, "approved": True,
                "exec_result": f"执行失败: {e}", "error": True}
    # 审计 (P1-2): task_id=真实会话ID(无则 "manual"), operator=当前用户/角色, target=服务名
    try:
        _audit_log(req.thread_id or "manual", "approve",
                   _audit_operator(request),
                   req.service or "", script[:300], "approved",
                   {"source": "ai_chat"})
    except Exception:
        pass
    return {"thread_id": req.thread_id, "approved": True, "exec_result": exec_result}


class FinalReportRequest(BaseModel):
    session_id: str = ""
    service: str = ""


@app.post("/api/v1/ai/final_report")
async def final_report(req: FinalReportRequest):
    """输出最终版本报告：汇总该会话全部上下文（含多次处置执行），调 LLM 生成。"""
    if not req.session_id:
        raise HTTPException(400, "session_id is required")
    brain = _get_brain()
    vals = brain.get_session_state(req.session_id) or {}
    # 执行历史（审计）
    try:
        from db_audit import AuditStore
        exec_history = AuditStore().query_by_task(req.session_id)
    except Exception:
        exec_history = []
    # 组装上下文
    parts = []
    parts.append(f"## 用户原始问题\n{vals.get('user_message','') or '(无)'}")
    parts.append(f"## 初步分析\n{vals.get('final_response','') or '(无)'}")
    if exec_history:
        h = []
        for e in exec_history:
            h.append(f"- [{e.get('created_at','')}] action={e.get('action','')} target={e.get('target_service','')}\n"
                     f"  命令: {e.get('command','')}\n  结果: {e.get('result','')}")
        parts.append("## 处置执行历史（多次）\n" + "\n".join(h))
    else:
        parts.append("## 处置执行历史\n(无已执行记录)")
    service = req.service or vals.get("service", "")
    context = "\n\n".join(parts)
    # LLM 配置：与 main.py 既有模式一致（`_get_brain().llm_config`）
    from orchestrator import _llm, _llm_key_ready
    if not _llm_key_ready():
        return {"report": f"### 最终版本报告\n\n（LLM 未配置，基于已有分析汇总）\n\n{context[:4000]}"}
    system = ("你是资深 SRE 报告撰写员。基于用户原始问题、初步分析、以及全部处置执行历史，"
              "输出**最终版本报告**，包含：1) 根因结论 2) 处置过程（哪些命令已执行、结果）"
              "3) 当前状态与执行结果 4) 遗留风险 5) 后续建议。用 Markdown，条理清晰，完整不省略。")
    cfg = brain.llm_config or {}
    # _llm 为同步阻塞调用，放到线程池避免阻塞 event loop
    report = await asyncio.to_thread(_llm, cfg, system, f"服务: {service}\n\n{context}", "最终报告")
    return {"report": report}


@app.get("/api/v1/ai/sessions")
async def list_sessions():
    try:
        rows = _get_brain()._conn.execute(
            "SELECT DISTINCT thread_id FROM checkpoints LIMIT 200"
        ).fetchall()
        # Issue4: 用 session_store 的 updated_at 提供真实时间戳，历史会话按最近活跃时间倒序
        meta = {}
        try:
            from session_store import SessionStore
            store = SessionStore()
            for s in store.list_sessions(500):
                meta[s["session_id"]] = s.get("updated_at", "") or ""
        except Exception:
            pass
        sessions, seen = [], set()
        for (tid,) in rows:
            if tid in seen: continue
            seen.add(tid)
            # P0: 用同步 get_session_state 读 checkpoint state（不再用 graph.get_state，避免跨 loop 崩溃）
            vals = _get_brain().get_session_state(tid)
            updated = meta.get(tid, "")
            if vals:
                sessions.append({
                    "session_id": tid,
                    "preview": (vals.get("user_message", "") or vals.get("final_response", "") or tid)[:80],
                    "intent": vals.get("intent", ""),
                    "created_at": updated,
                })
            else:
                sessions.append({"session_id": tid, "preview": tid, "intent": "", "created_at": updated})
        # 按 updated_at 倒序（有时间的优先，无时间的排后），保持最近会话在最前
        # 注意: created_at 可能是 float(epoch) 或 ""，排序 key 需统一为数值，避免 float/str 混比报错
        def _key(s):
            ts = s.get("created_at")
            if isinstance(ts, (int, float)):
                return float(ts)
            return 0.0
        sessions.sort(key=_key, reverse=True)
        return {"sessions": sessions}
    except Exception as _e:
        print(f"[sessions] list error: {_e}")
        return {"sessions": []}


@app.get("/api/v1/ai/session/{sid}")
async def get_session(sid: str):
    # P0: 改用同步 SQLite 读取 checkpoint state（get_session_state），
    # 不再用 graph.get_state()（AsyncSqliteSaver 主线程同步调用会抛 InvalidStateError /
    # 跨 loop 抛 RuntimeError → HTTP 500 → 前端历史会话点击无反应）。
    vals = _get_brain().get_session_state(sid)
    if vals:
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
        # Issue4: 同步删除 session_store 中的元数据，保证列表与清除一致
        try:
            from session_store import SessionStore
            store = SessionStore()
            conn = _get_brain()._conn
            import sqlite3
            _sc = sqlite3.connect(store.db_path)
            _sc.execute("DELETE FROM sessions WHERE session_id = ?", (sid,))
            _sc.commit(); _sc.close()
        except Exception:
            pass
        return {"message": "session deleted", "session_id": sid}
    except Exception as e:
        raise HTTPException(500, f"delete failed: {e}")


@app.delete("/api/v1/ai/sessions")
async def clear_sessions():
    """Issue4: 清除全部历史会话（checkpoints + writes + session_store 元数据）。"""
    try:
        _get_brain()._conn.execute("DELETE FROM checkpoints")
        _get_brain()._conn.execute("DELETE FROM writes")
        _get_brain()._conn.commit()
        try:
            from session_store import SessionStore
            store = SessionStore()
            import sqlite3
            _sc = sqlite3.connect(store.db_path)
            _sc.execute("DELETE FROM sessions")
            _sc.commit(); _sc.close()
        except Exception:
            pass
        return {"message": "all sessions cleared"}
    except Exception as e:
        raise HTTPException(500, f"clear failed: {e}")


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


def _infer_service(req, message: str = "", fallback: str = "") -> str:
    """从请求或消息文本中推断服务名；取不到时返回 fallback（空串由前端兜底显示 '-'）。
    避免将未知服务硬编码为 'unknown'，保证数据可读性。"""
    svc = (getattr(req, "service", None) or "").strip()
    if svc and svc != "unknown":
        return svc
    # 从消息/上下文里尝试提取常见服务标识（如 xxx-service / xxx-server / xxx-agent）
    text = message or getattr(req, "context", "") or ""
    for kw in ["service", "server", "agent", "api", "db", "redis", "mysql"]:
        if kw in text:
            # 取包含该关键词的最短子串，形如 "deepflow-server"
            import re
            m = re.search(r"[\w-]*" + kw + r"[\w-]*", text)
            if m:
                return m.group(0)
    return fallback


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
            "service": _infer_service(req, getattr(req, "message", "")),
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


# ═══════════════════════════════════════════════════════════════
#  诊断执行硬超时 (P0): LLM 挂起/图中断时 asyncio.run(ainvoke) 可能永久阻塞,
#  任务将无限期停在 diagnosing。放独立 daemon 线程执行, 等待 DIAGNOSIS_TIMEOUT 秒,
#  超时抛 concurrent.futures.TimeoutError, 由调用方将任务置为 failed("诊断超时")。
# ═══════════════════════════════════════════════════════════════
DIAGNOSIS_TIMEOUT = int(os.environ.get("DIAGNOSIS_TIMEOUT", "600"))


def _run_dag_diagnosis(tid: str, svc: str, ctx: str) -> dict:
    """在独立 daemon 线程中执行 execute_sync_full(mode=full) 并等待至多
    DIAGNOSIS_TIMEOUT 秒。超时抛 concurrent.futures.TimeoutError(调用方置 failed);
    线程不可取消但为 daemon, 进程退出不被阻塞。"""
    import queue as _queue
    import threading as _threading
    from concurrent.futures import TimeoutError as _FutureTimeoutError

    result_q = _queue.Queue(maxsize=1)

    def _worker():
        try:
            final = asyncio.run(_get_brain().execute_sync_full("diagnosis", svc, ctx, tid))
            result_q.put(final)
        except BaseException as e:  # noqa: BLE001  — 透传给等待方统一走 failed
            result_q.put(e)

    _threading.Thread(target=_worker, name=f"diag-{tid}", daemon=True).start()
    try:
        item = result_q.get(timeout=DIAGNOSIS_TIMEOUT)
    except _queue.Empty:
        raise _FutureTimeoutError(f"diagnosis timeout {DIAGNOSIS_TIMEOUT}s")
    if isinstance(item, BaseException):
        raise item
    return item


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
    # P0-2 修复: llm_config 已安全剔除 api_key, 需用 _llm_key_ready() 判断真实可用性
    from orchestrator import _llm_key_ready
    if not _llm_key_ready():
        task["status"] = "failed"
        task["diagnosis"] = "LLM API Key 未配置, 请在设置页面配置"
        return {"task": task}

    task["status"] = "diagnosing"
    def _run():
        from concurrent.futures import TimeoutError as _FutureTimeoutError
        try:
            # execute_sync_full 已改为 async；线程内无 event loop，用 asyncio.run 驱动。
            # P0: 加硬超时——LLM 挂起/图中断时不永久停在 diagnosing, 超时置 failed。
            final_state = _run_dag_diagnosis(tid, req.service, req.context)
            # P0-2: 非交互 full 图初始 approved=False，DAG 在 wait_approval 处中断等待人工审批。
            # 中断态无 final_response，不能误标 done；回填 plan/script/risk 供审批面板展示。
            if final_state.get("__interrupt__"):
                _task_store[tid]["status"] = "waiting"
                _task_store[tid]["plan"] = final_state.get("plan", "")[:6000]
                _task_store[tid]["script"] = final_state.get("script", "")[:4000]
                _task_store[tid]["risk_score"] = final_state.get("risk_score", 0)
                _task_store[tid]["risk_reason"] = final_state.get("risk_reason", "")[:1000]
                return
            _task_store[tid]["diagnosis"] = final_state.get("final_response", "")[:5000]
            _task_store[tid]["plan"] = final_state.get("plan", "")[:6000]
            _task_store[tid]["script"] = final_state.get("script", "")[:4000]
            _task_store[tid]["risk_score"] = final_state.get("risk_score", 0)
            _task_store[tid]["risk_reason"] = final_state.get("risk_reason", "")[:1000]
            _task_store[tid]["report"] = final_state.get("report", "")[:8000]
            _task_store[tid]["done_at"] = time.strftime("%Y-%m-%dT%H:%M:%SZ")
            _task_store[tid]["status"] = "done"
            if final_state.get("final_response"):
                try: _upload_report(tid, final_state["final_response"], service=req.service or "", question=req.context or "")
                except Exception: pass
        except _FutureTimeoutError:
            _task_store[tid]["status"] = "failed"
            _task_store[tid]["diagnosis"] = f"诊断超时({DIAGNOSIS_TIMEOUT}s)"
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

    # === Mount A3: 告警触发 workflow（后台派发，不阻塞 webhook 返回）===
    try:
        import threading as _t
        import logging as _logging
        from flow_api import get_flow_service as _get_flow_service
        from flow_engine.flow_alert_dispatch import dispatch_alert

        def _dispatch_alert_bg():
            try:
                wsvc = _get_flow_service()
                fired = dispatch_alert(
                    lambda: [f for f in wsvc.list_flows() if f.get("enabled")],
                    wsvc.run_flow, source, severity, body)
                if fired:
                    _logging.getLogger("flow_dispatch").info("告警触发 workflow: %s", fired)
            except Exception:
                _logging.getLogger("flow_dispatch").exception("告警→workflow 派发失败(不影响告警入库)")

        _t.Thread(target=_dispatch_alert_bg, daemon=True).start()
    except Exception:
        pass

    # === Mount B6: 告警自动调查 (incident-investigator，daemon 线程异步执行，不阻塞告警入库) ===
    try:
        import logging as _logging
        import threading as _t
        from investigator import maybe_investigate

        _inv_log = _logging.getLogger("investigator")

        def _investigate_bg():
            try:
                maybe_investigate(
                    source, severity,
                    {"service": service, "summary": context, "context": context},
                    run_worker=None)
            except Exception:
                _inv_log.exception("告警自动调查失败(不影响告警入库)")

        _t.Thread(target=_investigate_bg, daemon=True,
                  name=f"investigate-{tid}").start()
    except Exception:
        pass

    # 不再自动触发 LLM 诊断：只登记任务，等待人工在任务工作台手动触发
    # (避免每次告警都自动调用 LLM，造成大量开销；也避免 vmalert 15s 重复 webhook 反复诊断)
    return {"task_id": tid, "status": "queued", "message": "task queued, trigger diagnosis manually"}


@app.get("/api/v1/ops/tasks")
async def list_tasks(status: str = "", source: str = ""):
    result = list(_task_store.values())
    if status:
        # P0: 任务生命周期枚举: queued → diagnosing → done(成功) / waiting / approved /
        # rejected / failed。对外兼容 "succeeded" 查询 (存储态为 "done", 与 flow 引擎一致)。
        if status == "succeeded":
            status = "done"
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
        from concurrent.futures import TimeoutError as _FutureTimeoutError
        try:
            # execute_sync_full 已改为 async；线程内无 event loop，用 asyncio.run 驱动。
            # P0: 加硬超时——LLM 挂起/图中断时不永久停在 diagnosing, 超时置 failed。
            final_state = _run_dag_diagnosis(tid, svc, ctx)
            # P0-2: 非交互 full 图初始 approved=False，DAG 在 wait_approval 处中断等待人工审批。
            # 中断态无 final_response，不能误标 done；回填 plan/script/risk 供审批面板展示。
            if final_state.get("__interrupt__"):
                _task_store[tid]["status"] = "waiting"
                _task_store[tid]["plan"] = final_state.get("plan", "")[:6000]
                _task_store[tid]["script"] = final_state.get("script", "")[:4000]
                _task_store[tid]["risk_score"] = final_state.get("risk_score", 0)
                _task_store[tid]["risk_reason"] = final_state.get("risk_reason", "")[:1000]
                return
            _task_store[tid]["status"] = "done"
            _task_store[tid]["diagnosis"] = final_state.get("final_response", "")[:5000]
            _task_store[tid]["plan"] = final_state.get("plan", "")[:6000]
            _task_store[tid]["script"] = final_state.get("script", "")[:4000]
            _task_store[tid]["risk_score"] = final_state.get("risk_score", 0)
            _task_store[tid]["risk_reason"] = final_state.get("risk_reason", "")[:1000]
            _task_store[tid]["report"] = final_state.get("report", "")[:8000]
            _task_store[tid]["done_at"] = time.strftime("%Y-%m-%dT%H:%M:%SZ")
            if final_state.get("final_response"):
                try: _upload_report(tid, final_state["final_response"], service=svc, question=ctx)
                except Exception: pass
        except _FutureTimeoutError:
            _task_store[tid]["status"] = "failed"
            _task_store[tid]["diagnosis"] = f"诊断超时({DIAGNOSIS_TIMEOUT}s)"
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
    # P0-2 修复: llm_config 已安全剔除 api_key, 用 _llm_key_ready() 判断
    from orchestrator import _llm_key_ready
    if not _llm_key_ready():
        task["status"] = "failed"
        task["diagnosis"] = "LLM API Key 未配置"
        return {"task_id": tid, "status": "failed"}
    task["status"] = "diagnosing"
    _run_diagnosis(tid, task.get("service", "") or "", task.get("context", "") or "")
    return {"task_id": tid, "status": "diagnosing"}


def _audit_operator(request: Request) -> str:
    """审计日志 operator：优先 X-Internal-User（query-api 代理注入的真实用户名），
    其次 X-Internal-Approver 用户名（排除 "0"/"1" 标志位），最后 X-Internal-Role
    **标注为 role:<角色>** 回退，不再把角色当用户名裸写入。
    X-Internal-Approver 常为 "1"/"0" 标志位（见 _require_approver），不作为用户名写入。"""
    user = (request.headers.get("X-Internal-User") or "").strip()
    if user:
        return user
    for h in ("X-Internal-Approver",):
        v = (request.headers.get(h) or "").strip()
        if v and v not in ("0", "1"):
            return v
    role = (request.headers.get("X-Internal-Role") or "").strip()
    if role:
        # 标注来源为角色而非用户名，避免审计 operator 被误读为具体操作人
        return f"role:{role}"
    return "system"


def _require_approver(request: Request):
    """审批权限校验：仅 admin 或已配置的审批人可操作。

    安全：X-Internal-Role / X-Internal-Approver 必须来自可信的 query-api 代理。
    代理（query-api）在完成 JWT 鉴权与角色注入后，会附上 X-Internal-Token（与
    INTERNAL_TOKEN 共享）。此处校验该 token，防止绕过 query-api 直连本服务伪造
    header 提权。
    """
    expected = os.environ.get("INTERNAL_TOKEN", "")
    got = request.headers.get("X-Internal-Token", "")
    if not expected or got != expected:
        raise HTTPException(403, "请求来源不可信（内部 token 校验失败）")
    role = request.headers.get("X-Internal-Role", "")
    is_approver = request.headers.get("X-Internal-Approver", "0") == "1"
    if role != "admin" and not is_approver:
        raise HTTPException(403, "仅管理员或审批人可操作")


@app.post("/api/v1/ops/tasks/{tid}/approve")
def approve_task(tid: str, request: Request):
    """同步 handler：内部含阻塞的审批持久化 + 审计 MySQL 写入，放线程池执行。"""
    _require_approver(request)
    if tid not in _task_store:
        raise HTTPException(404, "task not found")
    task = _task_store[tid]
    task["status"] = "approved"
    # P0-1 修复: 审计与审批后执行必须分离 —— 此前执行分支被错误缩进在
    # `except Exception: pass` 块内部(_audit_log 从不抛异常, 该块永不进入),
    # 导致审批后任务永远停在 approved 不执行。现将执行逻辑移到 try 主块。
    try:
        _audit_log(tid, "approve", _audit_operator(request),
                   _task_service(task), task.get("script", "")[:300], "approved",
                   {"source": task.get("source", "")})
    except Exception:
        pass
    # 恢复任务: 执行前再次校验恢复命令在白名单内（安全边界）
    if task.get("source") == "recovery":
        script = task.get("script", "")
        from recovery_policy import check_allowed
        ok, reason = check_allowed(script)
        if not ok:
            raise Exception(f"恢复动作不在白名单内: {reason}")
        exec_result = _get_brain().execute_suggestion(
            task.get("service", ""), script, task.get("diagnosis", ""), task_id=tid)
        task["status"] = "done"
        task["report"] = f"恢复方案已审批并执行。\n操作: {script}\n结果:\n{exec_result}"
        task["done_at"] = time.strftime("%Y-%m-%dT%H:%M:%SZ")
        _task_store.persist(tid)
        return {"task": task}
    # AI Chat 建议任务: 通过后执行 script (若有)
    if task.get("source") == "ai_chat":
        exec_result = _get_brain().execute_suggestion(
            task.get("service", ""), task.get("script", ""), task.get("context", ""), task_id=tid)
        task["status"] = "done"
        task["report"] = f"已人工确认并执行建议。\n操作结果:\n{exec_result}"
        task["done_at"] = time.strftime("%Y-%m-%dT%H:%M:%SZ")
        try:
            _upload_report(tid, task["report"], service=task.get("service", ""),
                           question=task.get("context", "") or task.get("diagnosis", ""))
        except Exception: pass
    else:
        # LangGraph 任务: Resume with approval
        # approve_and_resume 已改为 async；sync handler 在线程池中运行，无 event loop，用 asyncio.run
        final = asyncio.run(_get_brain().approve_and_resume(tid, approved=True))
        task["status"] = "done"
        task["report"] = final.get("final_response", "")[:500]
        task["done_at"] = time.strftime("%Y-%m-%dT%H:%M:%SZ")
        if final.get("final_response"):
            try:
                _upload_report(tid, final["final_response"], service=task.get("service", ""),
                               question=task.get("context", "") or task.get("diagnosis", ""))
            except Exception: pass
    _task_store.persist(tid)  # 审批结果落 MySQL
    return {"task": task}


@app.post("/api/v1/ops/tasks/{tid}/reject")
def reject_task(tid: str, request: Request):
    """同步 handler：内部含阻塞的审批持久化 + 审计 MySQL 写入，放线程池执行。"""
    _require_approver(request)
    if tid not in _task_store:
        raise HTTPException(404, "task not found")
    task = _task_store[tid]
    task["status"] = "rejected"
    task["done_at"] = time.strftime("%Y-%m-%dT%H:%M:%SZ")
    try:
        _audit_log(tid, "reject", _audit_operator(request),
                   _task_service(task), task.get("script", "")[:300], "rejected",
                   {"source": task.get("source", "")})
    except Exception:
        pass
    try:
        # 仅 LangGraph 任务需要 resume 拒绝
        if task.get("source") != "ai_chat":
            # approve_and_resume 已改为 async；sync handler 在线程池中，用 asyncio.run
            asyncio.run(_get_brain().approve_and_resume(tid, approved=False))
    except: pass
    _task_store.persist(tid)  # 拒绝结果落 MySQL
    return {"task": task}


# ═══════════════════════════════════════════════════════════════
#  K8s 结构化动作: preflight / execute (工作流 C)
# ═══════════════════════════════════════════════════════════════

class K8sActionBody(BaseModel):
    action: str
    kind: str = ""
    namespace: str = ""
    name: str = ""
    extra: dict = None
    preflight_token: str = ""
    expected_resource_version: str = ""
    approval_task_id: str = ""


@app.post("/api/v1/ops/k8s/preflight")
def k8s_preflight(body: K8sActionBody, request: Request):
    """预检: 白名单校验 + 资源存在性 + resourceVersion + preflight token (TTL 5min)。"""
    _require_approver(request)
    import k8s_actions
    result = k8s_actions.preflight(body.action, kind=body.kind, namespace=body.namespace,
                                   name=body.name, **(body.extra or {}))
    if not result.get("ok"):
        raise HTTPException(400, result.get("error", "预检失败"))
    try:
        _audit_log(f"k8s:{body.action}", "k8s_preflight", _audit_operator(request),
                   f"{body.kind}/{body.name}", result.get("command", ""), "ok",
                   {"source": "k8s_actions"})
    except Exception:
        pass
    return result


@app.post("/api/v1/ops/k8s/execute")
def k8s_execute(body: K8sActionBody, request: Request):
    """执行: 审批(destructive) → preflight token → 乐观锁 → 执行 + 审计。"""
    _require_approver(request)
    import k8s_actions
    try:
        result = k8s_actions.execute_guarded(
            body.action, kind=body.kind, namespace=body.namespace, name=body.name,
            preflight_token=body.preflight_token,
            expected_resource_version=body.expected_resource_version,
            approval_task_id=body.approval_task_id,
            extra=body.extra or {},
            audit=lambda action, kind, name, out: _audit_log(
                f"k8s:{action}", "k8s_execute", _audit_operator(request),
                f"{kind}/{name}", body.action, "ok",
                {"output": str(out)[:500], "source": "k8s_actions"}))
    except k8s_actions.K8sActionError as e:
        raise HTTPException(e.status_code, str(e))
    return {"ok": True, "output": result["output"]}


@app.get("/api/v1/ops/recovery/policy")
async def get_recovery_policy():
    """读取恢复白名单（安全边界配置）。"""
    from recovery_policy import get_policy
    return get_policy()


@app.put("/api/v1/ops/recovery/policy")
async def put_recovery_policy(policy: dict, request: Request):
    """保存恢复白名单（需审批人/admin）。"""
    _require_approver(request)
    from recovery_policy import set_policy
    ok = set_policy(policy)
    return {"ok": ok}


@app.post("/api/v1/ops/recovery/plan")
async def gen_recovery_plan(payload: dict, request: Request):
    """基于调查结果生成恢复方案（AI），并创建审批任务（source=recovery）。

    payload: {service, diagnosis?, investigation?, script?}
    若调用方已提供 script（预设动作），直接使用；否则由 AI 生成恢复建议。
    安全(P0-4): 恢复方案含环境变更脚本，生成/预设均须审批人/admin 显式发起。
    """
    _require_approver(request)
    service = payload.get("service", "")
    if not service:
        raise HTTPException(400, "service required")
    script = payload.get("script", "")
    # 恢复白名单检查（预设 script 必须先通过）
    if script:
        from recovery_policy import check_allowed
        ok, reason = check_allowed(script)
        if not ok:
            raise HTTPException(400, f"恢复动作不在白名单内: {reason}")
    # 生成恢复方案文本
    if not script:
        script = "kubectl rollout restart deployment/%s" % service
    diagnosis = payload.get("diagnosis") or payload.get("investigation") or ""
    plan_text = f"恢复方案：\n- 操作: {script}\n- 影响面: 重启服务 {service}\n- 风险: 低（滚动重启，短暂中断）\n- 依据: {diagnosis[:200]}"
    # 创建审批任务（source=recovery）
    tid = f"rec-{int(time.time()*1000)}"
    _task_store[tid] = {
        "id": tid, "task_id": tid, "source": "recovery",
        "service_name": service, "service": service,
        "plan": plan_text, "script": script, "status": "pending",
        "risk_score": 2, "risk_reason": "滚动重启，影响面受控",
        "diagnosis": diagnosis, "requester": "ai",
        "created_at": time.strftime("%Y-%m-%dT%H:%M:%SZ"),
    }
    _task_store.persist(tid)
    return {"task_id": tid, "plan": plan_text, "script": script, "status": "pending"}


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
    return full_rca_analysis(req.service, cluster_id=getattr(req, "cluster_id", "all") or "all")


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
        "object": req.object,
        "namespace": req.namespace,
    }
    result = full_rca_analysis(req.service or "kubernetes", anomaly_event=anomaly_event,
                              cluster_id=getattr(req, "cluster_id", "all") or "all")
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
        "object": req.object,
        "namespace": req.namespace,
    }
    result = full_rca_analysis(req.service or "kubernetes", anomaly_event=anomaly_event,
                              cluster_id=getattr(req, "cluster_id", "all") or "all")
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
    raw = await asyncio.to_thread(get_service_list)
    try:
        svc_data = _json.loads(raw)
    except Exception:
        svc_data = []
    for svc in (svc_data if isinstance(svc_data, list) else []):
        name = svc.get("service_name", "")
        if not name:
            continue
        # 对关键指标做检测。
        # error_rate 来自 query-api 的真实错误率（errors/calls*100），而非延迟估算。
        metrics_vals = {
            "error_rate": float(svc.get("error_rate", 0) or 0),
            "p99_latency": float(svc.get("max_ms", 0) or 0),
            "request_rate": float(svc.get("traces", 0) or 0),
        }
        for metric, val in metrics_vals.items():
            # detect 内部会 feed 滑动窗口；此处不再显式 feed，避免同一数据点被
            # 喂进窗口两次导致统计基线污染（异常检测失真）。
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
async def list_anomalies(service: str = "", limit: int = 50):
    """查询历史异常事件（从 MySQL anomaly_events 表）"""
    limit = max(1, min(limit, 500))  # 上界 500，防止超大分页查询
    try:
        from db import db_available, get_conn
        import pymysql.cursors
        if not db_available():
            return {"anomaly_trends": [], "total": 0}
        conn = get_conn()
        if conn is None:
            return {"anomaly_trends": [], "total": 0}
        try:
            with conn.cursor(pymysql.cursors.DictCursor) as cur:
                sql = ("SELECT service_name, metric, value, method, severity, score, "
                       "detected_at FROM anomaly_events")
                args = []
                if service:
                    sql += " WHERE service_name=%s"
                    args.append(service)
                sql += " ORDER BY detected_at DESC LIMIT %s"
                args.append(limit)
                cur.execute(sql, args)
                rows = cur.fetchall()
            return {"anomaly_trends": rows, "total": len(rows)}
        finally:
            conn.close()
    except Exception as e:
        return {"anomaly_trends": [], "total": 0, "error": str(e)}


# ═══════════════════════════════════════════════════════════════
#  报告产物持久化 (P0: 移除 MinIO — AGPLv3 且已归档停更)
#  正文统一落 MySQL reports 表 (content 列) + 本地 AIOPS_DATA_DIR/reports 兜底,
#  不再依赖 MinIO 对象存储 / MINIO_* 环境变量。
# ═══════════════════════════════════════════════════════════════


def _extract_service_from_text(text: str) -> str:
    """从自然语言消息/报告文本中粗提取目标服务名（用于报告中心 service 归属）。
    匹配模式：'分析/诊断/巡检/排查 <服务>' 等。无法识别返回 ''。"""
    if not text:
        return ""
    # 常见指令前缀 + 服务名（取到空白/换行/标点前）
    m = re.search(r'(?:分析|诊断|巡检|排查|查看|检查|诊断一下|分析一下|巡检一下)\s*[:：]?\s*([A-Za-z0-9_.\-]{2,40})', text)
    if m:
        return m.group(1).strip()
    # 报告首行形如 '**目标**: xxx' 或 '服务 xxx 诊断报告'
    m2 = re.search(r'(?:\*\*目标\*\*|目标|服务)\s*[:：]\s*([A-Za-z0-9_.\-]{2,40})', text)
    if m2:
        return m2.group(1).strip()
    return ""


def _task_service(task: dict) -> str:
    """审计日志目标服务：优先 task.service；ai_chat 任务 service 为空时从 context 里的消息提取。"""
    svc = (task.get("service") or "").strip()
    if svc:
        return svc
    ctx = task.get("context") or ""
    # ai_chat context 形如 "[AI Chat] 分析 frontend 服务状态"
    msg = re.sub(r'^\[AI Chat\]\s*', '', ctx)
    return _extract_service_from_text(msg)


def _upload_report(task_id: str, content: str, filename: str = "report.md", service: str = "", question: str = ""):
    """持久化任务报告：markdown 正文写入 MySQL reports 表 (content 列, 含元数据)，
    并落盘 AIOPS_DATA_DIR/reports/{task_id}/{filename} 作本地备份（替代 MinIO 对象存储）。
    返回相对对象名（兼容旧返回语义）。question: 原始用户问题（报告中心意图判定）。"""
    obj_name = f"{task_id}/{filename}"
    # 本地文件兜底 (PVC 挂载目录, 替代 MinIO)
    try:
        import os as _os
        data_dir = _os.environ.get("AIOPS_DATA_DIR", "/var/lib/aiops")
        local_path = _os.path.join(data_dir, "reports", task_id, filename)
        _os.makedirs(_os.path.dirname(local_path), exist_ok=True)
        with open(local_path, "w", encoding="utf-8") as f:
            f.write(content or "")
    except Exception as e:
        print(f"[reports] 本地落盘失败: {e}")
    # MySQL reports 表持久化（正文在 content 列，ReportStore 已兼容 llm_mode 缺列降级）
    try:
        _persist_inspection_report(task_id, service or "", content, filename, question)
    except Exception as e:
        print(f"[reports] MySQL 持久化失败: {e}")
    return obj_name


# ═══════════════════════════════════════════════════════════════
#  ClickHouse 巡检报告持久化
# ═══════════════════════════════════════════════════════════════

_CH_HOST = os.environ.get("CLICKHOUSE_HOST", "clickhouse.observability.svc.cluster.local")
_CH_PORT = os.environ.get("CLICKHOUSE_PORT", "8123")
_CH_USER = os.environ.get("CLICKHOUSE_USER", "default")
_CH_PASSWORD = os.environ.get("CLICKHOUSE_PASSWORD", "")


def _ch_query_json(sql: str) -> list:
    """执行 SELECT 并以 JSONEachRow 返回 dict 列表（供 NL→SQL 结果展示）。"""
    import base64
    import urllib.parse
    import urllib.request
    import json as _json
    url = (f"http://{_CH_HOST}:{_CH_PORT}/?query="
           + urllib.parse.quote(sql) + "&default_format=JSONEachRow")
    req = urllib.request.Request(url)
    if _CH_PASSWORD:
        # Basic Auth（与 query-api/ingest 一致），避免 CH 裸奔
        token = base64.b64encode(f"{_CH_USER}:{_CH_PASSWORD}".encode()).decode()
        req.add_header("Authorization", f"Basic {token}")
    with urllib.request.urlopen(req, timeout=20) as resp:
        raw = resp.read().decode("utf-8", errors="replace")
    rows = []
    for line in raw.splitlines():
        if line.strip():
            try:
                rows.append(_json.loads(line))
            except Exception:
                pass
    return rows


def _extract_report_fields(content: str, service: str, filename: str, question: str = "") -> dict:
    """从自由文本报告中启发式抽取结构化字段（verdict / risk_score / summary / report_type）。

    P1-3: 信息查询类问题（如"当前有哪些服务在运行?"）不做异常判定 → verdict="信息", risk=0；
    故障诊断类才按异常证据计算 0~1 风险分（不再硬编码 0.85）。
    """
    low = content.lower()

    # 信息查询意图判断：优先于内容关键词（"有哪些服务/列表/总结" 且无故障语义）
    if _is_info_query(question):
        verdict = "信息"
        risk = 0.0
    else:
        # 健康判定：兼容中英文关键词，未命中时保持 unknown（前端统一兜底展示为 '-'）
        # 先剥离否定语境（"无异常/未发现异常" 含 "异常" 子串，不能误判为 异常）
        low_neg_free = (low.replace("无异常", "").replace("未发现异常", "")
                        .replace("没有异常", "").replace("未发现异常", ""))
        verdict = "unknown"
        if any(k in low_neg_free for k in [
            "异常", "高危", "严重", "中高风险", "高风险", "异常告警",
            "critical", "error", "failed", "unhealthy", "degraded",
        ]):
            verdict = "异常"
        elif any(k in low for k in [
            "关注", "中风险", "注意", "潜在风险",
            "warning", "warn", "attention", "concerning", "caution",
        ]):
            verdict = "关注"
        elif any(k in low for k in [
            "健康", "正常", "良好", "无异常", "稳定",
            "ok", "healthy", "normal", "green", "all good", "no issue",
        ]):
            verdict = "健康"

        # P1-3b: 风险分 0~1 —— 基于诊断结论中的异常证据（活跃告警 critical 数、
        # 错误率>5% 服务数、错误率峰值）计算；无异常证据 = 0，不再硬编码 0.85。
        risk = _risk_from_evidence(content)

    # 报告类型
    rtype = "inspection" if ("巡检" in content or "inspection" in low or "检查" in content) else "report"

    # 摘要：截取报告开头若干行作为 summary（健壮处理短行/审批结果类报告）
    summary = ""
    first_line = ""
    for line in content.splitlines():
        line = line.strip()
        if not line:
            continue
        # 跳过标题/分隔
        if line.startswith("#") or line.startswith("---") or line.startswith("|") or line.startswith("**时间**"):
            continue
        if not first_line:
            first_line = line[:200]
        if len(line) > 20:
            summary = line[:200]
            break
    if not summary:
        # 所有行都短（如"已人工确认并执行建议…"）时，取首个非空行作为摘要
        summary = first_line
    return {"verdict": verdict, "risk_score": risk, "report_type": rtype, "summary": summary}


def _persist_inspection_report(task_id: str, service: str, content: str, filename: str = "report.md", question: str = ""):
    """将报告写入 MySQL reports 表（ReportStore）。question 用于报告中心意图判定。"""
    from db_agents import ReportStore
    fields = _extract_report_fields(content, service, filename, question)
    from orchestrator import _llm_key_ready
    ReportStore().save({
        "task_id": task_id, "service_name": service or "-",
        "report_type": fields["report_type"], "verdict": fields["verdict"],
        "risk_score": fields["risk_score"], "summary": fields["summary"],
        "content": content,
        "llm_mode": "llm" if _llm_key_ready() else "deterministic",
    })
    return task_id


# ── 巡检报告查询接口 ────────────────────────────────────────────

@app.get("/api/v1/ops/artifacts")
async def list_artifacts_endpoint(limit: int = 50, type_filter: str = ""):
    """产物中心聚合（C3）：统一聚合报告/审批单/工作流运行，按时间倒序。"""
    from artifacts import list_artifacts, TYPE_LABELS
    items = list_artifacts(limit=limit)
    if type_filter:
        items = [it for it in items if it["type"] == type_filter]
    return {"artifacts": items, "total": len(items), "labels": TYPE_LABELS}


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
            "llm_mode": r.get("llm_mode") or "llm",  # P0-1c: 报告生成时记录的 LLM 模式
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
    """Download task report. 优先 MySQL reports 表 content 列; 降级读本地
    AIOPS_DATA_DIR/reports/{task_id}/report.md (替代 MinIO 对象存储)。"""
    from fastapi.responses import PlainTextResponse
    content = ""
    try:
        from db_agents import ReportStore
        row = ReportStore().get_by_task_id(task_id)
        if row and row.get("content"):
            content = row["content"]
    except Exception:
        row = None
    if not content:
        # 降级: 本地文件
        import os as _os
        data_dir = _os.environ.get("AIOPS_DATA_DIR", "/var/lib/aiops")
        local_path = _os.path.join(data_dir, "reports", task_id, "report.md")
        try:
            with open(local_path, encoding="utf-8") as f:
                content = f.read()
        except Exception:
            raise HTTPException(404, "report not found")
    return PlainTextResponse(content, media_type="text/markdown",
                             headers={"Content-Disposition": f"attachment; filename={task_id}-report.md"})


@app.get("/api/v1/ops/reports")
async def list_reports():
    """List all stored reports (来自 MySQL reports 表, 替代 MinIO 对象列表)。"""
    try:
        from db_agents import ReportStore
        result = ReportStore().list(page=1, size=1000)
        return {"reports": [
            {"name": f"{r.get('task_id', '')}/report.md",
             "task_id": r.get("task_id", ""),
             "size": len(r.get("content") or ""),
             "last_modified": str(r.get("created_at") or ""),
             "service_name": r.get("service_name", ""),
             "report_type": r.get("report_type", "")}
            for r in result["items"]],
            "count": len(result["items"])}
    except Exception as e:
        return {"reports": [], "count": 0, "error": str(e)}


# ═══════════════════════════════════════════════════════════════
#  审计日志 / 知识库 / 规则（MySQL 持久化）
# ═══════════════════════════════════════════════════════════════

@app.get("/api/v1/ops/audit-logs")
def list_audit_logs(action: str = "", operator: str = "", service: str = "",
                    page: int = 1, size: int = 50):
    """同步 handler：内部含阻塞的 MySQL 查询，放线程池执行。"""
    from db_audit import AuditStore
    return AuditStore().query(page=page, size=size,
                              action=action or None, operator=operator or None, service=service or None)


@app.get("/api/v1/ai/knowledge")
async def list_knowledge(q: str = "", page: int = 1, size: int = 50, type: str = "knowledge"):
    """知识库列表。type: all=全部 | case=故障案例 | knowledge=知识文档（已废弃, 返回空列表）。
    统一从 ChromaDB 读取（单一真源），支持关键词过滤与分页。"""
    from rag import rag
    if type == "knowledge":
        # 知识文档类型已废弃：不再创建/展示该类型，保持字段兼容返回空列表
        return {"items": [], "total": 0}
    if type == "all":
        items = rag.list_all(type_filter="", q=q, limit=size, offset=(page - 1) * size)
        total = len(rag.list_all(type_filter="", q=q, limit=100000))
    else:
        items = rag.list_all(type_filter=type, q=q, limit=size, offset=(page - 1) * size)
        total = len(rag.list_all(type_filter=type, q=q, limit=100000))
    return {"items": items, "total": total}


# ═══════════════════════════════════════════════════════════════
#  内置运维 playbook 浏览（工作流 E4）— 列表(按分类) / 原文
# ═══════════════════════════════════════════════════════════════

@app.get("/api/v1/ai/knowledge/playbooks")
async def knowledge_playbooks(category: str = "", q: str = ""):
    """playbook 列表: 按分类目录扫描。q 非空时走向量检索(ops_playbooks)。"""
    if q:
        from playbook_loader import query_knowledge
        try:
            return query_knowledge(q, path_prefix=category or None)
        except Exception:
            return {"items": []}
    import os
    from playbook_loader import _default_playbooks_dir
    base = _default_playbooks_dir()
    items = []
    if os.path.isdir(base):
        for root, _, files in os.walk(base):
            for fn in sorted(files):
                if not fn.endswith(".md"):
                    continue
                rel = os.path.relpath(os.path.join(root, fn), base)
                cat = rel.split(os.sep)[0]
                if category and cat != category:
                    continue
                items.append({"path": rel, "category": cat, "title": fn[:-3]})
    return {"playbooks": items}


@app.get("/api/v1/ai/knowledge/playbooks/{path:path}")
async def knowledge_playbook_detail(path: str):
    """playbook 原文（路径逃逸校验）。"""
    import os
    from playbook_loader import _default_playbooks_dir
    base = os.path.realpath(_default_playbooks_dir())
    target = os.path.realpath(os.path.join(base, path))
    if not (target.startswith(base + os.sep) or target == base):
        raise HTTPException(400, "非法路径")
    if not os.path.isfile(target) or not target.endswith(".md"):
        raise HTTPException(404, "playbook not found")
    with open(target, encoding="utf-8") as f:
        return {"path": path, "content": f.read()}


@app.post("/api/v1/ai/knowledge")
async def add_knowledge(body: dict = None):
    from db_agents import KnowledgeStore
    b = body or {}
    kid = KnowledgeStore().add(b.get("title", ""), b.get("content", ""),
                               b.get("source", "manual"), b.get("tags", ""), b.get("code_ref"))
    return {"ok": True, "id": kid}


@app.delete("/api/v1/ai/knowledge/{kid}")
async def delete_knowledge(kid: str):
    """删除知识库条目（ChromaDB 统一存储，case 与 knowledge 类型均可按 id 删除，
    id 形如 kn-xxxx / case_id 的 md5/hex 字符串）。"""
    from db_agents import KnowledgeStore
    ok = KnowledgeStore().delete(kid)
    return {"ok": ok}


# ── RAG 故障案例库管理（ChromaDB, 供 AI 诊断检索） ──────────────
@app.get("/api/v1/ai/knowledge/rag/stats")
async def rag_knowledge_stats():
    """统一知识库统计：故障案例总数（知识文档已废弃, knowledge 恒为 0）。
    total/cases 均为全部案例数, 保持字段兼容前端展示。"""
    from rag import rag
    try:
        items = rag.list_all(limit=100000)
        cases = sum(1 for i in items if (i.get("type") or "case") == "case")
    except Exception:
        cases = 0
    return {"collection": "ops_cases", "total": cases, "cases": cases, "knowledge": 0}


@app.post("/api/v1/ai/knowledge/rag/reload")
async def rag_knowledge_reload():
    """重载项目内置知识库文件 (data/knowledge_cases.json)，幂等去重。
    上线后向该文件追加新案例并调用此接口即可增量导入，无需重建镜像。"""
    from knowledge_seed import seed_default
    r = seed_default()
    return {"ok": True, **r}


@app.post("/api/v1/ai/knowledge/rag/import")
async def rag_knowledge_import(body: dict = None):
    """单条导入 RAG 案例 (供运行时动态新增，写入 ChromaDB 并落盘到持久目录)。
    入库前校验：symptom 长度≥8 且非纯信息查询意图（复用 orchestrator 质量判定），
    防止"总结一下集群状况"这类对话文本被误存为故障案例。"""
    b = body or {}
    symptom = (b.get("symptom", "") or "").strip()
    if len(symptom) < 8:
        raise HTTPException(400, "symptom 过短 (至少 8 字符)")
    from orchestrator import _is_info_query
    if _is_info_query(symptom):
        raise HTTPException(400, "疑似信息查询/对话意图, 非故障案例, 拒绝入库")
    service = b.get("service", "kubernetes")
    import hashlib
    cid = hashlib.md5(symptom.encode()).hexdigest()[:12]
    from rag import rag
    case = {
        "case_id": cid,
        "service": service,
        "symptom": symptom,
        "root_cause": b.get("root_cause", ""),
        "plan": b.get("plan", ""),
        "outcome": b.get("outcome", "success"),
        "report": f"[{service}] 故障案例: {symptom}",
    }
    r = rag.add_case(case)
    return {"ok": True, "case_id": cid, "inserted": r == cid}


def _report_section(content: str, headers: tuple) -> str:
    """从 markdown 报告中抽取第一个命中 headers 的小节正文，取前 500 字符。
    锚点须为标题行：`#` 开头，或 `**标题**` 纯粗体标题行（不含冒号内容，
    避免 "**目标**: ..." 这类含关键词的内容行被误判为标题）。
    遇到下一个 # 标题即停止收集；重复的同类标题行视为同一节跳过。"""
    if not content:
        return ""
    capture = False
    buf = []
    for ln in content.splitlines():
        s = ln.strip()
        if not capture:
            # 标题行判定: # 开头; 或 ** 开头且为纯粗体标题(去除 ** 后不含 ':')
            is_heading = False
            if s.startswith("#"):
                is_heading = True
            elif s.startswith("**") and s.endswith("**") and ":" not in s:
                is_heading = True
            if is_heading:
                title = s.lstrip("#").lstrip("*").strip()
                if any(h in title for h in headers):
                    capture = True
            continue
        if s.startswith("#"):
            # 下一个标题: 若与当前小节同类(重复标题)则跳过继续, 否则结束
            t = s.lstrip("#").strip()
            if any(h in t for h in headers):
                continue
            break
        buf.append(s)
    return "\n".join(buf).strip()[:500]


@app.post("/api/v1/ai/knowledge/case")
async def add_knowledge_case(body: dict = None):
    """手动将 AI 处理完成的故障加入知识库（故障案例）。
    模式A（推荐）: {report_id} 或 {task_id} → 从 reports 表取报告解析入库；
    模式B: {service, symptom, root_cause, plan} 直接入库。
    复用 orchestrator._case_quality_check 质量审查；rag.add_case 内置 0.92 相似度去重。"""
    b = body or {}
    task_id = (b.get("report_id") or "").strip() or (b.get("task_id") or "").strip()
    if task_id:
        # ── 模式A: 按报告入库（从 MySQL reports 表按 task_id 取报告）──
        from db_agents import ReportStore
        report = ReportStore().get_by_task_id(task_id)
        if not report:
            raise HTTPException(404, f"报告不存在: {task_id}")
        content = report.get("content") or ""
        service = (report.get("service_name") or "").strip() or "unknown"
        if service == "-":
            service = "unknown"
        # 原始用户问题未持久化，best-effort 从会话 checkpoint 取（task_id 即 thread_id）
        question = ""
        try:
            vals = _get_brain().get_session_state(task_id) or {}
            question = (vals.get("user_message") or "").strip()
        except Exception:
            question = ""
        fields = _extract_report_fields(content, service, "report.md", question=question)
        summary = fields.get("summary") or ""
        symptom = (question or summary or content)[:200]
        root_cause = _report_section(content, ("诊断结论", "根因"))
        plan = _report_section(content, ("处置方案", "执行计画", "建议"))
    else:
        # ── 模式B: 直接传字段 ──
        service = (b.get("service") or "").strip() or "unknown"
        symptom = (b.get("symptom") or "").strip()[:200]
        root_cause = (b.get("root_cause") or "").strip()
        plan = (b.get("plan") or "").strip()
    # 公共：质量审查（_case_quality_check 接收 state dict 结构）
    # 手动入库为结构化字段(根因+方案完整)，分析字段拼入 root_cause+plan，
    # 满足"有实质内容"检查，同时保留查询意图/过短/占位符过滤。
    analysis_text = (root_cause or symptom) + "\n" + plan
    ok, reason = _case_quality_check({
        "user_message": symptom, "plan": plan,
        "crewai_result": analysis_text, "report": analysis_text,
    })
    if not ok:
        raise HTTPException(400, f"质量审查未通过: {reason}")
    import hashlib
    cid = hashlib.md5(symptom.encode()).hexdigest()[:12]  # 与 knowledge_seed.load_case 一致
    from rag import rag, infer_case_tags
    # 自动补标签：未显式传 tags 时按 service/symptom/plan 关键词推断领域标签；
    # type 透传（缺省 case）。质量审查逻辑保持不变（上方已执行）。
    tags = (b.get("tags") or "").strip() or infer_case_tags(service, symptom, plan)
    case = {
        "case_id": cid,
        "type": (b.get("type") or "case").strip() or "case",
        "service": service,
        "symptom": symptom,
        "root_cause": root_cause,
        "plan": plan,
        "outcome": "success",
        "tags": tags,
        "report": f"[{service}] 故障案例: {symptom}",
    }
    r = rag.add_case(case)
    resp = {"ok": True, "case_id": r, "inserted": r == cid, "validated": "pending"}
    if r != cid:
        resp["message"] = "已存在相似案例"
    # ── 复盘回写：best-effort 将案例写入知识图谱（失败仅日志，不阻断入库）──
    try:
        from kg_graph import upsert_node, upsert_edge
        case_node_id = upsert_node("case", cid, {
            "service": service,
            "symptom": symptom[:200],
            "root_cause": (root_cause or "")[:200],
            "cluster_id": "default",
            "created_by": "auto",
        })
        svc = (service or "").strip()
        if svc and svc != "unknown":
            service_id = upsert_node("service", svc, {
                "cluster_id": "default",
                "created_by": "auto",
            })
            upsert_edge(service_id, case_node_id, "MENTIONED_IN", {
                "case_id": cid,
                "created_by": "auto",
            })
        # 说明：CAUSED_BY（根因实体识别）需从 root_cause 中提取疑似根因实体，
        # 实现复杂且易误判，留待后续；此处仅建立 MENTIONED_IN 关联边。
    except Exception as e:  # noqa: BLE001
        print(f"[kg回写] 失败: {e}")
    return resp


# ═══════════════════════════════════════════════════════════════
#  告警事件自动入库（草稿）— 端点 + 定时任务
# ═══════════════════════════════════════════════════════════════

def _fetch_alert_events(limit: int = 50) -> list:
    """从 query-api 拉取告警事件（status=firing/resolved），容错失败返回空列表。

    复用 orchestrator._collect_alerts 的调用模式：GET /api/v1/alerts/events?limit=N，
    携带 X-Internal-Token。过滤仅保留 firing / resolved 状态（缺省视为 firing）。
    """
    import urllib.request
    qa = os.environ.get("QUERY_API_URL", "http://query-api.observability.svc.cluster.local:8080/api/v1")
    token = os.environ.get("INTERNAL_TOKEN", "")
    try:
        req = urllib.request.Request(f"{qa}/alerts/events?limit={limit}", method="GET")
        if token:
            req.add_header("X-Internal-Token", token)
        with urllib.request.urlopen(req, timeout=10) as resp:
            data = json.loads(resp.read().decode())
        events = data.get("data", []) or []
        # 仅保留 firing / resolved 状态告警（缺省视为 firing）
        kept = [e for e in events if (e.get("status") or "firing") in ("firing", "resolved")]
        return kept
    except Exception as e:  # noqa: BLE001
        print(f"[from-alerts] 拉取告警事件失败: {e}")
        return []


def _ingest_alerts_to_cases(limit: int = 50) -> dict:
    """将告警事件自动入库为 case 草稿。

    注意：plan 为空过不了 _case_quality_check，因此本函数**不走质量审查**，
    直接 rag.add_case（内置 0.92 相似度去重），outcome=pending、tags=auto-alert。
    每条告警（rule_name/service/message）构造：
      symptom   = rule_name + message 前 120 字
      root_cause= 由告警自动生成，待人工确认（告警/服务）
      plan      = 空
    返回 {added, dup, total}。
    """
    import hashlib
    from rag import rag, infer_case_tags
    events = _fetch_alert_events(limit)
    added, dup = 0, 0
    for e in events:
        rule_name = (e.get("rule_name") or "").strip() or "unknown-alert"
        service = (e.get("service") or "").strip() or "unknown"
        message = (e.get("message") or "").strip()
        symptom = (f"{rule_name} {message}".strip())[:120]
        root_cause = f"由告警自动生成，待人工确认（告警: {rule_name}, 服务: {service}）"
        cid = hashlib.md5(symptom.encode()).hexdigest()[:12]  # 与 knowledge_seed.load_case 一致
        tags = "auto-alert"
        inferred = infer_case_tags(service, symptom, "")
        if inferred:
            tags = f"{tags},{inferred}"
        case = {
            "case_id": cid,
            "type": "case",
            "service": service,
            "symptom": symptom,
            "root_cause": root_cause,
            "plan": "",
            "outcome": "pending",
            "tags": tags,
            "source": "alert",
            "title": symptom[:80],
            "report": f"[{service}] 告警自动生成: {rule_name}",
        }
        try:
            r = rag.add_case(case)
            if r == cid:
                added += 1
            else:
                dup += 1
        except Exception as ex:  # noqa: BLE001 单条失败不影响整体
            print(f"[from-alerts] 单条告警入库失败: {ex}")
    return {"added": added, "dup": dup, "total": len(events)}


@app.post("/api/v1/ops/cases/from-alerts")
async def from_alerts(body: dict = None):
    """告警自动入库草稿：拉取 query-api 告警事件（status=firing/resolved）转为 case 草稿。

    不经过 _case_quality_check（plan 为空无法通过），直接 rag.add_case，
    outcome=pending、tags=auto-alert；去重由 rag.add_case 内置 0.92 相似度保证。
    本服务每 15 分钟定时调用；也可由外部 cron 调用本端点。
    请求体可选: {"limit": 50}（上限 500）。
    """
    b = body or {}
    try:
        limit = int(b.get("limit") or 50)
    except Exception:  # noqa: BLE001
        limit = 50
    limit = max(1, min(limit, 500))
    return _ingest_alerts_to_cases(limit)


async def _scheduled_alert_to_case():
    """定时任务：每 15 分钟将告警事件自动入库为 case 草稿（失败仅打日志，不抛错）。"""
    try:
        res = await asyncio.to_thread(_ingest_alerts_to_cases, 50)
        print(f"[scheduler] alert-to-case 入库完成: {res}")
    except Exception as e:  # noqa: BLE001
        print(f"[scheduler] alert-to-case error: {e}")


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
    "observability.log_records(service_name,timestamp,severity,body,trace_id). "
    "严格遵守以下规则："
    "1. 无论用户是否提到时间，trace_spans/log_records 查询都必须带时间过滤（默认最近 24 小时），例如 "
    "trace_spans 用 `start_time >= now() - INTERVAL 24 HOUR`，log_records 用 `timestamp >= now() - INTERVAL 24 HOUR`；"
    "用户提到具体时间（近1小时/近7天等）时按其指定窗口。"
    "2. 用户要求'调用量最高/最多'时用 `ORDER BY count() DESC` 或 `ORDER BY calls DESC`；"
    "'错误率最高'用 `ORDER BY error_rate DESC`；严格按用户指定的指标排序，不要随意改排序字段。"
    "3. 用户要求前 N 个（如5个）时用 `LIMIT N`，且 N 等于用户数字。"
    "4. 计算错误率用 `round(countIf(is_error=1)/count()*100, 2) AS error_rate`（trace_spans）。"
    "5. 涉及 trace_spans 聚合时 GROUP BY 之后才可 SELECT 聚合与分组成员。"
    "6. 只返回 SQL 本体，不要任何解释、注释或 markdown 代码块。"
)


def _extract_time_window(question: str) -> tuple:
    """从问题中提取时间窗口："近 N 分钟/小时/天"（支持中文数字与阿拉伯数字）。
    如 "近1小时"→(1, "HOUR")、"近30分钟"→(30, "MINUTE")、"24小时"→(24, "HOUR")、"近1天"→(1, "DAY")。
    未识别默认 (24, "HOUR")。unit ∈ {"MINUTE","HOUR","DAY"}。"""
    _cn = {"一": 1, "二": 2, "两": 2, "三": 3, "四": 4, "五": 5, "六": 6, "七": 7, "八": 8, "九": 9}
    _units = {"分钟": "MINUTE", "分": "MINUTE", "小时": "HOUR", "钟头": "HOUR", "天": "DAY", "日": "DAY"}
    q = (question or "").strip()
    m = re.search(r"(?:近|最近|过去|前)?\s*([0-9]+|[一二两三四五六七八九])\s*(分钟|分|小时|钟头|天|日)", q)
    if m:
        v = m.group(1)
        value = int(v) if v.isdigit() else _cn.get(v, 24)
        if value <= 0:
            value = 1
        return value, _units[m.group(2)]
    return 24, "HOUR"


def _fallback_nl2sql(question: str) -> str:
    """在 LLM 不可用 / mock 输出非 SQL 时，根据问题关键词生成一条白名单内、确定性的合法 SQL。
    仅支持白名单表，始终 SELECT + 加 LIMIT 护栏，确保通过 validate_sql。
    P1-1: 时间窗口从问题中解析（近 N 分钟/小时/天），不再恒为 INTERVAL 24 HOUR。
    P1-4: 错误类查询按**错误率**（countIf 比例）排序而非错误总量，聚焦 Top 10（LIMIT 10）。
    """
    q = question.lower()
    value, unit = _extract_time_window(question)
    iv = f"INTERVAL {value} {unit}"
    if "错误" in q or "error" in q:
        if "日志" in q or "log" in q:
            return (f"SELECT service_name, countIf(severity = 'error') AS errors, count() AS logs, "
                    f"round(countIf(severity = 'error') / count() * 100, 2) AS error_rate "
                    f"FROM observability.log_records WHERE timestamp >= now() - {iv} "
                    f"GROUP BY service_name ORDER BY error_rate DESC LIMIT 10")
        return (f"SELECT service_name, countIf(is_error = 1) AS errors, count() AS calls, "
                f"round(countIf(is_error = 1) / count() * 100, 2) AS error_rate "
                f"FROM observability.trace_spans WHERE start_time >= now() - {iv} "
                f"GROUP BY service_name ORDER BY error_rate DESC LIMIT 10")
    if "日志" in q or "log" in q:
        return f"SELECT service_name, count() AS logs FROM observability.log_records WHERE timestamp >= now() - {iv} GROUP BY service_name ORDER BY logs DESC LIMIT 10"
    if "拓扑" in q or "topology" in q or "调用关系" in q:
        return (f"SELECT source_service, destination_service, calls, error_rate "
                f"FROM observability.service_topology WHERE time_bucket >= now() - {iv} ORDER BY calls DESC LIMIT 10")
    if "延迟" in q or "latency" in q or "响应" in q:
        return (f"SELECT service_name, quantile(0.95)(duration_ns)/1000000 AS p95_ms, avg(duration_ns)/1000000 AS avg_ms "
                f"FROM observability.trace_spans WHERE start_time >= now() - {iv} GROUP BY service_name ORDER BY p95_ms DESC LIMIT 10")
    # 默认：近窗口各服务调用量/错误率（附错误率字段便于前端按异常排序）
    return (f"SELECT service_name, count() AS calls, countIf(is_error = 1) AS errors, "
            f"round(countIf(is_error = 1) / count() * 100, 2) AS error_rate "
            f"FROM observability.trace_spans WHERE start_time >= now() - {iv} "
            f"GROUP BY service_name ORDER BY calls DESC LIMIT 10")


@app.post("/api/v1/ai/nl2sql/translate")
async def nl2sql_translate(body: dict = None, request: Request = None):
    b = body or {}
    question = (b.get("question") or "").strip()
    if not question:
        raise HTTPException(400, "question is required")

    # P1-4: 与其他端点一致，从请求头解析 LLM 配置（优先 X-LLM-API-Key / 已保存配置），
    # 不再依赖 brain.llm_config 残留状态（密钥持有者生命周期由 set_llm_config 统一管理）。
    if request is not None:
        _parse_llm_config(request)

    sql_raw = ""
    try:
        cfg = _get_brain().llm_config
        # P2: async 端点内改用 _llm_async（内部 asyncio.to_thread + 共享线程池），
        # 不阻塞 event loop；同时修复此前 _llm 未在模块层导入导致的 NameError 静默 fallback。
        sql_raw = await _llm_async(cfg, _NL2SQL_SYSTEM, question, role="ClickHouse SQL 专家")
    except Exception:
        sql_raw = ""
    sql_raw = extract_sql_from_markdown(sql_raw or "")
    used_fallback = False
    if not validate_sql(sql_raw):
        # LLM 不可用 / mock 输出非 SQL / 校验不过：回退到确定性 SQL，保证功能可用
        sql_raw = _fallback_nl2sql(question)
        used_fallback = True
        if not validate_sql(sql_raw):
            return {"error": "生成的 SQL 未通过安全校验，请重试或简化查询",
                    "sql": sql_raw, "id": None, "pending": False, "used_fallback": True}
    sql = normalize_sql(sql_raw)
    # P1-1: fallback 时把解析出的时间窗口写入 explanation（如 "近1小时错误率最高的服务 (时间窗口: 1小时)"）
    if used_fallback:
        value, unit = _extract_time_window(question)
        label = {"MINUTE": "分钟", "HOUR": "小时", "DAY": "天"}.get(unit, unit)
        explanation = f"{question} (时间窗口: {value}{label})"
    else:
        explanation = question
    item = new_item(sql, explanation)
    sid = _nl2sql_store.save(item)
    # P1-4: 显式返回 used_fallback，前端可据此提示"AI 降级为模板 SQL"
    return {"id": sid, "sql": sql, "explanation": explanation, "pending": True,
            "used_fallback": used_fallback}


@app.post("/api/v1/ai/nl2sql/{sid}/execute")
def nl2sql_execute(sid: str):
    """同步 handler：内部含阻塞的 ClickHouse 查询 + 审计 MySQL 写入，
    用同步 def 让 FastAPI 放入线程池执行，避免 async 事件循环中同步 DB 写入被吞。"""
    item = _nl2sql_store.get(sid)
    if not item:
        raise HTTPException(404, "not found")
    if item.get("status") == "executed":
        raise HTTPException(409, "already executed")
    # P1-4 安全: 执行前重新跑 validate_sql，不过则拒绝执行——
    # 防止存储层被污染（LLM 原文/越权表/拼接注入）时执行任意 SQL。
    if not validate_sql(item.get("sql", "")):
        return {"error": "SQL 未通过安全校验，已拒绝执行（存储层可能被污染）",
                "columns": [], "rows": [], "count": 0}
    try:
        rows = _ch_query_json(item["sql"])
    except Exception as e:
        return {"error": str(e), "columns": [], "rows": [], "count": 0}
    columns = list(rows[0].keys()) if rows else []
    try:
        _audit_log(item.get("id", "") or "manual", "nl2sql", "system", "", item["sql"],
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


def _seed_knowledge_bg():
    """后台线程加载知识库，避免阻塞 startup；仅首次冷启动导入，后续幂等跳过。"""
    try:
        import logging
        from knowledge_seed import seed_default
        r = seed_default()
        logging.getLogger("knowledge_seed").info("知识库自动加载完成: %s", r)
    except Exception as e:  # noqa: BLE001
        import logging
        logging.getLogger("knowledge_seed").warning("知识库自动加载失败(不影响启动): %s", e)


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
#  变更时间线 Change Events（P1-1）
# ═══════════════════════════════════════════════════════════════

class ChangeEventRequest(BaseModel):
    cluster_id: str = "default"
    service: str = ""
    change_type: str = ""
    operator: str = ""
    content: str = ""
    related_trace_ids: str = ""


@app.post("/api/v1/ops/changes")
async def create_change_event(req: ChangeEventRequest, request: Request):
    """记录一条运维变更到变更时间线，返回变更 id。写入审计（复用 _audit_log）。"""
    service = (req.service or "").strip()
    change_type = (req.change_type or "").strip()
    content = (req.content or "").strip()
    if not service or not change_type:
        raise HTTPException(400, "service and change_type required")
    if not content:
        raise HTTPException(400, "content required")
    operator = (req.operator or "").strip() or _audit_operator(request)
    cid = (req.cluster_id or "default").strip() or "default"
    trace_ids = (req.related_trace_ids or "").strip()
    from db import db_available, get_conn
    if not db_available():
        raise HTTPException(503, "MySQL unavailable, cannot record change event")
    conn = get_conn()
    try:
        with conn.cursor() as cur:
            cur.execute(
                "INSERT INTO change_events (cluster_id, service, change_type, operator, content, related_trace_ids) "
                "VALUES (%s,%s,%s,%s,%s,%s)",
                (cid, service, change_type, operator, content, trace_ids or None))
            new_id = cur.lastrowid
        conn.commit()
    except Exception as e:
        try:
            conn.rollback()
        except Exception:
            pass
        raise HTTPException(500, f"insert change_event failed: {e}")
    finally:
        conn.close()
    try:
        _audit_log(f"change:{new_id}", "change_event", operator, service,
                   content[:300], "ok",
                   {"change_type": change_type, "cluster_id": cid, "related_trace_ids": trace_ids})
    except Exception:
        pass
    return {"ok": True, "id": new_id}


@app.get("/api/v1/ops/changes")
async def list_change_events(service: str = "", cluster_id: str = "", limit: int = 50):
    """变更时间线列表（按 created_at 倒序）。"""
    limit = max(1, min(limit, 500))
    from db import db_available, get_conn
    if not db_available():
        return {"changes": [], "total": 0}
    conn = get_conn()
    try:
        where, vals = [], []
        if service:
            where.append("service=%s"); vals.append(service)
        if cluster_id:
            where.append("cluster_id=%s"); vals.append(cluster_id)
        w = (" WHERE " + " AND ".join(where)) if where else ""
        sql = "SELECT * FROM change_events" + w + " ORDER BY created_at DESC, id DESC LIMIT %s"
        with conn.cursor() as cur:
            cur.execute(sql, vals + [limit])
            rows = cur.fetchall()
        return {"changes": rows, "total": len(rows)}
    except Exception as e:
        return {"changes": [], "total": 0, "error": str(e)}
    finally:
        conn.close()


# ═══════════════════════════════════════════════════════════════
#  IPMI（本地 /dev/ipmi0 上报）+ 部件可用性
# ═══════════════════════════════════════════════════════════════

from ipmi_ingest import IPMIStore
from node_health import NodeHealthAggregator


@app.post("/api/v1/ipmi/ingest")
async def ipmi_ingest(body: dict = None):
    """ipmi-exporter 上报节点传感器 + SEL 事件（sel_events）。可降级。"""
    b = body or {}
    node = b.get("node") or b.get("node_name")
    if not node:
        raise HTTPException(400, "node required")
    sensors = b.get("sensors") or []
    sel_events = b.get("sel_events") or []
    store = IPMIStore()
    store.ingest(node, sensors, sel_events)
    return {"ok": True, "count": len(sensors), "sel_count": len(sel_events)}


@app.get("/api/v1/ipmi/sensors")
async def list_ipmi_sensors(node: str = "", sensor_type: str = ""):
    return {"sensors": IPMIStore().query(node=node or None, sensor_type=sensor_type or None)}


@app.get("/api/v1/ipmi/events")
async def list_ipmi_sel_events(node: str = "", limit: int = 50):
    """SEL 事件明细列表（按 event_time 倒序）。"""
    return {"events": IPMIStore().query_sel(node=node or None, limit=limit)}


@app.get("/api/v1/node/health")
async def list_node_health(node: str = ""):
    return {"health": NodeHealthAggregator().query(node=node or None)}


@app.post("/api/v1/node/health/aggregate")
async def aggregate_node_health(body: dict = None):
    """手动触发一次真实聚合（P1-3）：从 VM + IPMI 采集真实指标，按阈值判定部件状态并落库。

    body: {node?: 仅聚合该节点, metrics?: 兼容旧 mock 模式（提供 metrics 时走原判定逻辑）}
    不带 node 时聚合 VM 发现的全部节点。
    """
    b = body or {}
    node = (b.get("node") or b.get("node_name") or "").strip()
    agg = NodeHealthAggregator()
    # 兼容旧 mock 调用：显式提供 metrics 时按原逻辑判定单节点
    if b.get("metrics"):
        if not node:
            raise HTTPException(400, "node required when metrics provided")
        status = agg.aggregate(node, b.get("metrics") or {})
        return {"ok": True, "mode": "provided_metrics", "health": [{"node": node, "components": status}]}
    results = agg.aggregate_all(nodes=[node] if node else None)
    return {"ok": True, "mode": "auto_pipeline",
            "aggregated_at": time.strftime("%Y-%m-%dT%H:%M:%SZ"), "health": results}


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
