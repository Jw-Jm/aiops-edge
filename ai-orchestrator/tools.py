"""Observability tools for AI agents"""
from __future__ import annotations
import json
import os
import subprocess
import urllib.error
import urllib.request
import uuid as _uuid
from uuid import UUID

from contracts import RequestContext
from invocation_scope import ScopeView
from internal_query import signed_query_api_request
from skill_registry import ToolRegistry
from kg_tools import kg_evidence_tool
from trusted_context import TrustedContextError
from mtls import urlopen as mtls_urlopen

QUERY_API = os.environ.get("QUERY_API_URL", "http://query-api.observability.svc.cluster.local:8080/api/v1")
INTERNAL_TOKEN = os.environ.get("INTERNAL_TOKEN", "")


def _get_json(url: str, *, request_context: RequestContext | None = None) -> dict:
    try:
        return json.loads(signed_query_api_request(url, context=request_context))
    except TrustedContextError as e:
        return {"error": e.error_code}
    except (urllib.error.URLError, json.JSONDecodeError) as e:
        return {"error": str(e)}


# ── P19.7 K8sGPT：按需拉取平台 LLM 配置 + 子进程私有 env 注入 ─────────────
#
# 安全约束：
#  - 禁止 k8sgpt auth add --password、命令行实参、/root/.k8sgpt、镜像层、持久卷写入 key。
#  - 不修改全局 os.environ；key 仅存在于本次 subprocess.run 的 env 映射（子进程结束即失效）。
#  - k8sgpt 的 openai backend 支持 OPENAI_API_KEY / OPENAI_BASE_URL / OPENAI_MODEL 环境变量，
#    因此无需 tmpfs 临时文件（仅在 K8sGPT 不支持环境变量时才允许 0600 tmpfs，并在 finally 删除）。
#  - 拉取失败/provider 未配置/K8sGPT 错误均返回 unavailable，不伪造健康结论。
#  - 对 argv/stdout/stderr/异常/SSE/审计日志统一做 key 脱敏。

import time as _time
from datetime import datetime, timedelta, timezone

_LLM_CONFIG_CACHE: dict = {"fetched_at": 0.0, "config": None}
_LLM_CONFIG_TTL = 60.0  # 短时内存缓存，避免每次调用重复拉取


def _redact_key(text: str, api_key: str) -> str:
    """从文本中移除/脱敏 API key（避免子进程错误回显泄露）。"""
    if not api_key or not text:
        return text
    return text.replace(api_key, "***REDACTED***")


def _is_production() -> bool:
    return os.environ.get("AIOPS_ENV", "").strip().lower() == "production" or os.environ.get(
        "AIOPS_DEPLOYMENT_MODE", ""
    ).strip().lower() == "production"


def _legacy_graph_snapshot_enabled() -> bool:
    """Allow the retired MySQL graph snapshot only in an explicit local mode.

    The old snapshot is a compatibility reader, not a production data owner.
    An unset ``GRAPH_BACKEND`` must therefore never silently select it in a
    production Gateway; production graph facts come from Query API/HugeGraph.
    """
    if _is_production():
        return False
    return os.environ.get("GRAPH_BACKEND", "").strip().lower() == "legacy_mysql"


