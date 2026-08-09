"""Brain orchestrator v4 — 15-node LangGraph DAG + ChromaDB RAG + Skill Registry"""
import json
import os
import time as _time
import subprocess
from typing import TypedDict, Annotated, Optional
import operator

from langgraph.graph import StateGraph, END
from langgraph.checkpoint.sqlite import SqliteSaver
from langgraph.types import interrupt, Command

from tools import (query_metrics, query_traces, get_service_list, query_topology,
                   execute_shell, k8sgpt_diagnose, deepflow_status, get_infrastructure)
from rag import rag
from rca import full_rca_analysis
from skill_registry import ToolRegistry, ExpertRegistry

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

    # collected
    services_data: str
    infra_data: str
    alert_data: str
    red_metrics: str
    trace_data: str
    k8sgpt_raw: str

    # RAG
    similar_cases: str

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
    execute_output: str

    # verify
    before_metrics: str
    after_metrics: str
    verify_pass: bool

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


# ═══════════════════════════════════════════════════════════════
#  LLM call
# ═══════════════════════════════════════════════════════════════

from llm_mock import is_mock_enabled, mock_llm_response, should_skip_llm
from llm_mock import mock_llm_decision, mock_coordinator_plan, mock_reviewer_result
from dual_agent import parse_subtasks, run_subtasks, merge_review

def _llm(cfg: dict, system_prompt: str, user_prompt: str, role: str = "分析专家") -> str:
    if is_mock_enabled():
        return mock_llm_response(system_prompt + user_prompt)
    if not cfg or not cfg.get("api_key"):
        return ""
    try:
        import os as _os
        # 禁用 CrewAI 遥测，避免启动时网络超时阻塞
        _os.environ.setdefault("CREWAI_DISABLE_TELEMETRY", "true")
        _os.environ.setdefault("CREWAI_TELEMETRY_OPT_OUT", "true")
        # 关键: CrewAI 内部从环境变量读取 OPENAI_API_KEY/BASE_URL/MODEL，
        # 必须显式设置（否则部分版本会报 "OPENAI_API_KEY is required" 或卡住）
        _os.environ["OPENAI_API_KEY"] = cfg.get("api_key", "")
        _os.environ["OPENAI_BASE_URL"] = cfg.get("base_url", "https://api.openai.com/v1")
        _os.environ["OPENAI_MODEL"] = cfg.get("model", "gpt-4o")

        from crewai import Agent, Task, Crew, LLM
        llm = LLM(model=cfg["model"], api_key=cfg["api_key"],
                   base_url=cfg["base_url"], provider=cfg.get("backend", "openai"), temperature=0.3)
        agent = Agent(role=role, goal=system_prompt, backstory="可观测性分析专家",
                       allow_delegation=False, verbose=False, llm=llm)
        task = Task(description=user_prompt, agent=agent, expected_output="请用中文输出。")
        crew = Crew(agents=[agent], tasks=[task], verbose=False)

        # 解决: FastAPI 异步事件循环与 CrewAI 同步 kickoff 冲突
        # 在线程中执行同步 kickoff。
        # 关键修复: 不能使用 with 语句管理 executor，否则 future.result 超时后，
        # with 退出会等待 kickoff 跑完（可能 15 分钟）才释放。必须用 shutdown(wait=False)。
        import asyncio
        import concurrent.futures
        executor = concurrent.futures.ThreadPoolExecutor(max_workers=1)
        try:
            # 无论是否在事件循环中，统一在线程池中执行，并强制 60s 超时
            future = executor.submit(crew.kickoff)
            try:
                return str(future.result(timeout=60))[:4000]
            except concurrent.futures.TimeoutError:
                # 超时直接放弃，不等待后台线程
                return "[LLM 调用超时, 请稍后重试]"
        finally:
            executor.shutdown(wait=False)
    except Exception as e:
        return f"[LLM error: {e}]"


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
               and ("kubectl" in line or "curl" in line or "kubectl" in line):
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


# ═══════════════════════════════════════════════════════════════
#  Helpers
# ═══════════════════════════════════════════════════════════════

def _parse(raw):
    try: return json.loads(raw)
    except: return None


