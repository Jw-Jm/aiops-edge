"""Brain orchestrator v4 — 15-node LangGraph DAG + ChromaDB RAG + Skill Registry"""
from __future__ import annotations

import asyncio
import json
import os
import time as _time
import subprocess
from typing import TypedDict, Annotated, Optional
import operator

from langgraph.graph import StateGraph, END
from langgraph.checkpoint.memory import MemorySaver
from langgraph.types import interrupt, Command

from tools import (query_metrics, query_traces, query_logs, get_service_list, query_topology,
                   execute_shell, k8sgpt_diagnose, deepflow_status, get_infrastructure)
from rca import full_rca_analysis
from skill_registry import ToolRegistry, ExpertRegistry
from contracts import RequestContext
from invocation_scope import (
    InvocationScope, LegacyScopeAdapter, ScopeView, ScopeViewSnapshot,
)
from internal_query import signed_query_api_request

QUERY_API_VL = os.environ.get("QUERY_API_URL", "http://query-api.observability.svc.cluster.local:8080/api/v1") + "/logs/victorialogs"


# ═══════════════════════════════════════════════════════════════
#  State
# ═══════════════════════════════════════════════════════════════

class AgentState(TypedDict):
    messages: Annotated[list, operator.add]
    intent: str
    service: str
    user_message: str
    llm_config: Optional[dict]
    history_context: str  # 多轮上下文：上一轮用户问题 + 回答摘要（stream_sync 读 checkpoint 注入）

    # collected
    services_data: str
    infra_data: str
    infra_error: str
    alert_data: str
    red_metrics: str
    trace_data: str
    k8sgpt_raw: str
    k8sgpt_error: str

    # RAG
    similar_cases: str
    knowledge_tool_error: str

    # analysis
    crewai_result: str
    holmesgpt_result: str

    # dual agent (批3)
    subtasks: list
    sub_results: dict
    review_result: str

    # plan + risk
    plan: str
    script: str
    risk_score: int
    risk_reason: str

    # execute
    approved: bool
    human_approved: bool  # 写操作需人工审批；由 interrupt/approve 端点置 True
    execute_output: str

    # verify
    before_metrics: str
    after_metrics: str
    verify_pass: bool
    verify_status: str  # passed | failed | regressed | inconclusive
    verify_error_code: str
    action_id: str

    # rca
    rca_mode: str
    rca_root_cause: str
    rca_evidence: str
    rca_confidence: float
    rca_hypotheses_tested: int

    # report
    final_response: str
    report: str
    error: str

    # 需求2/3: 多轮处置闭环。exec_context = 上一轮已确认执行的处置脚本的结果，
    # iteration = 当前第几轮（防死循环，上限由调用方控制）
    exec_context: str
    iteration: int
    # A-5: 集群范围（all/default/集群 name）。必须在 TypedDict 中声明，
    # 否则 LangGraph 节点间 state 传递会丢弃未声明键，导致集群过滤失效。
    cluster_id: str
    # P1-5: 轻量意图分流标记 —— 信息查询类问题（有哪些服务/列表/总结等）跳过深度诊断链路
    light_query: bool
    # LEGACY_FIELD_NAME_ONLY: keeps old field name but the type is now the internal
    # ScopeView (InvocationScope or LegacyScopeAdapter), not the HTTP transport contract.
    request_context: ScopeView


# ═══════════════════════════════════════════════════════════════
#  LLM call
# ═══════════════════════════════════════════════════════════════

from llm_mock import is_mock_enabled, mock_llm_response
from llm_mock import mock_llm_decision, mock_coordinator_plan, mock_reviewer_result
from dual_agent import parse_subtasks, run_subtasks, merge_review

# 安全：LLM API key 仅存于进程内存单例（不经 AgentState/checkpoint 持久化）。
# state 中的 llm_config 剔除 api_key 字段，节点调用 _llm 时若缺 key 从此处回填，
# 避免明文 key 落入 LangGraph SqliteSaver checkpoint / 日志。
_LLM_KEY_HOLDER = {"api_key": ""}


def _llm_production_mode() -> bool:
    return os.environ.get("AIOPS_ENV", "").strip().lower() == "production" or os.environ.get(
        "AIOPS_DEPLOYMENT_MODE", ""
    ).strip().lower() == "production"


def _llm_boundary_ready() -> bool:
    """LLM egress is proxy-only; LLM_MOCK is the sole non-network bypass."""
    if os.environ.get("LLM_MOCK", "").lower() in ("1", "true", "yes", "on"):
        return True
    return bool(os.environ.get("LLM_PROXY_URL", "").strip() and os.environ.get("LLM_PROXY_TOKEN", "").strip())


def _llm_key_ready() -> bool:
    """判断是否具备真实 LLM 调用条件。
    注意: cfg(llm_config) 里 api_key 已被 set_llm_config 安全剔除(只存 _LLM_KEY_HOLDER),
    因此必须检查 _LLM_KEY_HOLDER 而非 cfg.get('api_key'), 否则真实 key 存在时也会被误判为
    '无 key' 而全部走 mock/跳过 —— 这正是"LLM 并未实际调用"的根因。

    set_llm_config() only receives the proxy ingress token; a provider key is
    never accepted as a capability signal.
    """
    # A deterministic mock provider is not a credential path and remains
    # available to unit tests/dev diagnostics without weakening proxy-only
    # production egress.
    try:
        cfg = getattr(brain, "llm_config", None)
        if isinstance(cfg, dict) and cfg.get("provider") == "mock" and bool(cfg.get("api_key")):
            return True
    except Exception:
        pass
    if not _llm_boundary_ready():
        return False
    return bool(_LLM_KEY_HOLDER.get("api_key"))


def _is_llm_failure(text) -> bool:
    """判断 LLM 输出是否为超时/错误占位符（P1-5）。

    这些占位符不应直接进入最终报告/案例库，下游应降级为确定性诊断段落。
    """
    if not text:
        return False
    s = str(text)
    return s.startswith("[LLM") or "LLM 调用超时" in s or "LLM error" in s


# P1-5: LLM 调用并发控制——进程级 Semaphore + 共享有界线程池。
# Semaphore(4) 限制同时运行的 LLM 调用数（含排队等待者）；
# 共享 executor max_workers=4 保证即使 kickoff 超时被放弃，遗留后台线程
# 数量也恒 ≤ 4（不再每次调用新建 executor 导致超时线程无界累积）。
import threading as _threading
import concurrent.futures as _cf
_LLM_SEMAPHORE = _threading.Semaphore(4)
_LLM_EXECUTOR = _cf.ThreadPoolExecutor(max_workers=4, thread_name_prefix="llm-call")


def _llm(cfg: dict, system_prompt: str, user_prompt: str, role: str = "分析专家",
         timeout: float = 60) -> str:
    if is_mock_enabled():
        return mock_llm_response(system_prompt + user_prompt)
    if not cfg:
        return ""
    # 安全：若 cfg 无明文 key（state 中已剔除），从进程内存单例回填
    api_key = cfg.get("api_key") or _LLM_KEY_HOLDER.get("api_key", "")
    if not api_key:
        return ""
    if _llm_production_mode():
        # Production egress is a single capability boundary. Reject any
        # provider URL or credential that did not originate from the configured
        # proxy, even if a legacy caller invokes _llm directly.
        proxy_url = (os.environ.get("AI_LLM_EGRESS_PROXY_URL") or os.environ.get("LLM_PROXY_URL") or "").strip().rstrip("/")
        proxy_token = os.environ.get("LLM_PROXY_TOKEN", "").strip()
        base_url = str(cfg.get("base_url") or "").strip().rstrip("/")
        if not proxy_url or not proxy_token or api_key != proxy_token or not base_url.startswith(proxy_url + "/v1/proxy/"):
            return ""
    try:
        import os as _os
        # 禁用 CrewAI 遥测，避免启动时网络超时阻塞
        _os.environ.setdefault("CREWAI_DISABLE_TELEMETRY", "true")
        _os.environ.setdefault("CREWAI_TELEMETRY_OPT_OUT", "true")
        # 安全修复(G7): 不再设置进程全局 OPENAI_API_KEY/BASE_URL/MODEL 环境变量——
        # 并发调用会互相覆盖（LLM env 竞态），且明文 key 会常驻进程环境/泄漏到子进程。
        # CrewAI LLM 构造器直接接收 api_key/base_url/model 参数（见下方 LLM(...)），
        # 无需依赖环境变量；env 仅作为构造器内部兜底，不在此处写入。
        from crewai import Agent, Task, Crew, LLM
        llm = LLM(model=cfg["model"], api_key=api_key,
                   base_url=cfg["base_url"], provider=cfg.get("backend", "openai"), temperature=0.3)
        agent = Agent(role=role, goal=system_prompt, backstory="可观测性分析专家",
                       allow_delegation=False, verbose=False, llm=llm)
        task = Task(description=user_prompt, agent=agent, expected_output="请用中文输出。")
        crew = Crew(agents=[agent], tasks=[task], verbose=False)

        # 解决: FastAPI 异步事件循环与 CrewAI 同步 kickoff 冲突。
        # _llm 恒在 to_thread 线程中执行（_llm_async）或调用方线程（同步调用方），
        # 信号量 acquire 不会阻塞 event loop。
        # P1-5: 信号量限制并发 LLM 调用 ≤4；共享 executor 的 max_workers=4 兜底，
        # 超时后遗留的 kickoff 线程数有界（≤4），不会每次超时泄漏一个线程。
        _LLM_SEMAPHORE.acquire()
        try:
            # 超时直接放弃，不等待后台线程（executor 中遗留线程由 max_workers 上界约束）
            future = _LLM_EXECUTOR.submit(crew.kickoff)
            try:
                return str(future.result(timeout=timeout))[:4000]
            except _cf.TimeoutError:
                return "[LLM 调用超时, 请稍后重试]"
        finally:
            _LLM_SEMAPHORE.release()
    except Exception as e:
        logging.getLogger("aiops.llm").error(
            "llm call failed error_type=%s", type(e).__name__
        )
        return "[LLM error]"


async def _llm_async(cfg: dict, system_prompt: str, user_prompt: str,
                     role: str = "分析专家", timeout: float = 60) -> str:
    """LLM 调用丢到线程池，不阻塞 event loop。

    502 根因修复: _llm() 内部 future.result(timeout=60) 是同步阻塞调用，若直接在
    async 节点中调用会卡住 uvicorn event loop，导致 liveness probe 超时 → kubelet kill → 502。
    用 asyncio.to_thread 把整个 _llm (含 crew.kickoff 阻塞) 丢到默认线程池执行，
    event loop 可继续处理 liveness probe / 其他请求。
    不重写 crewai，只包裹其同步 kickoff。
    P1-5: timeout 透传（诊断/汇总类节点传 120s），并发由 _llm 内信号量 + 共享 executor 控制。
    """
    return await asyncio.to_thread(_llm, cfg, system_prompt, user_prompt, role, timeout)


def _extract_script(text: str) -> str:
    """从 LLM 分析结果中提取可执行的 kubectl/curl 等命令作为操作建议。"""
    import re
    if not text:
        return ""
    script_lines = []
    # 优先提取 ```bash/```sh 代码块
    blocks = re.findall(r"```(?:bash|sh)?\s*\n(.*?)```", text, re.DOTALL)
    for b in blocks:
        for line in b.splitlines():
            line = line.strip()
            if line and not line.startswith("#") and not line.startswith("$") \
               and ("kubectl" in line or "curl" in line):
                script_lines.append(line)
    # 优先提取『## 处置命令』小节中的 kubectl/curl 命令（LLM 结构化输出）
    if not script_lines:
        section = re.split(r"#{1,3}\s*处置命令", text, flags=re.IGNORECASE)
        if len(section) > 1:
            for line in section[-1].splitlines():
                line = line.strip()
                if not line or line.startswith("#") or line.startswith("-") or line.startswith("*"):
                    continue
                if line.startswith("kubectl") or line.startswith("curl"):
                    script_lines.append(line)
    # 如果没有代码块，直接从全文提取以 kubectl/curl 开头的行
    if not script_lines:
        for line in text.splitlines():
            line = line.strip()
            if line.startswith("kubectl") or line.startswith("curl"):
                script_lines.append(line)
    # 去重，最多取 10 条
    seen = set()
    result = []
    for s in script_lines:
        if s not in seen:
            seen.add(s)
            result.append(s)
        if len(result) >= 10:
            break
    return "\n".join(result)


def _sanitize_script_placeholders(script: str, service: str = "") -> str:
    """把 LLM 生成命令中的尖括号占位符（<pod-name>/<ns>/<namespace>/<deployment> 等）
    替换为可执行的真实值。LLM 不知道真实 pod 名，常原样输出 <pod-name>，导致命令不可执行。
    兜底策略：用服务名替换 pod/deployment 名（转标签定位更安全）；命名空间默认 observability。"""
    import re
    if not script:
        return script
    ns = "observability"
    # 命名空间占位符 → observability
    script = re.sub(r"<ns>|<namespace>|<namespace名>|namespace>", ns, script)
    # 含 <pod-name>/<pod> 的命令：describe/logs/delete 改为按 app 标签定位（更安全，避免未知 pod 名）
    def _replace_pod_cmd(m):
        full = m.group(0)
        return re.sub(r"<pod[^>]*>", f"$(kubectl get pods -n {ns} -l app={service or 'default'} -o name | head -1 | cut -d/ -f2)", full)
    if service:
        # 仅当存在具体服务名时才替换，避免替换成空值产生更糟的命令
        script = re.sub(
            r"(?i)\bkubectl\s+(logs|describe)\s+pod\s+<pod[^>]*>",
            lambda m: m.group(0).replace("pod <pod", f"pod -l app={service} <pod").replace(" <pod", ""),
            script,
        )
    # 剩余任意 <xxx> 占位符：日志/describe/delete/restart 命令里替换为 service，其余保留
    script = re.sub(r"<pod[^>]*>", service or "default", script)
    script = re.sub(r"<deployment[^>]*>|<deploy[^>]*>", service or "default", script)
    script = re.sub(r"<svc>|<service[^>]*>", service or "default", script)
    return script


def _fallback_script(text: str, service: str = "") -> str:
    """当 LLM 分析未产出可执行命令时，基于分析文本/告警类型生成确定性可执行命令。

    目的：处置建议卡必须给出**具体的可执行命令**（kubectl/curl），而非只罗列分析报告。
    按文本特征（重启/OOM/负载/不可用）分派到对应的只读诊断命令。
    """
    svc = service or "default"
    low = (text or "").lower()
    cmds = []
    # 重启类
    if any(k in low for k in ["重启", "restart", "crashloop", "crash loop"]):
        cmds = [
            "kubectl get pods -n observability | grep -E 'CrashLoopBackOff|Error'",
            f"kubectl describe pod -l app={svc} -n observability | tail -60",
            f"kubectl logs -l app={svc} -n observability --tail=100 --prefix",
        ]
    # OOM / 内存
    elif any(k in low for k in ["oom", "内存", "memory", "killed"]):
        cmds = [
            f"kubectl get pods -n observability | grep OOMKilled",
            f"kubectl describe pod -l app={svc} -n observability | grep -iE 'Memory|OOMKilled'",
            f"kubectl top pod -l app={svc} -n observability",
        ]
    # 高负载 / CPU
    elif any(k in low for k in ["cpu", "负载", "load", "高"]):
        cmds = [
            f"kubectl top pods -n observability",
            f"kubectl describe pod -l app={svc} -n observability | tail -40",
        ]
    # Deployment 不可用
    elif any(k in low for k in ["不可用", "unavailable", "部署", "deployment"]):
        cmds = [
            f"kubectl get deploy -n observability | grep -vE '1/1|2/2|3/3|4/4|5/5'",
            f"kubectl rollout status deploy/{svc} -n observability",
            f"kubectl get pods -n observability | grep {svc}",
        ]
    # 通用诊断
    else:
        cmds = [
            f"kubectl get pods -n observability -l app={svc}",
            f"kubectl describe pod -l app={svc} -n observability | tail -40",
        ]
    return "\n".join(cmds)