def _fetch_llm_config_for_k8sgpt() -> dict | None:
    """Return an ephemeral K8sGPT configuration.

    Production is strictly proxy-only: the orchestrator may receive the short-lived
    proxy ingress token, but it must never read a provider API key or provider URL.
    The non-production branch is retained only for the isolated local K8sGPT seam
    covered by legacy tests; it is unreachable when ``AIOPS_ENV=production``.
    """
    now = _time.time()
    if _LLM_CONFIG_CACHE["config"] and (now - _LLM_CONFIG_CACHE["fetched_at"]) < _LLM_CONFIG_TTL:
        return _LLM_CONFIG_CACHE["config"]
    try:
        if _is_production():
            # Provider credentials and endpoint ownership stay in the egress proxy.
            # Fetch only routing metadata through the same signed internal boundary
            # used by the normal LLM path.  A missing signing key or proxy token is
            # an explicit unavailable state, never a direct-provider fallback.
            proxy_url = (os.environ.get("AI_LLM_EGRESS_PROXY_URL") or os.environ.get("LLM_PROXY_URL") or "").strip()
            proxy_token = os.environ.get("LLM_PROXY_TOKEN", "").strip()
            if not proxy_url or not proxy_token:
                return None
            from contracts import TrustedRequestContext
            from uuid import UUID

            now_dt = datetime.now(timezone.utc)
            system_tenant = os.environ.get("AIOPS_SYSTEM_TENANT_ID", "").strip()
            system_cluster = os.environ.get("AIOPS_SYSTEM_CLUSTER_ID", "").strip()
            if not system_tenant or not system_cluster:
                return None
            context = TrustedRequestContext(
                issuer="ai-orchestrator", audience="ai-apm-query-go", request_id=UUID(str(_uuid.uuid4())),
                run_id=UUID(str(_uuid.uuid4())), principal_type="system", principal_id=UUID(str(_uuid.uuid4())),
                session_id=None,
                tenant_id=UUID(system_tenant),
                scope_kind="cluster",
                cluster_id=UUID(system_cluster),
                capability="llm.config.read", source="k8sgpt-config-reader", workload_kind="platform",
                issued_at=now_dt, expires_at=now_dt + timedelta(seconds=30), nonce=UUID(str(_uuid.uuid4())),
            )
            raw = signed_query_api_request(f"{QUERY_API}/settings/llm/internal", context=context, timeout=5)
            data = json.loads(raw.decode() if isinstance(raw, bytes) else raw)
            cfg = data.get("data", data)
            provider = str(cfg.get("provider", "openai") or "openai").lower()
            if provider not in ("openai", "deepseek"):
                return None
            result = {
                "api_key": proxy_token,
                "model": str(cfg.get("model") or "gpt-4o"),
                "base_url": proxy_url.rstrip("/") + "/v1/proxy/" + provider,
                "proxy_only": True,
            }
        else:
            # Local-only compatibility seam. Never execute this branch in a
            # production deployment (guard above is deliberately fail-closed).
            req = urllib.request.Request(f"{QUERY_API}/settings/llm/internal", method="GET")
            req.add_header("X-Internal-Token", INTERNAL_TOKEN)
            with mtls_urlopen(req, timeout=5) as resp:
                data = json.loads(resp.read().decode())
                cfg = data.get("data", data)
                api_key = cfg.get("api_key") or cfg.get("apiKey")
                if not api_key:
                    return None
                provider = str(cfg.get("provider", "openai") or "openai").lower()
                if provider not in ("openai", "deepseek", "azure", "custom", "openai-compatible"):
                    return None
                result = {
                    "api_key": api_key,
                    "model": str(cfg.get("model") or "gpt-4o"),
                    "base_url": str(cfg.get("base_url") or "https://api.openai.com/v1"),
                    "proxy_only": False,
                }
        # 原地更新（不重绑定模块全局，保证外部引用与模块读取同一 dict）
        _LLM_CONFIG_CACHE["fetched_at"] = now
        _LLM_CONFIG_CACHE["config"] = result
        return result
    except Exception:
        return None


def _cluster_param(cluster_id: str = "") -> str:
    """Return only an explicit canonical UUID; broad/implicit scope is invalid."""
    try:
        cid = str(UUID(str(cluster_id)))
    except (ValueError, TypeError, AttributeError):
        return ""
    if cid != str(cluster_id).lower():
        return ""
    return f"cluster_id={cid}"


def _context_for_cluster(
    cluster_id: str, request_context: ScopeView | None
) -> ScopeView | None:
    if not isinstance(request_context, ScopeView):
        return None
    if str(request_context.cluster_id) != str(cluster_id):
        return None
    if not _cluster_param(cluster_id):
        return None
    return request_context


