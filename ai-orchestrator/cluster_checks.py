"""P1-S1: 结构化集群检查执行器（kubectl 调查路径的唯一入口）。

替代 rca.py::_run_kubectl_safe 的通用 shell 白名单方案：
- LLM 只允许输出结构化检查 (kind + namespace + pod)，不再输出 shell command；
- kubectl 一律以 argv + shell=False 执行，动词/参数全部来自代码内静态模板；
- 原 tail/head/grep/tr/sort/wc 管道后处理全部在 Python 内实现，awk 删除；
- 参数校验 fail-closed：路径穿越、flag 注入、任意 shell 字符串全部拒绝；
- 审计记录含 check_kind/namespace/pod/exit_code/duration_ms/truncated，不记录 Secret。

审核依据：《AIOps_全面技术审核与生产整改最终报告_2026-09-03》§5 (P1-S1)。
"""
from __future__ import annotations

import re
import subprocess
import time
from dataclasses import dataclass
from typing import Literal

# ── 结构化检查模型 ───────────────────────────────────────────────────

ClusterCheckKind = Literal[
    "pod_events",
    "pod_restarts",
    "pod_oom",
    "pod_waiting",
    "node_status",
    "node_usage",
    "pod_usage",
    "deploy_replicas",
    "svc_endpoints",
    "describe_pod",
]

VALID_KINDS: tuple[str, ...] = (
    "pod_events",
    "pod_restarts",
    "pod_oom",
    "pod_waiting",
    "node_status",
    "node_usage",
    "pod_usage",
    "deploy_replicas",
    "svc_endpoints",
    "describe_pod",
)

DEFAULT_NAMESPACE = "observability"

# 输出上限（报告 §5.2.5: 2000~8000 字符，对齐原实现 2000）
OUTPUT_LIMIT_CHARS = 2000
# 超时固定上限
TIMEOUT_CAP = 20


class InvalidClusterCheck(ValueError):
    """结构化检查参数非法（fail-closed）。"""


@dataclass(frozen=True)
class ClusterCheck:
    kind: ClusterCheckKind
    namespace: str | None = None
    pod: str | None = None


# ── 参数校验（K8s DNS label / subdomain 规范，天然拒绝路径与 flag 注入）──

_DNS_LABEL_RE = re.compile(r"^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$")


def _validate_name(value: object, field: str) -> str:
    if not isinstance(value, str) or not value:
        raise InvalidClusterCheck(f"{field} 必须为非空字符串")
    # 按子域名规则逐段校验：'/'、'~'、空格、'='、大写、前导 '-'、'..' 全部拒绝
    for seg in value.split("."):
        if not seg or not _DNS_LABEL_RE.match(seg):
            raise InvalidClusterCheck(f"{field} 不符合 Kubernetes 命名规范: {value[:60]!r}")
    return value


def parse_cluster_check(data: object) -> ClusterCheck:
    """dict -> ClusterCheck。仅接受 kind/namespace/pod，额外字段一律忽略。"""
    if not isinstance(data, dict):
        raise InvalidClusterCheck("检查参数必须为结构化对象")
    kind = data.get("kind")
    if not isinstance(kind, str) or kind not in VALID_KINDS:
        raise InvalidClusterCheck(f"未知检查类型: {kind!r}")
    namespace = data.get("namespace")
    if namespace is not None:
        namespace = _validate_name(namespace, "namespace")
    pod = data.get("pod")
    if pod is not None:
        pod = _validate_name(pod, "pod")
    if kind == "describe_pod" and not pod:
        raise InvalidClusterCheck("describe_pod 必须提供 pod 名称")
    return ClusterCheck(kind=kind, namespace=namespace, pod=pod)


def check_from_hypothesis(obj: object) -> ClusterCheck | None:
    """从 LLM hypothesis 提取结构化检查。

    - hypothesis dict（含 proposed_check 键）→ 取 proposed_check；
    - proposed_check 为 dict → parse_cluster_check（非法抛 InvalidClusterCheck）；
    - proposed_check 为 '{kind}' 模板引用 → 映射为结构化检查（未知模板抛
      InvalidClusterCheck，模板引用是合法语法但必须落在已知 kind 上）；
    - 其余（空串/裸 shell 字符串/非字符串/None）→ None（拒绝，不执行）。
    """
    if obj is None:
        return None
    if isinstance(obj, dict):
        # hypothesis dict：提取 proposed_check（缺失视为未提出检查 → None）
        obj = obj.get("proposed_check")
    if obj is None:
        return None
    if isinstance(obj, dict):
        return parse_cluster_check(obj)
    if isinstance(obj, str):
        s = obj.strip()
        if s.startswith("{") and s.endswith("}"):
            return parse_cluster_check({"kind": s[1:-1]})
        # 任意裸字符串（含 kubectl/shell 命令）一律拒绝
        return None
    return None


# ── 静态 argv 模板（唯一命令构造来源；调用方无法注入 flag/-o）──────────