def _action_summary(script: str, analysis: str, service: str = "") -> str:
    """生成简洁的处置动作摘要（命令 + 一句依据），避免卡片只罗列整篇分析报告。
    P3-7: 目标为空时输出"未指定"，不残留 '-'。"""
    svc = service or "未指定"
    lines = [f"**目标**: {svc}"]
    if script:
        lines.append(f"**建议执行命令**:")
        lines.append("```bash\n" + script[:600] + "\n```")
    # 依据取分析文本首句（去空白）
    if analysis:
        first = " ".join(analysis.split())[:180]
        if first:
            lines.append(f"**依据**: {first}…")
    return "\n".join(lines)


# ═══════════════════════════════════════════════════════════════
#  Helpers
# ═══════════════════════════════════════════════════════════════

def _entity_validation_warning(user_msg: str, services_data: str) -> str:
    """实体存在性校验：从用户消息中提取可能的服务名/资源名，
    若不在 services_data 中则返回警告，让 LLM 看到真实可用的服务清单。
    这是防止 LLM 幻觉（编造不存在的 Pod 名/namespace/状态）的关键护栏。
    """
    import re
    # services_data 实际格式: "- 服务名: 数据"（每行一个服务），提取所有出现过的服务名
    real_services = set()
    for m in re.finditer(r"-\s+([a-zA-Z][a-zA-Z0-9_\-\.]+)\s*[:：]", services_data):
        real_services.add(m.group(1))
    # 也兼容 service_name: xxx 格式
    for m in re.finditer(r"service_name[:：]\s*([a-zA-Z0-9_\-\.]+)", services_data):
        real_services.add(m.group(1))
    # 从用户消息提取可能的服务名（小写英文+中划线+点，至少 3 个字符）
    mentioned = set()
    for m in re.finditer(r"\b([a-z][a-z0-9\-\.]{2,}[a-z0-9])\b", user_msg):
        word = m.group(1)
        if word not in ("kubernetes", "kubectl", "k8s", "redis-cli", "deepflow", "shell", "bash", "mysql", "pod", "pods", "service", "services", "config", "map"):
            mentioned.add(word)
    if not mentioned or not real_services:
        return ""
    real_list = ", ".join(sorted(real_services))
    msg = "【⚠️ 实体存在性校验】用户消息中提到的服务/资源不在当前系统中：\n"
    msg += "用户提到：" + ", ".join(sorted(mentioned)) + "\n"
    msg += "当前真实可用的服务（来自 services_data）：" + real_list + "\n"
    msg += "**请明确回复用户该服务未在系统中发现**，列出当前可用的服务让用户选择，并"
    msg += "**禁止编造不存在的 Pod 名/namespace/状态/数据**。"
    return msg


def _parse(raw):
    try: return json.loads(raw)
    except: return None


def _collect_alerts(
    *, request_context: ScopeView | None = None
) -> str:
    """采集告警态势：告警事件（聚合） + 告警规则，供 LLM 分析。"""
    import os as _os
    # V9.2: context must be explicit; do not fall back or issue queries without one.
    if request_context is None:
        return "采集失败"
    if getattr(request_context, "workload_kind", "") == "investigation":
        try:
            from tools import _internal_investigation_query
            data = _internal_investigation_query(
                tool_id="query_alerts.v1", operation="alerts", params={"limit": 15}, context=request_context,
            )
            events = data.get("events", data.get("data", [])) if isinstance(data, dict) else []
            if not events:
                return "活跃告警事件: 无"
            lines = ["活跃告警事件:"]
            for event in events:
                lines.append(
                    f"- [{event.get('severity', '')}] {event.get('rule_name', '?')} "
                    f"服务={event.get('service', '?')} 触发次数={event.get('count', 1)}"
                )
            return "\n".join(lines)
        except Exception as exc:
            return f"活跃告警事件: 采集失败（{str(exc)[:120]}）"
    if getattr(request_context, "workload_kind", "") == "chat":
        try:
            from tools import _chat_compatibility_allowed, _chat_context_ready, _internal_chat_query, _unwrap_internal_query_result
            if not _chat_context_ready(request_context):
                if _chat_compatibility_allowed(request_context):
                    raise LookupError("legacy chat compatibility")
                return "活跃告警事件: CHAT_TOOL_AUDIT_REQUIRED"
            body = _internal_chat_query(
                tool_id="query_alerts.v1", operation="alerts", params={"limit": 15}, context=request_context,
            )
            payload, error = _unwrap_internal_query_result(body)
            if error or payload is None:
                return f"活跃告警事件: 采集失败（{error or 'query failed'}）"
            events = payload.get("alerts", [])
            if not events:
                return "活跃告警事件: 无"
            lines = ["活跃告警事件:"]
            for event in events:
                lines.append(
                    f"- [{event.get('severity', '')}] {event.get('rule_name', '?')} "
                    f"服务={event.get('service', '?')} 触发次数={event.get('count', 1)}"
                )
            return "\n".join(lines)
        except LookupError:
            pass
        except Exception as exc:
            return f"活跃告警事件: 采集失败（{str(exc)[:120]}）"
    qa = _os.environ.get("QUERY_API_URL", "http://query-api.observability.svc.cluster.local:8080/api/v1")
    out = []

    # 告警事件（聚合）
    try:
        data = json.loads(
            signed_query_api_request(
                f"{qa}/alerts/events?limit=15",
                context=request_context,
                timeout=5,
            )
        )
        events = data.get("data", [])
        if events:
            lines = ["活跃告警事件:"]
            for e in events:
                lines.append(
                    f"- [{e.get('severity','')}] {e.get('rule_name','?')} "
                    f"服务={e.get('service','?')} 触发次数={e.get('count',1)} "
                    f"最近={str(e.get('last_timestamp',''))[:19]}")
            out.append("\n".join(lines))
        else:
            out.append("活跃告警事件: 无")
    except Exception:
        out.append("活跃告警事件: 采集失败")

    # 告警规则
    try:
        data = json.loads(
            signed_query_api_request(
                f"{qa}/alerts/rules", context=request_context, timeout=5
            )
        )
        rules = data.get("data", [])
        if rules:
            lines = ["告警规则:"]
            for r in rules:
                en = "启用" if r.get("enabled") else "禁用"
                lines.append(
                    f"- [{en}] {r.get('name','?')} 服务={r.get('service','?')} "
                    f"{r.get('metric','?')} {r.get('condition','>')} {r.get('threshold','?')} "
                    f"{r.get('severity','')}")
            out.append("\n".join(lines))
    except Exception:
        pass

    # B2: 工具结果汇总放宽到 8000 字符（服务/告警明细多时不再被截断）。
    # 现有拼接顺序即"头部优先"，头部为告警事件明细，保留顺序即可。
    return "\n\n".join(out)[:8000]

def _now():
    import datetime
    return datetime.datetime.now().strftime("%Y-%m-%d %H:%M:%S")


# P1-3/P1-5: 信息查询类问题判定与 0~1 风险分（与 main.py 报告中心共用）。
_INFO_QUERY_MARKERS = (
    "有哪些", "都有哪些", "列表", "列出", "总结", "概括", "汇总", "说说",
    "有什么", "是什么", "一览", "清单", "列举", "怎么用", "如何使用", "介绍",
)
_FAULT_MARKERS = (
    "异常", "错误", "失败", "故障", "根因", "排查", "告警", "宕机",
    "报错", "崩溃", "不可用", "重启", "慢", "延迟", "性能", "报警",
)

# Explicit tool requests must take precedence over the lightweight information
# path.  For example, "用 k8sgpt 诊断当前集群有哪些问题" contains the
# information marker "有哪些", but the user explicitly asked for a tool call.
_EXPLICIT_TOOL_KEYWORDS = {
    "k8sgpt_diagnose": ("k8sgpt", "k8s gpt", "k8s-gpt"),
    "query_knowledge": (
        "知识库", "知识搜索", "搜索知识", "案例库", "案例检索", "历史案例",
        "参考知识", "运维手册", "playbook", "rag", "knowledge", "knowledge base", "knowledge search",
    ),
}


def _explicit_tool_route(question: str) -> Optional[str]:
    """Return the explicitly requested read-only tool, if any.

    This is deliberately deterministic: an LLM should not be able to turn an
    explicit tool request into a generic service-list answer.
    """
    routes = _explicit_tool_routes(question)
    return routes[0] if routes else None


def _explicit_tool_routes(question: str) -> list[str]:
    """Return all explicitly requested tools, preserving keyword declaration order.

    A request can legitimately require both K8sGPT and knowledge search.  The
    singular route helper remains for backwards compatibility with lightweight
    intent checks, while execution paths use this complete list.
    """
    q = (question or "").lower()
    routes = []
    for tool_name, keywords in _EXPLICIT_TOOL_KEYWORDS.items():
        if any(keyword in q for keyword in keywords):
            routes.append(tool_name)
    return routes


def _is_info_query(question: str) -> bool:
    """信息查询类问题判定：含查询词（有哪些/列表/总结等）且不含故障语义 → True。

    这类问题（如"当前有哪些服务在运行?"）不做异常判定/深度诊断，
    直接基于采集数据作答（chat 图走 collect→summarize 轻量链路）。
    """
    q = (question or "").strip()
    if not q:
        return False
    if _explicit_tool_route(q):
        return False
    if any(k in q for k in _FAULT_MARKERS):
        return False
    return any(k in q for k in _INFO_QUERY_MARKERS)


def _k8sgpt_error_reason(raw: str) -> str:
    """把 K8sGPT 无输出、不可用、权限/执行错误与真实扫描结果区分开。"""
    text = (raw or "").strip()
    low = text.lower()
    if not text:
        return "K8sGPT unavailable: empty result"
    if text == "未发现集群问题":
        return "K8sGPT unavailable: no verifiable diagnostic result"
    markers = (
        "unavailable", "not installed", "not found", "timeout", "timed out",
        "error", "失败", "未安装", "超时", "权限", "forbidden", "permission denied",
    )
    if any(marker in low for marker in markers):
        return text
    return ""


def _risk_from_evidence(content: str) -> float:
    """基于诊断结论/报告中的异常证据计算 0~1 风险分（不再硬编码 0.85）。

    证据：活跃告警 critical/严重 数、错误率>5% 服务数、错误率峰值、显式高危关键词。
    无异常证据返回 0。
    """
    import re as _re
    low = (content or "").lower()
    score = 0.0
    # 1) 活跃告警 critical/严重 数（每起 0.15，上限 0.4）
    criticals = 0
    for line in (content or "").splitlines():
        ll = line.lower()
        if "告警" in ll and ("critical" in ll or "严重" in ll):
            criticals += 1
    if criticals:
        score += min(0.4, 0.15 * criticals)
    # 2) 错误率 >5% 的服务数（每个 0.15，上限 0.3）+ 错误率峰值（0~30% 线性 0~0.3）
    rates = []
    for m in _re.finditer(r"错误率[=：\s]*([\d.]+)\s*%", content or ""):
        try:
            v = float(m.group(1))
            if 0 <= v <= 100:
                rates.append(v)
        except ValueError:
            pass
    high_err = sum(1 for r in rates if r > 5)
    if high_err:
        score += min(0.3, 0.15 * high_err)
    # 错误率峰值（仅当确实异常 >5% 时计入，峰值 0~30% 线性映射 0~0.3；
    # 正常低错误率不产生风险，保证"无异常证据=0"）
    peak = max(rates) if rates else 0.0
    if peak > 5:
        score += min(0.3, peak / 100.0)
    # 3) 显式高危关键词兜底（0.4，而非旧硬编码 0.85）
    if any(k in low for k in ("中高风险", "高风险", "严重告警", "critical", "宕机", "频繁重启", "crashloop")):
        score = max(score, 0.4)
    return round(min(1.0, score), 2)


def _has_anomaly_evidence(text: str) -> bool:
    """是否存在真实异常证据：服务错误率>5% 或活跃告警。
    用于门控处置建议（suggestion）只在真实异常时生成。"""
    import re as _re
    low = (text or "").lower()
    # 活跃告警（排除"无/采集失败"）
    if ("活跃告警" in low
            and "活跃告警事件: 无" not in low
            and "活跃告警事件: 采集失败" not in low):
        return True
    # 服务错误率 > 5%
    for m in _re.finditer(r"错误率[=：\s]*([\d.]+)\s*%", text or ""):
        try:
            if float(m.group(1)) > 5:
                return True
        except ValueError:
            pass
    return False


def _build_info_answer(state: dict) -> str:
    """信息查询（如"有哪些服务在运行"）基于已采集数据**针对用户问题**作答。

    P1-7: 必须包含用户原始问题并要求针对性回答——列出与问题直接相关的数据摘要
    （服务名清单/关键指标），不允许输出全量原始数据转储。
    """
    msg = (state.get("user_message", "") or "").strip()
    svc_data = state.get("services_data", "") or ""
    infra = state.get("infra_data", "") or ""
    infra_error = state.get("infra_error", "") or ""
    alerts = state.get("alert_data", "") or ""
    low = msg.lower()
    parts = [f"**问题**: {msg or '(未提供)'}"]

    # 服务清单：解析 "- name: 指标..." 行，仅提取服务名（针对"有哪些服务"类问题）
    svc_names = []
    for line in svc_data.splitlines():
        line = line.strip()
        if line.startswith("- "):
            svc_names.append(line[2:].split(":")[0].strip() or line[2:].strip())
    if svc_names:
        parts.append(
            f"### 回答\n当前共有 **{len(svc_names)}** 个服务在运行：\n"
            + "\n".join(f"- {s}" for s in svc_names[:30])
            + (f"\n\n（共 {len(svc_names)} 个，仅展示前 30 个）" if len(svc_names) > 30 else "")
        )
    else:
        # 非服务清单场景：摘要展示与问题相关的采集数据（截断，非全量转储）
        relevant = ""
        if svc_data:
            relevant = svc_data[:1200]
        elif infra_error:
            relevant = f"基础设施证据不确定：{infra_error[:1200]}"
        elif infra:
            relevant = infra[:1200]
        parts.append(f"### 回答\n{relevant if relevant else '（未采集到实时数据，请稍后重试）'}")
    # 告警态势仅在问题涉及告警/异常/健康/状态时附带（针对性，而非每次全量）
    if alerts and any(k in low for k in ("告警", "异常", "健康", "报警", "状态", "正常")):
        parts.append(f"### 告警态势\n{alerts[:600]}")
    return "\n\n".join(parts)


# 疑似查询意图的句式前缀（这类对话不入库，避免"用户提问"被误存为故障案例）
_QUERY_INTENT_PREFIXES = (
    "请分析", "请帮我", "帮我", "分析一下", "请问", "看看", "查一下", "检查一下",
    "如何", "怎么", "为什么", "是什么", "有哪些", "能否", "能不能", "解释",
)


def _cap_symptom(symptom: str) -> str:
    """标题长度控制：symptom 超长（>500）时截断到 200 并提示，避免超长标题/文档入库。"""
    s = (symptom or "").strip()
    if len(s) > 500:
        print(f"[case_quality] symptom 过长 ({len(s)} 字符), 标题截断到 200")
        return s[:200]
    return s


def _strip_heading(text: str) -> str:
    """去掉 markdown 标题行（#/##/### 等），返回剩余正文（用于判断分析是否有实质内容）。"""
    return "\n".join(l for l in (text or "").splitlines() if not l.lstrip().startswith("#"))