def _internal_investigation_query(*, tool_id: str, operation: str,
                                  params: dict, context: ScopeView) -> dict:
    """Run an Investigation read through the ToolRun-owned query boundary."""
    from internal_query import _load_private_key
    from invocation_scope import current_execution_lease_token
    from internal_query_client import InternalQueryClient
    from tool_execution_context import ToolExecutionContext
    from trusted_context_issuer import TrustedContextIssuer

    private_key = _load_private_key(os.environ.get("TRUSTED_CONTEXT_PRIVATE_KEY", ""))
    issuer = TrustedContextIssuer(private_key=private_key)
    execution = {
        "workload_kind": "investigation",
        "run_id": str(getattr(context, "run_id", "") or ""),
        "invocation_id": str(getattr(context, "invocation_id", "") or ""),
        "tenant_id": str(context.tenant_id),
        "cluster_id": str(context.cluster_id),
        "executor_id": str(getattr(context, "executor_id", "") or ""),
        "lease_epoch": int(getattr(context, "lease_epoch", 0) or 0),
        # The token is intentionally not part of checkpoint state.  Workers bind
        # it in a task-local context immediately before graph execution.
        "lease_token": str(getattr(context, "lease_token", "") or current_execution_lease_token()),
    }
    client = InternalQueryClient(issuer=issuer)
    result = client.query(
        tool_id=tool_id, operation=operation,
        tenant_id=str(context.tenant_id), cluster_id=str(context.cluster_id),
        params=params, context_ref=str(context.request_id),
        execution_context=ToolExecutionContext.from_mapping(execution, tool_id=tool_id, params=params),
    )
    body = result.body
    tool_run_id = str(body.get("tool_run_id") or "")
    if tool_run_id and body.get("quality") in {"complete", "partial"}:
        from control_plane_client import ControlPlaneClient
        evidence_id = str(_uuid.uuid5(_uuid.UUID(execution["run_id"]), tool_run_id))
        raw = json.dumps(body.get("data", body), ensure_ascii=False, sort_keys=True, default=str)
        ControlPlaneClient().consume_tool_evidence(
            run_id=execution["run_id"], tenant_id=execution["tenant_id"],
            cluster_id=execution["cluster_id"], tool_run_id=tool_run_id,
            evidence_id=evidence_id, evidence_type=operation,
            source_ref=f"query-api:{operation}", raw_ref=tool_run_id,
            raw_digest_sha256=str(body.get("digest") or ""), summary=raw[:4000],
            metadata={"quality": body.get("quality"), "count": body.get("count", 0)},
            provenance_fingerprint=str(body.get("digest") or ""),
        )
    return body


def _unwrap_internal_query_result(result: object) -> tuple[dict | None, str | None]:
    """Unwrap the canonical query-api ToolResultEnvelope.

    Internal query endpoints return ``{quality, data, digest, ...}``, while
    older test seams sometimes return the data object directly.  Keep the
    compatibility shape for those seams, but never treat a failed envelope as
    an empty successful result.
    """
    if not isinstance(result, dict):
        return None, "invalid query response"
    if result.get("quality") == "failed":
        errors = result.get("source_errors") or result.get("errors") or []
        detail = "; ".join(str(item) for item in errors[:3]) if isinstance(errors, list) else str(errors)
        return None, detail or "query failed"
    payload = result.get("data", result)
    if not isinstance(payload, dict):
        return None, "invalid query data"
    return payload, None


def query_metrics(service: str, tenant_id: str = "", cluster_id: str = "", *, request_context: RequestContext | None = None) -> str:
    if not service:
        return "未指定服务名称"
    cp = _cluster_param(cluster_id)
    context = _context_for_cluster(cluster_id, request_context)
    if context is None:
        return "查询失败: invalid_context"
    if getattr(context, "workload_kind", "") == "investigation":
        try:
            return json.dumps(_internal_investigation_query(
                tool_id="query_metrics.v1", operation="metrics", params={"service": service}, context=context,
            ), ensure_ascii=False)
        except Exception as exc:
            return f"查询失败: {str(exc)[:200]}"
    data = _get_json(f"{QUERY_API}/services/{service}?{cp}", request_context=context)
    if isinstance(data, dict) and "error" in data:
        return f"查询失败: {data['error']}"
    return json.dumps(data, indent=2, ensure_ascii=False)[:5000]


