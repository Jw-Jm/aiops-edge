"""P7.1 Tool Registry MVP — V9.3 Phase7 工具能力注册（注册不执行）。

核心原则（F1-F5 红线）：
- 注册不执行：ToolDefinition 只做能力注册与 capability gate，不绑定可执行函数。
- Tool ≠ Data Owner：owner 声明数据所有者，Agent 不能通过注册 Tool 自定义数据源。
- risk immutable：激活后 risk_level 只可保持/升级，不可静默降级。
- capability 只读：capability 来自注册映射，LLM/Agent 不能生成/篡改。
- execute_k8s / execute_shell 仅注册为 R4 / planner_selectable=false / automatic=false /
  execution_state=disabled，绝不形成实际执行路径。
"""
from __future__ import annotations

from dataclasses import dataclass, field
from typing import Optional, Set

# ═══════════════════════════════════════════════════════
#  契约常量
# ═══════════════════════════════════════════════════════

CONTRACT_VERSION = "v1"

# risk 枚举（§28.8）：R0 自动 → R4 严格审批
RISK_LEVELS = {"R0", "R1", "R2", "R3", "R4"}

# lifecycle（评审建议）
LIFECYCLE_STATUSES = {"draft", "active", "deprecated", "disabled"}

# execution_state：enabled 仅理论值；Phase7 执行类恒 disabled
EXECUTION_STATES = {"enabled", "disabled"}

# capability（§16 能力集 + §31 Tool→capability 映射）
KNOWN_CAPABILITIES = {
    "observability.metrics.read",
    "observability.logs.read",
    "observability.traces.read",
    "observability.alerts.read",
    "observability.topology.read",
    "kubernetes.resources.read",
    "kubernetes.events.read",
    "kubernetes.logs.read",
    "knowledge.search",
    "knowledge.graph.read",
    "kubevirt.resources.read",
    "hardware.inventory.read",
    "hardware.health.read",
    "catalog.read",
    "network.topology.read",
    "changes.read",
    "execution.k8s",
    "execution.shell",
}

# 执行类 Tool 白名单：仅这些可作为 read_only=False 注册，且强制 R4/disabled
EXECUTE_TOOL_IDS = {"execute_k8s.v1", "execute_shell.v1"}


# ═══════════════════════════════════════════════════════
#  ToolDefinition
# ═══════════════════════════════════════════════════════

@dataclass
class ToolDefinition:
    tool_id: str
    version: str
    name: str
    description: str
    category: str
    domain: str = "observability"
    owner: str = ""
    contract_version: str = CONTRACT_VERSION
    lifecycle_status: str = "active"
    read_only: bool = True
    baseline_risk: str = "R0"
    risk_level: str = "R0"
    evidence_required: bool = True
    capability: str = ""
    required_capability: str = ""
    availability: str = "per-cluster"
    allowed_scope: str = "cluster"
    input_schema: dict = field(default_factory=dict)
    output_schema: dict = field(default_factory=dict)
    timeout_class: str = "fast"
    timeout: int = 30
    retry: int = 0
    backend: str = "query-api"
    planner_selectable: bool = True
    automatic: bool = True
    execution_state: str = "enabled"

    def to_dict(self) -> dict:
        return {
            "tool_id": self.tool_id, "version": self.version, "contract_version": self.contract_version,
            "domain": self.domain, "name": self.name, "category": self.category,
            "description": self.description, "owner": self.owner,
            "lifecycle_status": self.lifecycle_status, "read_only": self.read_only,
            "baseline_risk": self.baseline_risk, "risk_level": self.risk_level,
            "evidence_required": self.evidence_required, "capability": self.capability,
            "required_capability": self.required_capability, "availability": self.availability,
            "allowed_scope": self.allowed_scope, "input_schema": self.input_schema,
            "output_schema": self.output_schema, "timeout_class": self.timeout_class,
            "timeout": self.timeout, "retry": self.retry, "backend": self.backend,
            "planner_selectable": self.planner_selectable, "automatic": self.automatic,
            "execution_state": self.execution_state,
        }


# ═══════════════════════════════════════════════════════
#  校验
# ═══════════════════════════════════════════════════════