def _case_quality_check(state: dict) -> tuple:
    """入库前质量校验：过滤测试/无效对话/LLM 占位符/纯信息查询，防止污染 RAG 案例库。
    返回 (是否通过, 拒绝原因)。"""
    symptom = _cap_symptom(state.get("user_message") or "")
    plan = state.get("plan") or ""
    crewai = state.get("crewai_result") or ""
    report = state.get("report") or ""

    # 1. 症状长度：过短（<8 字符）视为无效内容（"你好"/"test"/"hi"）
    if len(symptom) < 8:
        return False, f"symptom 过短 ({len(symptom)} 字符)"
    # 2. 查询意图句式：以提问前缀开头视为"用户问询"而非"故障描述"
    low = symptom.lower()
    if low in ("test", "你好", "hi", "hello", "测试", "help") or low.startswith(_QUERY_INTENT_PREFIXES):
        return False, "疑似查询意图，非故障案例"
    # 2b. 信息查询/对话类意图：纯查询词（总结/概括/有哪些/列表等）且不含故障语义 → 拒绝入库。
    #     复用 _is_info_query 判定，拦截"总结一下集群状况"这类测试对话被误存为故障案例
    if _is_info_query(state.get("user_message") or ""):
        return False, "信息查询非故障案例"
    # 3. LLM 占位符/失败结果：plan/crewai 含超时占位符时丢弃
    if "LLM 调用超时" in plan or "LLM 调用超时" in crewai or plan.startswith("[LLM"):
        return False, "LLM 结果为超时占位符"
    # 3b. 占位检测：crewai_result 只有模板标题无实质内容（去掉标题后 <50 字符）则拒绝。
    #     注意: "确定性分析"/"[mock]" 字样在当前环境是常态, 不作为拒绝依据
    if len(_strip_heading(crewai).strip()) < 50:
        return False, "分析结果无实质内容"
    # 4. 无故障证据 + 无方案：crewai/report 明确"无异常/一切正常/健康"结论且无 plan → 拒绝
    if not plan.strip():
        if any(k in crewai for k in ("无异常", "一切正常", "健康")) or any(k in report for k in ("无异常", "一切正常", "健康")):
            return False, "结论为无异常, 非故障案例"
        return False, "plan 为空"
    # 5. 关键字段缺失：无根因结论视为不完整
    if not crewai.strip():
        return False, "crewai_result 为空"
    if not report.strip():
        return False, "report 为空"
    return True, ""


def _infer_target_from_script(script: str, fallback: str = "") -> str:
    """修复(P2-2)：从执行脚本中推断目标资源（namespace/服务名），
    填充审计日志的 target_service 字段（此前 service 为空时显示 "-"）。
    优先级：kubectl -n <ns> 的 namespace > kubectl <resource>/<name> 的资源名。
    """
    if not script:
        return fallback or ""
    import re
    m = re.search(r"kubectl[^\n]*?-n\s+([a-zA-Z0-9\-]+)", script)
    if m:
        return m.group(1)
    m = re.search(r"kubectl[^\n]*?\b(logs|describe|exec|delete|get)\s+([a-zA-Z0-9\-]+(?:/[a-zA-Z0-9\-]+)?)", script)
    if m:
        return m.group(2).split("/")[0]
    return fallback or ""


def _audit_log(task_id: str, action: str, operator: str,
               target: str, command: str, result: str, detail: dict = None):
    """写入审计日志到 MySQL（AuditStore），失败静默不影响主流程。"""
    try:
        from db_audit import AuditStore
        AuditStore().log(action, operator, target, command, result, detail, task_id)
    except Exception:
        pass  # 审计日志失败不影响主流程


# ═══════════════════════════════════════════════════════════════
#  Nodes
# ═══════════════════════════════════════════════════════════════

async def node_collect(state: AgentState) -> dict:
    """数据采集节点（async: 与 LangGraph async 图统一；内部 HTTP/子进程调用有短超时兜底）。"""
    cfg = state.get("llm_config")
    api_key = _LLM_KEY_HOLDER.get("api_key", "")
    result = {"messages": [f"[{_now()}] 数据采集开始"]}
    # P-fix: state 中 request_context 是纯 dict 投影，还原为满足 ScopeView 协议的对象
    # An absent projection is an unscoped development/test invocation, not a
    # Chat request.  Do not let ScopeViewSnapshot's safe default workload_kind
    # ("chat") accidentally turn legacy helper tests or maintenance calls into
    # an audited Chat path without durable transcript identity.
    context_projection = state.get("request_context")
    request_context = (
        ScopeViewSnapshot.from_projection(context_projection)
        if context_projection
        else None
    )
    cid = str(request_context.cluster_id) if isinstance(request_context, ScopeView) else ""
    # Services — 全局服务概览（含错误率，供巡检/诊断分析）
    try:
        service_raw = await asyncio.to_thread(
            get_service_list, cluster_id=cid, request_context=request_context
        )
        data = _parse(service_raw)
        if isinstance(data, list):
            lines = []
            for s in data:
                name = s.get('service_name', '?')
                traces = s.get('traces', 0)
                avg = float(s.get('avg_ms', 0))
                # 尝试从服务数据中获取错误信息
                errs = s.get('errors', s.get('error_count', None))
                line = f"- {name}: 调用量={traces} 平均延迟={avg:.0f}ms"
                if errs is not None and traces:
                    rate = (float(errs) / float(traces)) * 100
                    line += f" 错误数={errs} 错误率={rate:.2f}%"
                lines.append(line)
            result["services_data"] = "\n".join(lines)
        else:
            # Chat/Investigation helper may return a bounded human-readable
            # summary when the canonical topology payload is not a service
            # list.  Keep that evidence instead of silently dropping it.
            if service_raw:
                result["services_data"] = str(service_raw)[:12000]
    except: pass
    # Infra
    try:
        infra_data = (await asyncio.to_thread(
            get_infrastructure, request_context=request_context
        )).replace("## K8s 基础设施\n", "").strip()[:20000]
        result["infra_data"] = infra_data
        if "K8s 基础设施数据不可用" in infra_data:
            result["infra_error"] = infra_data
    except: pass
    # Alerts — 告警态势（规则 + 事件聚合）
    try:
        result["alert_data"] = _collect_alerts(request_context=request_context)
    except:
        result["alert_data"] = ""
    # RED
    svc = state.get("service", "")
    if svc:
        try:
            raw = await asyncio.to_thread(
                query_metrics, svc, cluster_id=cid, request_context=request_context
            )
            data = _parse(raw)
            items = []
            if isinstance(data, dict) and isinstance(data.get("data"), list):
                items = data["data"]
            elif isinstance(data, dict) and isinstance(data.get("data"), dict):
                items = data["data"].get("points") or data["data"].get("data") or []
            if items:
                total_calls = sum(int(i.get("calls", 0)) for i in items)
                total_errors = sum(int(i.get("errors", 0)) for i in items)
                avg_lat = sum(float(i.get("avg_ms", 0)) for i in items) / max(len(items), 1)
                err_rate = (total_errors / max(total_calls, 1)) * 100
                lines = [f"服务={svc} 总调用={total_calls} 错误率={err_rate:.2f}% P50延迟={avg_lat:.1f}ms"]
                for item in items[:5]:
                    lines.append(f"  ep={item.get('endpoint','?')} calls={item.get('calls',0)} errors={item.get('errors',0)} avg={item.get('avg_ms',0):.1f}ms")
                result["red_metrics"] = "\n".join(lines)
                result["before_metrics"] = f"总调用={total_calls} 错误率={err_rate:.2f}% P50延迟={avg_lat:.1f}ms"
        except: pass
        try: result["trace_data"] = (await asyncio.to_thread(
            query_traces, svc, cluster_id=cid, request_context=request_context
        ))[:30000]
        except: pass
    # 日志 — 每次对话无条件采集（结合日志分析）
    try:
        result["logs_data"] = await asyncio.to_thread(
            query_logs, svc, 30, cluster_id=cid, request_context=request_context
        )
    except:
        result["logs_data"] = ""
    # P1-6: K8sGPT 不再每次对话无条件调用——仅 diagnosis 意图且非信息查询时调用，
    # 避免聊天/信息查询链路被 k8sgpt analyze 拖慢；显式 k8sgpt 请求始终调用匹配工具。
    import shutil
    is_diag = (state.get("intent") or "").lower() == "diagnosis"
    explicit_tools = _explicit_tool_routes(state.get("user_message", ""))
    investigation_workload = getattr(request_context, "workload_kind", "") == "investigation"
    chat_workload = getattr(request_context, "workload_kind", "") == "chat"
    if chat_workload:
        # K8sGPT talks to the Kubernetes API directly and has no ChatTool audit
        # record.  It is therefore not part of the ordinary Chat read surface;
        # a user asking for it must take the Investigation CTA.
        if "k8sgpt_diagnose" in explicit_tools or (is_diag and not _is_info_query(state.get("user_message", ""))):
            result["k8sgpt_error"] = "CHAT_TOOL_AUDIT_REQUIRED"
    elif "k8sgpt_diagnose" in explicit_tools and not investigation_workload:
        try:
            # Use the registered read-only tool so an explicit request is
            # observable and gets the same fallback text as MCP callers.
            raw = await asyncio.to_thread(k8sgpt_diagnose)
            raw_text = str(raw or "").strip()[:20000]
            if (reason := _k8sgpt_error_reason(raw_text)):
                result["k8sgpt_error"] = reason
            else:
                result["k8sgpt_raw"] = raw_text
        except Exception as exc:
            result["k8sgpt_error"] = f"K8sGPT error: {str(exc)[:300]}"
    elif (not investigation_workload and is_diag and
          not _is_info_query(state.get("user_message", "")) and shutil.which("k8sgpt")):
        # P19.7：统一走 k8sgpt_diagnose（安全注入版）——按需拉取平台 LLM key 子进程私有 env 注入，
        # 不写 /root/.k8sgpt、不用 --all-namespaces（该 flag 在当前 k8sgpt 版本无效）。
        try:
            raw = await asyncio.to_thread(k8sgpt_diagnose, "observability")
            raw_text = str(raw or "").strip()[:20000]
            if (reason := _k8sgpt_error_reason(raw_text)):
                result["k8sgpt_error"] = reason
            else:
                result["k8sgpt_raw"] = raw_text
        except Exception as exc:
            result["k8sgpt_error"] = f"K8sGPT error: {str(exc)[:300]}"
    result["messages"] = [f"[{_now()}] 数据采集完成"]
    return result


_INVESTIGATION_CTA_KEYWORDS = (
    "发起调查", "创建调查", "完整根因分析", "结构化调查", "根因分析",
    "跨服务", "影响面", "调用链分析", "变更关联", "知识图谱", "依赖关系",
    "上下游", "拓扑关联", "图谱", "k8sgpt", "investigation",
)
# 纯闲聊/信息查询（无数据采集需求）→ chat_pure 跳过 heavy collect（B2-03/F-07：Chat 不做固定实时采集）。
_PURE_CONVERSATION_KEYWORDS = (
    "你好", "谢谢", "感谢", "再见", "总结", "介绍一下", "你能做什么",
    "hello", "hi ", "thanks", "thank you", "help",
)


def _needs_investigation_cta(message: str) -> bool:
    """判断 Chat 消息是否需显式结构化调查（B2-03 / F-07）。

    结构化 RCA、跨服务/图谱关联和 K8sGPT 等能力必须返回 CTA；普通
    诊断类问题（诊断/错误率/告警等）仍走受限 Chat 只读分析，由
    node_collect 通过已审计的 Query API 工具支撑，不做固定全量实时采集。
    """
    m = (message or "").lower()
    return any(kw in m for kw in _INVESTIGATION_CTA_KEYWORDS)


def _is_pure_conversation(message: str) -> bool:
    """判断是否纯闲聊（无数据采集需求）→ chat_pure，跳过 heavy collect。"""
    m = (message or "").lower()
    return any(kw in m for kw in _PURE_CONVERSATION_KEYWORDS)


async def node_chat_classify(state: AgentState) -> dict:
    """Chat 入口意图分流（B2-03 / F-07）：普通 Chat 不做固定全量实时采集。

    - 明确要求结构化调查（"发起调查"/"完整根因分析"）→ investigation_required CTA，
      由前端触发显式 createRun()。
    - 纯闲聊/信息查询 → chat_pure=True，直接轻量 summarize（跳过 heavy collect）。
    - 普通诊断/数据查询 → 走正常 Chat 链路（collect→clean→light/rca→summarize），
      保留 exec_context/处置结果分析能力（不做每轮固定全量采集）。
    """
    msg = state.get("user_message", "")
    if _needs_investigation_cta(msg):
        return {
            "investigation_required": True,
            "chat_pure": False,
            "messages": [f"[{_now()}] 检测到结构化调查意图，建议发起显式智能调查（createRun）"],
            "final_response": (
                "__investigation_required__\n该问题需要结构化智能调查（含 Run/Evidence/Plan/RCA 闭环）。"
                "请在智能调查页发起调查，或点击按钮创建调查。"
            ),
        }
    if _is_pure_conversation(msg):
        return {"chat_pure": True, "investigation_required": False,
                "messages": [f"[{_now()}] 纯对话模式：不采集实时数据"]}
    return {"chat_pure": False, "investigation_required": False,
            "messages": [f"[{_now()}] 诊断/查询模式：按需采集"]}


async def node_clean(state: AgentState) -> dict:
    """Deduplicate and standardize collected data.
    P1-5: 顺带做轻量意图分流判定——信息查询类问题置 light_query=True,
    让 chat 图在 clean 节点直接路由到 summarize, 跳过 RCA/RAG/CrewAI 深度节点。"""
    light = _is_info_query(state.get("user_message", ""))
    msgs = [f"[{_now()}] 数据清洗完成"]
    if light:
        msgs.append(f"[{_now()}] 信息查询: 跳过深度诊断链路, 直接基于采集数据汇总")
    return {"light_query": light, "messages": msgs}


def _friendly_tool_result(node_name: str, node_data: dict) -> str:
    """把 langgraph 节点的 state dict 转为用户友好的中文摘要。
    避免 str(dict) 把 {'rca_mode': 'deterministic', ...} 这种调试数据直接展示给用户。
    """
    if not isinstance(node_data, dict):
        return "已完成"
    name = node_name.lower()
    # RCA 根因分析
    if name == "rca":
        mode = node_data.get("rca_mode", "")
        cause = node_data.get("rca_root_cause") or ""
        if mode == "skipped":
            return "未指定目标服务，已跳过"
        if cause and cause != "unknown":
            return f"已定位根因: {cause}"
        return "根因分析完成"
    # RAG 案例匹配
    if name == "rag":
        cases = node_data.get("similar_cases") or ""
        if cases and cases.strip():
            return f"匹配到 {len(cases) if isinstance(cases, list) else 1} 个相似案例"
        msgs = node_data.get("messages") or []
        if msgs:
            # 提取 RAG: 后的简短说明
            for m in msgs:
                t = str(m)
                if "RAG:" in t:
                    return t.split("RAG:", 1)[1].strip()[:80] or "无相似案例"
        return "暂无相似历史案例"
    # CrewAI 分析
    if name == "crewai":
        result = node_data.get("crewai_result") or ""
        if result and result.strip():
            return result[:120]
        return "团队分析完成"
    # Trace 调查 (holmes)
    if name == "holmes":
        return "调用链分析完成"
    # 生成操作方案
    if name == "plan":
        plan = node_data.get("plan") or ""
        if plan:
            return f"已生成操作方案 ({len(plan)} 字符)"
        return "操作方案已就绪"
    # 其它节点: 提取 messages 字段第一条
    msgs = node_data.get("messages")
    if isinstance(msgs, list) and msgs:
        return str(msgs[-1])[:120]
    return "已完成"