def query_traces(service: str = "", tenant_id: str = "", cluster_id: str = "", *, request_context: RequestContext | None = None) -> str:
    cp = _cluster_param(cluster_id)
    url = f"{QUERY_API}/traces?limit=5"
    url += "&" + cp if cp else ""
    context = _context_for_cluster(cluster_id, request_context)
    if context is None:
        return "查询失败: invalid_context"
    if getattr(context, "workload_kind", "") == "investigation":
        try:
            return json.dumps(_internal_investigation_query(
                tool_id="query_traces.v1", operation="traces", params={"service": service, "limit": 5}, context=context,
            ), ensure_ascii=False)
        except Exception as exc:
            return f"查询失败: {str(exc)[:200]}"
    data = _get_json(url, request_context=context)
    if isinstance(data, dict) and "error" in data:
        return f"查询失败: {data['error']}"
    return json.dumps(data, indent=2, ensure_ascii=False)[:4000]


def query_logs(service: str = "", minutes: int = 30, cluster_id: str = "", *, request_context: RequestContext | None = None) -> str:
    """查询最近 N 分钟日志（ClickHouse log_records，经 query-api）。
    空 service 走全量最近日志。"""
    params = []
    if service:
        params.append(f"service={service}")
    params.append(f"minutes={minutes}")
    cp = _cluster_param(cluster_id)
    if cp:
        params.append(cp)
    url = f"{QUERY_API}/logs/query?" + "&".join(params)
    context = _context_for_cluster(cluster_id, request_context)
    if context is None:
        return "日志查询失败: invalid_context"
    if getattr(context, "workload_kind", "") == "investigation":
        try:
            return json.dumps(_internal_investigation_query(
                tool_id="query_logs.v1", operation="logs",
                params={"service": service, "minutes": minutes}, context=context,
            ), ensure_ascii=False)
        except Exception as exc:
            return f"日志查询失败: {str(exc)[:200]}"
    data = _get_json(url, request_context=context)
    if isinstance(data, dict) and "error" in data:
        return f"日志查询失败: {data['error']}"
    rows = data.get("data", []) if isinstance(data, dict) else []
    if not rows:
        return "（近 30 分钟无日志）"
    lines = []
    for r in rows[:50]:
        sev = r.get("severity", "")
        body = (r.get("body", "") or "").strip().replace("\n", " ")
        lines.append(f"[{r.get('timestamp','')}] {r.get('service_name','')} {sev}: {body[:200]}")
    return "\n".join(lines)


def query_topology(tenant_id: str = "", cluster_id: str = "", *, request_context: RequestContext | None = None) -> str:
    cp = _cluster_param(cluster_id)
    context = _context_for_cluster(cluster_id, request_context)
    if context is None:
        return "查询失败: invalid_context"
    data = _get_json(f"{QUERY_API}/topology/global?{cp}", request_context=context)
    if isinstance(data, dict) and "error" in data:
        return f"查询失败: {data['error']}"
    return json.dumps(data, indent=2, ensure_ascii=False)[:3000]


