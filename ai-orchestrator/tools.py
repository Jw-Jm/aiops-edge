"""Observability tools for AI agents"""
import json
import os
import subprocess
import urllib.request
import urllib.error

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


def query_metrics(service: str, tenant_id: str = "default") -> str:
    if not service:
        return "未指定服务名称"
    data = _get_json(f"{QUERY_API}/services/{service}")
    if isinstance(data, dict) and "error" in data:
        return f"查询失败: {data['error']}"
    return json.dumps(data, indent=2, ensure_ascii=False)[:5000]


def query_traces(service: str = "", tenant_id: str = "default") -> str:
    url = f"{QUERY_API}/traces?limit=5"
    data = _get_json(url)
    if isinstance(data, dict) and "error" in data:
        return f"查询失败: {data['error']}"
    return json.dumps(data, indent=2, ensure_ascii=False)[:4000]


def query_topology(tenant_id: str = "default") -> str:
    data = _get_json(f"{QUERY_API}/topology/global")
    if isinstance(data, dict) and "error" in data:
        return f"查询失败: {data['error']}"
    return json.dumps(data, indent=2, ensure_ascii=False)[:3000]


def get_service_list(tenant_id: str = "default") -> str:
    data = _get_json(f"{QUERY_API}/services")
    if isinstance(data, dict) and "error" in data:
        return f"查询失败: {data['error']}"
    if isinstance(data, dict) and "data" in data:
        data = data["data"]
    if isinstance(data, list):
        summary = [{"service_name": s.get("service_name"), "traces": s.get("traces", 0),
                    "avg_ms": round(float(s.get("avg_ms", 0)), 1),
                    "max_ms": round(float(s.get("max_ms", 0)), 1)}
                   for s in data[:10]]
        return json.dumps(summary, indent=2, ensure_ascii=False)
    return json.dumps(data, indent=2, ensure_ascii=False)[:4000]


def execute_shell(command: str, timeout: int = 30) -> str:
    import shlex
    from shell_policy import ShellPolicy
    policy = ShellPolicy()
    reject = policy.check(command)
    if reject:
        return f"命令被安全策略拒绝: {reject}"
    try:
        # 安全: 使用 shlex.split() + shell=False 防止命令注入
        args = shlex.split(command)
        result = subprocess.run(args, shell=False, capture_output=True, text=True, timeout=timeout)
        output = result.stdout[:2000]
        if result.stderr:
            output += "\n[stderr]: " + result.stderr[:500]
        return output or "(no output)"
    except subprocess.TimeoutExpired:
        return f"命令超时 (>{timeout}s)"
    except ValueError as e:
        return f"命令解析失败: {str(e)}"
    except Exception as e:
        return f"执行失败: {str(e)}"


def k8sgpt_diagnose(namespace: str = "observability") -> str:
    try:
        result = subprocess.run(
            ["k8sgpt", "analyze", "--explain", "-n", namespace, "-o", "text"],
            capture_output=True, text=True, timeout=60
        )
        if result.returncode == 0 and result.stdout.strip():
            return result.stdout[:3000]
        if result.stderr:
            return f"K8sGPT: {result.stderr[:500]}"
        return "未发现集群问题"
    except FileNotFoundError:
        return "K8sGPT 未安装"
    except subprocess.TimeoutExpired:
        return "K8sGPT 诊断超时"
    except Exception as e:
        return f"K8sGPT 诊断失败: {str(e)}"


def deepflow_status() -> str:
    data = _get_json(f"{QUERY_API}/deepflow/status")
    return json.dumps(data, indent=2, ensure_ascii=False)


def get_infrastructure() -> str:
    """获取K8s基础设施信息"""
    pods_data = _get_json(f"{QUERY_API}/infrastructure/pods?namespace=observability")
    nodes_data = _get_json(f"{QUERY_API}/infrastructure/nodes")

    pods = pods_data.get("pods", [])
    nodes = nodes_data.get("nodes", [])

    report = f"运行中 Pods: {len(pods)} 个\n"
    # 展示全部 Pod，避免总数与列表数量不一致导致误判
    infos = [(p.get('name','?')[:50], p.get('status','?')) for p in pods]
    for name, st in infos:
        report += f"  - {name}: {st}\n"

    report += f"\n- 节点: {len(nodes)} 个\n"
    for n in nodes:
        report += f"  - {n.get('name','?')}: {n.get('status','?')} CPU={n.get('cpu','?')} MEM={n.get('memory','?')}\n"
    return report
