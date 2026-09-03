"""MCP (Model Context Protocol) Server for observability tools"""
import json
import tools as agent_tools
from execution_gate import check_tool_executable
from error_safety import stable_error_code


class MCPServer:
    """Exposes observability tools as MCP-compatible endpoints"""

    def __init__(self):
        self.tools = {
            "query_metrics": {
                "name": "query_metrics",
                "description": "查询服务RED指标（请求量/错误率/延迟）",
                "parameters": {"service": "string"},
                "handler": agent_tools.query_metrics,
            },
            "query_traces": {
                "name": "query_traces",
                "description": "查询服务调用链Trace数据",
                "parameters": {"service": "string"},
                "handler": agent_tools.query_traces,
            },
            "query_topology": {
                "name": "query_topology",
                "description": "查询全局服务拓扑图",
                "parameters": {},
                "handler": agent_tools.query_topology,
            },
            "get_service_list": {
                "name": "get_service_list",
                "description": "获取所有服务的RED指标概览",
                "parameters": {},
                "handler": agent_tools.get_service_list,
            },
            "k8sgpt_diagnose": {
                "name": "k8sgpt_diagnose",
                "description": "使用K8sGPT诊断Kubernetes集群问题",
                "parameters": {"namespace": "string"},
                "handler": agent_tools.k8sgpt_diagnose,
            },
            "deepflow_status": {
                "name": "deepflow_status",
                "description": "查询DeepFlow可观测性平台状态",
                "parameters": {},
                "handler": agent_tools.deepflow_status,
            },
            "execute_shell": {
                "name": "execute_shell",
                "description": "安全执行Shell命令（受安全策略管控）",
                "parameters": {"command": "string"},
                "handler": agent_tools.execute_shell,
            },
            "get_infrastructure": {
                "name": "get_infrastructure",
                "description": "获取K8s集群基础设施信息",
                "parameters": {},
                "handler": agent_tools.get_infrastructure,
            },
        }

    def list_tools(self):
        return [
            {"name": t["name"], "description": t["description"], "parameters": t["parameters"]}
            for t in self.tools.values()
        ]

    def call_tool(self, name: str, args: dict) -> str:
        if name not in self.tools:
            return json.dumps({"error": f"Tool not found: {name}"})
        tool = self.tools[name]
        # 安全：execute_shell 是危险工具，MCP 路径必须经过 execution_gate 且已人工审批
        # （approved=True）。MCP 端点本身不承载审批能力，这里强制拒绝未审批的 shell 执行，
        # 防止通过 /api/v1/mcp/call 绕过审批闸门直接执行任意命令。
        if name == "execute_shell":
            try:
                from skill_registry import ToolRegistry, ToolDef
                td = ToolRegistry.get("execute_shell")
                allowed, reason = check_tool_executable(td, approved=False)
                if not allowed:
                    return json.dumps({"error": f"execute_shell 需要人工审批后执行: {reason}"})
            except Exception as e:
                return json.dumps({"error": "EXECUTION_GATE_UNAVAILABLE"})
        try:
            return tool["handler"](**args)
        except Exception as e:
            return json.dumps({"error": stable_error_code(
                getattr(e, "error_code", ""), "MCP_TOOL_FAILED"
            )})


mcp = MCPServer()
