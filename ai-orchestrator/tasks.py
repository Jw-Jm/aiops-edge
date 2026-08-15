"""arq worker — 异步诊断任务定义"""
import os
import time
from arq import create_pool
from arq.connections import RedisSettings, ArqRedis


async def diagnose_task(ctx, task_id: str, source: str, service: str, context: str):
    """Worker: 运行 LangGraph DAG → 更新 Redis 状态，同步内存 _task_store"""
    redis: ArqRedis = ctx["redis"]

    await redis.hset(f"ops:task:{task_id}", "status", "diagnosing")

    try:
        from orchestrator import brain
        # execute_sync_full 已改为 async (节点 async def); arq worker 在 async 上下文，直接 await
        result = await brain.execute_sync_full("diagnosis", service, context, task_id)

        # P0-2: 非交互 full 图初始 approved=False，DAG 在 wait_approval 处中断等待人工审批。
        # 中断态无 final_response，不能误标 done；回填 plan/script/risk 供审批面板展示。
        if result.get("__interrupt__"):
            await redis.hset(f"ops:task:{task_id}", mapping={
                "status": "waiting",
                "plan": result.get("plan", "")[:2000],
                "script": result.get("script", "")[:1000],
                "risk_score": str(result.get("risk_score", 0)),
                "risk_reason": result.get("risk_reason", "")[:500],
            })
            try:
                from store import _task_store
                if task_id in _task_store:
                    t = _task_store[task_id]
                    t["status"] = "waiting"
                    t["plan"] = result.get("plan", "")[:2000]
                    t["script"] = result.get("script", "")[:1000]
                    t["risk_score"] = result.get("risk_score", 0)
                    t["risk_reason"] = result.get("risk_reason", "")[:500]
            except Exception:
                pass
            return

        mapping = {
            "status": "done",
            "diagnosis": result.get("final_response", "")[:5000],
            "plan": result.get("plan", "")[:2000],
            "script": result.get("script", "")[:1000],
            "risk_score": str(result.get("risk_score", 0)),
            "risk_reason": result.get("risk_reason", "")[:500],
            "report": result.get("report", "")[:2000],
            "rca_mode": result.get("rca_mode", ""),
            "rca_root_cause": result.get("rca_root_cause", ""),
            "rca_confidence": str(result.get("rca_confidence", 0)),
            "done_at": time.strftime("%Y-%m-%dT%H:%M:%SZ"),
        }
        await redis.hset(f"ops:task:{task_id}", mapping=mapping)

        # 同步到内存 _task_store
        try:
            from store import _task_store
            if task_id in _task_store:
                t = _task_store[task_id]
                t["status"] = "done"
                t["diagnosis"] = result.get("final_response", "")[:5000]
                t["plan"] = result.get("plan", "")[:2000]
                t["script"] = result.get("script", "")[:1000]
                t["risk_score"] = result.get("risk_score", 0)
                t["risk_reason"] = result.get("risk_reason", "")[:500]
                t["report"] = result.get("report", "")[:2000]
                t["done_at"] = time.strftime("%Y-%m-%dT%H:%M:%SZ")
        except Exception:
            pass

    except Exception as e:
        await redis.hset(f"ops:task:{task_id}", mapping={
            "status": "failed",
            "diagnosis": str(e)[:500],
        })


async def startup(ctx):
    """arq worker 启动钩子"""
    ctx["redis"] = ctx.get("redis")


async def shutdown(ctx):
    """arq worker 关闭钩子"""
    pass


class WorkerSettings:
    functions = [diagnose_task]
    # Redis 地址/密码从环境变量注入（可移植，不写死本环境）；密码为 Secret 注入
    redis_settings = RedisSettings(
        host=os.environ.get("REDIS_HOST", "redis.observability.svc.cluster.local"),
        port=int(os.environ.get("REDIS_PORT", "6379")),
        password=os.environ.get("REDIS_PASSWORD") or None,
    )
    max_jobs = 3           # 限制并发 LLM 调用
    job_timeout = 300      # 单个任务最长 5 分钟
    keep_result = 300      # 结果保留 5 分钟
    on_startup = startup
    on_shutdown = shutdown