def validate_tool_definition(t: ToolDefinition) -> Optional[str]:
    """返回错误消息；合法返回 None。"""
    if not t.tool_id:
        return "tool_id 必填"
    if not t.name:
        return "name 必填"
    if not t.owner:
        return "owner 必填（Tool ≠ Data Owner）"
    if t.contract_version not in (CONTRACT_VERSION,):
        return f"contract_version 不兼容: {t.contract_version}"
    if t.lifecycle_status not in LIFECYCLE_STATUSES:
        return f"lifecycle_status 非法: {t.lifecycle_status}"
    if t.baseline_risk not in RISK_LEVELS:
        return f"baseline_risk 非法: {t.baseline_risk}"
    if t.risk_level not in RISK_LEVELS:
        return f"risk_level 非法: {t.risk_level}"
    if t.execution_state not in EXECUTION_STATES:
        return f"execution_state 非法: {t.execution_state}"
    if not t.capability:
        return "capability 必填"
    if not t.required_capability:
        return "required_capability 必填"
    if t.capability not in KNOWN_CAPABILITIES:
        return f"capability 未注册/非法: {t.capability}"
    if t.required_capability != t.capability:
        return "required_capability 必须等于 capability（capability 来自注册映射）"
    if not t.allowed_scope:
        return "allowed_scope 必填"
    if t.allowed_scope not in {"cluster", "tenant", "global"}:
        return f"allowed_scope 非法: {t.allowed_scope}"

    # 执行类约束（F1 红线 + P7.1 设计）
    if not t.read_only:
        if t.tool_id not in EXECUTE_TOOL_IDS:
            return f"非只读 Tool 必须属于执行白名单: {t.tool_id}"
        # execute_* 强制 R4 + 不可选 + 非自动 + disabled
        if t.baseline_risk != "R4" or t.risk_level != "R4":
            return "execute_* Tool 必须 baseline_risk=R4, risk_level=R4"
        if t.planner_selectable:
            return "execute_* Tool 必须 planner_selectable=false"
        if t.automatic:
            return "execute_* Tool 必须 automatic=false"
        if t.execution_state != "disabled":
            return "execute_* Tool 必须 execution_state=disabled"
    else:
        # 只读 Tool 不得是执行类 capability
        if t.capability in {"execution.k8s", "execution.shell"}:
            return "只读 Tool 不能声明 execution capability"
    return None


# ═══════════════════════════════════════════════════════
#  Registry
# ═══════════════════════════════════════════════════════

class ToolRegistry:
    _tools: dict[str, ToolDefinition] = {}
    # 记录激活时 risk 快照，用于 immutable 校验
    _activated_risk: dict[str, str] = {}

    @classmethod
    def register(cls, t: ToolDefinition) -> ToolDefinition:
        err = validate_tool_definition(t)
        if err:
            raise ValueError(f"ToolDefinition 校验失败: {err}")
        if t.tool_id in cls._tools:
            raise ValueError(f"tool_id 重复: {t.tool_id}")
        cls._tools[t.tool_id] = t
        if t.lifecycle_status == "active":
            cls._activated_risk[t.tool_id] = t.risk_level
        return t

    @classmethod
    def get(cls, tool_id: str) -> Optional[ToolDefinition]:
        return cls._tools.get(tool_id)

    @classmethod
    def get_by_name(cls, name: str) -> Optional[ToolDefinition]:
        for t in cls._tools.values():
            if t.name == name:
                return t
        return None

    @classmethod
    def list_all(cls) -> list[ToolDefinition]:
        return list(cls._tools.values())

    @classmethod
    def list_selectable(
        cls,
        capabilities: Set[str],
        scope: str = "cluster",
        cluster_available: bool = True,
    ) -> list[ToolDefinition]:
        out = []
        for t in cls._tools.values():
            if cls.is_selectable(
                t.tool_id, capabilities=capabilities, scope=scope, cluster_available=cluster_available
            ):
                out.append(t)
        return out

    @classmethod
    def is_selectable(
        cls,
        tool_id: str,
        capabilities: Optional[Set[str]] = None,
        scope: str = "cluster",
        cluster_available: bool = True,
    ) -> bool:
        """Planner selection gate（§30）：
        registered ∧ active ∧ available ∧ capability ⊆ principal ∧ scope 覆盖 ∧ planner_selectable。
        """
        t = cls._tools.get(tool_id)
        if t is None:
            return False
        if t.lifecycle_status != "active":
            return False
        if not cluster_available:
            return False
        if not t.planner_selectable:
            return False
        # capability gate（§31）：required ⊆ principal
        caps = set(capabilities or set())
        if t.required_capability not in caps:
            return False
        # scope gate
        if scope not in {"cluster", "tenant", "global"}:
            return False
        if t.allowed_scope == "cluster" and scope not in {"cluster", "tenant"}:
            return False
        if t.allowed_scope == "tenant" and scope not in {"tenant", "global"}:
            return False
        if t.allowed_scope == "global" and scope != "global":
            return False
        return True

    @classmethod
    def update(cls, tool_id: str, **fields) -> bool:
        """受限更新：risk immutable、capability 需重审、contract_version 不兼容拒绝。"""
        t = cls._tools.get(tool_id)
        if t is None:
            return False
        # contract_version 不兼容拒绝
        if "contract_version" in fields and fields["contract_version"] != t.contract_version:
            return False
        # active 后 risk 只可保持/升级，不可降级
        if "risk_level" in fields:
            new = fields["risk_level"]
            if new not in RISK_LEVELS:
                return False
            activated = cls._activated_risk.get(tool_id, t.risk_level)
            if _rank(new) < _rank(activated):
                return False  # 静默降级 → 拒绝
        # active 修改 capability 需重新审批（F1 红线）
        if "capability" in fields or "required_capability" in fields:
            return False
        for k, v in fields.items():
            if hasattr(t, k):
                setattr(t, k, v)
        # 若生命周期离开 active，移除 risk 快照
        if fields.get("lifecycle_status") != "active":
            cls._activated_risk.pop(tool_id, None)
        return True


