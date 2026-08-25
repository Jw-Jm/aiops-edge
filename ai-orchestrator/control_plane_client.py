"""V9.3 Phase 10 (P10 完整闭环 Plan B) — ControlPlaneClient。

orchestrator（system principal）经 /internal/v1/control-plane/* 让 query-api
（persistence owner）做 CAS + 持久化 Run/Event。

capability 为独立内部服务能力域（control_plane.*），**不进入** Tool Registry
KNOWN_CAPABILITIES（D1），故不使用 TrustedContextIssuer.build_claims（其会拒绝
control_plane.* 能力），而直接构造 claims + sign_trusted_request_context_v2 签发。

调用方向：orchestrator（issuer=ai-orchestrator）→ query-api（audience=ai-apm-query-go）。
"""
from __future__ import annotations

import json
import os
import uuid
from datetime import datetime, timedelta, timezone
from typing import Any, Callable, Mapping, Optional
from urllib.error import HTTPError
from urllib.request import Request, urlopen

from trusted_context import TrustedContextError, sign_trusted_request_context_v2

# ── control-plane 独立内部服务能力域（D1，不进 Tool Registry）─────────────
CP_RUNS_MUTATE = "control_plane.runs.mutate"
CP_RUNS_RECOVER = "control_plane.runs.recover"
# A0-05（F-18）：全局 unfinished 扫描（跨所有 tenant）用独立 system capability，
# 与单 Run recover（control_plane.runs.recover）分离，防止普通恢复身份枚举全量非终态 Run。
CP_RUNS_RECOVER_GLOBAL = "control_plane.runs.recover.global"
CP_EVENTS_APPEND = "control_plane.events.append"
CP_EVENTS_REPLAY = "control_plane.events.replay"
CP_EVIDENCE_CONSUME = "control_plane.evidence.consume"
CP_VERIFICATIONS_APPEND = "control_plane.verifications.append"
CP_SETTINGS_READ = "control_plane.settings.read"
CP_SETTINGS_WRITE = "control_plane.settings.write"
CP_KNOWLEDGE_GRAPH_READ = "control_plane.knowledge_graph.read"
CP_KNOWLEDGE_GRAPH_WRITE = "control_plane.knowledge_graph.write"

# orchestrator system principal 的固定 canonical UUID（非用户/Agent）。
SYSTEM_PRINCIPAL_ID = "f4a4b8c2-3d5e-4f6a-8b9c-0d1e2f3a4b5c"
DEFAULT_SYSTEM_TENANT_ID = "7ed01afc-cc79-4ecd-8767-a2befa6168ad"
_CP_LIFETIME_SECONDS = 30
_DEFAULT_TIMEOUT = 10


class ControlPlaneError(TrustedContextError):
    """control-plane 边界错误。kind 对应 HTTP 语义（RUN_STATE_CONFLICT 等）。"""

    def __init__(self, kind: str, http_status: int, message: str = ""):
        self.kind = kind
        self.http_status = http_status
        super().__init__(kind)


def _default_http(
    path: str,
    *,
    context_claims: Mapping[str, Any],
    method: str = "POST",
    data: Optional[bytes] = None,
    headers: Optional[Mapping[str, str]] = None,
) -> tuple:
    """把 claims 签成 EdDSA JWS 并发到 query-api control-plane 端点。"""
    from internal_query import _load_private_key, _validate_query_api_url

    private_key = _load_private_key(os.environ.get("TRUSTED_CONTEXT_PRIVATE_KEY", ""))
    token = sign_trusted_request_context_v2(dict(context_claims), private_key)
    service_token = os.environ.get("INTERNAL_TOKEN", "")
    if not service_token:
        raise TrustedContextError("invalid_service")
    base = os.environ.get("QUERY_API_URL", "").rstrip("/")
    # URL 接线修复（真实环境验证 Phase A）：QUERY_API_URL 常含 /api/v1（用于公共 API），
    # 而 control-plane 路由注册在根 /internal/v1/control-plane/*。剥掉 base 路径，保留
    # scheme://host:port，使 internal 路由落在根路径（_validate_query_api_url 已放行 /internal/v1/）。
    from urllib.parse import urlparse
    parsed = urlparse(base)
    origin = f"{parsed.scheme}://{parsed.netloc}"
    url = origin + path
    _validate_query_api_url(url)
    request_headers = {
        **(dict(headers) if headers else {}),
        "X-Internal-Token": service_token,
        "X-Trusted-Request-Context": token,
    }
    request = Request(url, data=data, method=method.upper(), headers=request_headers)
    try:
        with urlopen(request, timeout=_DEFAULT_TIMEOUT) as response:
            return response.status, response.read()
    except HTTPError as e:
        try:
            body = e.read()
        except Exception:
            body = b"{}"
        return e.code, body


