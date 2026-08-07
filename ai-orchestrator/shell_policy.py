"""Shell Command Security Policy - Apache 2.0"""
import re
from dataclasses import dataclass
from typing import Optional, List

@dataclass
class PolicyRule:
    pattern: str
    category: str
    description: str

BUILTIN_RULES = [
    PolicyRule(r"rm\s+(-[rRf]+\s+)*[/~]", "file-removal", "禁止递归强制删除系统路径"),
    PolicyRule(r"\bdd\b.*\bof=/dev/", "disk", "禁止dd写入裸盘设备"),
    PolicyRule(r"\b(shutdown|reboot|halt)\b", "power", "禁止关机/重启"),
    PolicyRule(r"\bsudo\b", "privilege", "禁止提权操作"),
    PolicyRule(r"chmod\s+.*777", "permission", "禁止chmod 777"),
    PolicyRule(r"/etc/(shadow|passwd)", "credential", "禁止读取凭据文件"),
    PolicyRule(r"\b(mkfs|fdisk|parted)\b", "disk", "禁止磁盘格式化"),
    PolicyRule(r"curl\s+.*\|\s*(ba)?sh", "rce", "禁止网络脚本管道到Shell"),
    PolicyRule(r"\bkill\s+-9\s+-1\b", "process", "禁止杀死所有进程"),
    PolicyRule(r"\biptables\s+-F\b", "firewall", "禁止清空防火墙规则"),
    PolicyRule(r":\(\)\s*\{\s*:\|:&\s*\}\s*;:", "fork-bomb", "禁止fork bomb"),
    PolicyRule(r"\b(wget|curl)\s+.*\s*-O\s+/etc/", "rce", "禁止下载文件到系统目录"),
]

class ShellPolicy:
    def __init__(self, extra_patterns: Optional[List] = None):
        self.rules = list(BUILTIN_RULES)
        if extra_patterns:
            for p in extra_patterns:
                self.rules.append(PolicyRule(p, "custom", "用户自定义"))

    def check(self, command: str) -> Optional[str]:
        for rule in self.rules:
            if re.search(rule.pattern, command, re.IGNORECASE):
                return f"命令被拒绝: [{rule.category}] {rule.description}"
        return None

    # ═════════════════════════════════════════════════════════
    #  Execute whitelist (K8s operations — require approval)
    # ═════════════════════════════════════════════════════════
    EXEC_READONLY = [
        r"kubectl get pods", r"kubectl describe pod", r"kubectl logs",
        r"kubectl get events", r"kubectl top pod", r"kubectl top node",
        r"kubectl get nodes", r"kubectl describe node",
        r"kubectl get deployments", r"kubectl get services",
        r"kubectl get hpa", r"kubectl get configmaps",
        r"kubectl api-resources",
        # KubeVirt 只读操作
        r"kubectl get vm", r"kubectl get vmi", r"kubectl get virtualmachine",
        r"kubectl describe vm", r"kubectl describe vmi",
        r"kubectl get vmrestore", r"kubectl get vmsnapshot",
        r"virtctl version", r"virtctl vnc \S+", r"virtctl console \S+",
    ]
    EXEC_WRITE = [
        r"kubectl rollout restart deployment/\S+",
        r"kubectl scale deployment/\S+ --replicas=\d+",
        r"kubectl rollout undo deployment/\S+",
        r"kubectl delete pod \S+ --grace-period=\d+",
        r"kubectl exec \S+ -- ",
        # KubeVirt VM 操作白名单 (需人工审批)
        r"virtctl restart \S+",
        r"virtctl stop \S+",
        r"virtctl start \S+",
        r"virtctl migrate \S+",
        r"kubectl patch vm \S+",
    ]

    def is_whitelisted_for_execute(self, command: str) -> tuple:
        """Returns (allowed: bool, category: str)."""
        for pattern in self.EXEC_READONLY:
            if re.search(pattern, command):
                return (True, "readonly")
        for pattern in self.EXEC_WRITE:
            if re.search(pattern, command):
                return (True, "write")
        return (False, "not_whitelisted")
