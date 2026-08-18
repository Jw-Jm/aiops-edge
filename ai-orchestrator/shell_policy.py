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

    # Shell 拼接/重定向元字符。任何白名单命令若含这些字符，都说明它可能被
    # 用于拼接/重定向/子 shell，一律拒绝，防止 `kubectl get pods; cat /etc/shadow`
    # 这类"白名单子串 + 任意命令"的注入绕过。
    # 说明：`>`/`<` 无条件拦截（无论后接空白还是 `/` 等路径字符），杜绝
    # `kubectl get pods >/etc/shadow` 这类"白名单命令 + 重定向写任意文件"绕过。
    SHELL_METACHARS = re.compile(
        r"[;&|`]|\$\(|\b(?:&&|\|\|)\b|"
        r"[\r\n]|[<>]",
        re.IGNORECASE,
    )

    def check_shell_metachars(self, command: str) -> Optional[str]:
        """检查是否含可导致 shell 拼接/重定向的元字符。命中则返回拒绝原因，否则 None。

        安全修复(P0): 此前按"产品要求放宽"恒返回 None, 导致 `kubectl get pods; cat /etc/shadow`
        这类"白名单子串 + 任意命令"拼接绕过, 任意已登录用户可达集群 RCE。
        现在恢复拦截: 仅允许 `|`(管道, 处置命令常用 `kubectl ... | grep`), 其余
        拼接/重定向/换行/子 shell 元字符一律拒绝。人工审批不再是安全边界的替代品。
        """
        if not command:
            return None
        # 去除管道(单字符 | 两侧的空白)后检查其余元字符, 管道本身放行
        stripped = re.sub(r"\s*\|\s*", " ", command)
        m = self.SHELL_METACHARS.search(stripped)
        if m:
            return f"命令含禁止的 shell 元字符 [{m.group(0)}], 拒绝执行(防拼接/重定向注入)"
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
        # 结构化动作预检/描述: kubectl get|describe <kind>/<name> (工作流 C)
        r"kubectl (get|describe) (deployment|statefulset|daemonset|pod|node)/\S+",
        # 管道只读过滤器(kubectl ... | grep/head/tail/awk/wc/sort)
        r"^\s*(grep|egrep|head|tail|awk|wc|sort|uniq|sed)\s+",
    ]
    # kubectl exec 仅允许：命名空间限定目标 + `--` 后只读诊断命令白名单。
    # 安全修复(G5): 原 `kubectl exec \S+ -- ` 允许任意 pod 任意命令（cat /etc/shadow、
    # rm -rf / 等），且元字符检查对 `--` 后的 payload 无效。现收紧为：
    #   1) 目标必须是 `pod/名称 -n 命名空间`（拒绝集群级/无命名空间目标）；
    #   2) `--` 后的命令必须命中只读诊断命令白名单（cat/ls/ps/env/df/free/top -b -n1/
    #      curl -s/wget -qO-/grep/tail/head/date/hostname/uptime/ip addr/ss -tlnp）；
    #   3) 敏感路径（凭据/密钥）在 is_whitelisted_for_execute 内二次拦截。
    EXEC_READONLY_COMMANDS = (
        "cat", "ls", "ps", "env", "df", "free", "top -b -n1",
        "curl -s", "wget -qO-", "grep", "tail", "head", "date",
        "hostname", "uptime", "ip addr", "ss -tlnp",
    )
    _EXEC_CMD_ALT = "|".join(re.escape(c) for c in EXEC_READONLY_COMMANDS)
    # exec 内禁止读取的敏感路径（凭据/密钥/宿主信息），防 `cat /etc/shadow` 类越权读取
    _EXEC_SENSITIVE_RE = re.compile(
        r"/etc/(shadow|passwd|sudoers)|/var/run/secrets|/root/\.(ssh|aws|kube)|\.kube/config",
        re.IGNORECASE,
    )
    EXEC_WRITE = [
        r"kubectl rollout restart deployment/\S+",
        r"kubectl scale deployment/\S+ --replicas=\d+",
        r"kubectl rollout undo deployment/\S+",
        r"kubectl delete pod \S+ --grace-period=\d+",
        r"kubectl exec (pod/)?[A-Za-z0-9_.-]+ -n [A-Za-z0-9_.-]+ -- (" + _EXEC_CMD_ALT + r")(\s|$)",
        # 节点调度写操作 (工作流 C, 需人工审批)
        r"kubectl cordon node \S+",
        r"kubectl uncordon node \S+",
        r"kubectl drain node \S+",
        # KubeVirt VM 操作白名单 (需人工审批)
        r"virtctl restart \S+",
        r"virtctl stop \S+",
        r"virtctl start \S+",
        r"virtctl migrate \S+",
        r"kubectl patch vm \S+",
    ]

    def is_whitelisted_for_execute(self, command: str) -> tuple:
        """Returns (allowed: bool, category: str).

        安全修复(P0): 此前放宽为"仅首行前缀匹配"且元字符检查失效, 攻击者可
        `kubectl get pods; cat /etc/shadow` / 多行拼接绕过 → 任意命令执行。
        现在: ① 元字符拦截(见 check_shell_metachars); ② 整段命令必须命中
        EXEC_READONLY/EXEC_WRITE 之一(前缀匹配仅作兼容提示); ③ 危险参数黑名单
        (kubectl delete/exec/apply/rollout undo、curl 外联下载、systemctl stop、
        docker 写操作等)对**未命中白名单**的命令兜底拦截——白名单显式放行的受控
        形式(如 `kubectl delete pod X --grace-period=N` / `kubectl drain node X
        --ignore-daemonsets`)按白名单通过, 防止宽泛黑名单误伤受控写操作。
        保持函数签名不变。
        """
        if not command or not command.strip():
            return (False, "empty")
        # 1) 元字符硬拦截(管道放行, 其余拼接/重定向拒绝)
        meta = self.check_shell_metachars(command)
        if meta:
            return (False, "metachars")
        # 1.5) exec 命令敏感路径拦截（防 `kubectl exec ... -- cat /etc/shadow` 越权读取）
        if re.search(r"kubectl exec", command, re.IGNORECASE) and self._EXEC_SENSITIVE_RE.search(command):
            return (False, "sensitive_path")
        # 2) 危险参数黑名单(整段命令, 不限首行) — 白名单外的 kubectl 危险动词兜底
        for pattern, cat, desc in self.EXTRA_BLACKLIST:
            if re.search(pattern, command, re.IGNORECASE):
                return (False, cat)
        # 3) 白名单: 整段命令(去管道后逐段)必须整体命中只读或写规则
        segments = [s.strip() for s in re.split(r"\s*\|\s*", command) if s.strip()]
        readonly_hit = write_hit = False
        all_whitelisted = True
        for seg in segments:
            if re.search(r"kubectl exec", seg, re.IGNORECASE):
                # exec 命令必须命中收紧后的 exec 白名单（命名空间限定 + 只读诊断命令），
                # 不允许被其他只读模式（如 `kubectl get pods`）子串匹配绕过——
                # 否则 `kubectl exec pod/x -n ns -- kubectl get pods` 会绕过命令白名单。
                seg_wr = any(re.search(p, seg) for p in self.EXEC_WRITE)
                if not seg_wr:
                    all_whitelisted = False
                    break
                write_hit = True
                continue
            seg_ro = any(re.search(p, seg) for p in self.EXEC_READONLY)
            seg_wr = any(re.search(p, seg) for p in self.EXEC_WRITE)
            if seg_ro:
                readonly_hit = True
            if seg_wr:
                write_hit = True
            # 任一段既非只读也非写白名单 → 拒绝(管道内也不允许任意命令)
            if not seg_ro and not seg_wr:
                all_whitelisted = False
                break
        if all_whitelisted:
            if write_hit:
                return (True, "write")
            if readonly_hit:
                return (True, "readonly")
            return (False, "not_whitelisted")
        # 4) 未命中白名单: 危险参数黑名单兜底(整段命令, 不限首行)
        for pat in (
            r"\bkubectl\b[^\n]*\b(delete|exec|edit|apply|create|replace|patch|drain|taint|rollout\s+undo)\b",
            r"\bcurl\b[^\n]*\s(-o|--output|--data|--data-binary|-d|--upload-file)\s",
            r"\bsystemctl\s+(stop|disable|mask|restart)\b",
            r"\bdocker\s+(rm|rmi|run|exec|build|push|pull)\b",
            r"\brm\s+(-[rf]+\s*)*(/|/etc|/var|/usr|/root)",
            r"\bchmod\s+777\b|\bchown\b",
            r"\bbase64\s+-d\b",
        ):
            if re.search(pat, command, re.IGNORECASE):
                return (False, "dangerous_params")
        return (False, "not_whitelisted")
        if write_hit:
            return (True, "write")
        if readonly_hit:
            return (True, "readonly")
        return (False, "not_whitelisted")

    # ═════════════════════════════════════════════════════════
    #  Extra blacklist (G: external deploy, H: log/resource cleanup)
    #  ═════════════════════════════════════════════════════════
    EXTRA_BLACKLIST = [
        # G — 部署/拉取外部组件
        (r"\bhelm\s+(install|upgrade|create|add|repo|pull)\b", "external-deploy", "禁止 helm 部署/拉取外部组件"),
        (r"\bkubectl\s+(apply|create)\s+(-f|-k|-R)", "external-deploy", "禁止应用外部 manifest"),
        (r"curl\s+.*\|\s*kubectl\s+(apply|create)", "external-deploy", "禁止网络脚本注入 kubectl"),
        (r"\bdocker\s+(pull|run|build|push)\b", "external-deploy", "禁止拉取/构建/推送容器镜像"),
        (r"\bgit\s+clone\b", "external-deploy", "禁止克隆外部仓库"),
        # H — 日志/资源清理
        (r"\bjournalctl\s+--vacuum", "log-cleanup", "禁止日志清理"),
        (r"\brm\s+(-[rR]f\s*)+", "resource-cleanup", "禁止递归强制删除"),
        (r"\btruncate\b", "resource-cleanup", "禁止清空文件"),
        (r"\bkubectl\s+delete\s+\S+\s+--all\b", "batch-delete", "禁止批量删除资源"),
        (r"\bkubectl\s+delete\s+\S+\s+-l\b", "batch-delete", "禁止按标签批量删除资源"),
    ]

    def check_extra_blacklist(self, command: str) -> Optional[str]:
        """G/H 范围收窄：在 is_whitelisted_for_execute 放行后二次拦截。
        命中则返回拒绝原因，否则 None。"""
        for pattern, cat, desc in self.EXTRA_BLACKLIST:
            if re.search(pattern, command, re.IGNORECASE):
                return f"命令超出允许范围: [{cat}] {desc}"
        return None