def _top_anomaly_service(
    cluster_id: str = "", *, request_context: ScopeView | None = None
) -> str:
    """从全局服务概览中选出最异常的服务（错误率最高），供「未指定服务时默认为所有服务」的 RCA 兜底。
    cluster_id 为空时覆盖全部集群（A-5 补充透传）。"""
    try:
        data = _parse(get_service_list(
            cluster_id=cluster_id, request_context=request_context
        ))
        if not isinstance(data, list) or not data:
            return ""
        best_name, best_rate = "", -1.0
        for s in data:
            name = s.get("service_name", "")
            traces = float(s.get("traces", 0) or 0)
            if not name or traces <= 0:
                continue
            # 优先用 error_rate 字段（get_service_list 摘要含 error_rate）；
            # 兼容带 errors/error_count 的完整服务列表（traces>0 时折算比例）
            rate = float(s.get("error_rate", 0) or 0)
            if rate <= 0:
                errs = float(s.get("errors", s.get("error_count", 0)) or 0)
                if errs > 0:
                    rate = errs / traces * 100
            if rate > best_rate:
                best_rate, best_name = rate, name
        return best_name or ""
    except Exception:
        return ""


async def node_rca(state: AgentState) -> dict:
    """RCA 节点: 自动选择确定性或假设引擎模式。
    未指定服务时默认为所有服务 —— 自动选全服务中最异常者做 RCA，而非直接跳过。
    """
    svc = state.get("service", "")
    cid = state.get("cluster_id", "")  # A-5：RCA 按集群范围
    # P-fix: state 中 request_context 是纯 dict 投影，还原为满足 ScopeView 协议的对象
    rc = ScopeViewSnapshot.from_projection(state.get("request_context"))
    if not svc:
        svc = await asyncio.to_thread(
            _top_anomaly_service, cid,
            request_context=rc,
        )
        if not svc:
            return {"rca_mode": "skipped", "messages": [f"[{_now()}] RCA: 无异常服务数据, 跳过"]}
    try:
        # full_rca_analysis 内部多次 query_metrics/trace 是同步阻塞 HTTP，丢线程池避免阻塞 event loop
        result = await asyncio.to_thread(
            full_rca_analysis,
            svc,
            None,
            cid,
            request_context=rc,
        )
        mode = result.get("mode", "deterministic")
        if mode == "deterministic":
            det = result.get("result", {})
            return {
                "rca_mode": "deterministic",
                "rca_root_cause": det.get("root_cause_service", ""),
                "rca_evidence": json.dumps(det.get("evidence_chain", []), ensure_ascii=False)[:1000],
                "rca_confidence": det.get("confidence", 0),
                "messages": [f"[{_now()}] RCA: 根因={det.get('root_cause_service','?')} (置信度 {det.get('confidence',0):.2f})"],
            }
        else:
            hyp = result.get("result", {}).get("hypothesis_result", {})
            best = hyp.get("best_hypothesis", {})
            return {
                "rca_mode": "hypothesis_engine",
                "rca_root_cause": best.get("hypothesis", best.get("id", "")),
                "rca_evidence": json.dumps(hyp.get("evidence_log", []), ensure_ascii=False)[:1000],
                "rca_confidence": best.get("confidence", 0),
                "rca_hypotheses_tested": len(hyp.get("all_hypotheses", [])),
                "messages": [f"[{_now()}] RCA: 假设引擎分析 (结论={hyp.get('conclusion','?')})"],
            }
    except Exception as e:
        return {"rca_mode": "error", "rca_root_cause": "", "messages": [f"[{_now()}] RCA: 失败 ({e})"]}


async def node_rag(state: AgentState) -> dict:
    """Search knowledge through the canonical scoped boundary.

    A valid runtime scope must never fall through to the orchestrator-local
    Chroma store.  The latter remains available only for unscoped development
    compatibility tests; production requests fail closed when no scoped Query
    API context is present.
    """
    symptom = state.get("user_message", "")
    svc = state.get("service", "")
    query = f"{svc}: {symptom}" if svc else symptom
    request_context = ScopeViewSnapshot.from_projection(state.get("request_context"))
    scoped = bool(
        str(getattr(request_context, "tenant_id", "") or "").strip()
        and str(getattr(request_context, "cluster_id", "") or "").strip()
    )
    explicit_knowledge = "query_knowledge" in _explicit_tool_routes(symptom)
    if explicit_knowledge or scoped or _llm_production_mode():
        try:
            from tools import _query_knowledge
            knowledge = await asyncio.to_thread(
                _query_knowledge, query=query, request_context=request_context,
            )
            parsed = None
            try:
                parsed = json.loads(knowledge) if knowledge else None
            except (TypeError, json.JSONDecodeError):
                parsed = None
            if isinstance(parsed, dict) and parsed.get("error"):
                return {
                    "similar_cases": "",
                    "knowledge_tool_error": str(parsed["error"])[:200],
                    "messages": [f"[{_now()}] 知识库检索失败"],
                }
            if knowledge:
                return {
                    "similar_cases": f"## 知识库检索结果\n{knowledge}",
                    "messages": [f"[{_now()}] 知识库检索完成"],
                }
            return {
                "similar_cases": "",
                "knowledge_tool_error": "知识库未返回结果",
                "messages": [f"[{_now()}] 知识库检索失败: 未返回结果"],
            }
        except Exception as exc:
            return {
                "similar_cases": "",
                "knowledge_tool_error": f"知识库检索失败: {str(exc)[:300]}",
                "messages": [f"[{_now()}] 知识库检索失败"],
            }
    try:
        # Local Chroma is only a development compatibility seam.  Scoped
        # production requests return above and cannot reach this branch.
        from rag import rag
        cases = await asyncio.to_thread(rag.search, query, 3)
        if cases:
            lines = ["## 相似历史案例"]
            for i, c in enumerate(cases):
                lines.append(f"{i+1}. [{c['outcome']}] {c['service']}: {c['symptom'][:100]}")
                lines.append(f"   根因: {c['root_cause'][:100]}")
                lines.append(f"   方案: {c['plan'][:100]}")
            return {"similar_cases": "\n".join(lines), "messages": [f"[{_now()}] RAG 检索到 {len(cases)} 个相似案例"]}
    except:
        pass
    return {"similar_cases": "", "messages": [f"[{_now()}] RAG: 无相似案例"]}


def _deterministic_diagnosis(state: dict) -> str:
    """无 LLM 时的确定性诊断：基于已采集的实时数据逐项汇总为结构化巡检/诊断结论。
    确保即使未配置 LLM，AI 也能输出完整的诊断内容（含健康状态/问题/风险/建议），
    供 node_summarize 拼装完整报告，也供 node_plan 生成处置方案。"""
    svc = state.get("service", "")
    intent = state.get("intent", "inspection")
    lines = []
    if intent == "diagnosis":
        lines.append(f"### 诊断结论（确定性分析）\n- **目标服务**: {svc or '未知'}")
    # 服务 RED 指标
    red = state.get("red_metrics", "")
    if red:
        lines.append(f"- **服务指标**: {red[:600]}")
    # 服务总览（traces/错误率）
    svc_data = state.get("services_data", "")
    if svc_data:
        lines.append(f"- **服务数据**: {svc_data[:800]}")
    # 基础设施
    infra = state.get("infra_data", "")
    infra_error = state.get("infra_error", "")
    if infra_error:
        lines.append(f"- **基础设施证据不确定**: {infra_error[:1200]}")
    elif infra:
        lines.append(f"- **基础设施**: {infra[:3000]}")
    # 告警
    alerts = state.get("alert_data", "")
    if alerts:
        lines.append(f"- **活跃告警**: {alerts[:600]}")
    # 根因
    rca = state.get("rca_evidence", "")
    if rca:
        lines.append(f"- **根因线索**: {rca[:600]}")
    # K8s
    k8s = state.get("k8sgpt_raw", "")
    if k8s:
        lines.append(f"- **K8s 诊断**: {k8s[:600]}")
    # 日志
    logs = state.get("logs_data", "")
    if logs:
        lines.append(f"- **日志**: {logs[:1500]}")
    # 需求2/3: 无 LLM 时也纳入上一轮执行结果，基于处置结果继续深入分析
    exec_ctx = state.get("exec_context", "")
    if exec_ctx:
        lines.append(f"### 处置结果分析（第 {state.get('iteration', 1)} 轮）\n- **已执行操作结果**: {exec_ctx[:800]}")
        lines.append("- **深入分析**: 需结合处置结果判断是否仍有异常、是否需要下一轮处置。")
    if len(lines) <= 1:
        lines.append("- 未采集到实时数据，请检查数据采集链路或稍后重试。")
    return "\n".join(lines)


async def node_crewai(state: AgentState) -> dict:
    cfg = state.get("llm_config")
    if not _llm_key_ready():
        # Issue1: 无 LLM 时用确定性诊断兜底，保证报告/任务有实质内容
        return {"crewai_result": _deterministic_diagnosis(state)}

    # LLM 动态路由: 根据用户消息匹配最佳专家
    user_msg = state.get("user_message", "")
    intent = state.get("intent", "inspection")
    matched = ExpertRegistry.match_intent(user_msg)
    expert_name = matched.name if matched else intent

    # 获取专家定义 + 关联 Skill
    expert = ExpertRegistry.get(expert_name) or ExpertRegistry.get(intent) or ExpertRegistry.get("inspection")
    # 专家关联的所有 Skill
    skills = ExpertRegistry.skills_of(expert.name) if expert else []
    # 收集 Skill 的工具（去重）
    skill_tools = []
    skill_prompts = []
    for s in skills:
        if not s:
            continue
        skill_tools.extend(t for t in s.tools if t not in skill_tools)
        if s.system_prompt:
            skill_prompts.append(f"[{s.title}] {s.system_prompt}")
    # 工具描述（优先展示当前专家 Skill 相关工具，否则全量）
    tools_desc = ToolRegistry.describe_for_llm()
    if skill_tools:
        tools_desc = "\n".join(
            f"- {t}" for t in skill_tools
        )

    ctx_parts = []
    for k in ["similar_cases", "services_data", "infra_data", "alert_data", "red_metrics", "trace_data", "logs_data", "k8sgpt_raw", "rca_evidence"]:
        v = state.get(k)
        if v: ctx_parts.append(v)
    # 需求2/3: 若存在上一轮已确认执行的处置结果，作为深入分析的上下文
    exec_ctx = state.get("exec_context", "")
    if exec_ctx:
        ctx_parts.append(
            "【上一步已执行的处置操作结果】\n" + exec_ctx[:2500] +
            "\n请基于该执行结果，深入分析：处置是否生效、是否还有残留问题、是否需要进一步处置。"
        )
    context = "\n\n".join(ctx_parts)[:6500]
    if not context:
        context = "(未采集到实时数据)"

    # 修复(P0-3.4 AI 推理)：实体存在性校验 — 防止 LLM 幻觉。
    # 用户消息中提到的服务/资源若不在 services_data 列表中，注入警告到 context。
    user_msg = state.get("user_message", "")
    services_data = state.get("services_data", "")
    if user_msg and services_data:
        _warn = _entity_validation_warning(user_msg, services_data)
        if _warn:
            context = context + "\n\n" + _warn

    if expert:
        system_prompt = (
            f"你是{expert.role}。{expert.goal}。\n\n"
            f"你的专业领域技能:\n{chr(10).join(skill_prompts) if skill_prompts else '(通用分析)'}\n\n"
            f"【重要】以下已采集到的系统数据可直接用于分析，请【直接给出巡检/诊断结论】。\n"
            f"先逐项分析，给出具体的健康状态、发现的问题、风险和建议；不要罗列采集过程或工具调用步骤。\n"
            f"然后在末尾用『## 处置命令』小节，给出 1~3 条**可直接执行的 kubectl/curl 命令**"
            f"（每行一条，不要反引号包裹），用于定位/处置发现的问题。\n"
            f"【硬性要求-确定性资源名】处置命令中的 kubectl 必须使用**真实存在的资源名**：\n"
            f"1. 具体 Pod 名（如 redis-76dd9b85cb-q7p2r）或 Deployment 名（如 redis），禁止使用 `<pod>`、`<PodName>`、`<deployment>` 等占位符；\n"
            f"2. 命名空间必须用**真实的 namespace**（从上下文数据中读取，如 redis/deepflow/observability），禁止写死为 observability 或使用 `<ns>` 占位符；\n"
            f"3. 若上下文已提供某个 Pod 的具体名字和命名空间，请直接引用它（如 `kubectl describe pod <真实Pod名> -n <真实ns>`），不要用 `-l app=xxx` label 选择器代替；\n"
            f"4. 若上下文确实缺少具体资源名，请先用一条 `kubectl get pods -A | grep <关键词>` 定位真实资源，再给出针对性命令；\n"
            f"若数据不足，明确说明缺少哪些数据即可。"
        )
    else:
        system_prompt = (
            f"你是巡检专家。执行全量环境巡检。\n\n"
            f"【重要】以下已采集到的系统数据可直接用于分析，请【直接给出巡检结论】。\n"
            f"先逐项分析，给出具体的健康状态、发现的问题、风险和建议；不要罗列采集过程。\n"
            f"然后在末尾用『## 处置命令』小节，给出 1~3 条**可直接执行的 kubectl/curl 命令**"
            f"（每行一条，不要反引号包裹），用于定位/处置发现的问题。\n"
            f"【硬性要求-确定性资源名】处置命令中的 kubectl 必须使用**真实存在的资源名**：\n"
            f"1. 具体 Pod 名或 Deployment 名，禁止使用 `<pod>`、`<PodName>`、`<deployment>` 等占位符；\n"
            f"2. 命名空间用**真实的 namespace**（从上下文读取），禁止写死 observability 或使用 `<ns>` 占位符；\n"
            f"3. 若上下文已提供具体资源名和命名空间，直接引用，不要用 `-l app=xxx` label 选择器代替；\n"
            f"4. 若缺少具体资源名，先用 `kubectl get pods -A | grep <关键词>` 定位，再给出针对性命令。"
        )

    # P1-5: 诊断类节点 LLM 超时放宽到 120s；超时占位符不进入报告——降级为确定性诊断段落
    history = state.get("history_context", "")
    history_block = f"【历史对话上下文】\n{history}\n\n" if history else ""
    result = await _llm_async(cfg, system_prompt,
                              f"{history_block}用户问题:「{user_msg}」\n已采集数据:\n{context}",
                              expert.role if expert else "巡检专家", timeout=120)
    if _is_llm_failure(result):
        result = _deterministic_diagnosis(state)
    return {"crewai_result": result, "messages": [f"[{_now()}] CrewAI ({expert.role if expert else '巡检'}) 分析完成"]}


async def node_holmes(state: AgentState) -> dict:
    cfg = state.get("llm_config")
    if not _llm_key_ready():
        return {"holmesgpt_result": ""}
    svc = state.get("service", "")
    context = f"服务: {svc}\nRED: {state.get('red_metrics','')}\nTrace: {state.get('trace_data','')}"[:4000]
    result = await _llm_async(cfg, "你是 Trace 调查引擎。深入分析 Trace 与指标，定位根因。", context, "Trace专家", timeout=120)
    return {"holmesgpt_result": result, "messages": [f"[{_now()}] HolmesGPT 分析完成"]}


def _deterministic_plan(state: dict) -> dict:
    """无 LLM 时基于诊断生成确定性处置方案（plan + script）。
    按告警/指标/异常类型给出可操作的恢复建议与只读诊断命令。"""
    svc = state.get("service", "") or "default"
    red = state.get("red_metrics", "") or ""
    alerts = state.get("alert_data", "") or ""
    analysis = state.get("crewai_result", "") or ""
    # 修复(P2-5)：命名空间不再硬编码 observability，优先取用户消息/上下文推断，
    # 复用 _infer_target_from_script 的推断逻辑，避免命令定位不到真实资源。
    _ns = _infer_target_from_script(svc, "observability")
    lines = [f"## 处置方案（确定性建议）\n- **目标服务**: {svc}（namespace: {_ns}）"]
    # 按告警类型给出动作
    if "restart" in alerts.lower() or "restart" in red.lower():
        lines.append("- **动作**: 滚动重启异常服务以恢复\n  - `kubectl rollout restart deployment/%s -n %s`" % (svc, _ns))
    elif "high" in alerts.lower() or "cpu" in red.lower() or "load" in red.lower():
        lines.append("- **动作**: 排查高负载来源并扩容/限流\n  - `kubectl top pods -n %s`\n  - `kubectl describe pod -l app=%s -n %s`" % (_ns, svc, _ns))
    else:
        lines.append("- **动作**: 常规巡检与诊断\n  - `kubectl get pods -n %s -l app=%s`\n  - `kubectl describe pod -l app=%s -n %s`" % (_ns, svc, svc, _ns))
    lines.append("- **风险**: 低（只读诊断 / 受控重启）")
    if analysis:
        lines.append(f"- **依据**: {analysis[:2000]}")
    plan = "\n".join(lines)
    # 提取脚本（把 k8s 相关行收集为可执行命令）
    script = "\n".join(l.strip() for l in plan.splitlines() if l.strip().startswith("kubectl"))
    return {"plan": plan, "script": script}