def get_service_list(tenant_id: str = "", cluster_id: str = "", *, request_context: RequestContext | None = None) -> str:
    request_context = _context_for_cluster(cluster_id, request_context)
    if request_context is None:
        return "查询失败: invalid_context"
    if getattr(request_context, "workload_kind", "") == "investigation":
        try:
            data = _internal_investigation_query(
                tool_id="query_topology.v1", operation="topology", params={}, context=request_context,
            )
            nodes = data.get("nodes", []) if isinstance(data, dict) else []
            services = sorted({str(n.get("name") or n.get("service_name")) for n in nodes
                               if isinstance(n, dict) and n.get("type") == "service"})
            return "服务数 " + str(len(services)) + "：\n" + "\n".join(services[:50])
        except Exception as exc:
            return f"查询失败: {str(exc)[:200]}"
    # The legacy MySQL graph snapshot is compatibility-only.  It must be
    # explicitly selected in a non-production process; an unset backend never
    # silently becomes a second production data owner.
    if _legacy_graph_snapshot_enabled():
        try:
            from kg_graph import _load_graph, _json_loads
            node_rows, _ = _load_graph()
            svcs = set()
            for r in node_rows:
                if r.get("type") != "service":
                    continue
                name = str(r.get("name") or "").strip()
                if not name or name.endswith("(deleted)"):
                    continue
                if cluster_id:
                    props = _json_loads(r.get("props", r.get("props_json")))
                    if str(props.get("cluster_id", "")) != str(cluster_id):
                        continue
                svcs.add(name)
            if svcs:
                svcs = sorted(svcs)
                return "服务数 " + str(len(svcs)) + "：\n" + "\n".join(svcs[:50])
        except Exception:
            pass
    url = f"{QUERY_API}/services"
    cp = _cluster_param(cluster_id)
    if cp:
        url += "?" + cp
    data = _get_json(url, request_context=request_context)
    if isinstance(data, dict) and "error" in data:
        return f"查询失败: {data['error']}"
    # P0-1 修复：/api/v1/services 顶层键为 "services"（非 "data"），兼容两种契约
    if isinstance(data, dict):
        if "services" in data:
            data = data["services"]
        elif "data" in data:
            data = data["data"]
    if isinstance(data, list):
        summary = []
        for s in data[:10]:
            calls = float(s.get("calls", s.get("traces", 0)) or 0)
            errors = float(s.get("errors", s.get("error_count", 0)) or 0)
            error_rate = round(errors / calls * 100, 2) if calls > 0 else 0.0
            summary.append({
                "service_name": s.get("service_name"),
                "traces": int(calls),
                "avg_ms": round(float(s.get("avg_latency_ms", s.get("avg_ms", 0)) or 0), 1),
                "max_ms": round(float(s.get("max_ms", 0)), 1),
                "error_rate": error_rate,
            })
        return json.dumps(summary, indent=2, ensure_ascii=False)
    return json.dumps(data, indent=2, ensure_ascii=False)[:4000]


def execute_shell(command: str, timeout: int = 30) -> str:
    from shell_policy import ShellPolicy
    policy = ShellPolicy()
    reject = policy.check(command)
    if reject:
        return f"命令被安全策略拒绝: {reject}"
    # 安全修复(G5): 纵深防御——低层执行函数强制白名单 + 元字符校验，不依赖调用方自觉。
    # 任何调用方（含未来新增）都无法绕过 is_whitelisted_for_execute / check_shell_metachars。
    if mc := policy.check_shell_metachars(command):
        return f"命令被安全策略拒绝: {mc}"
    allowed, category = policy.is_whitelisted_for_execute(command)
    if not allowed:
        return f"命令被安全策略拒绝: 不在可执行白名单内 ({category})"
    if blk := policy.check_extra_blacklist(command):
        return f"命令被安全策略拒绝: {blk}"
    try:
        # 已按产品要求放宽：命令支持管道/重定向（shell=True），执行前经人工审批，
        # 因此按 shell 语义执行（`kubectl ... | grep` 等管道生效）。
        result = subprocess.run(command, shell=True, capture_output=True, text=True, timeout=timeout)
        output = result.stdout[:2000]
        if result.stderr:
            output += "\n[stderr]: " + result.stderr[:500]
        return output or "(no output)"
    except subprocess.TimeoutExpired:
        return f"命令超时 (>{timeout}s)"
    except Exception as e:
        return f"执行失败: {str(e)}"


_K8SGPT_TMPFS = "/dev/shm"


def _run_k8sgpt(cmd, child_env, api_key, timeout=60):
    """执行 k8sgpt 并统一脱敏 stdout/stderr，返回 (ok, text)。"""
    try:
        result = subprocess.run(cmd, capture_output=True, text=True, timeout=timeout, env=child_env)
    except FileNotFoundError:
        return False, "K8sGPT unavailable: not installed"
    except subprocess.TimeoutExpired:
        return False, "K8sGPT unavailable: timeout"
    stdout = _redact_key(result.stdout[:3000], api_key)
    stderr = _redact_key(result.stderr[:500], api_key)
    if result.returncode == 0 and stdout.strip():
        return True, stdout
    if stderr:
        return False, f"K8sGPT unavailable: {stderr}"
    # 空 stdout/无 stderr 不是“集群健康”的证据；保留明确的不可用语义。
    return False, "K8sGPT unavailable: no diagnostic output"