class ControlPlaneClient:
    """orchestrator → query-api control-plane 持久化客户端（system principal）。"""

    def __init__(self, *, issuer: str = "ai-orchestrator", audience: str = "ai-apm-query-go",
                 http: Optional[Callable] = None) -> None:
        self._issuer = issuer
        self._audience = audience
        self._http = http or _default_http

    # ── claims 构造（system principal，scope_kind=run）────────────────────
    def _claims(self, *, run_id: str, capability: str, tenant_id: str,
                request_id: Optional[str] = None, cluster_id: str = "",
                scope_kind: str = "run", workload_kind: str = "platform") -> dict:
        now = datetime.now(timezone.utc)
        return {
            "version": 1,
            "context_type": "trusted_request",
            "issuer": self._issuer,
            "audience": self._audience,
            "request_id": request_id or str(uuid.uuid4()),
            "run_id": run_id,
            "principal_type": "system",
            "principal_id": SYSTEM_PRINCIPAL_ID,
            "session_id": "",  # system principal 必须空 session
            "tenant_id": tenant_id,
            "scope_kind": scope_kind,
            "cluster_id": cluster_id,
            "capability": capability,
            "source": "control-plane",
            "workload_kind": workload_kind,
            "issued_at": now,
            "expires_at": now + timedelta(seconds=_CP_LIFETIME_SECONDS),
            "nonce": str(uuid.uuid4()),
        }

    def _post(self, path: str, claims: dict, body: dict) -> dict:
        status, raw = self._http(
            path,
            context_claims=claims,
            method="POST",
            data=json.dumps(body, separators=(",", ":")).encode("utf-8"),
            headers={"Content-Type": "application/json"},
        )
        parsed = self._parse(status, raw)
        if status not in (200, 201):
            raise self._error(status, parsed)
        return parsed

    def _get(self, path: str, claims: dict) -> dict:
        status, raw = self._http(path, context_claims=claims, method="GET")
        parsed = self._parse(status, raw)
        if status != 200:
            raise self._error(status, parsed)
        return parsed

    @staticmethod
    def _parse(status: int, raw: bytes) -> dict:
        try:
            body = json.loads(raw.decode("utf-8")) if raw else {}
        except Exception:
            body = {}
        return body if isinstance(body, dict) else {}

    def _error(self, status: int, body: dict) -> ControlPlaneError:
        kind = {
            401: "service_auth_failed",
            403: "permission_denied",
            404: "not_found",
            409: body.get("error") or "run_state_conflict",
            422: "validation_failed",
            503: "unavailable",
        }.get(status, "internal")
        return ControlPlaneError(kind=kind, http_status=status, message=str(body.get("error", "")))

    # ── runs ─────────────────────────────────────────────────────────────
    def transition(self, *, run_id: str, target: str, expected_version: int,
                   tenant_id: str, command_id: str) -> dict:
        claims = self._claims(run_id=run_id, capability=CP_RUNS_MUTATE, tenant_id=tenant_id,
                              request_id=command_id)
        return self._post(f"/internal/v1/control-plane/runs/{run_id}/transition", claims, {
            "target": target, "expected_version": expected_version, "command_id": command_id,
        })

    def cancel(self, *, run_id: str, tenant_id: str, expected_version: int,
               command_id: str) -> dict:
        """cancel 必须显式携带 expected_version + command_id（A0-01 / F-02）。
        expected_version 用于 CAS 冲突检测；command_id 用于业务幂等（响应丢失后
        query-api 返回首次结果）。"""
        claims = self._claims(run_id=run_id, capability=CP_RUNS_MUTATE, tenant_id=tenant_id,
                              request_id=command_id)
        return self._post(f"/internal/v1/control-plane/runs/{run_id}/cancel", claims, {
            "expected_version": expected_version, "command_id": command_id,
        })

    def append_action(self, *, run_id: str, tenant_id: str, cluster_id: str,
                      action_id: str, action_type: str, action_hash: str,
                      idempotency_key: str, proposed_risk: str = "R0",
                      authoritative_risk: str = "R0", status: str = "proposed",
                      dry_run: bool = True, params: Optional[Mapping[str, Any]] = None,
                      target_name: str = "", target_uid: str = "",
                      resource_version: str = "", namespace: str = "",
                      operation: str = "", resource_type: str = "deployment") -> dict:
        """Persist an action proposal; never executes the data-plane mutation."""
        claims = self._claims(run_id=run_id, capability=CP_RUNS_MUTATE,
                              tenant_id=tenant_id, cluster_id=cluster_id,
                              scope_kind="cluster" if cluster_id else "run")
        return self._post(f"/internal/v1/control-plane/runs/{run_id}/actions", claims, {
            "action_id": action_id, "action_type": action_type,
            "action_hash": action_hash, "idempotency_key": idempotency_key,
            "proposed_risk": proposed_risk, "authoritative_risk": authoritative_risk,
            "status": status, "dry_run": dry_run, "params": dict(params or {}),
            "target_name": target_name, "target_uid": target_uid,
            "resource_version": resource_version, "namespace": namespace,
            "operation": operation, "resource_type": resource_type,
        })

    def append_hypothesis(self, *, run_id: str, tenant_id: str, cluster_id: str,
                          hypothesis_id: str, content: str, confidence: float = 0.0,
                          status: str = "proposed", confirmed_by_evidence: bool = False) -> dict:
        claims = self._claims(run_id=run_id, capability=CP_RUNS_MUTATE,
                              tenant_id=tenant_id, cluster_id=cluster_id,
                              scope_kind="cluster" if cluster_id else "run")
        return self._post(f"/internal/v1/control-plane/runs/{run_id}/hypotheses", claims, {
            "hypothesis_id": hypothesis_id, "content": content,
            "confidence": confidence, "status": status,
            "confirmed_by_evidence": confirmed_by_evidence,
        })

    def append_plan_step(self, *, run_id: str, tenant_id: str, cluster_id: str,
                         step_id: str, seq: int, step_type: str,
                         status: str = "success", description: str = "",
                         depends_on: Optional[list[str]] = None,
                         parameters: Optional[Mapping[str, Any]] = None) -> dict:
        claims = self._claims(run_id=run_id, capability=CP_RUNS_MUTATE,
                              tenant_id=tenant_id, cluster_id=cluster_id,
                              scope_kind="cluster" if cluster_id else "run")
        return self._post(f"/internal/v1/control-plane/runs/{run_id}/plan-steps", claims, {
            "step_id": step_id, "seq": seq, "step_type": step_type,
            "status": status, "cluster_id": cluster_id, "description": description,
            "depends_on": list(depends_on or []), "parameters": dict(parameters or {}),
        })

    def get(self, *, run_id: str, tenant_id: str) -> dict:
        claims = self._claims(run_id=run_id, capability=CP_RUNS_RECOVER, tenant_id=tenant_id)
        return self._get(f"/internal/v1/control-plane/runs/{run_id}", claims)

    def list_unfinished(self, *, tenant_id: str, worker_kind: str = "investigation",
                        after_created_at: str = "", after_run_id: str = "") -> list:
        # A0-05（F-18）：全局 unfinished 扫描用独立 system capability
        # control_plane.runs.recover.global。
        # 注意：query-api 对 TrustedRequestContext 的 validateCommon 要求 tenant_id 是有效
        # UUID（不允许空），而 internalControlPlaneRunUnfinished 本身**不按 tenant 过滤**
        # （ScanUnfinishedLimit 扫全部非终态 Run）。因此这里传调用方传入的 tenant_id
        #（必须是有效 UUID）以满足签名格式要求；扫描仍是全局的。
        claims = self._claims(run_id=str(uuid.uuid4()), capability=CP_RUNS_RECOVER_GLOBAL, tenant_id=tenant_id)
        query = []
        if worker_kind:
            query.append(f"worker_kind={worker_kind}")
        if after_created_at:
            query.append(f"after_created_at={after_created_at}")
        if after_run_id:
            query.append(f"after_run_id={after_run_id}")
        path = "/internal/v1/control-plane/runs/unfinished"
        if query:
            from urllib.parse import quote
            encoded = []
            for item in query:
                key, value = item.split("=", 1)
                encoded.append(f"{key}={quote(value, safe='')}")
            path += "?" + "&".join(encoded)
        return self._get(path, claims).get("runs", [])

    # ── platform settings ───────────────────────────────────────────────
    def get_recovery_policy(self, *, tenant_id: Optional[str] = None) -> dict:
        """读取由 query-api 持有的恢复策略配置。"""
        claims = self._claims(
            run_id=str(uuid.uuid4()), capability=CP_SETTINGS_READ,
            tenant_id=tenant_id or os.environ.get("AIOPS_SYSTEM_TENANT_ID", DEFAULT_SYSTEM_TENANT_ID),
        )
        return self._get("/internal/v1/control-plane/settings/recovery-policy", claims)

    def set_recovery_policy(self, policy: Mapping[str, Any], *, tenant_id: Optional[str] = None) -> dict:
        """写入由 query-api 持有的恢复策略配置。"""
        claims = self._claims(
            run_id=str(uuid.uuid4()), capability=CP_SETTINGS_WRITE,
            tenant_id=tenant_id or os.environ.get("AIOPS_SYSTEM_TENANT_ID", DEFAULT_SYSTEM_TENANT_ID),
        )
        return self._post(
            "/internal/v1/control-plane/settings/recovery-policy", claims,
            {"policy": dict(policy)},
        )

    def knowledge_graph(self, operation: str, body: Mapping[str, Any], *, write: bool = False,
                        tenant_id: Optional[str] = None) -> dict:
        """Call the query-api-owned knowledge graph persistence boundary."""
        payload = {"operation": operation, **dict(body)}
        cluster_id = str(payload.get("cluster_id") or "")
        for nested_key in ("props", "edge_props"):
            nested = payload.get(nested_key)
            if not cluster_id and isinstance(nested, Mapping):
                cluster_id = str(nested.get("cluster_id") or "")
        claims = self._claims(
            run_id=str(uuid.uuid4()),
            capability=CP_KNOWLEDGE_GRAPH_WRITE if write else CP_KNOWLEDGE_GRAPH_READ,
            tenant_id=tenant_id or os.environ.get("AIOPS_SYSTEM_TENANT_ID", DEFAULT_SYSTEM_TENANT_ID),
            cluster_id=cluster_id,
            scope_kind="cluster" if cluster_id else "run",
        )
        return self._post("/internal/v1/control-plane/knowledge-graph", claims, payload)

    # ── lease / commit（B2-01 / A1）────────────────────────────────────────
    def claim_lease(self, *, run_id: str, tenant_id: str, owner_id: str,
                    lease_seconds: int = 60, claim_id: str = "",
                    lease_token: str = "", claim_source: str = "LIVE_INVOCATION") -> dict:
        """Claim 一次 Run execution lease（fencing：epoch + token）。

        P0-LEASE-03：支持 caller 提供 claim_id + lease_token（>=256-bit random），
        使"Claim 响应丢失后以相同 claim_id 精确重试"恢复同一 Lease（epoch 不变）。
        缺省时服务端生成。
        """
        claims = self._claims(run_id=run_id, capability=CP_RUNS_MUTATE, tenant_id=tenant_id)
        return self._post(f"/internal/v1/control-plane/runs/{run_id}/claim", claims, {
            "owner_id": owner_id, "lease_seconds": lease_seconds,
            "claim_id": claim_id, "lease_token": lease_token, "claim_source": claim_source,
        })

    def renew_lease(self, *, run_id: str, tenant_id: str, owner_id: str,
                    epoch: int, token: str, lease_seconds: int = 60) -> dict:
        claims = self._claims(run_id=run_id, capability=CP_RUNS_MUTATE, tenant_id=tenant_id)
        return self._post(f"/internal/v1/control-plane/runs/{run_id}/renew", claims, {
            "owner_id": owner_id, "epoch": epoch, "token": token, "lease_seconds": lease_seconds,
        })

    def release_lease(self, *, run_id: str, tenant_id: str, epoch: int, token: str) -> dict:
        claims = self._claims(run_id=run_id, capability=CP_RUNS_MUTATE, tenant_id=tenant_id)
        return self._post(f"/internal/v1/control-plane/runs/{run_id}/release", claims, {
            "epoch": epoch, "token": token,
        })

    def commit(self, *, run_id: str, tenant_id: str, commit_id: str, payload_hash: str,
               target: str, result: Mapping[str, Any], events: list,
               expected_version: int, owner_id: str, epoch: int, token: str) -> dict:
        """原子 Runtime Commit：Lease fencing + Run CAS + 事件追加 + commit 记录（幂等）。"""
        claims = self._claims(run_id=run_id, capability=CP_RUNS_MUTATE, tenant_id=tenant_id,
                              request_id=commit_id)
        return self._post(f"/internal/v1/control-plane/runs/{run_id}/commit", claims, {
            "commit_id": commit_id, "payload_hash": payload_hash, "target": target,
            "result": dict(result or {}), "events": list(events or []),
            "expected_version": expected_version, "owner_id": owner_id,
            "epoch": epoch, "token": token,
        })

    # ── events ───────────────────────────────────────────────────────────
    def append_event(self, *, run_id: str, tenant_id: str, event_id: str,
                     event_type: str, payload: Mapping[str, Any]) -> dict:
        claims = self._claims(run_id=run_id, capability=CP_EVENTS_APPEND, tenant_id=tenant_id)
        return self._post(f"/internal/v1/control-plane/runs/{run_id}/events", claims, {
            "event_id": event_id, "event_type": event_type, "payload": dict(payload or {}),
        })

    def replay(self, *, run_id: str, tenant_id: str, after_sequence: int = 0) -> list:
        claims = self._claims(run_id=run_id, capability=CP_EVENTS_REPLAY, tenant_id=tenant_id)
        path = f"/internal/v1/control-plane/runs/{run_id}/events?after_sequence={after_sequence}"
        return self._get(path, claims).get("events", [])

    def consume_tool_evidence(self, *, run_id: str, tenant_id: str, cluster_id: str,
                              tool_run_id: str, evidence_id: str, evidence_type: str,
                              source_ref: str, raw_ref: str, raw_digest_sha256: str,
                              summary: str, metadata: Mapping[str, Any] | None = None,
                              provenance_fingerprint: str = "") -> dict:
        """Atomically consume an eligible ToolRun into query-api Evidence."""
        claims = self._claims(run_id=run_id, capability=CP_EVIDENCE_CONSUME,
                              tenant_id=tenant_id, scope_kind="run")
        return self._post(
            f"/internal/v1/control-plane/tools/{tool_run_id}/evidence/consume", claims,
            {"run_id": run_id, "tenant_id": tenant_id, "cluster_id": cluster_id,
             "evidence_id": evidence_id, "evidence_type": evidence_type,
             "source_ref": source_ref, "raw_ref": raw_ref,
             "raw_digest_sha256": raw_digest_sha256, "summary": summary,
             "metadata": dict(metadata or {}),
            "provenance_fingerprint": provenance_fingerprint},
        )

    def append_verification(self, *, run_id: str, tenant_id: str,
                            verification_id: str, action_id: str, status: str,
                            before_snapshot: Mapping[str, Any],
                            after_snapshot: Mapping[str, Any],
                            observation_window_seconds: int,
                            checks: list[Mapping[str, Any]] | None = None,
                            summary: str = "") -> dict:
        """Persist an independent observer result in query-api/MySQL."""
        claims = self._claims(run_id=run_id, capability=CP_VERIFICATIONS_APPEND,
                              tenant_id=tenant_id, scope_kind="run")
        return self._post(
            f"/internal/v1/control-plane/runs/{run_id}/verifications", claims,
            {"verification_id": verification_id, "action_id": action_id,
             "status": status, "before_snapshot": dict(before_snapshot or {}),
             "after_snapshot": dict(after_snapshot or {}),
             "observation_window_seconds": observation_window_seconds,
             "checks": list(checks or []), "summary": summary},
        )