async def node_plan(state: AgentState) -> dict:
    """Generate execution plan + shell script."""
    cfg = state.get("llm_config")
    if not _llm_key_ready() or not cfg:
        # Issue1: 无 LLM 时用确定性处置方案兜底，任务工作台/AI chat 也能给出可审批的处置建议
        return _deterministic_plan(state)
    analysis = state.get("crewai_result", "")[:6000]
    prompt = f"基于诊断结果，生成执行计划 + Shell/K8s 命令。只输出可执行的脚本。诊断:\n{analysis}"
    result = await _llm_async(cfg, "你是 K8s 运维工程师。生成可直接执行的 Shell 脚本。", prompt, "运维工程师", timeout=120)
    # P1-5: LLM 超时/失败时不把占位符当 plan 使用——回退确定性处置方案
    if _is_llm_failure(result):
        return _deterministic_plan(state)
    plan = result[:6000]
    # Extract script block
    script = ""
    if "```" in plan:
        parts = plan.split("```")
        for i, p in enumerate(parts):
            if i % 2 == 1 and ("kubectl" in p or "curl" in p):
                script = p.strip().replace("bash\n", "").replace("sh\n", "")
                break
    return {"plan": plan, "script": script, "messages": [f"[{_now()}] 执行计划已生成"]}


async def node_risk(state: AgentState) -> dict:
    """LLM risk assessment 1-5."""
    cfg = state.get("llm_config")
    if not cfg:
        return {"risk_score": 1, "risk_reason": "无 LLM 配置"}
    plan = state.get("plan", "")[:4000]
    result = await _llm_async(cfg,
        "评估执行计划风险，输出 JSON: {\"score\": 1-5, \"reason\": \"理由\"}",
        f"执行计画:\n{plan}", "风险评估师", timeout=120)
    try:
        d = json.loads(result.strip().split("\n")[-1] if "{" in result else result)
        return {"risk_score": int(d.get("score", 3)), "risk_reason": d.get("reason", result[:200])}
    except:
        return {"risk_score": 3, "risk_reason": "风险评估默认中等"}


async def node_wait_approval(state: AgentState) -> dict:
    """Pause for human approval. Resumed via /api/v1/ops/tasks/:id/approve.
    If state['approved'] is already True (e.g. chat mode), skip interrupt."""
    # Chat mode / non-interactive: skip interrupt
    if state.get("approved"):
        return {"approved": True, "messages": [f"[{_now()}] 自动审批通过 (非交互模式)"]}

    plan = state.get("plan", "无执行计画")[:2000]
    score = state.get("risk_score", 3)
    reason = state.get("risk_reason", "")

    approval = interrupt({
        "message": "请审批此执行计划",
        "plan": plan,
        "risk_score": score,
        "risk_reason": reason,
        "script": state.get("script", ""),
    })

    approved = approval.get("approved", False) if isinstance(approval, dict) else False
    if not approved:
        return {"approved": False, "human_approved": False, "messages": [f"[{_now()}] 执行计划被拒绝"], "final_response": "## 执行被拒绝\n\n人工审批未通过。"}
    # 人工审批通过 → 同时置 human_approved（允许写操作执行）
    return {"approved": True, "human_approved": True, "messages": [f"[{_now()}] 审批通过, 开始执行"]}


async def node_execute(state: AgentState) -> dict:
    """Emit an Action proposal; mutation is performed only by Action Executor.

    This node intentionally has no shell/Kubernetes side effect.  A signed,
    approved Action must be persisted by query-api and executed by the isolated
    ``ai-action-executor`` service.
    """
    script = state.get("script", "")
    if not script or not state.get("approved"):
        return {"execute_output": ""}
    return {
        "execute_output": "",
        "action_proposal": {"script": script[:2000],
                             "service": state.get("service", "")},
        "execute_error_code": "ACTION_EXECUTOR_REQUIRED",
        "messages": [f"[{_now()}] 已生成动作提案，需经 query-api/Action Executor 执行"],
    }


async def node_verify(state: AgentState) -> dict:
    """升级版验证: Cohen's d 效果量 + 副作用检测 + 二次取样确认"""
    import re, statistics as _stats
    svc = state.get("service", "")
    if not svc:
        return {
            "verify_pass": False,
            "verify_status": "inconclusive",
            "verify_error_code": "VERIFICATION_INCONCLUSIVE",
            "messages": [f"[{_now()}] 验证: 未绑定服务，无法得出成功结论"],
        }

    before_str = state.get("before_metrics", "")
    cid = state.get("cluster_id", "")  # A-5：验证阶段查询也按集群范围
    # P-fix: state 中 request_context 是纯 dict 投影，还原为满足 ScopeView 协议的对象
    rc = ScopeViewSnapshot.from_projection(state.get("request_context"))
    try:
        # 二次取样 (间隔 30s 确认非瞬时波动)
        samples = []
        for _ in range(2):
            raw = await asyncio.to_thread(
                query_metrics, svc, cluster_id=cid,
                request_context=rc,
            )
            data = _parse(raw)
            if data and isinstance(data.get("data"), list):
                items = data["data"]
                lat = sum(float(i.get("avg_ms", 0)) for i in items) / max(len(items), 1)
                total_calls = sum(int(i.get("calls", 0)) for i in items)
                total_errors = sum(int(i.get("errors", 0)) for i in items)
                err = (total_errors / max(total_calls, 1)) * 100
                samples.append({"latency": lat, "error_rate": err})
            # 502 修复: 原用 _t.sleep(30) 同步阻塞 event loop 30s → liveness 超时。
            # 改用 asyncio.sleep 让出 event loop，liveness probe 可正常响应。
            if _ == 0:
                await asyncio.sleep(30)

        after_lat = _stats.mean([s["latency"] for s in samples]) if samples else float("inf")
        after_err = _stats.mean([s["error_rate"] for s in samples]) if samples else 100
        after_str = f"P50延迟={after_lat:.1f}ms 错误率={after_err:.2f}%"

        # Cohen's d 效果量
        bpm = re.findall(r"P50延迟=([\d.]+)", before_str)
        before_lat = float(bpm[0]) if bpm else 0
        effect_size = (before_lat - after_lat) / max(before_lat * 0.1, 1) if before_lat > 0 else 0

        # 副作用检测: 关联服务
        side_effect = False
        try:
            topo_raw = await asyncio.to_thread(
                query_topology, cluster_id=cid,
                request_context=rc,
            )
            topo_data = _parse(topo_raw)
            edges = topo_data.get("edges", []) if topo_data else []
            downstreams = [e.get("target_service", e.get("target", "")) for e in edges if e.get("source_service", e.get("source", "")) == svc]
            for ds in downstreams[:3]:
                ds_data = _parse(await asyncio.to_thread(
                    query_metrics, ds, cluster_id=cid,
                    request_context=rc,
                ))
                if ds_data and isinstance(ds_data.get("data"), list):
                    ds_lat = sum(float(i.get("avg_ms", 0)) for i in ds_data["data"]) / max(len(ds_data["data"]), 1)
                    if ds_lat > before_lat * 1.5 and before_lat > 0:
                        side_effect = True
                        break
        except:
            pass

        improved = effect_size > 0.5 and after_lat < before_lat and not side_effect
        if not improved and effect_size > 0.2:
            improved = after_err < 5 and after_lat < 1000

        # No samples means the observer did not establish an after window;
        # this is inconclusive, never a failure disguised as success.
        status = "passed" if improved else "failed"
        if not samples:
            status = "inconclusive"
        elif side_effect and after_lat > before_lat * 1.5:
            status = "regressed"

        result = {
            "verify_pass": improved,
            "verify_status": status,
            "verify_error_code": "" if improved else f"VERIFICATION_{status.upper()}",
            "verify_effect_size": round(effect_size, 2),
            "verify_side_effect": side_effect,
            "after_metrics": after_str,
            "messages": [f"[{_now()}] 验证: {'✅ 通过' if improved else '❌ 未通过'} (d={effect_size:.2f}, 副作用={'有' if side_effect else '无'})"],
        }
        _persist_verification_result(state, rc, result)
        return result
    except Exception as e:
        return {
            "verify_pass": False,
            "verify_status": "inconclusive",
            "verify_error_code": "VERIFICATION_SOURCE_UNAVAILABLE",
            "messages": [f"[{_now()}] 验证: 数据源不可用 ({e})"],
        }


def _persist_verification_result(state: AgentState, scope: ScopeView, result: dict) -> None:
    """Persist a verification only when the run has an immutable Action binding.

    The legacy/chat graph has no action and therefore cannot manufacture one;
    those results remain explicitly non-durable/inconclusive.  For an action
    run, persistence failure downgrades a computed pass so the UI never shows a
    success that the control plane cannot recover.
    """
    run_id = str(getattr(scope, "run_id", "") or state.get("run_id", ""))
    action_id = str(state.get("action_id", "") or "")
    tenant_id = str(getattr(scope, "tenant_id", "") or state.get("tenant_id", ""))
    if not run_id or not action_id or not tenant_id:
        return
    try:
        import uuid
        from control_plane_client import ControlPlaneClient
        try:
            verification_id = str(uuid.uuid5(uuid.UUID(run_id), f"verification:{action_id}"))
        except (ValueError, AttributeError):
            verification_id = str(uuid.uuid4())
        ControlPlaneClient().append_verification(
            run_id=run_id, tenant_id=tenant_id, verification_id=verification_id,
            action_id=action_id, status=str(result.get("verify_status", "inconclusive")),
            before_snapshot={"raw": state.get("before_metrics", "")},
            after_snapshot={"raw": result.get("after_metrics", "")},
            observation_window_seconds=60,
            # Query API derives the verdict from the checks.  Keep the measured
            # delta for audit, while effect_size is a boolean policy signal so
            # a positive-but-insufficient improvement cannot be mislabeled as
            # passed by a second implementation.
            checks=[{"effect_size": 1.0 if result.get("verify_pass") else 0.0,
                     "measured_effect_size": result.get("verify_effect_size"),
                     "side_effect": result.get("verify_side_effect")}],
            summary=" ".join(str(m) for m in result.get("messages", [])),
        )
    except Exception as exc:
        result["verify_pass"] = False
        result["verify_status"] = "inconclusive"
        result["verify_error_code"] = "VERIFICATION_PERSISTENCE_FAILED"
        result.setdefault("messages", []).append(
            f"[{_now()}] 验证结果未持久化，已降级为不充分: {str(exc)[:160]}"
        )


async def node_report(state: AgentState) -> dict:
    """LLM generates execution summary report."""
    cfg = state.get("llm_config")
    verify_status = state.get("verify_status", "")
    verify = {
        "passed": "✅ 修复成功",
        "failed": "❌ 修复未达到预期",
        "regressed": "⚠️ 验证发现回归",
        "inconclusive": "⏳ 验证不充分，无法确认结果",
    }.get(verify_status, "✅ 修复成功" if state.get("verify_pass") else "❌ 修复未达到预期")
    context = f"""
    before: {state.get('before_metrics','')}
    after: {state.get('after_metrics','')}
    execute: {state.get('execute_output','')[:500]}
    verify: {verify}
    """
    if cfg:
        rep = await _llm_async(cfg, "生成运维执行总结报告，含 before/after 对比和后续建议。", context, "报告生成器")
        return {"report": rep[:8000]}
    return {"report": f"执行结果: {verify}（状态={verify_status or 'unknown'}）"}


async def node_memorize(state: AgentState) -> dict:
    """Persist a successful case without creating a second production owner.

    Query API owns production knowledge persistence.  The historical local
    Chroma writer has no tenant/cluster transaction or audit boundary, so a
    scoped/production graph must report the write as unavailable rather than
    silently persisting data outside Query API.
    """
    if state.get("verify_pass") and state.get("crewai_result"):
        request_context = ScopeViewSnapshot.from_projection(state.get("request_context"))
        scoped = bool(
            str(getattr(request_context, "tenant_id", "") or "").strip()
            and str(getattr(request_context, "cluster_id", "") or "").strip()
        )
        if scoped or _llm_production_mode():
            return {
                "messages": [
                    f"[{_now()}] 案例未入库 (知识库写入需经 Query API owner，当前写入边界未配置)"
                ]
            }
        ok, reason = _case_quality_check(state)
        if not ok:
            return {"messages": [f"[{_now()}] 案例未入库（质量校验未通过: {reason}）"]}
        try:
            import uuid
            case = {
                "case_id": uuid.uuid4().hex[:12],
                "service": state.get("service", ""),
                "symptom": _cap_symptom(state.get("user_message", "")),
                "root_cause": state.get("crewai_result", "")[:2000],
                "plan": state.get("plan", "")[:2000],
                "outcome": "success",
                "report": state.get("report", "")[:2000],
            }
            # rag.add_case (ChromaDB) 同步阻塞 IO，丢线程池
            from rag import rag
            await asyncio.to_thread(rag.add_case, case)
            return {"messages": [f"[{_now()}] 案例已入库 (总数: {rag.count()})"]}
        except: pass
    return {"messages": [f"[{_now()}] 案例未入库 (verify_pass={state.get('verify_pass')})"]}


async def node_coordinator(state):
    """双层 Agent - Coordinator：拆解用户意图为子任务列表。"""
    cfg = state.get("llm_config")
    user_msg = state.get("user_message", "")
    context = (state.get("services_data", "") + state.get("alert_data", ""))[:2000]
    raw = ""
    if (not _llm_key_ready()) or is_mock_enabled():
        raw = json.dumps(mock_coordinator_plan(), ensure_ascii=False)
    else:
        # B5: coordinator system prompt 注入 specialist persona 目录 (LLM 自主路由)
        try:
            from persona_registry import load_personas, build_catalog, PERSONAS_BUILTIN_DIR, USER_PERSONAS_DIR
            from dual_agent import coordinator_system_prompt
            _catalog = build_catalog(load_personas(PERSONAS_BUILTIN_DIR, USER_PERSONAS_DIR))
            _sys = coordinator_system_prompt(_catalog)
        except Exception:
            _sys = "你是任务协调器。把用户请求拆解为可并行执行的子任务，" \
                   "输出 JSON 数组，每项含 task_id/task_type/target_service/query。" \
                   "task_type 限 diagnosis/inspection/ops/query。只输出 JSON。"
        raw = await _llm_async(cfg, _sys,
                   f"用户请求:「{user_msg}」\n上下文:\n{context}", "Coordinator")
    subtasks = parse_subtasks(raw)
    if not subtasks:
        subtasks = [{"task_id": "t1", "task_type": "diagnosis",
                     "target_service": state.get("service", ""), "query": user_msg}]
    return {"subtasks": subtasks, "messages": [f"[{_now()}] Coordinator 拆解为 {len(subtasks)} 个子任务"]}


