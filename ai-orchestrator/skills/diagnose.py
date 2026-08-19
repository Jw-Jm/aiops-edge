"""Skill: diagnose — 服务诊断（主动探测服务/查看日志）

提供 probe_http / probe_tcp / read_journal / tail_file 四个诊断工具，
让 AI 能主动验证服务可用性、端口连通性与查看系统/应用日志。

安全约束（防止 SSRF / 敏感文件读取）：
- tail_file：可读路径限定在 SAFE_LOG_DIRS 白名单内，禁止系统敏感路径。
- probe_http：仅允许 http/https，禁止内网/链路本地/元数据地址。
- probe_tcp：禁止内网/链路本地地址（防内网端口扫描）。
"""
import ipaddress
import os
import socket
import subprocess
import urllib.parse
import urllib.request

from skill_registry import SkillDef, SkillRegistry, ToolRegistry

# 可读日志目录白名单（env 可配置，换环境无需改代码）。生产默认允许 /var/log、/tmp。
SAFE_LOG_DIRS = [d for d in os.environ.get("SAFE_LOG_DIRS", "/var/log,/tmp").split(",") if d]

# 禁止探测的内网/链路本地网段（防 SSRF 与内网扫描）
_BLOCKED_NETWORKS = [
    ipaddress.ip_network("169.254.169.254/32"),  # 云元数据
    ipaddress.ip_network("169.254.0.0/16"),      # 链路本地
    ipaddress.ip_network("10.0.0.0/8"),          # 内网
    ipaddress.ip_network("172.16.0.0/12"),       # 内网
    ipaddress.ip_network("192.168.0.0/16"),      # 内网
    ipaddress.ip_network("127.0.0.0/8"),         # loopback
    ipaddress.ip_network("0.0.0.0/8"),
    ipaddress.ip_network("100.64.0.0/10"),       # CGNAT
    ipaddress.ip_network("::1/128"),
    ipaddress.ip_network("fc00::/7"),            # ULA
    ipaddress.ip_network("fe80::/10"),           # 链路本地 IPv6
]


def _is_blocked_host(host: str) -> bool:
    """判断 host 是否解析到被禁止的地址（内网/链路本地/元数据）。"""
    try:
        ips = socket.getaddrinfo(host, None)
    except Exception:
        return True  # 解析失败视为不可信
    for info in ips:
        ip = info[4][0]
        try:
            addr = ipaddress.ip_address(ip)
        except ValueError:
            continue
        for net in _BLOCKED_NETWORKS:
            if addr in net:
                return True
    return False


def probe_http(url: str = "", timeout: int = 5):
    """HTTP 探测：请求指定 URL，返回状态码与耗时（服务可达性诊断）。
    仅允许 http/https，且禁止内网/链路本地/元数据地址（防 SSRF）。"""
    if not url:
        return "缺少 url 参数（如 http://query-api:8080/health）"
    try:
        parsed = urllib.parse.urlparse(url)
        if parsed.scheme not in ("http", "https"):
            return "仅允许 http/https 协议"
        if _is_blocked_host(parsed.hostname or ""):
            return "目标地址被安全策略禁止（内网/链路本地/云元数据）"
        start = __import__("time").time()
        # This is a generic public probe, not a query-api authority path. Never
        # attach tenant, service, or signed-context credentials to its target.
        req = urllib.request.Request(url)
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            body = resp.read(200).decode(errors="replace")
            cost_ms = int((__import__("time").time() - start) * 1000)
            return f"HTTP {resp.status}，耗时 {cost_ms}ms，响应: {body[:200]}"
    except urllib.error.HTTPError as e:
        return f"HTTP 错误 {e.code}: {e.reason}"
    except Exception as e:
        return f"连接失败: {e}"


def probe_tcp(host: str = "", port: int = 80, timeout: int = 3):
    """TCP 端口探测：检测主机端口是否可达（连通性诊断）。
    禁止内网/链路本地/元数据地址（防内网端口扫描）。"""
    if not host:
        return "缺少 host 参数"
    if _is_blocked_host(host):
        return "目标地址被安全策略禁止（内网/链路本地/云元数据）"
    try:
        sock = socket.create_connection((host, port), timeout=timeout)
        sock.close()
        return f"TCP {host}:{port} 可达"
    except Exception as e:
        return f"TCP {host}:{port} 不可达: {e}"