# kind -> (动词, 资源, 是否需要 -n namespace)
_BASE_ARGV: dict[str, tuple[str, str, bool]] = {
    "pod_events": ("get", "events", True),
    "pod_restarts": ("get", "pods", True),
    "pod_oom": ("get", "pods", True),
    "pod_waiting": ("get", "pods", True),
    "node_status": ("get", "nodes", False),
    "node_usage": ("top", "node", False),
    "pod_usage": ("top", "pod", True),
    "deploy_replicas": ("get", "deployment", True),
    "svc_endpoints": ("get", "endpoints", True),
    "describe_pod": ("describe", "pod", True),
}

# kind -> 静态 -o 输出参数（None 表示不使用 -o）
_OUTPUT_TEMPLATES: dict[str, str | None] = {
    "pod_events": None,
    "pod_restarts": "wide",
    "pod_oom": "jsonpath={.items[*].status.containerStatuses[*].lastState.terminated.reason}",
    "pod_waiting": None,
    "node_status": "wide",
    "node_usage": None,
    "pod_usage": None,
    "deploy_replicas": "custom-columns=NAME:.metadata.name,DESIRED:.spec.replicas,READY:.status.readyReplicas",
    "svc_endpoints": None,
    "describe_pod": None,
}

# kind -> 附加固定参数（field-selector / --sort-by 等，全部代码内静态定义）
_EXTRA_ARGS: dict[str, tuple[str, ...]] = {
    "pod_events": ("--sort-by=.lastTimestamp", "--field-selector=type!=Normal"),
    "pod_waiting": ("--field-selector=status.phase=Pending",),
}


def build_argv(check: ClusterCheck) -> list[str]:
    """构造 kubectl argv。动词/flag 全部来自静态模板，调用方零注入面。"""
    if check.kind not in _BASE_ARGV:
        raise InvalidClusterCheck(f"未知检查类型: {check.kind!r}")
    verb, resource, needs_ns = _BASE_ARGV[check.kind]
    argv = ["kubectl", verb, resource]
    if needs_ns:
        argv += ["-n", check.namespace or DEFAULT_NAMESPACE]
    out_tmpl = _OUTPUT_TEMPLATES[check.kind]
    if out_tmpl:
        argv += ["-o", out_tmpl]
    argv += list(_EXTRA_ARGS.get(check.kind, ()))
    if check.kind == "describe_pod":
        argv += [check.pod or ""]
    return argv


# ── Python 后处理（原 tail/head/grep/tr/sort/wc 职责内化，awk 删除）───

def _postprocess(kind: str, raw: str) -> str:
    lines = raw.splitlines()
    if kind == "pod_events":
        # 原: | tail -20
        return "\n".join(lines[-20:])
    if kind == "describe_pod":
        # 原: | tail -30
        return "\n".join(lines[-30:])
    if kind == "pod_oom":
        # 原: | tr ' ' '\n' | grep -i oom（受控字符串匹配，正则由程序定义）
        return "\n".join(t for t in raw.split() if "oom" in t.lower())
    return raw


# ── 审计（不含 stdout/Secret）────────────────────────────────────────

AUDIT_LOG: list[dict] = []


def _record_audit(check: ClusterCheck, exit_code: int,
                  duration_ms: float, truncated: bool) -> None:
    AUDIT_LOG.append({
        "check_kind": check.kind,
        "namespace": check.namespace,
        "pod": check.pod,
        "exit_code": exit_code,
        "duration_ms": round(duration_ms, 1),
        "truncated": truncated,
    })


# ── 执行（argv + shell=False）────────────────────────────────────────

def run_cluster_check(check: ClusterCheck, timeout: int = 15) -> str:
    """执行结构化集群检查，返回净化后的输出文本。

    - subprocess.run(argv, shell=False)，无任何 shell 语义；
    - 超时固定上限 TIMEOUT_CAP；
    - stdout 截断到 OUTPUT_LIMIT_CHARS；stderr 净化后有限返回；
    - 异常不向调用方 dump 内部细节；
    - 每次执行写一条审计记录（无 Secret）。
    """
    effective_timeout = min(timeout, TIMEOUT_CAP)
    argv = build_argv(check)
    started = time.monotonic()
    exit_code = -1
    truncated = False
    try:
        r = subprocess.run(argv, shell=False, check=False, capture_output=True,
                           text=True, timeout=effective_timeout)
        exit_code = r.returncode
        out = _postprocess(check.kind, r.stdout or "")
        if len(out) > OUTPUT_LIMIT_CHARS:
            out = out[:OUTPUT_LIMIT_CHARS]
            truncated = True
        if r.returncode != 0 and r.stderr:
            out += f"\n[stderr]: {r.stderr[:300]}"
        return out or "(no output)"
    except subprocess.TimeoutExpired:
        return f"命令超时 (>{effective_timeout}s)"
    except FileNotFoundError:
        return "[集群检查不可用] kubectl 不可用"
    except Exception:  # noqa: BLE001 — 兜底 fail-safe：不得向 LLM/调用方 dump 内部异常
        return "[集群检查不可用] 执行失败"
    finally:
        _record_audit(check, exit_code,
                      (time.monotonic() - started) * 1000, truncated)