async def node_subagent(state):
    """双层 Agent - 子 Agent：并行跑每个子任务的 function-calling 循环。"""
    subtasks = state.get("subtasks") or []
    if not subtasks:
        return {"sub_results": {}}
    cfg = state.get("llm_config")
    if (not _llm_key_ready()) or is_mock_enabled():
        decision = mock_llm_decision
    else:
        from llm_fc import make_llm_decision_fn
        decision = make_llm_decision_fn(cfg, "你是可观测性诊断子 Agent，通过调用工具收集证据并给出结论。")
    # run_subtasks 内部并行跑子 Agent 的 function-calling 循环（含同步 LLM 调用），
    # 整体丢线程池避免阻塞 event loop。decision 内部若调 _llm 仍是同步阻塞，
    # 但在 to_thread 的线程中执行，不会卡 uvicorn event loop。
    sub_results = await asyncio.to_thread(run_subtasks, subtasks, decision, ExpertRegistry)
    return {"sub_results": sub_results,
            "messages": [f"[{_now()}] {len(sub_results)} 个子 Agent 完成"]}


async def node_reviewer(state):
    """双层 Agent - Reviewer：合并审查全部子结论，输出最终报告。"""
    cfg = state.get("llm_config")
    sub_results = state.get("sub_results") or {}
    if (not _llm_key_ready()) or is_mock_enabled():
        final = merge_review(sub_results, mock_reviewer_result)
    else:
        parts = "\n\n".join(f"[{tid}]({r.get('task_type', '')}): {r.get('conclusion', '')[:500]}"
                            for tid, r in sub_results.items())
        llm_final = await _llm_async(cfg, "你是结果审查员。合并子 Agent 结论，校验依据与冲突，输出最终诊断报告。",
                         f"子结论:\n{parts}", "Reviewer")
        # LLM 失败/为空时兜底到确定性合并
        final = llm_final if llm_final and not llm_final.startswith("[LLM") else merge_review(sub_results, None)
    return {"review_result": final, "final_response": final,
            "messages": [f"[{_now()}] Reviewer 审查完成"]}


async def node_summarize(state: AgentState) -> dict:
    # If rejected / already set (dual mode reviewer), pass through final_response
    if state.get("final_response"):
        return {"final_response": state.get("final_response"),
                "messages": [f"[{_now()}] 报告生成完成"]}

    intent = state.get("intent", "inspection")
    svc = state.get("service", "")
    msg = state.get("user_message", "")
    crewai = state.get("crewai_result", "")
    holmes = state.get("holmesgpt_result", "")
    k8sgpt = state.get("k8sgpt_raw", "")
    report = state.get("report", "")
    verify = "✅ 执行成功" if state.get("verify_pass") else "❌ 执行未达预期" if state.get("script") else ""
    plan = state.get("plan", "")
    risk = state.get("risk_score", 0)
    explicit_tools = _explicit_tool_routes(msg)

    # P1-5: LLM 超时/错误占位符不得进入最终报告诊断结论 → 降级为确定性诊断段落
    if _is_llm_failure(crewai):
        crewai = _deterministic_diagnosis(state)
    if _is_llm_failure(holmes):
        holmes = ""

    parts = [f"## 分析报告\n**时间**: {_now()}"]
    if state.get("light_query"):
        # P1-5: 信息查询类问题 → 直接基于采集数据作答，不做异常判定
        parts.append(_build_info_answer(state))
    elif intent == "inspection":
        parts.append(crewai or "LLM 未配置")
    elif intent == "diagnosis":
        # P3-7: 目标为空时输出"未指定"，不残留 '-'（P3-1 遗留的 markdown 残留修复）
        parts.append(f"**目标**: {svc or '未指定'} | **问题**: {msg}")
        if holmes: parts.append(f"### Trace 分析\n{holmes[:3000]}")
        if crewai: parts.append(f"### 诊断结论\n{crewai[:3000]}")
    else:
        parts.append(crewai or "LLM 未配置")

    if "query_knowledge" in explicit_tools:
        knowledge = state.get("similar_cases", "")
        knowledge_error = state.get("knowledge_tool_error", "")
        if knowledge_error:
            parts.append(f"### 知识库检索不可用\n{str(knowledge_error)[:1500]}\n未能据此引用知识库内容。")
        else:
            parts.append(knowledge[:6000] if knowledge else "### 知识库检索结果\n未检索到相关知识。")
    k8sgpt_error = state.get("k8sgpt_error", "")
    if k8sgpt_error:
        parts.append(f"### K8sGPT 诊断不可用\n{str(k8sgpt_error)[:1500]}\n未能据此判定集群健康。")
    elif k8sgpt and (intent == "diagnosis" or "k8sgpt_diagnose" in explicit_tools):
        parts.append(f"### K8sGPT 诊断\n{k8sgpt[:2000]}")
    if state.get("infra_error"):
        parts.append(f"### 基础设施证据不确定\n{str(state['infra_error'])[:1500]}\n未能据此判定集群健康。")

    # P1-3/P1-5: 统一 0~1 风险分（由诊断结论中的异常证据计算），不再输出 ⭐(1/5) 文本
    risk_score01 = _risk_from_evidence(crewai or "") if (crewai or risk) else 0.0
    if risk_score01 > 0:
        parts.append(f"\n**风险等级**: {risk_score01:.2f} (0~1)")
    if plan: parts.append(f"\n### 执行计画\n{plan[:6000]}")
    if verify: parts.append(f"\n### 执行结果\n{verify}")
    if report: parts.append(f"\n### 执行报告\n{report[:8000]}")

    # P3-1: 全文清理——多余空行折叠为 2、去行尾空白、去结尾空行
    import re as _re
    text = "\n\n".join(parts)
    text = _re.sub(r"\n{3,}", "\n\n", text)
    text = "\n".join(l.rstrip() for l in text.splitlines()).rstrip("\n")
    return {"final_response": text, "messages": [f"[{_now()}] 报告生成完成"]}


# ═══════════════════════════════════════════════════════════════
#  Graph
# ═══════════════════════════════════════════════════════════════

def build_graph(checkpointer=None, mode: str = "full"):
    """构建 LangGraph。mode:
    - "full": 完整 15 节点 DAG (collect→...→execute→verify→report→memorize→summarize)，用于运维任务
    - "chat": 精简 6 节点 DAG (collect→clean→rca→rag→crewai→summarize)，用于交互式 Chat，
             只做 1 次 LLM 调用，避免 Chat 卡在多次 LLM + verify 30s sleep 上。
    """
    builder = StateGraph(AgentState)

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
    if mode == "chat":
        # B2-03（F-07）：chat 图入口意图分流——普通 Chat 不做固定实时采集。
        nodes.insert(0, ("chat_classify", node_chat_classify))
    for name, fn in nodes:
        builder.add_node(name, fn)

    if mode == "chat":
        # B2-03：chat 入口先分流：需要调查 → investigation_required CTA（不再执行 node_collect
        # 的固定实时采集）；纯对话 → 直接 summarize；否则才走 collect→clean 轻量采集链路。
        builder.set_entry_point("chat_classify")
        builder.add_edge("collect", "clean")

        def route_chat(state: AgentState) -> str:
            if state.get("investigation_required"):
                return "__end__"
            if state.get("chat_pure"):
                return "summarize"
            return "collect"

        builder.add_conditional_edges(
            "chat_classify", route_chat,
            {"__end__": END, "summarize": "summarize", "collect": "collect"})

        # P1-5: 轻量意图分流——信息查询跳过深度 RCA/RAG/CrewAI，走 summarize。
        def route_light(state: AgentState) -> str:
            # Ordinary Chat may perform the bounded read-only collection above,
            # but correlation/RCA is an Investigation capability.  The explicit
            # CTA branch is handled by chat_classify; reaching clean here means
            # the user did not request a durable Investigation Run.
            request_context = ScopeViewSnapshot.from_projection(state.get("request_context"))
            if getattr(request_context, "workload_kind", "") == "chat":
                return "summarize" if state.get("light_query") else "rag"
            return "summarize" if state.get("light_query") else "rca"

        builder.add_conditional_edges("clean", route_light,
                                      {"summarize": "summarize", "rag": "rag", "rca": "rca"})
    else:
        builder.set_entry_point("collect")
        builder.add_edge("collect", "clean")
        builder.add_edge("clean", "rca")

    builder.add_edge("rca", "rag")

    if mode == "chat":
        # Chat 精简路径: collect→clean→rag→crewai→summarize
        # 普通 Chat 不运行结构化 RCA/跨源关联；需完整根因分析时由
        # chat_classify 返回 Investigation CTA。只做 1 次 LLM 分析。
        builder.add_edge("rag", "crewai")
        builder.add_edge("crewai", "summarize")
    elif mode == "dual":
        # 双层 Agent 路径: collect→clean→rca→rag→coordinator→subagent→reviewer→summarize
        builder.add_edge("rag", "coordinator")
        builder.add_edge("coordinator", "subagent")
        builder.add_edge("subagent", "reviewer")
        builder.add_edge("reviewer", "summarize")
    else:
        # 完整路径
        builder.add_edge("rag", "crewai")
        builder.add_edge("rag", "holmes")
        builder.add_edge("crewai", "plan")
        builder.add_edge("holmes", "plan")
        builder.add_edge("plan", "risk")
        builder.add_edge("risk", "wait_approval")

        # Conditional: approved → execute, rejected → summarize
        def route_approval(state: AgentState) -> str:
            return "execute" if state.get("approved") else "summarize"

        builder.add_conditional_edges("wait_approval", route_approval, {"execute": "execute", "summarize": "summarize"})
        builder.add_edge("execute", "verify")
        builder.add_edge("verify", "report")
        builder.add_edge("report", "memorize")
        builder.add_edge("memorize", "summarize")

    builder.add_edge("summarize", END)
    return builder.compile(checkpointer=checkpointer)


# ═══════════════════════════════════════════════════════════════
#  BrainOrchestrator
# ═══════════════════════════════════════════════════════════════