def _collect_alerts() -> str:
    """采集告警态势：告警事件（聚合） + 告警规则，供 LLM 分析。"""
    import urllib.request
    import os as _os
    qa = _os.environ.get("QUERY_API_URL", "http://query-api.observability.svc.cluster.local:8080/api/v1")
    token = _os.environ.get("INTERNAL_TOKEN", "")
    out = []

    # 告警事件（聚合）
    try:
        req = urllib.request.Request(f"{qa}/alerts/events?limit=15", method="GET")
        if token:
            req.add_header("X-Internal-Token", token)
        with urllib.request.urlopen(req, timeout=5) as resp:
            data = json.loads(resp.read().decode())
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
        req = urllib.request.Request(f"{qa}/alerts/rules", method="GET")
        if token:
            req.add_header("X-Internal-Token", token)
        with urllib.request.urlopen(req, timeout=5) as resp:
            data = json.loads(resp.read().decode())
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

    return "\n\n".join(out)[:2000]

def _now():
    import datetime
    return datetime.datetime.now().strftime("%Y-%m-%d %H:%M:%S")


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

def node_collect(state: AgentState) -> dict:
    cfg = state.get("llm_config")
    result = {"messages": [f"[{_now()}] 数据采集开始"]}
    # Services — 全局服务概览（含错误率，供巡检/诊断分析）
    try:
        data = _parse(get_service_list())
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
    except: pass
    # Infra
    try:
        result["infra_data"] = get_infrastructure().replace("## K8s 基础设施\n", "").strip()[:2000]
    except: pass
    # Alerts — 告警态势（规则 + 事件聚合）
    try:
        result["alert_data"] = _collect_alerts()
    except:
        result["alert_data"] = ""
    # RED
    svc = state.get("service", "")
    if svc:
        try:
            raw = query_metrics(svc)
            data = _parse(raw)
            if data and isinstance(data.get("data"), list):
                items = data["data"]
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
        try: result["trace_data"] = query_traces(svc)[:3000]
        except: pass
    # K8sGPT — 快速失败不阻塞, timeout 5s
    if cfg and cfg.get("api_key"):
        try:
            import shutil
            if shutil.which("k8sgpt"):
                backend = cfg.get("backend", "openai")
                subprocess.run(["k8sgpt", "auth", "add", "-b", backend, "-m", cfg.get("model", "gpt-4o"), "-p", cfg["api_key"],]
                               + (["-u", cfg["base_url"]] if cfg.get("base_url","").replace("openai.com","") != "" else []),
                               capture_output=True, text=True, timeout=5)
                r = subprocess.run(["k8sgpt", "analyze", "--explain", "-n", "observability", "-o", "text"],
                                   capture_output=True, text=True, timeout=10)
                if r.returncode == 0 and r.stdout.strip() and len(r.stdout.strip()) > 50:
                    result["k8sgpt_raw"] = r.stdout[:2000]
        except: pass
    result["messages"] = [f"[{_now()}] 数据采集完成"]
    return result


def node_clean(state: AgentState) -> dict:
    """Deduplicate and standardize collected data."""
    return {"messages": [f"[{_now()}] 数据清洗完成"]}


def node_rca(state: AgentState) -> dict:
    """RCA 节点: 自动选择确定性或假设引擎模式"""
    svc = state.get("service", "")
    if not svc:
        return {"rca_mode": "skipped", "messages": [f"[{_now()}] RCA: 无目标服务, 跳过"]}
    try:
        result = full_rca_analysis(svc)
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


def node_rag(state: AgentState) -> dict:
    """Search ChromaDB for similar historical cases."""
    symptom = state.get("user_message", "")
    svc = state.get("service", "")
    query = f"{svc}: {symptom}" if svc else symptom
    try:
        cases = rag.search(query, limit=3)
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


