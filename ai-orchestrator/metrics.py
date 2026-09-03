"""AIOps 平台 Prometheus 指标"""
from prometheus_client import Counter, Histogram, Gauge, generate_latest, REGISTRY, Info

# ── LLM 调用指标 ──
aio_llm_call_total = Counter(
    "aio_llm_call_total", "Total LLM calls",
    ["provider", "model", "status"]
)
aio_llm_call_duration = Histogram(
    "aio_llm_call_duration_seconds", "LLM call duration",
    ["provider", "node"],
    buckets=[1, 5, 10, 30, 60, 120, 300]
)

# ── DAG 执行指标 ──
aio_dag_total = Counter(
    "aio_dag_total", "Total DAG runs",
    ["intent", "status"]
)
aio_dag_node_duration = Histogram(
    "aio_dag_node_duration_seconds", "DAG node duration",
    ["node"],
    buckets=[0.1, 0.5, 1, 5, 10, 30, 60, 120]
)

# ── 任务队列指标 ──
aio_task_queue_depth = Gauge(
    "aio_task_queue_depth", "Tasks by status",
    ["status"]
)
aio_task_duration = Histogram(
    "aio_task_duration_seconds", "Task end-to-end duration",
    buckets=[10, 30, 60, 120, 300, 600]
)

# ── RAG 指标 ──
aio_rag_search_total = Counter(
    "aio_rag_search_total", "Total RAG queries",
    ["result"]
)
aio_rag_case_count = Gauge(
    "aio_rag_case_count", "Total cases in RAG store"
)

# ── 平台健康指标 ──
aio_info = Info("aio_build", "AIOps platform build info")
aio_info.info({"version": "5.0", "langgraph": "true", "detector": "true"})


def track_llm_call(provider: str, node: str):
    """装饰器: 记录 LLM 调用指标"""
    def decorator(func):
        def wrapper(*args, **kwargs):
            try:
                with aio_llm_call_duration.labels(provider=provider, node=node).time():
                    result = func(*args, **kwargs)
                # 检测是否返回了 [LLM error: ...] 
                if isinstance(result, str) and result.startswith("[LLM error:"):
                    aio_llm_call_total.labels(provider=provider, model="unknown", status="error").inc()
                else:
                    aio_llm_call_total.labels(provider=provider, model="unknown", status="success").inc()
                return result
            except Exception:
                aio_llm_call_total.labels(provider=provider, model="unknown", status="error").inc()
                raise
        return wrapper
    return decorator


def update_rag_metrics():
    """更新 RAG 相关 gauge"""
    try:
        from rag import rag
        aio_rag_case_count.set(rag.count())
    except:
        pass


def update_task_metrics(task_store: dict | None):
    """根据 _task_store 更新队列深度 gauge。

    The production composition deliberately disables the legacy in-memory
    task owner, so ``main._task_store`` is ``None`` there.  Metrics scraping
    must stay healthy when that compatibility store is absent; canonical Run
    metrics are exported by Query API instead.
    """
    if task_store is None:
        return
    statuses = {}
    for t in task_store.values():
        s = t.get("status", "unknown")
        statuses[s] = statuses.get(s, 0) + 1
    for status, count in statuses.items():
        aio_task_queue_depth.labels(status=status).set(count)