class BrainOrchestrator:
    def __init__(self, db_path=None):
        import os as _os
        import tempfile
        self._stateless_worker = (
            _os.environ.get("INVESTIGATION_WORKER_MODE", "0").lower() in {"1", "true", "yes", "on"}
            or (
                _os.environ.get("AIOPS_DEPLOYMENT_MODE", "").lower() == "production"
                and _os.environ.get("AIOPS_ORCHESTRATOR_LOCAL_CHECKPOINTS", "0").lower() not in {"1", "true", "yes", "on"}
            )
        )
        self.llm_config = None
        if self._stateless_worker:
            # Investigation workers rebuild work from query-api Run snapshots and
            # leases. They must not open the legacy SQLite session/checkpoint DB.
            self._db_path = ""
            self._conn = None
            self.checkpointer = MemorySaver()
            self._async_saver_initialized = False
            self._async_saver_loop = None
            self._async_conn = None
            self.graph = build_graph(checkpointer=self.checkpointer, mode="full")
            self.chat_graph = build_graph(checkpointer=self.checkpointer, mode="chat")
            self.dual_graph = build_graph(checkpointer=self.checkpointer, mode="dual")
            from skill_registry import _init_defaults
            _init_defaults()
            return
        if db_path is None:
            data_dir = _os.environ.get("AIOPS_DATA_DIR", "/var/lib/aiops")
            db_path = _os.path.join(data_dir, "ai-sessions.db")
        # 持久化目录不可写（本机/无 PVC 环境）时降级到临时目录，绝不阻断 import orchestrator
        try:
            _os.makedirs(_os.path.dirname(db_path), exist_ok=True)
        except OSError:
            db_path = _os.path.join(tempfile.gettempdir(), "ai-sessions.db")
        self._db_path = db_path
        import sqlite3
        _os.makedirs(_os.path.dirname(db_path), exist_ok=True)
        # 同步 sqlite3 连接：仅供 main.py 直接 SQL 查询/删除 checkpoints 表使用
        # （list_sessions / delete_session 端点）。AsyncSqliteSaver 落盘到同一文件，
        # 该连接可读到 saver 写入的行（SQLite WAL 模式支持并发读）。
        self._conn = sqlite3.connect(db_path, check_same_thread=False)
        # 节点已改为 async def → graph.ainvoke/astream 要求 checkpointer 支持 async 方法。
        # SqliteSaver (sync) 会抛 "does not support async methods"；
        # AsyncSqliteSaver 需要 running event loop（模块加载时不可用，因 __init__ 在
        # `brain = BrainOrchestrator()` 模块加载期执行，无 running loop）。
        # 方案: __init__ 用 MemorySaver 占位（同时支持 sync/async，不阻塞 ainvoke），
        # 首次 ainvoke/astream 前在 async context 中调 _ensure_async_checkpointer()
        # 延迟切换为 AsyncSqliteSaver，使 checkpoint 落盘到 SQLite 文件。
        self.checkpointer = MemorySaver()
        self._async_saver_initialized = False
        self._async_saver_loop = None  # saver 绑定的 event loop（用于跨 loop 检测）
        self._async_conn = None        # aiosqlite 连接（saver 持有，此处保引用防 GC）
        # 双图: chat_graph 用于交互式 Chat (精简快速)，graph 用于完整运维任务
        self.graph = build_graph(checkpointer=self.checkpointer, mode="full")
        self.chat_graph = build_graph(checkpointer=self.checkpointer, mode="chat")
        self.dual_graph = build_graph(checkpointer=self.checkpointer, mode="dual")
        # 初始化 Skill Registry
        from skill_registry import _init_defaults
        _init_defaults()

    async def _ensure_async_checkpointer(self):
        """在 async context 中延迟初始化 AsyncSqliteSaver，替换占位 MemorySaver。

        为什么需要延迟初始化:
        - AsyncSqliteSaver.__init__ 调 `asyncio.get_running_loop()`，要求当前线程已有
          running event loop。但 BrainOrchestrator 在模块加载期（`brain = BrainOrchestrator()`）
          实例化，此时无 running loop → 不能在 __init__ 中直接创建。
        - 故 __init__ 用 MemorySaver 占位，首次 ainvoke/astream 前在 async context 中
          调本方法切换为 AsyncSqliteSaver，使 checkpoint 落盘。

        跨 event loop 兼容:
        - main.py 的 sync handler 用 `asyncio.run()` 每次新建临时 event loop；
          AsyncSqliteSaver 把 aiosqlite 的回调绑定到创建时的 loop（self.loop）。
          若第一次 asyncio.run() 结束（loop 关闭），第二次 asyncio.run() 复用旧 saver，
          aiosqlite 会向已关闭的 loop 提交协程 → RuntimeError。
        - 解法: 检测当前 running loop 与 saver 绑定的 loop 是否一致且仍存活；
          不一致时关闭旧 aiosqlite 连接并重建 saver（同一 db 文件，checkpoint 不丢）。
        """
        if self._stateless_worker:
            return
        try:
            current_loop = asyncio.get_running_loop()
        except RuntimeError:
            # 本方法是 async def，正常情况下一定有 running loop；防御性处理
            current_loop = None

        # 已初始化且绑定到当前仍存活的 loop → 复用，避免重复创建
        if (self._async_saver_initialized
                and self._async_saver_loop is not None
                and self._async_saver_loop is current_loop
                and not self._async_saver_loop.is_closed()):
            return

        # loop 变了（或首次初始化）→ 关闭旧 aiosqlite 连接，重建 saver
        if self._async_conn is not None:
            try:
                await self._async_conn.close()
            except Exception:
                pass
            self._async_conn = None
            self._async_saver_initialized = False

        from langgraph.checkpoint.sqlite.aio import AsyncSqliteSaver
        import aiosqlite
        conn = await aiosqlite.connect(self._db_path)
        self._async_conn = conn
        self.checkpointer = AsyncSqliteSaver(conn)
        await self.checkpointer.setup()  # 建表（首次，幂等）
        # 重建图，绑定新 checkpointer（旧图持有的是 MemorySaver 引用）
        self.graph = build_graph(checkpointer=self.checkpointer, mode="full")
        self.chat_graph = build_graph(checkpointer=self.checkpointer, mode="chat")
        self.dual_graph = build_graph(checkpointer=self.checkpointer, mode="dual")
        self._async_saver_loop = current_loop
        self._async_saver_initialized = True

    def get_session_state(self, thread_id: str) -> dict | None:
        """同步读取某会话（thread）的最终 checkpoint state（主线程安全，不依赖 event loop）。

        P0 修复: main.py 的 get_session/list_sessions 曾用 `graph.get_state()`（sync 包装），
        但 AsyncSqliteSaver 在 get_tuple 中检测 `asyncio.get_running_loop() is self.loop` 时会抛
        InvalidStateError（主线程同步调用），或绑定到已关闭的 loop 抛 RuntimeError。
        这里改为直接从同步 `self._conn` 读 checkpoints 表并反序列化 checkpoint，
        完全绕开 async checkpointer，跨 event loop 安全。

        返回会话摘要和原始 messages/state 字段，供历史会话无损还原 AiChat 卡片。
        """
        if self._conn is None:
            return None
        try:
            cur = self._conn.execute(
                "SELECT type, checkpoint FROM checkpoints WHERE thread_id = ? "
                "ORDER BY checkpoint_id DESC LIMIT 1",
                (thread_id,),
            )
            row = cur.fetchone()
            if not row:
                return None
            type_, blob = row
            from langgraph.checkpoint.serde.jsonplus import JsonPlusSerializer
            serde = JsonPlusSerializer()
            ckpt = serde.loads_typed((type_, bytes(blob)))
            values = (ckpt or {}).get("channel_values") or {}
            return {
                "user_message": values.get("user_message", ""),
                "final_response": values.get("final_response", ""),
                "intent": values.get("intent", ""),
                "service": values.get("service", ""),
                "messages": values.get("messages", []),
                "plan": values.get("plan", ""),
                "script": values.get("script", ""),
                "risk_score": values.get("risk_score", 0),
                "risk_reason": values.get("risk_reason", ""),
                "execute_output": values.get("execute_output", ""),
                "exec_context": values.get("exec_context", ""),
            }
        except Exception as _e:
            print(f"[get_session_state] error for {thread_id}: {_e}")
            return None

    def set_llm_config(self, config: dict | None):
        if config is None:
            self.llm_config = None
            _LLM_KEY_HOLDER["api_key"] = ""
            return
        # 安全：明文 key 只存进程内存单例，不进 state/checkpoint。
        # In production the only accepted credential is the proxy ingress token;
        # direct provider keys/base URLs are rejected before they can reach the
        # CrewAI client or a subprocess.
        if _llm_production_mode():
            proxy_url = (os.environ.get("AI_LLM_EGRESS_PROXY_URL") or os.environ.get("LLM_PROXY_URL") or "").strip().rstrip("/")
            proxy_token = os.environ.get("LLM_PROXY_TOKEN", "").strip()
            candidate_url = str(config.get("base_url") or "").strip().rstrip("/")
            candidate_key = str(config.get("api_key") or "")
            if not proxy_url or not proxy_token or candidate_key != proxy_token or not candidate_url.startswith(proxy_url + "/v1/proxy/"):
                self.llm_config = None
                _LLM_KEY_HOLDER["api_key"] = ""
                return
        _LLM_KEY_HOLDER["api_key"] = config.get("api_key", "")
        self.llm_config = {
            # 剔除 api_key：state/checkpoint 只保留非敏感配置
            "model": config.get("model", "gpt-4o"),
            "base_url": config.get("base_url", "https://api.openai.com/v1"),
            "provider": config.get("provider", "openai"),
            "backend": config.get("backend", "openai"),
        }
        # 安全：不再把 API Key 写入进程环境变量（避免常驻 env、泄漏到子进程/log）。
        # LLM 调用时（_llm 内）按每次请求传入的 cfg + 内存单例 key 临时设置所需环境变量，
        # 用完即走，避免跨请求/跨会话共享明文 key。

    async def execute_sync(self, intent: str, service: str, message: str, thread_id: str = "default",
                           cluster_id: str | None = None, mode: str = "chat", *,
                           request_context: ScopeView | None = None) -> str:
        """Run DAG and return final_response text. (async — 调用方需 await)

        P1-2 修复: 默认走 chat_graph（精简图，无 wait_approval interrupt），
        避免非流式 /ai/chat 因 full 图挂在审批中断上而恒返回空报告。
        需要完整运维图请用 execute_sync_full(mode="full")。
        """
        final = await self._run_dag(
            intent, service, message, thread_id, cluster_id, mode=mode,
            request_context=request_context,
        )
        return final.get("final_response", "")

    async def execute_sync_full(self, intent: str, service: str, message: str, thread_id: str = "default",
                                cluster_id: str | None = None, mode: str = "full", *,
                                request_context: ScopeView | None = None) -> dict:
        """Run full DAG and return complete final state. (async — 调用方需 await)
        /ops/tasks、workflow run 等完整运维链路使用 mode="full"（含审批/执行/验证）。"""
        return await self._run_dag(
            intent, service, message, thread_id, cluster_id, mode=mode,
            request_context=request_context,
        )

    async def _run_dag(self, intent: str, service: str, message: str, thread_id: str = "default",
                       cluster_id: str | None = None, mode: str = "chat", *,
                       request_context: ScopeView | None = None) -> dict:
        if not isinstance(request_context, ScopeView):
            return {"final_response": "[invalid_context]", "error": "invalid_context"}
        canonical_cluster = str(request_context.cluster_id)
        if cluster_id not in (None, "", canonical_cluster):
            return {"final_response": "[invalid_context]", "error": "invalid_context"}
        cluster_id = canonical_cluster
        await self._ensure_async_checkpointer()  # 延迟切换 MemorySaver → AsyncSqliteSaver
        if not service:
            service = await asyncio.to_thread(
                self._detect_service, message, request_context=request_context
            )
        initial: AgentState = {
            "messages": [], "intent": intent, "service": service, "user_message": message,
            "cluster_id": cluster_id,  # A-5：集群范围透传至查询工具
            # P-fix: request_context 以可 msgpack 序列化的纯 dict 投影入 state，
            # 避免持久化 saver 序列化 ScopeView 实例崩溃；节点读取处用 ScopeViewSnapshot 还原。
            "request_context": ScopeViewSnapshot.to_projection(request_context),
            "llm_config": self.llm_config,
            "services_data": "", "infra_data": "", "infra_error": "", "alert_data": "", "red_metrics": "", "trace_data": "", "k8sgpt_raw": "", "k8sgpt_error": "",
            "rca_mode": "", "rca_root_cause": "", "rca_evidence": "", "rca_confidence": 0, "rca_hypotheses_tested": 0,
            "similar_cases": "", "knowledge_tool_error": "", "crewai_result": "", "holmesgpt_result": "",
            "plan": "", "script": "", "risk_score": 0, "risk_reason": "",
            # P0-2: 初始 approved=False——非交互 full 图必须走人工审批
            # (approved 由 approve/resume 显式置位；chat 图无 wait_approval 节点不受影响)
            "approved": False, "human_approved": False, "execute_output": "",
            "before_metrics": "", "after_metrics": "", "verify_pass": False,
            "verify_status": "inconclusive", "verify_error_code": "",
            "action_id": "",
            "final_response": "", "report": "", "error": "",
            "subtasks": [], "sub_results": {}, "review_result": "",
            "light_query": False,
        }
        config = {"configurable": {"thread_id": thread_id}}
        try:
            # 节点已全部改为 async def，必须用 ainvoke（sync invoke 会在已运行的
            # event loop 中失败）。ainvoke 内部并发执行 async 节点，LLM 调用走
            # asyncio.to_thread 不阻塞 event loop → liveness probe 不超时。
            # P1-2: 与 stream_sync 的图选择逻辑保持一致——chat/dual/full 各用其图；
            # chat 图无 wait_approval interrupt，非流式对话不再挂起返回空报告。
            if mode == "dual":
                graph = getattr(self, "dual_graph", self.graph)
            elif mode == "full":
                graph = getattr(self, "graph", self.graph)
            else:
                graph = getattr(self, "chat_graph", self.graph)
            return await graph.ainvoke(initial, config)
        except Exception as e:
            return {"final_response": f"[DAG 执行异常: {str(e)[:200]}]", "error": str(e)[:200]}

    async def stream_sync(self, intent: str, service: str, message: str, thread_id: str = "default",
                          mode: str = "chat", exec_context: str = "", iteration: int = 1,
                          cluster_id: str | None = None, *,
                          request_context: ScopeView | None = None,
                          history_context_override: str = ""):
        """异步生成器: async for event in brain.stream_sync(...)。
        节点为 async def，必须用 graph.astream (不能用 sync graph.stream, 会在
        已运行的 event loop 中失败)。astream 让出 event loop 给 liveness probe。
        exec_context: 需求2/3 多轮闭环——上一轮已确认执行的处置脚本的结果，作为下一轮深入分析的上下文。
        """
        if not isinstance(request_context, ScopeView):
            yield {"type": "error", "error": "invalid_context", "text": "invalid_context"}
            return
        canonical_cluster = str(request_context.cluster_id)
        if cluster_id not in (None, "", canonical_cluster):
            yield {"type": "error", "error": "invalid_context", "text": "invalid_context"}
            return
        cluster_id = canonical_cluster
        await self._ensure_async_checkpointer()  # 延迟切换 MemorySaver → AsyncSqliteSaver
        if not service:
            service = await asyncio.to_thread(
                self._detect_service, message, request_context=request_context
            )
        # 多轮上下文：读上一轮 checkpoint（user_message + final_response 摘要）注入本轮
        history_context = str(history_context_override or "")
        try:
            if not history_context:
                prev = await asyncio.to_thread(self.get_session_state, thread_id)
                if prev and prev.get("user_message") and prev.get("user_message") != message:
                    prev_q = str(prev.get("user_message", ""))[:200]
                    prev_a = str(prev.get("final_response", ""))[:500]
                    history_context = f"上一轮问题: {prev_q}\n上一轮回答要点: {prev_a}"
        except Exception:
            history_context = ""
        initial: AgentState = {
            "messages": [], "intent": intent, "service": service, "user_message": message,
            "history_context": history_context,
            "cluster_id": cluster_id,  # A-5：集群范围透传至查询工具
            # P-fix: request_context 以可 msgpack 序列化的纯 dict 投影入 state，
            # 避免持久化 saver 序列化 ScopeView 实例崩溃；节点读取处用 ScopeViewSnapshot 还原。
            "request_context": ScopeViewSnapshot.to_projection(request_context),
            "llm_config": self.llm_config,
            "services_data": "", "infra_data": "", "infra_error": "", "alert_data": "", "red_metrics": "", "trace_data": "", "k8sgpt_raw": "", "k8sgpt_error": "",
            "rca_mode": "", "rca_root_cause": "", "rca_evidence": "", "rca_confidence": 0, "rca_hypotheses_tested": 0,
            "similar_cases": "", "knowledge_tool_error": "", "crewai_result": "", "holmesgpt_result": "",
            "plan": "", "script": "", "risk_score": 0, "risk_reason": "",
            # P0-2: 初始 approved=False——非交互 full 图必须走人工审批
            # (approved 由 approve/resume 显式置位；chat 图无 wait_approval 节点不受影响)
            "approved": False, "human_approved": False, "execute_output": "",
            "before_metrics": "", "after_metrics": "", "verify_pass": False,
            "verify_status": "inconclusive", "verify_error_code": "",
            "action_id": "",
            "final_response": "", "report": "", "error": "",
            "subtasks": [], "sub_results": {}, "review_result": "",
            "exec_context": exec_context, "iteration": iteration,
            "light_query": False,
        }
        config = {"configurable": {"thread_id": thread_id}}
        step_names = {"collect": "数据采集", "clean": "数据清洗", "rca": "根因分析", "rag": "案例匹配",
                      "crewai": "CrewAI 分析", "holmes": "Trace 分析",
                      "coordinator": "Coordinator 拆解", "subagent": "子Agent 分析", "reviewer": "Reviewer 审查",
                      "plan": "生成方案", "risk": "风险评估", "summarize": "汇总报告"}
        try:
            yield {"type": "progress", "node": "start", "text": "分析开始", "step": 0, "total": 8}
            step_num = 0
            suggestion = {}
            evidence = {}  # P1-5b: 采集节点的异常证据（services_data/alert_data/red_metrics）
            is_light = False  # P1-5b: 信息查询(light_query)时强制不弹处置建议卡
            # 交互式 Chat 使用精简 chat_graph；dual 模式用双层 Agent 图；full 用完整运维图
            if mode == "dual":
                graph = getattr(self, "dual_graph", self.graph)
            elif mode == "full":
                graph = getattr(self, "graph", self.graph)
            else:
                graph = getattr(self, "chat_graph", self.graph)
            async for step in graph.astream(initial, config):
                node_name = list(step.keys())[0] if step else "unknown"
                node_data = step.get(node_name, {}) if step else {}
                step_num += 1
                label = step_names.get(node_name, node_name)
                yield {"type": "progress", "node": node_name, "text": f"{label}", "step": min(step_num, 7), "total": 8}
                # A CTA intentionally terminates the Chat graph before
                # ``summarize``. Preserve the classifier's final response so
                # the SSE stream emits the Investigation CTA instead of an
                # empty ``done`` frame.
                if node_name == "chat_classify" and node_data.get("investigation_required"):
                    suggestion.update(node_data)
                # 工具级事件（节点级推断；真实工具级采集为独立后续）
                explicit_tools = _explicit_tool_routes(message)
                explicit_tool = explicit_tools[0] if explicit_tools else None
                tool_node_map = {"crewai": "CrewAI 分析", "holmes": "Trace 调查", "rca": "RCA 根因分析",
                                 "rag": "RAG 案例匹配", "plan": "生成操作方案"}
                if "query_knowledge" in explicit_tools:
                    tool_node_map.pop("rag", None)
                tool_id = f"tool_{node_name}_{step_num}"
                if node_name in tool_node_map:
                    yield {"type": "tool_start", "tool_call_id": tool_id,
                           "name": tool_node_map[node_name], "status": "pending", "arguments": {}}
                    # 提取用户友好的结果摘要, 避免把整个 state dict str() 后给用户看
                    friendly_result = _friendly_tool_result(node_name, node_data)
                    yield {"type": "tool_end", "tool_call_id": tool_id,
                           "name": tool_node_map[node_name], "status": "success",
                           "arguments": {}, "result": friendly_result}
                if node_name == "rca":
                    root_cause = str(node_data.get("rca_root_cause") or "").strip()
                    if root_cause and root_cause != "unknown":
                        yield {"type": "hypothesis", "content": root_cause,
                               "confidence": float(node_data.get("rca_confidence") or 0),
                               "status": "proposed",
                               "confirmed_by_evidence": bool(node_data.get("rca_evidence"))}
                if node_name == "collect" and "k8sgpt_diagnose" in explicit_tools:
                    tool_id = f"tool_k8sgpt_{step_num}"
                    k8sgpt_error = node_data.get("k8sgpt_error", "")
                    k8sgpt_raw = str(node_data.get("k8sgpt_raw") or "").strip()
                    error_lower = str(k8sgpt_error).lower()
                    unavailable_markers = ("unavailable", "not installed", "no verifiable", "not found")
                    if k8sgpt_error:
                        # V9.2 §32: dependency missing / backend unreachable → unavailable; other error → failed
                        k8sgpt_status = "unavailable" if any(marker in error_lower for marker in unavailable_markers) else "failed"
                    elif not k8sgpt_raw:
                        # V9.2 §32: tool executed successfully + empty result → no_data (NOT unavailable)
                        k8sgpt_status = "no_data"
                    else:
                        k8sgpt_status = "success"
                    yield {"type": "tool_start", "tool_call_id": tool_id,
                           "name": "k8sgpt_diagnose", "status": "pending", "arguments": {}}
                    yield {"type": "tool_end", "tool_call_id": tool_id,
                           "name": "k8sgpt_diagnose",
                           "status": k8sgpt_status, "arguments": {},
                           "result": str(k8sgpt_error or k8sgpt_raw or "未返回 K8sGPT 结果")[:3000]}
                elif node_name == "rag" and "query_knowledge" in explicit_tools:
                    tool_id = f"tool_knowledge_{step_num}"
                    knowledge_error = node_data.get("knowledge_tool_error", "")
                    knowledge_result = str(node_data.get("similar_cases") or "").strip()
                    yield {"type": "tool_start", "tool_call_id": tool_id,
                           "name": "query_knowledge", "status": "pending", "arguments": {}}
                    yield {"type": "tool_end", "tool_call_id": tool_id,
                           "name": "query_knowledge",
                           # V9.2 §32: backend error → failed; successful+empty → no_data (NOT unavailable)
                           "status": "failed" if knowledge_error else ("no_data" if not knowledge_result else "success"),
                           "arguments": {},
                           "result": str(knowledge_error or knowledge_result or "未检索到知识库结果")[:3000]}
                # 双层 Agent 节点级事件（批3）：coordinator/subagent/reviewer
                if node_name == "coordinator":
                    yield {"type": "tool_start", "tool_call_id": tool_id, "name": "Coordinator 拆解",
                           "agent_type": "coordinator", "status": "pending", "arguments": {}}
                    yield {"type": "tool_end", "tool_call_id": tool_id, "name": "Coordinator 拆解",
                           "agent_type": "coordinator", "status": "success",
                           "arguments": {}, "result": str(node_data.get("subtasks", ""))[:500]}
                elif node_name == "subagent":
                    for tid, r in (node_data.get("sub_results") or {}).items():
                        sid = f"sub_{tid}"
                        yield {"type": "tool_start", "tool_call_id": sid, "name": f"子Agent {r.get('task_type', '')}",
                               "agent_type": "subagent", "status": "pending", "arguments": {}}
                        yield {"type": "tool_end", "tool_call_id": sid, "name": f"子Agent {r.get('task_type', '')}",
                               "agent_type": "subagent", "status": "success",
                               "arguments": {}, "result": r.get("conclusion", "")[:500],
                               "tool_trace": r.get("tool_trace", [])}
                elif node_name == "reviewer":
                    yield {"type": "tool_start", "tool_call_id": tool_id, "name": "Reviewer 审查",
                           "agent_type": "reviewer", "status": "pending", "arguments": {}}
                    yield {"type": "tool_end", "tool_call_id": tool_id, "name": "Reviewer 审查",
                           "agent_type": "reviewer", "status": "success",
                           "arguments": {}, "result": str(node_data.get("review_result", ""))[:500]}
                # 捕获分析结果供任务工作台生成建议
                if node_name == "crewai":
                    suggestion.update(node_data)
                if node_name == "risk":
                    # P1-3: 捕获风险节点结果，让处置建议卡的 risk_score 反映 LLM 真实评估（1-5），而非恒为 0
                    suggestion.update(node_data)
                if node_name == "collect":
                    # P1-5: 采集节点输出即异常证据源（服务错误率/活跃告警），供处置建议门控
                    for _k in ("services_data", "alert_data", "red_metrics", "infra_data"):
                        _v = node_data.get(_k)
                        if _v:
                            evidence[_k] = _v
                # P1-5b: 捕获 clean 节点的 light_query 意图判定（信息查询不弹处置建议卡）
                if node_name == "clean":
                    if node_data.get("light_query"):
                        is_light = True
                if node_name == "summarize":
                    suggestion.update(node_data)
                    resp = node_data.get("final_response", "")
                    if resp:
                        for i in range(0, len(resp), 80):
                            yield {"type": "chunk", "text": resp[i:i+80]}
                            await asyncio.sleep(0.01)
            # P0-1: LLM 未连接时，在汇总/处置建议之前显式发送 notice 事件，
            # 前端据此展示"确定性分析模式"提示，避免全站静默降级用户无感知。
            if not _llm_key_ready():
                yield {"type": "notice", "level": "warning",
                       "text": "当前 LLM 未连接, 输出为确定性分析模式, 如需真实 AI 推理请在系统设置中配置 API Key"}
            # 从分析结果中提取可执行的 kubectl/curl 命令作为操作建议
            analysis = suggestion.get("crewai_result", "")
            full_resp = suggestion.get("final_response", "")
            plan = suggestion.get("plan", "")
            script = suggestion.get("script", "")
            # 从分析文本/报告提取命令（优先已有 plan/script，其次从分析结果提取）
            if not script:
                script = _extract_script(analysis or full_resp)
            # Issue5: 若仍无具体可执行命令（LLM 分析是报告文本而非命令），
            # 生成确定性可执行命令兜底，避免卡片只罗列分析报告而无命令。
            if not script:
                script = _fallback_script(analysis or full_resp, service)
            # plan 用简洁的动作摘要（命令+简短说明），而非整篇分析报告
            if not plan:
                plan = _action_summary(script, analysis or full_resp, service)
            # P2: 清理 LLM 命令中的尖括号占位符（<pod-name>/<ns>/<deployment>），
            # 替换为真实服务名/命名空间，避免"不可执行命令"建议。
            if script:
                script = _sanitize_script_placeholders(script, service)
            # P1-5b/P1-8: 处置建议只在【诊断意图(intent=diagnosis) 且非信息查询 且存在真实异常证据】
            # 时生成，信息查询/巡检意图不产出处置命令建议卡（_extract_script 提取也一并跳过）。
            ev_text = " ".join(str(v) for v in evidence.values())
            has_anomaly = _has_anomaly_evidence(ev_text)
            is_diagnosis = (intent or "").lower() == "diagnosis"
            if is_diagnosis and (script or plan) and has_anomaly and not is_light:
                # P1-5c: 统一 0~1 风险分（与报告中心 _risk_from_evidence 一致），
                # 前端 1-5 星换算由前端做，后端不再输出 ⭐(1/5) 文本。
                risk_score = _risk_from_evidence(full_resp or analysis or "")
                # Issue1: 每次分析只 yield 一个 suggestion 事件（内联审批卡），
                # 不再同时 yield suggestion + approval_pending（会导致前端出现 2 个
                # 内容一致的"处置建议·待确认"卡片）。task_id=thread_id 由前端回传确认。
                yield {"type": "suggestion",
                       "text": "已生成处置建议，请确认执行或自定义命令",
                       "task_id": thread_id,
                       "plan": plan,
                       "script": script[:4000],
                       "risk_score": risk_score,
                       "risk_reason": suggestion.get("risk_reason", "需要人工确认后执行"),
                       "requires_approval": True,
                       "final_response": full_resp}
            # Issue2: done 事件携带完整报告文本（不截断），供报告中心持久化完整巡检内容
            yield {"type": "done", "text": full_resp}
        except Exception as e:
            # DAG 执行异常必须保持稳定 wire contract；异常详情仅由类型
            # 进入服务端日志，不能随 Run event/report 传播到客户端或持久化层。
            logging.getLogger("aiops.investigation").error(
                "stream failed error_type=%s", type(e).__name__
            )
            yield {"type": "error", "error_code": "BRAIN_ERROR", "text": "BRAIN_ERROR"}

    async def approve_and_resume(self, thread_id: str, approved: bool = True):
        """Resume interrupted graph with approval decision. (async — 调用方需 await)"""
        await self._ensure_async_checkpointer()  # 延迟切换 MemorySaver → AsyncSqliteSaver
        config = {"configurable": {"thread_id": thread_id}}
        resume_value = {"approved": approved}
        # 节点为 async def，必须用 ainvoke 恢复中断的图
        return await self.graph.ainvoke(Command(resume=resume_value), config)

    def execute_suggestion(self, service: str, script: str, user_message: str = "", task_id: str = "") -> str:
        """审批通过后执行建议脚本（受 ShellPolicy 安全策略管控）。返回执行结果。
        task_id: 真实任务/会话 ID（用于审计日志，无则用 "manual"）。"""
        if not script:
            return "(无待执行脚本)"
        from shell_policy import ShellPolicy
        policy = ShellPolicy()
        reject = policy.check(script)
        if reject:
            return f"命令被安全策略拒绝: {reject}"
        # 安全加固：拦截 shell 拼接/重定向元字符（防 `kubectl ...; cat /etc/shadow` 注入）
        if mc := policy.check_shell_metachars(script):
            return f"命令被安全策略拒绝: {mc}"
        # 白名单强制：恢复/审批执行也必须落在可执行白名单（readonly/write）内，
        # 防止绕过 whitelist 执行任意命令（如读任意文件、访问网络）。
        allowed, category = policy.is_whitelisted_for_execute(script)
        if not allowed:
            return "命令不在可执行白名单内，已拒绝执行（可手动在控制台执行）"
        # G/H 黑名单：禁止外部部署 / 日志清理 / 批量删除
        if blk := policy.check_extra_blacklist(script):
            return f"命令被安全策略拒绝: {blk}"
        try:
            import subprocess
            # 已按产品要求放宽：命令支持管道/重定向/换行（shell=True），
            # 执行前有人工审批确认，因此按 shell 语义执行（`kubectl ... | grep` 等管道生效）。
            outputs = []
            for line in script.splitlines():
                line = line.strip()
                if not line or line.startswith("#"):
                    continue
                try:
                    r = subprocess.run(line, shell=True, capture_output=True, text=True, timeout=30)
                    outputs.append(f"$ {line}\n{r.stdout[:30000]}")
                    if r.stderr:
                        outputs.append(f"[stderr] {r.stderr[:10000]}")
                except subprocess.TimeoutExpired:
                    outputs.append(f"$ {line}\n(命令超时)")
                except Exception as e:
                    outputs.append(f"$ {line}\n(执行失败: {e})")
            # 审计日志 (P1-2): task_id=真实会话/任务ID(无则 "manual"), operator="system"(非状态值)
            _audit_log(task_id or "manual", "execute", "system",
                       _infer_target_from_script(script, service), script[:500],
                       "success" if not any("失败" in o or "超时" in o for o in outputs) else "error",
                       {"output_preview": "\n".join(outputs)[:200]})
            return "\n".join(outputs) or "(命令无输出)"
        except Exception as e:
            return f"执行异常: {str(e)[:200]}"

    def _detect_service(
        self,
        message: str = "",
        *,
        request_context: ScopeView | None = None,
    ) -> str:
        """推断目标服务名：优先从用户消息中匹配服务列表里的服务名（避免误诊为默认第一个），
        仅在能明确匹配时才返回，否则返回空让 RCA 跳过。
        """
        try:
            workload = str(getattr(request_context, "workload_kind", "") or "")
            if workload in {"investigation", "chat"}:
                from tools import (
                    _chat_context_ready,
                    _internal_chat_query,
                    _internal_investigation_query,
                    _unwrap_internal_query_result,
                )
                if workload == "chat":
                    if not _chat_context_ready(request_context):
                        return ""
                    body = _internal_chat_query(
                        tool_id="query_topology.v1", operation="topology", params={}, context=request_context,
                    )
                else:
                    body = _internal_investigation_query(
                        tool_id="query_topology.v1", operation="topology", params={}, context=request_context,
                    )
                payload, error = _unwrap_internal_query_result(body)
                if error or payload is None:
                    return ""
                nodes = payload.get("nodes") or []
                services = [
                    str(item.get("name") or item.get("service_name"))
                    for item in nodes
                    if isinstance(item, dict) and (item.get("name") or item.get("service_name"))
                ]
                if not services:
                    return ""
                if message:
                    msg_lower = message.lower()
                    for svc in sorted(services, key=len, reverse=True):
                        if svc.lower() in msg_lower:
                            return svc
                return ""
            # P0-1 补充修复：get_service_list 摘要截断前 10 个服务会漏掉目标服务，
            # 此处直接拉全量服务名列表做匹配。
            query_api = os.environ.get(
                "QUERY_API_URL",
                "http://query-api.observability.svc.cluster.local:8080/api/v1",
            )
            raw = json.loads(
                signed_query_api_request(
                    f"{query_api.rstrip('/')}/services",
                    context=request_context,
                )
            )
            if isinstance(raw, dict):
                items = raw.get("services") or raw.get("data") or []
            else:
                items = raw or []
            services = [d.get("service_name", "") for d in items if isinstance(d, dict) and d.get("service_name")]
            if not services:
                return ""
            if message:
                # 从消息中查找包含的服务名（按服务名长度降序匹配，避免短名误匹配）
                msg_lower = message.lower()
                for svc in sorted(services, key=len, reverse=True):
                    if svc and svc.lower() in msg_lower:
                        return svc
            # 消息中没有明确服务名 → 返回空让 RCA 跳过，避免误用首个服务
            return ""
        except Exception:
            return ""