def _rank(r: str) -> int:
    return {"R0": 0, "R1": 1, "R2": 2, "R3": 3, "R4": 4}[r]


# ═══════════════════════════════════════════════════════
#  最低生产 Tool 清单（§30）
# ═══════════════════════════════════════════════════════

def minimum_tool_ids() -> list[str]:
    return list(REGISTRY_TOOL_IDS)


# 只读查询 Tool（§30，含统一 Kubernetes/IPMI 事件事实读取）
_READONLY_TOOLS = [
    dict(tool_id="query_metrics.v1", name="query_metrics", domain="observability",
         description="查询服务指标 RED", capability="observability.metrics.read",
         required_capability="observability.metrics.read", read_only=True,
         baseline_risk="R0", risk_level="R0", backend="query-api", category="query"),
    dict(tool_id="query_logs.v1", name="query_logs", domain="observability",
         description="查询服务日志", capability="observability.logs.read",
         required_capability="observability.logs.read", read_only=True,
         baseline_risk="R0", risk_level="R0", backend="query-api", category="query"),
    dict(tool_id="query_traces.v1", name="query_traces", domain="observability",
         description="查询调用链追踪", capability="observability.traces.read",
         required_capability="observability.traces.read", read_only=True,
         baseline_risk="R0", risk_level="R0", backend="query-api", category="query"),
    dict(tool_id="query_alerts.v1", name="query_alerts", domain="observability",
         description="查询告警", capability="observability.alerts.read",
         required_capability="observability.alerts.read", read_only=True,
         baseline_risk="R0", risk_level="R0", backend="query-api", category="query"),
    dict(tool_id="query_k8s_events.v1", name="query_k8s_events", domain="kubernetes",
         description="查询冻结时间窗内的 Kubernetes/IPMI 事件", capability="kubernetes.events.read",
         required_capability="kubernetes.events.read", read_only=True,
         baseline_risk="R0", risk_level="R0", backend="query-api", category="query"),
    dict(tool_id="query_topology.v1", name="query_topology", domain="observability",
         description="查询服务拓扑", capability="observability.topology.read",
         required_capability="observability.topology.read", read_only=True,
         baseline_risk="R0", risk_level="R0", backend="query-api", category="query"),
    dict(tool_id="query_k8s.v1", name="query_k8s", domain="kubernetes",
         description="查询 Kubernetes 资源", capability="kubernetes.resources.read",
         required_capability="kubernetes.resources.read", read_only=True,
         baseline_risk="R0", risk_level="R0", backend="query-api", category="query"),
    dict(tool_id="k8sgpt_diagnose.v1", name="k8sgpt_diagnose", domain="kubernetes",
         description="K8sGPT 诊断", capability="kubernetes.resources.read",
         required_capability="kubernetes.resources.read", read_only=True,
         baseline_risk="R1", risk_level="R1", backend="k8sgpt", category="diagnostic"),
    dict(tool_id="knowledge_search.v1", name="knowledge_search", domain="knowledge",
         description="检索运维知识库", capability="knowledge.search",
         required_capability="knowledge.search", read_only=True,
         baseline_risk="R0", risk_level="R0", backend="knowledge", category="query"),
    dict(tool_id="query_changes.v1", name="query_changes", domain="changes",
         description="查询变更记录", capability="changes.read",
         required_capability="changes.read", read_only=True,
         baseline_risk="R0", risk_level="R0", backend="query-api", category="query"),
    dict(tool_id="query_graph.v1", name="query_graph", domain="knowledge",
         description="查询版本化知识图谱实体、邻居、路径和影响面", capability="knowledge.graph.read",
         required_capability="knowledge.graph.read", read_only=True,
         baseline_risk="R0", risk_level="R0", backend="query-api", category="query"),
    dict(tool_id="query_middleware.v1", name="query_middleware", domain="observability",
         description="查询中间件关系与状态", capability="observability.topology.read",
         required_capability="observability.topology.read", read_only=True,
         baseline_risk="R0", risk_level="R0", backend="query-api", category="query"),
    dict(tool_id="query_kubevirt.v1", name="query_kubevirt", domain="kubernetes",
         description="查询 KubeVirt VM、VMI 与迁移事实", capability="kubevirt.resources.read",
         required_capability="kubevirt.resources.read", read_only=True,
         baseline_risk="R0", risk_level="R0", backend="query-api", category="query"),
    dict(tool_id="query_hardware_inventory.v1", name="query_hardware_inventory", domain="hardware",
         description="查询物理服务器和硬件部件身份", capability="hardware.inventory.read",
         required_capability="hardware.inventory.read", read_only=True,
         baseline_risk="R0", risk_level="R0", backend="query-api", category="query"),
    dict(tool_id="query_hardware_health.v1", name="query_hardware_health", domain="hardware",
         description="查询硬件传感器健康与 SEL 事件", capability="hardware.health.read",
         required_capability="hardware.health.read", read_only=True,
         baseline_risk="R0", risk_level="R0", backend="query-api", category="query"),
    dict(tool_id="query_business_catalog.v1", name="query_business_catalog", domain="catalog",
         description="查询 MySQL 权威业务目录", capability="catalog.read",
         required_capability="catalog.read", read_only=True,
         baseline_risk="R0", risk_level="R0", backend="query-api", category="query"),
    dict(tool_id="query_network_topology.v1", name="query_network_topology", domain="network",
         description="查询网络、NAD、交换机和端口关系", capability="network.topology.read",
         required_capability="network.topology.read", read_only=True,
         baseline_risk="R0", risk_level="R0", backend="query-api", category="query"),
]