def k8sgpt_diagnose(namespace: str = "observability") -> str:
    """按需、子进程私有方式运行 K8sGPT（--explain 用平台 LLM key）。

    P19.7 安全模型：
      - 按需拉取平台 LLM 配置（短时内存缓存），key 仅为本次子进程使用。
      - 先尝试子进程 env 注入（无文件）。实测 k8sgpt 0.4.34 的 --explain 仅凭 env
        无法识别 provider（报 "AI provider not specified"），故 fallback 到
        tmpfs 中 0600 临时配置文件（k8sgpt 读 $HOME/.config/k8sgpt/k8sgpt.yaml），
        子进程结束 finally 删除——不写 /root/.k8sgpt、不写镜像/持久卷、无命令行实参。
      - 不修改全局 os.environ；key 仅存在于本次 child env（HOME 指向 tmpfs）+ 0600 临时文件。
      - argv 不含 key；stdout/stderr/异常统一脱敏。
      - 拉取失败 / provider 未配置 / K8sGPT 错误 → unavailable，不伪造健康结论。
    """
    api_key = None
    tmp_home = None
    try:
        llm = _fetch_llm_config_for_k8sgpt()
        if not llm:
            return "K8sGPT unavailable: LLM provider not configured"
        api_key = llm["api_key"]
        base_cmd = ["k8sgpt", "analyze", "--explain", "-n", namespace, "-o", "text"]

        # 尝试 1：纯 env 注入（无文件）。
        child_env = dict(os.environ)
        child_env["OPENAI_API_KEY"] = api_key
        child_env["OPENAI_BASE_URL"] = llm["base_url"]
        child_env["OPENAI_MODEL"] = llm["model"]
        ok, text = _run_k8sgpt(base_cmd, child_env, api_key)
        if ok:
            return text
        # k8sgpt 0.4.34 --explain 无法仅凭 env 识别 provider（实测 env 注入返回空输出/no diagnostic
        # output），失败即 fallback 到 tmpfs 0600 临时配置，确保真实 --explain 可用。
        import tempfile
        import shutil
        tmp_home = tempfile.mkdtemp(prefix="k8sgpt-", dir=_K8SGPT_TMPFS)
        cfg_dir = os.path.join(tmp_home, ".config", "k8sgpt")
        os.makedirs(cfg_dir, exist_ok=True)
        cfg_path = os.path.join(cfg_dir, "k8sgpt.yaml")
        with os.fdopen(os.open(cfg_path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600), "w") as f:
            f.write(
                "ai:\n"
                "    providers:\n"
                "        - name: openai\n"
                "          model: {model}\n"
                "          password: {key}\n"
                "          baseurl: {base}\n"
                "          temperature: 0.7\n"
                "          topp: 0.5\n"
                "          topk: 50\n"
                "          maxtokens: 2048\n"
                "          customheaders: []\n"
                "    defaultprovider: openai\n"
                "kubeconfig: ''\n".format(model=llm["model"], key=api_key, base=llm["base_url"])
            )
        os.chmod(cfg_path, 0o600)
        cfg_env = dict(os.environ)
        cfg_env["HOME"] = tmp_home  # k8sgpt 读 $HOME/.config/k8sgpt/k8sgpt.yaml（tmpfs）
        ok2, text2 = _run_k8sgpt(base_cmd, cfg_env, api_key)
        return text2
    except FileNotFoundError:
        return "K8sGPT unavailable: not installed"
    except subprocess.TimeoutExpired:
        return "K8sGPT unavailable: timeout"
    except Exception as e:
        # 异常消息脱敏（key 可能出现在异常 repr 中）
        return f"K8sGPT unavailable: {_redact_key(str(e), api_key) if api_key else str(e)}"
    finally:
        if tmp_home:
            import shutil
            try:
                shutil.rmtree(tmp_home, ignore_errors=True)  # 删除 tmpfs 临时配置，含 key
            except Exception:
                pass