# ═══════════════════════════════════════════════════════════════
#  内置 workflow 定义（与 build_graph 固定 DAG 对齐，只读展示/运行用）
# ═══════════════════════════════════════════════════════════════
GRAPH_DEFS = {
    "full": {
        "key": "workflow.full_diagnosis",
        "name": "完整诊断流程",
        "description": "采集→清洗→根因→RAG→AI分析→方案→风险→审批→执行→验证→报告→记忆→汇总",
        "nodes": [
            {"id": "collect", "label": "数据采集", "desc": "采集服务指标/调用链/错误"},
            {"id": "clean", "label": "数据清洗", "desc": "清洗归一化采集数据"},
            {"id": "rca", "label": "RCA 根因分析", "desc": "确定性+假设证伪定位根因"},
            {"id": "rag", "label": "RAG 案例匹配", "desc": "检索相似历史案例"},
            {"id": "crewai", "label": "CrewAI 专家分析", "desc": "多专家协同分析"},
            {"id": "plan", "label": "生成方案", "desc": "生成可执行运维方案"},
            {"id": "risk", "label": "风险评估", "desc": "评估方案风险"},
            {"id": "wait_approval", "label": "人工审批", "desc": "等待审批中断"},
            {"id": "execute", "label": "执行方案", "desc": "执行审批通过的脚本"},
            {"id": "verify", "label": "执行验证", "desc": "验证执行效果"},
            {"id": "report", "label": "生成报告", "desc": "生成诊断报告"},
            {"id": "memorize", "label": "记忆学习", "desc": "沉淀案例到 RAG"},
            {"id": "summarize", "label": "汇总输出", "desc": "生成最终总结"},
        ],
        "edges": [
            ("collect", "clean"), ("clean", "rca"), ("rca", "rag"), ("rag", "crewai"),
            ("crewai", "plan"), ("plan", "risk"), ("risk", "wait_approval"),
            ("wait_approval", "execute"), ("execute", "verify"), ("verify", "report"),
            ("report", "memorize"), ("memorize", "summarize"),
        ],
    },
    "chat": {
        "key": "workflow.chat_diagnosis",
        "name": "交互诊断流程",
        "description": "采集→清洗→根因→RAG→AI分析→汇总（对话用）",
        "nodes": [
            {"id": "collect", "label": "数据采集", "desc": "采集服务指标/调用链/错误"},
            {"id": "clean", "label": "数据清洗", "desc": "清洗归一化采集数据"},
            {"id": "rca", "label": "RCA 根因分析", "desc": "定位根因"},
            {"id": "rag", "label": "RAG 案例匹配", "desc": "检索相似案例"},
            {"id": "crewai", "label": "CrewAI 专家分析", "desc": "专家分析"},
            {"id": "summarize", "label": "汇总输出", "desc": "生成最终总结"},
        ],
        "edges": [
            ("collect", "clean"), ("clean", "rca"), ("rca", "rag"), ("rag", "crewai"), ("crewai", "summarize"),
        ],
    },
}


def describe_graph(mode: str = "full") -> dict:
    """返回内置 workflow 定义（nodes/edges 用 dict 结构），供 /ai/flows 展示。"""
    g = GRAPH_DEFS.get(mode, GRAPH_DEFS["full"])
    return {
        "key": g["key"],
        "name": g["name"],
        "description": g["description"],
        "mode": mode,
        "nodes": g["nodes"],
        "edges": [{"source": s, "target": t} for s, t in g["edges"]],
    }


brain = BrainOrchestrator()