def node_crewai(state: AgentState) -> dict:
    cfg = state.get("llm_config")
    if should_skip_llm(cfg):
        return {"crewai_result": ""}

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
    for k in ["similar_cases", "services_data", "infra_data", "alert_data", "red_metrics", "trace_data", "k8sgpt_raw", "rca_evidence"]:
        v = state.get(k)
        if v: ctx_parts.append(v)
    context = "\n\n".join(ctx_parts)[:6000]
    if not context:
        context = "(未采集到实时数据)"

    if expert:
        system_prompt = (
            f"你是{expert.role}。{expert.goal}。\n\n"
            f"你的专业领域技能:\n{chr(10).join(skill_prompts) if skill_prompts else '(通用分析)'}\n\n"
            f"【重要】以下已采集到的系统数据可直接用于分析，请【直接给出巡检/诊断结论】。\n"
            f"不要输出调用工具、查询命令、代码块或执行步骤，不要说要先调用哪个工具。\n"
            f"直接基于数据逐项分析，给出具体的健康状态、发现的问题、风险和建议。\n"
            f"如果数据不足，明确说明缺少哪些数据即可。"
        )
    else:
        system_prompt = (
            f"你是巡检专家。执行全量环境巡检。\n\n"
            f"【重要】以下已采集到的系统数据可直接用于分析，请【直接给出巡检结论】。\n"
            f"不要输出调用工具、查询命令、代码块或执行步骤，不要说要先调用哪个工具。\n"
            f"直接基于数据逐项分析，给出具体的健康状态、发现的问题、风险和建议。"
        )

    result = _llm(cfg, system_prompt, f"用户问题:「{user_msg}」\n已采集数据:\n{context}", expert.role if expert else "巡检专家")
    return {"crewai_result": result, "messages": [f"[{_now()}] CrewAI ({expert.role if expert else '巡检'}) 分析完成"]}


def node_holmes(state: AgentState) -> dict:
    cfg = state.get("llm_config")
    if should_skip_llm(cfg):
        return {"holmesgpt_result": ""}
    svc = state.get("service", "")
    context = f"服务: {svc}\nRED: {state.get('red_metrics','')}\nTrace: {state.get('trace_data','')}"[:4000]
    result = _llm(cfg, "你是 Trace 调查引擎。深入分析 Trace 与指标，定位根因。", context, "Trace专家")
    return {"holmesgpt_result": result, "messages": [f"[{_now()}] HolmesGPT 分析完成"]}


def node_plan(state: AgentState) -> dict:
    """Generate execution plan + shell script."""
    cfg = state.get("llm_config")
    if not cfg:
        return {"plan": "", "script": ""}
    analysis = state.get("crewai_result", "")[:2000]
    prompt = f"基于诊断结果，生成执行计划 + Shell/K8s 命令。只输出可执行的脚本。诊断:\n{analysis}"
    result = _llm(cfg, "你是 K8s 运维工程师。生成可直接执行的 Shell 脚本。", prompt, "运维工程师")
    plan = result[:2000]
    # Extract script block
    script = ""
    if "```" in plan:
        parts = plan.split("```")
        for i, p in enumerate(parts):
            if i % 2 == 1 and ("kubectl" in p or "curl" in p):
                script = p.strip().replace("bash\n", "").replace("sh\n", "")
                break
    return {"plan": plan, "script": script, "messages": [f"[{_now()}] 执行计划已生成"]}


def node_risk(state: AgentState) -> dict:
    """LLM risk assessment 1-5."""
    cfg = state.get("llm_config")
    if not cfg:
        return {"risk_score": 1, "risk_reason": "无 LLM 配置"}
    plan = state.get("plan", "")[:1000]
    result = _llm(cfg,
        "评估执行计划风险，输出 JSON: {\"score\": 1-5, \"reason\": \"理由\"}",
        f"执行计画:\n{plan}", "风险评估师")
    try:
        d = json.loads(result.strip().split("\n")[-1] if "{" in result else result)
        return {"risk_score": int(d.get("score", 3)), "risk_reason": d.get("reason", result[:200])}
    except:
        return {"risk_score": 3, "risk_reason": "风险评估默认中等"}