def deepflow_status(*, request_context: RequestContext | None = None) -> str:
    data = _get_json(f"{QUERY_API}/deepflow/status", request_context=request_context)
    return json.dumps(data, indent=2, ensure_ascii=False)


def get_infrastructure(*, request_context: RequestContext | None = None) -> str:
    """获取K8s基础设施信息（经内部边界路径 /internal/v1/query/kubernetes）。

    P19 修复：此前用特权公开 /infrastructure/* 端点（requirePrivilegedRole 拒 system
    principal → 403）。改为走内部边界（身份校验 + boundary 只读，system principal 可用），
    返回 nodes + node_details + pods。空/错误 → unavailable，不伪造健康结论。
    """
    context = _context_for_cluster(
        str(request_context.cluster_id) if isinstance(request_context, ScopeView) else "",
        request_context,
    )
    if context is None:
        return "K8s 基础设施数据不可用（invalid_context）"
    if getattr(context, "workload_kind", "") == "investigation":
        try:
            data = _internal_investigation_query(
                tool_id="query_k8s.v1", operation="kubernetes", params={"namespace": "all"}, context=context,
            )
            data, unwrap_error = _unwrap_internal_query_result(data)
            if unwrap_error or data is None or data.get("error"):
                detail = unwrap_error or data.get("error")
                return f"K8s 基础设施数据不可用（数据源错误: {detail}）"
            pods = data.get("pods") or []
            nodes = data.get("node_details") or data.get("nodes") or []
            if not nodes:
                return "K8s 基础设施数据不可用（未获取到节点信息，无法据此判断健康）"
            return f"运行中 Pods: {len(pods)} 个\n节点: {len(nodes)} 个"
        except Exception as exc:
            return f"K8s 基础设施数据不可用（查询失败: {str(exc)[:200]}）"
    # 内部边界端点 /internal/v1/query/kubernetes 是 POST + body(cluster_id)。
    # QUERY_API 常含 /api/v1 前缀（公共 API），内部端点必须用 origin 基址（不含 /api/v1），
    # 否则会拼出 .../api/v1/internal/v1/... 错误路径（P19 真实环境暴露的 URL 接线缺陷）。
    origin = QUERY_API.rstrip("/").removesuffix("/api/v1")
    # 用 TrustedContextIssuer 构造 claims（system principal → session_id="")，与
    # InternalQueryClient 一致（P7.2 已验证）；signed_query_api_request 的 raw claims
    # 对 system 产生 session_id=None，被 query-api DecodeStrict 拒绝（401 invalid trusted context）。
    try:
        from trusted_context_issuer import TrustedContextIssuer
        from internal_query import _load_private_key
        from trusted_context import sign_trusted_request_context_v2
        private_key = _load_private_key(os.environ.get("TRUSTED_CONTEXT_PRIVATE_KEY", ""))
        claims = TrustedContextIssuer(private_key=private_key).build_claims(
            tenant_id=str(getattr(context, "tenant_id", "") or ""),
            cluster_id=str(getattr(context, "cluster_id", "") or ""),
            capability="kubernetes.resources.read",
            run_id=str(getattr(context, "run_id", "") or "") or str(_uuid.uuid4()),
            principal_type="system",
            principal_id=str(getattr(context, "principal_id", "") or "") or str(_uuid.uuid4()),
            source="agent",
        )
        jws = sign_trusted_request_context_v2(claims, private_key)
        service_token = os.environ.get("INTERNAL_TOKEN", "")
        req = urllib.request.Request(
            f"{origin}/internal/v1/query/kubernetes",
            data=json.dumps({"cluster_id": str(getattr(context, "cluster_id", "") or "")}).encode(),
            method="POST",
            headers={
                "Content-Type": "application/json",
                "X-Internal-Token": service_token,
                "X-Trusted-Request-Context": jws,
            },
        )
        with mtls_urlopen(req, timeout=20) as resp:
            data = json.loads(resp.read().decode())
    except TrustedContextError as e:
        data = {"error": e.error_code}
    except urllib.error.HTTPError as e:
        data = {"error": "HTTP Error %s: %s" % (e.code, e.read().decode()[:200])}
    except (urllib.error.URLError, json.JSONDecodeError) as e:
        data = {"error": str(e)}
    data, unwrap_error = _unwrap_internal_query_result(data)
    if unwrap_error or data is None or data.get("error"):
        detail = unwrap_error or data.get("error")
        return f"K8s 基础设施数据不可用（节点/Pod 数量未知，无法据此判断健康）: {detail}"

    pods = data.get("pods") or []
    nodes = data.get("node_details") or []
    node_names = data.get("nodes") or []

    # 空列表不是“0 个资源”的健康证据：若集群有节点但边界未返回，标记 unavailable。
    if not nodes and not node_names:
        return "K8s 基础设施数据不可用（未获取到节点信息，无法据此判断健康）"

    report = f"运行中 Pods: {len(pods)} 个\n"
    # 完整展示每个 Pod 的名字、命名空间、状态、重启次数，
    # 让 LLM 能引用真实资源名（如 redis-76dd9b85cb-q7p2r / redis）生成确定性处置命令
    infos = [(p.get('name','?')[:50], p.get('namespace','?'), p.get('status','?'), p.get('restarts',0)) for p in pods]
    for name, ns, st, rc in infos:
        report += f"  - {ns}/{name}: {st} restarts={rc}\n"

    report += f"\n- 节点: {len(nodes)} 个\n"
    for n in nodes:
        report += f"  - {n.get('name','?')}: {n.get('status','?')} CPU={n.get('cpu','?')} MEM={n.get('memory','?')}\n"
    return report