def read_journal(service: str = "", lines: int = 50):
    """读取 systemd journal 日志（服务单元日志，用于查看最近输出/错误）"""
    if not service:
        return "缺少 service 参数（systemd 单元名）"
    try:
        out = subprocess.run(
            ["journalctl", "-u", service, "-n", str(lines), "--no-pager"],
            capture_output=True, text=True, timeout=10,
        ).stdout
        return out if out.strip() else f"journalctl 无输出（服务 {service} 可能无日志或无权限）"
    except Exception as e:
        return f"读取失败: {e}"


def tail_file(path: str = "", lines: int = 50):
    """查看文件末尾 N 行（应用日志文件诊断）。
    安全：仅允许读取 SAFE_LOG_DIRS 白名单内的日志目录，禁止系统敏感路径。"""
    if not path:
        return "缺少 path 参数"
    try:
        real = os.path.realpath(path)
        allowed = any(
            os.path.commonpath([real, os.path.realpath(d)]) == os.path.realpath(d)
            for d in SAFE_LOG_DIRS
        ) if SAFE_LOG_DIRS else False
        if not allowed:
            return f"拒绝读取：路径不在可读日志目录白名单内（{SAFE_LOG_DIRS or '未配置'}）"
        out = subprocess.run(
            ["tail", "-n", str(lines), real],
            capture_output=True, text=True, timeout=10,
        )
        if out.returncode != 0:
            return f"读取失败: {out.stderr.strip()}"
        return out.stdout if out.stdout.strip() else f"{real} 为空"
    except Exception as e:
        return f"读取失败: {e}"


def register_diagnose_skill():
    if not ToolRegistry.get("probe_http"):
        ToolRegistry.register(name="probe_http",
                              description="HTTP 探测：请求 URL 验证服务可用性，返回状态码与耗时",
                              category="diagnose",
                              params={"url": {"type": "str", "required": True, "desc": "目标 URL"},
                                      "timeout": {"type": "int", "required": False, "default": 5, "desc": "超时秒"}})(probe_http)
    if not ToolRegistry.get("probe_tcp"):
        ToolRegistry.register(name="probe_tcp",
                              description="TCP 端口探测：检测主机端口是否可达",
                              category="diagnose",
                              params={"host": {"type": "str", "required": True, "desc": "主机名/IP"},
                                      "port": {"type": "int", "required": False, "default": 80, "desc": "端口"},
                                      "timeout": {"type": "int", "required": False, "default": 3, "desc": "超时秒"}})(probe_tcp)
    if not ToolRegistry.get("read_journal"):
        ToolRegistry.register(name="read_journal",
                              description="读取 systemd journal 服务日志（最近 N 行）",
                              category="diagnose",
                              params={"service": {"type": "str", "required": True, "desc": "systemd 单元名"},
                                      "lines": {"type": "int", "required": False, "default": 50, "desc": "行数"}})(read_journal)
    if not ToolRegistry.get("tail_file"):
        ToolRegistry.register(name="tail_file",
                              description="查看文件末尾 N 行（应用日志文件）",
                              category="diagnose",
                              params={"path": {"type": "str", "required": True, "desc": "文件路径"},
                                      "lines": {"type": "int", "required": False, "default": 50, "desc": "行数"}})(tail_file)

    SkillRegistry.register(SkillDef(
        name="skill.diagnose",
        title="服务诊断",
        description="主动探测服务可用性（HTTP/TCP）、查看系统与应用日志，定位服务故障",
        intent_keywords=["诊断", "探测", "连通", "端口", "日志", "服务状态", "probe", "journal", "tail"],
        tools=["probe_http", "probe_tcp", "read_journal", "tail_file"],
        system_prompt=(
            "你擅长服务诊断。通过 HTTP/TCP 探测验证服务可用性，通过 journalctl/tail 查看日志定位故障根因。"
            "诊断时先探测可用性，再查日志定位具体错误，直接输出结论。"
        ),
    ))