def node_wait_approval(state: AgentState) -> dict:
    """Pause for human approval. Resumed via /api/v1/ops/tasks/:id/approve.
    If state['approved'] is already True (e.g. chat mode), skip interrupt."""
    # Chat mode / non-interactive: skip interrupt
    if state.get("approved"):
        return {"approved": True, "messages": [f"[{_now()}] 自动审批通过 (非交互模式)"]}

    plan = state.get("plan", "无执行计画")[:500]
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
        return {"approved": False, "messages": [f"[{_now()}] 执行计划被拒绝"], "final_response": "## 执行被拒绝\n\n人工审批未通过。"}
    return {"approved": True, "messages": [f"[{_now()}] 审批通过, 开始执行"]}


def node_execute(state: AgentState) -> dict:
    """Execute K8s command via ShellCommandPolicy whitelist."""
    script = state.get("script", "")
    if not script or not state.get("approved"):
        return {"execute_output": ""}
    result = execute_shell(script, timeout=30)
    # 审计日志
    _audit_log(state.get("user_message", "")[:8], "execute", "auto",
               state.get("service", ""), script,
               "success" if "error" not in result.lower()[:100] else "error",
               {"output_preview": result[:200]})
    return {"execute_output": result[:2000], "messages": [f"[{_now()}] 命令执行完成"]}


def node_verify(state: AgentState) -> dict:
    """升级版验证: Cohen's d 效果量 + 副作用检测 + 二次取样确认"""
    import re, statistics as _stats
    svc = state.get("service", "")
    if not svc:
        return {"verify_pass": True, "messages": [f"[{_now()}] 验证: 无服务, 跳过"]}

    before_str = state.get("before_metrics", "")
    try:
        # 二次取样 (间隔 30s 确认非瞬时波动)
        samples = []
        for _ in range(2):
            raw = query_metrics(svc)
            data = _parse(raw)
            if data and isinstance(data.get("data"), list):
                items = data["data"]
                lat = sum(float(i.get("avg_ms", 0)) for i in items) / max(len(items), 1)
                total_calls = sum(int(i.get("calls", 0)) for i in items)
                total_errors = sum(int(i.get("errors", 0)) for i in items)
                err = (total_errors / max(total_calls, 1)) * 100
                samples.append({"latency": lat, "error_rate": err})
            import time as _t
            _t.sleep(30) if _ == 0 else None

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
            topo_raw = query_topology()
            topo_data = _parse(topo_raw)
            edges = topo_data.get("edges", []) if topo_data else []
            downstreams = [e.get("target_service", e.get("target", "")) for e in edges if e.get("source_service", e.get("source", "")) == svc]
            for ds in downstreams[:3]:
                ds_data = _parse(query_metrics(ds))
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

        return {
            "verify_pass": improved,
            "verify_effect_size": round(effect_size, 2),
            "verify_side_effect": side_effect,
            "after_metrics": after_str,
            "messages": [f"[{_now()}] 验证: {'✅ 通过' if improved else '❌ 未通过'} (d={effect_size:.2f}, 副作用={'有' if side_effect else '无'})"],
        }
    except Exception as e:
        return {"verify_pass": False, "messages": [f"[{_now()}] 验证: 失败 ({e})"]}


def node_report(state: AgentState) -> dict:
    """LLM generates execution summary report."""
    cfg = state.get("llm_config")
    verify = "✅ 修复成功" if state.get("verify_pass") else "❌ 修复未达到预期"
    context = f"""
    before: {state.get('before_metrics','')}
    after: {state.get('after_metrics','')}
    execute: {state.get('execute_output','')[:500]}
    verify: {verify}
    """
    if cfg:
        rep = _llm(cfg, "生成运维执行总结报告，含 before/after 对比和后续建议。", context, "报告生成器")
        return {"report": rep[:2000]}
    return {"report": f"执行结果: {verify}"}