# 执行类 Tool（§30，仅注册，Phase7 禁用）
_EXECUTE_TOOLS = [
    dict(tool_id="execute_k8s.v1", name="execute_k8s", domain="execution",
         description="执行 Kubernetes 操作（Phase7 仅注册，禁用）", capability="execution.k8s",
         required_capability="execution.k8s", read_only=False,
         baseline_risk="R4", risk_level="R4", backend="query-api", category="execution",
         planner_selectable=False, automatic=False, execution_state="disabled"),
    dict(tool_id="execute_shell.v1", name="execute_shell", domain="execution",
         description="执行 shell（Phase7 仅注册，禁用）", capability="execution.shell",
         required_capability="execution.shell", read_only=False,
         baseline_risk="R4", risk_level="R4", backend="none", category="execution",
         planner_selectable=False, automatic=False, execution_state="disabled"),
]

REGISTRY_TOOL_IDS = [t["tool_id"] for t in (_READONLY_TOOLS + _EXECUTE_TOOLS)]


def init_default_tool_registry() -> None:
    """幂等注册最低生产 Tool 清单（§30）。"""
    if ToolRegistry.list_all():
        return
    for spec in _READONLY_TOOLS + _EXECUTE_TOOLS:
        full = dict(spec)
        full.setdefault("version", "1.0.0")
        full.setdefault("contract_version", CONTRACT_VERSION)
        full.setdefault("owner", "query-api")
        full.setdefault("evidence_required", True)
        full.setdefault("availability", "per-cluster")
        full.setdefault("allowed_scope", "cluster")
        full.setdefault("input_schema", {})
        full.setdefault("output_schema", {})
        full.setdefault("timeout_class", "fast")
        full.setdefault("timeout", 30)
        full.setdefault("retry", 0)
        full.setdefault("planner_selectable", True)
        full.setdefault("automatic", True)
        full.setdefault("lifecycle_status", "active")
        ToolRegistry.register(ToolDefinition(**full))
