"""告警自动调查: 告警 → incident-investigator 自动根因调查 (工作流 B6)。

门控 (对齐 ongrid biz/alert/investigator):
- INVESTIGATOR_ENABLED env (默认 true)
- min_severity = warning (复用 flow_engine.nodes_trigger.SEVERITY_ORDER)
- dedup_window = 300s (内存 dict 按 rule 去重)
- max_concurrent = 5 (BoundedSemaphore 限制并发调查)
- worker 超时 300s (agent_tool._BG_EXECUTOR 提交 + future.result(timeout=300))

调查结论写入知识库 type=investigation (rag.rag 模块级单例 add_case);
RAGStore 不可用时跳过入库, 仅在报告末尾注明。
任何异常都不抛出, 一律返回 None (或报告文本), 绝不阻塞告警入库。
"""
from __future__ import annotations

import json
import logging
import os
import threading
import time
import uuid
from concurrent.futures import TimeoutError as FutureTimeoutError

import agent_tool

log = logging.getLogger("investigator")

# 门控参数 (env 可覆盖)
MIN_SEVERITY = os.environ.get("INVESTIGATOR_MIN_SEVERITY", "warning")
DEDUP_WINDOW = int(os.environ.get("INVESTIGATOR_DEDUP_WINDOW", "300"))
MAX_CONCURRENT = int(os.environ.get("INVESTIGATOR_MAX_CONCURRENT", "5"))
WORKER_TIMEOUT = int(os.environ.get("INVESTIGATOR_WORKER_TIMEOUT", "300"))
PERSONA_NAME = "incident-investigator"

# 复用 A1 的级别序 (info<warning<critical); 环境缺失 flow_engine 时自建
try:
    from flow_engine.nodes_trigger import SEVERITY_ORDER
except Exception:  # noqa: BLE001
    SEVERITY_ORDER = {"info": 0, "warning": 1, "critical": 2}

_lock = threading.Lock()
_last_fired = {}  # rule -> epoch 秒
_semaphore = threading.BoundedSemaphore(MAX_CONCURRENT)

_PERSONAS = None


def _enabled() -> bool:
    """门控总开关 (env INVESTIGATOR_ENABLED, 默认 true)。"""
    return os.environ.get("INVESTIGATOR_ENABLED", "1").lower() in ("1", "true", "yes", "on")


def _personas() -> dict:
    """懒加载 persona 注册表 (builtin + 用户目录, 同名 user 覆盖)。"""
    global _PERSONAS
    if _PERSONAS is None:
        from persona_registry import load_personas, PERSONAS_BUILTIN_DIR, USER_PERSONAS_DIR
        _PERSONAS = load_personas(PERSONAS_BUILTIN_DIR, USER_PERSONAS_DIR)
    return _PERSONAS


def _severity_ok(severity: str) -> bool:
    return SEVERITY_ORDER.get(severity or "", 0) >= SEVERITY_ORDER.get(MIN_SEVERITY, 1)


def _dedupe(rule: str) -> bool:
    """去重窗口内已调查过该 rule 时返回 True (应跳过)。"""
    now = time.time()
    with _lock:
        last = _last_fired.get(rule)
        if last is not None and now - last < DEDUP_WINDOW:
            return True
        _last_fired[rule] = now
        return False


def _build_prompt(rule: str, severity: str, payload: dict | None = None) -> str:
    payload = payload or {}
    lines = [f"告警规则: {rule}", f"级别: {severity}"]
    for k in ("service", "summary", "context"):
        if payload.get(k):
            lines.append(f"{k}: {payload[k]}")
    extra = {k: v for k, v in payload.items() if k not in ("service", "summary", "context")}
    if extra:
        lines.append("附加信息: " + json.dumps(extra, ensure_ascii=False)[:800])
    return "\n".join(lines)


def _run_worker(persona, prompt: str, run_worker=None,
                timeout: int = WORKER_TIMEOUT):
    """提交到 agent_tool._BG_EXECUTOR 并等待结果; 超时返回 None 并记日志。

    run_worker 可注入 (测试/替换用); 缺省 agent_tool.run_worker。
    """
    fn = run_worker or agent_tool.run_worker
    future = agent_tool._BG_EXECUTOR.submit(fn, persona, prompt)
    try:
        return future.result(timeout=timeout)
    except FutureTimeoutError:
        log.error("告警调查超时(%ss): 返回 None, 后台任务继续执行", timeout)
        return None


def _store_report(rule: str, severity: str, payload: dict | None, report: str) -> str:
    """调查报告写知识库 (type=investigation)。RAGStore 不可用时仅返回说明文本。"""
    try:
        from rag import rag
    except Exception as e:  # noqa: BLE001
        log.warning("rag 模块不可用, 跳过调查报告入库: %s", e)
        return "(RAG 不可用, 调查报告未入库)"
    try:
        if not hasattr(rag, "add_case"):
            log.warning("未找到 RAGStore 实例 (rag.add_case), 调查报告未入库")
            return "(无 RAGStore 单例, 调查报告未入库)"
        payload = payload or {}
        content = report if isinstance(report, str) else str(report)
        summary = str(payload.get("summary") or payload.get("context") or "")
        case_id = rag.add_case({
            "case_id": "inv-" + uuid.uuid4().hex[:12],
            "type": "investigation",
            "symptom": f"[{severity}] {rule}: {summary}\n{content[:2000]}",
            "root_cause": content[:2000],
            "plan": "",
            "outcome": "success",
            "report": content[:500],
            "service": str(payload.get("service", "") or ""),
            "tags": f"investigation,{rule}",
            "source": "investigator",
            "title": f"[告警调查] {rule}",
        })
        return f"(调查报告已入库 case_id={case_id})"
    except Exception as e:  # noqa: BLE001
        log.warning("调查报告入库失败(不影响返回): %s", e)
        return f"(调查报告入库失败: {e})"


def maybe_investigate(rule: str, severity: str, payload: dict | None = None,
                      run_worker=None) -> str | None:
    """告警触发自动调查 (incident-investigator)。

    门控顺序: 总开关 → rule 非空 → 级别 → 去重 → persona 存在 → 并发 → 执行/超时。
    返回调查报告文本 (含入库说明); 门控不通过或异常一律返回 None。
    """
    try:
        if not _enabled():
            return None
        if not rule:
            return None
        if not _severity_ok(severity):
            log.info("告警 %s 级别 %s < %s, 跳过自动调查", rule, severity, MIN_SEVERITY)
            return None
        if _dedupe(rule):
            log.info("告警 %s 处于去重窗口(%ss)内, 跳过自动调查", rule, DEDUP_WINDOW)
            return None
        persona = _personas().get(PERSONA_NAME)
        if persona is None:
            log.warning("未找到 persona %s, 跳过自动调查", PERSONA_NAME)
            return None
        if not _semaphore.acquire(blocking=False):
            log.warning("告警调查并发已达上限(%s), 跳过 %s", MAX_CONCURRENT, rule)
            return None
        try:
            report = _run_worker(persona, _build_prompt(rule, severity, payload),
                                 run_worker=run_worker)
        finally:
            _semaphore.release()
        if not report:
            return None
        note = _store_report(rule, severity, payload, report)
        return f"{report}\n\n{note}"
    except Exception as e:  # noqa: BLE001
        log.exception("maybe_investigate(%s) 异常: %s", rule, e)
        return None