def node_memorize(state: AgentState) -> dict:
    """Write successful case to ChromaDB."""
    if state.get("verify_pass") and state.get("crewai_result"):
        try:
            import uuid
            rag.add_case({
                "case_id": uuid.uuid4().hex[:12],
                "service": state.get("service", ""),
                "symptom": state.get("user_message", "")[:500],
                "root_cause": state.get("crewai_result", "")[:500],
                "plan": state.get("plan", "")[:500],
                "outcome": "success",
                "report": state.get("report", "")[:500],
            })
            return {"messages": [f"[{_now()}] 案例已入库 (总数: {rag.count()})"]}
        except: pass
    return {"messages": [f"[{_now()}] 案例未入库 (verify_pass={state.get('verify_pass')})"]}


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
    cfg = state.get("llm_config")
    if should_skip_llm(cfg) or is_mock_enabled():
        decision = mock_llm_decision
    else:
        from llm_fc import make_llm_decision_fn
        decision = make_llm_decision_fn(cfg, "你是可观测性诊断子 Agent，通过调用工具收集证据并给出结论。")
    sub_results = run_subtasks(subtasks, decision, ExpertRegistry)
    return {"sub_results": sub_results,
            "messages": [f"[{_now()}] {len(sub_results)} 个子 Agent 完成"]}


def node_reviewer(state):
    """双层 Agent - Reviewer：合并审查全部子结论，输出最终报告。"""
    cfg = state.get("llm_config")
    sub_results = state.get("sub_results") or {}
    if should_skip_llm(cfg) or is_mock_enabled():
        final = merge_review(sub_results, mock_reviewer_result)
    else:
        parts = "\n\n".join(f"[{tid}]({r.get('task_type', '')}): {r.get('conclusion', '')[:500]}"
                            for tid, r in sub_results.items())
        llm_final = _llm(cfg, "你是结果审查员。合并子 Agent 结论，校验依据与冲突，输出最终诊断报告。",
                         f"子结论:\n{parts}", "Reviewer")
        # LLM 失败/为空时兜底到确定性合并
        final = llm_final if llm_final and not llm_final.startswith("[LLM") else merge_review(sub_results, None)
    return {"review_result": final, "final_response": final,
            "messages": [f"[{_now()}] Reviewer 审查完成"]}


