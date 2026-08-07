"""MCP (Model Context Protocol) Server for observability tools"""
import json
import tools as agent_tools


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
        try:
            return tool["handler"](**args)
        except Exception as e:
            return json.dumps({"error": str(e)})


mcp = MCPServer()
