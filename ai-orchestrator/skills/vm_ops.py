"""Skill: vm_ops — KubeVirt 虚拟机运维（状态查询 + 白名单操作）"""
import json
import os
import subprocess
import urllib.request
import urllib.error

from skill_registry import SkillDef, SkillRegistry, ToolRegistry

QUERY_API = os.environ.get("QUERY_API_URL", "http://query-api.observability.svc.cluster.local:8080/api/v1")
INTERNAL_TOKEN = os.environ.get("INTERNAL_TOKEN", "")


def _api_headers():
    h = {"X-Tenant-ID": "default"}
    if INTERNAL_TOKEN:
        h["X-Internal-Token"] = INTERNAL_TOKEN
    return h


def _get_json(url: str) -> dict:
    req = urllib.request.Request(url, headers=_api_headers())
    try:
        with urllib.request.urlopen(req, timeout=10) as resp:
            return json.loads(resp.read())
    except (urllib.error.URLError, json.JSONDecodeError) as e:
        return {"error": str(e)}


def vm_list():
    """列出所有 KubeVirt 虚拟机及其状态"""
    try:
        data = _get_json(f"{QUERY_API}/infrastructure/pods?namespace=&labelSelector=kubevirt.io/vm")
        if isinstance(data, dict) and data.get("error"):
            return f"查询失败: {data['error']}"
        pods = data.get("pods", [])
        if not pods:
            return "未发现 KubeVirt 虚拟机"
        lines = ["KubeVirt 虚拟机列表:"]
        for p in pods:
            lines.append(f"- {p.get('name','?')}: 状态={p.get('status','?')} 命名空间={p.get('namespace','?')}")
        return "\n".join(lines)
    except Exception as e:
        return f"查询 VM 列表失败: {e}"


def vm_status(vm_name: str):
    """查询单个虚拟机详细状态"""
    if not vm_name:
        return "请指定虚拟机名称"
    try:
        data = _get_json(f"{QUERY_API}/infrastructure/pods?namespace=&labelSelector=kubevirt.io/vm")
        pods = data.get("pods", [])
        for p in pods:
            if vm_name in p.get("name", ""):
                return json.dumps(p, ensure_ascii=False, indent=2)[:1000]
        return f"未找到虚拟机: {vm_name}"
    except Exception as e:
        return f"查询失败: {e}"


def vm_operate(action: str, vm_name: str):
    """执行虚拟机操作（需人工审批），action: restart/start/stop/migrate"""
    import shlex
    action = (action or "").lower()
    if action not in ("restart", "start", "stop", "migrate"):
        return f"不支持的操作: {action} (支持 restart/start/stop/migrate)"
    if not vm_name:
        return "请指定虚拟机名称"
    try:
        from shell_policy import ShellPolicy
        cmd = f"virtctl {action} {vm_name}"
        policy = ShellPolicy()
        reject = policy.check(cmd)
        if reject:
            return f"操作被安全策略拒绝: {reject}"
        r = subprocess.run(shlex.split(cmd), shell=False, capture_output=True, text=True, timeout=30)
        if r.returncode == 0:
            return f"虚拟机 {vm_name} {action} 已执行: {r.stdout[:200]}"
        return f"操作失败: {r.stderr[:300]}"
    except FileNotFoundError:
        return "virtctl 未安装"
    except subprocess.TimeoutExpired:
        return "操作超时"
    except Exception as e:
        return f"执行失败: {e}"


def register_vm_skill():
    if not ToolRegistry.get("vm_list"):
        ToolRegistry.register(name="vm_list",
                              description="列出所有 KubeVirt 虚拟机及其运行状态",
                              category="vm",
                              params={})(vm_list)
    if not ToolRegistry.get("vm_status"):
        ToolRegistry.register(name="vm_status",
                              description="查询单个 KubeVirt 虚拟机详细状态",
                              category="vm",
                              params={"vm_name": {"type": "string", "required": True, "default": "", "desc": "虚拟机名"}})(vm_status)
    if not ToolRegistry.get("vm_operate"):
        ToolRegistry.register(name="vm_operate",
                              description="对虚拟机执行运维操作 (restart/start/stop/migrate)，需人工审批",
                              category="vm", requires_approval=True, cls_="mutating",
                              params={"action": {"type": "string", "required": True, "default": "", "desc": "操作类型(restart/start/stop/migrate)"},
                                      "vm_name": {"type": "string", "required": True, "default": "", "desc": "虚拟机名"}})(vm_operate)

    SkillRegistry.register(SkillDef(
        name="skill.vm_ops",
        title="虚拟机运维",
        description="KubeVirt 虚拟机管理：查询 VM 列表与状态，执行受限的运维操作（重启/启动/停止/迁移，需人工审批）",
        intent_keywords=["虚拟机", "vm", "kubevirt", "虚机", "virtctl", "重启虚拟机", "迁移虚拟机"],
        tools=["vm_list", "vm_status", "vm_operate"],
        system_prompt=(
            "你擅长 KubeVirt 虚拟机运维。查询 VM 状态并给出管理建议；执行 restart/start/stop/migrate 等"
            "操作会生成待审批任务，需人工确认后才真正执行。"
        ),
        trigger_actions=["restart", "start", "stop", "migrate"],
    ))
