"""P8.4 Runtime Policy — V9.3 Phase8 Execution Policy Engine（执行前动态检查）。

核心原则（P8.4 v0.2）：
- 静态安全（Phase7 SecurityGate：能不能） + 动态策略（P8.4：什么条件下可以）双层。
- 5 类检查：time_window / resource_scope / action_scope / rate_limit / impact_limit。
- Policy Context 来源冻结（评审最大风险点）：
  仅 MySQL Authorization SoT + Cluster State + Execution History + Current Time 可作 context；
  禁 LLM output / Agent suggestion / Frontend parameter。
- DENY 不降级为 ALLOW；DENY 理由可审计（policy_id + reason）。
"""
from __future__ import annotations

from dataclasses import dataclass, field
from datetime import datetime, timezone
from typing import Any, Dict, List, Optional

from execution_contract import ExecutionContract

POLICY_TYPES = {"time_window", "resource_scope", "action_scope", "rate_limit", "impact_limit"}
_AUTHORITY_KEYS = {"current_time", "cluster_state", "execution_history", "authorization_sot"}
_FORBIDDEN_KEYS = {"llm_output", "agent_suggestion", "frontend_param"}


class PolicyContextInvalid(ValueError):
    def __init__(self, message: str):
        self.error_code = "POLICY_CONTEXT_INVALID"
        super().__init__(message)


@dataclass
class PolicyRule:
    policy_id: str
    policy_type: str
    allowed_values: List[Any] = field(default_factory=list)
    denied_values: List[Any] = field(default_factory=list)
    limit: int = 0
    scope: str = ""

    def __post_init__(self) -> None:
        if self.policy_type not in POLICY_TYPES:
            raise ValueError(f"非法 policy_type: {self.policy_type}")


@dataclass
class PolicyDecision:
    decision: str
    reason: str
    policy_id: str
    checked_at: datetime = field(default_factory=lambda: datetime.now(timezone.utc))


class ExecutionPolicyEngine:
    """执行前动态策略检查（内存 MVP）。DENY 不降级。"""

    def __init__(self, rules: Optional[List[PolicyRule]] = None) -> None:
        self._rules = list(rules or [])

    def add_rule(self, rule: PolicyRule) -> None:
        self._rules.append(rule)

    def evaluate(self, contract: ExecutionContract, context: Dict[str, Any]) -> PolicyDecision:
        """评估是否允许执行。context 必须来自权威来源。"""
        self._validate_context(context)
        for rule in self._rules:
            result = self._check(rule, contract, context)
            if result == "DENY":
                return PolicyDecision(decision="DENY", reason=rule.policy_id, policy_id=rule.policy_id)
        return PolicyDecision(decision="ALLOW", reason="all policies passed", policy_id="")

    def _validate_context(self, context: Dict[str, Any]) -> None:
        for key in _FORBIDDEN_KEYS:
            if key in context:
                raise PolicyContextInvalid(f"Policy Context 来源非法（禁 LLM/Agent/Frontend）: {key}")
        # context 必须含至少一个权威来源
        if not any(k in context for k in _AUTHORITY_KEYS):
            raise PolicyContextInvalid("Policy Context 缺少权威来源")

    def _check(self, rule: PolicyRule, contract: ExecutionContract, context: Dict[str, Any]) -> str:
        if rule.policy_type == "time_window":
            return self._time_window(rule, context)
        if rule.policy_type == "resource_scope":
            return self._resource_scope(rule, contract)
        if rule.policy_type == "action_scope":
            return self._action_scope(rule, contract)
        if rule.policy_type == "rate_limit":
            return self._rate_limit(rule, context)
        if rule.policy_type == "impact_limit":
            return self._impact_limit(rule, context)
        return "ALLOW"

    @staticmethod
    def _time_window(rule, context) -> str:
        now = context.get("current_time")
        if now is None:
            return "ALLOW"
        hour = now.hour
        if rule.denied_values and hour in rule.denied_values:
            return "DENY"
        if rule.allowed_values and hour not in rule.allowed_values:
            return "DENY"
        return "ALLOW"

    @staticmethod
    def _resource_scope(rule, contract) -> str:
        allowed = set(rule.allowed_values)
        if allowed and not set(contract.allowed_resources).issubset(allowed):
            return "DENY"
        return "ALLOW"

    @staticmethod
    def _action_scope(rule, contract) -> str:
        denied = set(rule.denied_values)
        if denied and set(contract.allowed_actions) & denied:
            return "DENY"
        return "ALLOW"

    @staticmethod
    def _rate_limit(rule, context) -> str:
        hist = context.get("execution_history", {})
        count = hist.get("count_1h", 0)
        if rule.limit > 0 and count > rule.limit:
            return "DENY"
        return "ALLOW"

    @staticmethod
    def _impact_limit(rule, context) -> str:
        state = context.get("cluster_state", {})
        impact = state.get("impact_pods", 0)
        if rule.limit > 0 and impact > rule.limit:
            return "DENY"
        return "ALLOW"