# ═══════════════════════════════════════════════════════════════
#  Mount E3: query_knowledge 内置运维知识库工具 (cls=safe, category=knowledge)
# ═══════════════════════════════════════════════════════════════
def _query_knowledge(query: str = "", path_prefix: str = "", tags: str = "",
                     max_results: int = 5) -> str:
    """查询内置运维知识库(playbook 处置手册 + 历史案例), 返回诊断建议/处置步骤。"""
    from playbook_loader import query_knowledge
    result = query_knowledge(
        query,
        path_prefix=path_prefix.strip() or None,
        tags=[t.strip() for t in tags.split(",") if t.strip()] if tags else None,
        max_results=int(max_results or 5),
    )
    return json.dumps(result, ensure_ascii=False)[:6000]


if not ToolRegistry.get("query_knowledge"):
    ToolRegistry.register(
        name="query_knowledge",
        description="查询内置运维知识库(playbook 处置手册 + 历史案例), 返回按相关度排序的诊断建议与处置步骤",
        category="knowledge",
        cls_="safe",
        params={
            "query": {"type": "string", "required": True, "default": "", "desc": "检索关键词"},
            "path_prefix": {"type": "string", "required": False, "default": "", "desc": "playbook 分类前缀(diagnostics/alerts/concepts/reference)"},
            "tags": {"type": "string", "required": False, "default": "", "desc": "标签过滤(逗号分隔)"},
            "max_results": {"type": "int", "required": False, "default": 5, "desc": "返回条数"},
        },
    )(_query_knowledge)

# ═══════════════════════════════════════════════════════════════
#  Mount E4: query_knowledge_graph 知识图谱证据链工具 (cls=safe, category=observability)
# ═══════════════════════════════════════════════════════════════
if not ToolRegistry.get("query_knowledge_graph"):
    ToolRegistry.register(
        name="query_knowledge_graph",
        description="查询运维知识图谱: 服务的依赖关系/上下游/关联变更/所属基础设施, 返回结构化证据链",
        category="observability",
        cls_="safe",
        params={
            "service": {"type": "string", "required": True, "default": "", "desc": "服务名"},
            "cluster_id": {"type": "string", "required": True, "default": "", "desc": "集群ID（必须由可信上下文提供）"},
        },
    )(kg_evidence_tool)