def node_summarize(state: AgentState) -> dict:
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

    parts = [f"## 分析报告\n**时间**: {_now()}"]
    if intent == "inspection":
        parts.append(crewai or "LLM 未配置")
    elif intent == "diagnosis":
        parts.append(f"**目标**: {svc} | **问题**: {msg}")
        if holmes: parts.append(f"### Trace 分析\n{holmes[:2000]}")
        if k8sgpt: parts.append(f"### K8s 诊断\n{k8sgpt[:1000]}")
        if crewai: parts.append(f"### 诊断结论\n{crewai[:2500]}")
    else:
        parts.append(crewai or "LLM 未配置")

    if risk > 0: parts.append(f"\n**风险等级**: {'⭐'*risk} ({risk}/5)")
    if plan: parts.append(f"\n### 执行计画\n{plan[:1000]}")
    if verify: parts.append(f"\n### 执行结果\n{verify}")
    if report: parts.append(f"\n### 执行报告\n{report[:1500]}")

    return {"final_response": "\n\n".join(parts), "messages": [f"[{_now()}] 报告生成完成"]}


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
    for name, fn in nodes:
        builder.add_node(name, fn)

    builder.set_entry_point("collect")
    builder.add_edge("collect", "clean")
    builder.add_edge("clean", "rca")
    builder.add_edge("rca", "rag")

    if mode == "chat":
        # Chat 精简路径: collect→clean→rca→rag→crewai→summarize
        # 只做 1 次 LLM 分析，script 操作建议在 stream_sync 中从分析结果提取
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
    def __init__(self, db_path="/tmp/ai-sessions.db"):
        self.llm_config = None
        import sqlite3
        self._conn = sqlite3.connect(db_path, check_same_thread=False)
        self.checkpointer = SqliteSaver(self._conn)
        # 双图: chat_graph 用于交互式 Chat (精简快速)，graph 用于完整运维任务
        self.graph = build_graph(checkpointer=self.checkpointer, mode="full")
        self.chat_graph = build_graph(checkpointer=self.checkpointer, mode="chat")
        self.dual_graph = build_graph(checkpointer=self.checkpointer, mode="dual")
        # 初始化 Skill Registry
        from skill_registry import _init_defaults
        _init_defaults()

    def set_llm_config(self, config: dict | None):
        if config is None:
            self.llm_config = None
            return
        self.llm_config = {
            "api_key": config.get("api_key", ""),
            "model": config.get("model", "gpt-4o"),
            "base_url": config.get("base_url", "https://api.openai.com/v1"),
            "provider": config.get("provider", "openai"),
            "backend": config.get("backend", "openai"),
        }
        os.environ["OPENAI_API_KEY"] = self.llm_config["api_key"]
        os.environ["OPENAI_BASE_URL"] = self.llm_config["base_url"]
        os.environ["OPENAI_MODEL"] = self.llm_config["model"]

    def execute_sync(self, intent: str, service: str, message: str, thread_id: str = "default") -> str:
        """Run full DAG synchronously and return final_response text."""
        final = self._run_dag(intent, service, message, thread_id)
        return final.get("final_response", "")

    def execute_sync_full(self, intent: str, service: str, message: str, thread_id: str = "default") -> dict:
        """Run full DAG synchronously and return complete final state."""
        return self._run_dag(intent, service, message, thread_id)

    def _run_dag(self, intent: str, service: str, message: str, thread_id: str = "default") -> dict:
        if not service: service = self._detect_service()
        initial: AgentState = {
            "messages": [], "intent": intent, "service": service, "user_message": message,
            "llm_config": self.llm_config,
            "services_data": "", "infra_data": "", "alert_data": "", "red_metrics": "", "trace_data": "", "k8sgpt_raw": "",
            "rca_mode": "", "rca_root_cause": "", "rca_evidence": "", "rca_confidence": 0, "rca_hypotheses_tested": 0,
            "similar_cases": "", "crewai_result": "", "holmesgpt_result": "",
            "plan": "", "script": "", "risk_score": 0, "risk_reason": "",
            "approved": True, "execute_output": "",
            "before_metrics": "", "after_metrics": "", "verify_pass": False,
            "final_response": "", "report": "", "error": "",
            "subtasks": [], "sub_results": {}, "review_result": "",
        }
        config = {"configurable": {"thread_id": thread_id}}
        try:
            return self.graph.invoke(initial, config)
        except Exception as e:
            return {"final_response": f"[DAG 执行异常: {str(e)[:200]}]", "error": str(e)[:200]}

    def stream_sync(self, intent: str, service: str, message: str, thread_id: str = "default", mode: str = "chat"):
        if not service: service = self._detect_service()
        initial: AgentState = {
            "messages": [], "intent": intent, "service": service, "user_message": message,
            "llm_config": self.llm_config,
            "services_data": "", "infra_data": "", "alert_data": "", "red_metrics": "", "trace_data": "", "k8sgpt_raw": "",
            "rca_mode": "", "rca_root_cause": "", "rca_evidence": "", "rca_confidence": 0, "rca_hypotheses_tested": 0,
            "similar_cases": "", "crewai_result": "", "holmesgpt_result": "",
            "plan": "", "script": "", "risk_score": 0, "risk_reason": "",
            "approved": True, "execute_output": "",
            "before_metrics": "", "after_metrics": "", "verify_pass": False,
            "final_response": "", "report": "", "error": "",
            "subtasks": [], "sub_results": {}, "review_result": "",
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
            # 交互式 Chat 使用精简 chat_graph；dual 模式用双层 Agent 图；full 用完整运维图
            if mode == "dual":
                graph = getattr(self, "dual_graph", self.graph)
            elif mode == "full":
                graph = getattr(self, "graph", self.graph)
            else:
                graph = getattr(self, "chat_graph", self.graph)
            for step in graph.stream(initial, config):
                node_name = list(step.keys())[0] if step else "unknown"
                node_data = step.get(node_name, {}) if step else {}
                step_num += 1
                label = step_names.get(node_name, node_name)
                yield {"type": "progress", "node": node_name, "text": f"{label}", "step": min(step_num, 7), "total": 8}
                # 工具级事件（节点级推断；真实工具级采集为独立后续）
                tool_node_map = {"crewai": "CrewAI 分析", "holmes": "Trace 调查", "rca": "RCA 根因分析",
                                 "rag": "RAG 案例匹配", "plan": "生成操作方案"}
                tool_id = f"tool_{node_name}_{step_num}"
                if node_name in tool_node_map:
                    yield {"type": "tool_start", "tool_call_id": tool_id,
                           "name": tool_node_map[node_name], "status": "pending", "arguments": {}}
                    yield {"type": "tool_end", "tool_call_id": tool_id,
                           "name": tool_node_map[node_name], "status": "success",
                           "arguments": {}, "result": str(node_data)[:500]}
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
                if node_name == "summarize":
                    suggestion.update(node_data)
                    resp = node_data.get("final_response", "")
                    if resp:
                        for i in range(0, len(resp), 80):
                            yield {"type": "chunk", "text": resp[i:i+80]}
                            import time as _t
                            _t.sleep(0.01)
            # 从分析结果中提取可执行的 kubectl/curl 命令作为操作建议
            analysis = suggestion.get("crewai_result", "")
            plan = suggestion.get("final_response", "")[:2000]
            script = _extract_script(analysis or plan)
            if script or plan:
                yield {"type": "suggestion", "text": "已生成操作建议，可在任务工作台审批执行",
                       "plan": plan,
                       "script": script[:1000],
                       "risk_score": suggestion.get("risk_score", 0),
                       "risk_reason": suggestion.get("risk_reason", ""),
                       "final_response": suggestion.get("final_response", "")[:3000]}
                # 内联审批卡事件：task_id 由调用方(main.py)捕获后回填
                yield {"type": "approval_pending",
                       "task_id": thread_id,
                       "plan": plan,
                       "script": script[:1000],
                       "risk_score": suggestion.get("risk_score", 0),
                       "risk_reason": suggestion.get("risk_reason", "需要人工确认后执行"),
                       "requires_approval": True}
            yield {"type": "done", "text": suggestion.get("final_response", "")[:500]}
        except Exception as e:
            # DAG 执行异常，返回错误信息而不是卡死
            import traceback
            err_detail = str(e)[:300]
            print(f"[stream_sync] DAG 执行异常: {err_detail}")
            yield {"type": "error", "text": f"分析执行异常: {err_detail}"}
            yield {"type": "done", "text": ""}

    def approve_and_resume(self, thread_id: str, approved: bool = True):
        """Resume interrupted graph with approval decision."""
        config = {"configurable": {"thread_id": thread_id}}
        resume_value = {"approved": approved}
        return self.graph.invoke(Command(resume=resume_value), config)

    def execute_suggestion(self, service: str, script: str, user_message: str = "") -> str:
        """审批通过后执行建议脚本（受 ShellPolicy 安全策略管控）。返回执行结果。"""
        if not script:
            return "(无待执行脚本)"
        from shell_policy import ShellPolicy
        policy = ShellPolicy()
        reject = policy.check(script)
        if reject:
            return f"命令被安全策略拒绝: {reject}"
        try:
            import subprocess, shlex
            # 逐行执行安全，避免注入
            outputs = []
            for line in script.splitlines():
                line = line.strip()
                if not line or line.startswith("#"):
                    continue
                args = shlex.split(line)
                try:
                    r = subprocess.run(args, shell=False, capture_output=True, text=True, timeout=30)
                    outputs.append(f"$ {line}\n{r.stdout[:500]}")
                    if r.stderr:
                        outputs.append(f"[stderr] {r.stderr[:200]}")
                except subprocess.TimeoutExpired:
                    outputs.append(f"$ {line}\n(命令超时)")
                except Exception as e:
                    outputs.append(f"$ {line}\n(执行失败: {e})")
            # 审计日志
            _audit_log(user_message[:8] or "chat", "execute", "approved",
                       service, script[:500],
                       "success" if not any("失败" in o or "超时" in o for o in outputs) else "error",
                       {"output_preview": "\n".join(outputs)[:200]})
            return "\n".join(outputs)[:2000] or "(命令无输出)"
        except Exception as e:
            return f"执行异常: {str(e)[:200]}"

    def _detect_service(self) -> str:
        try:
            data = json.loads(get_service_list())
            if isinstance(data, list) and data:
                return data[0].get("service_name", "unknown")
        except: pass
        return "unknown"


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
