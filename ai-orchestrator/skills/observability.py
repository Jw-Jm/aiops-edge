"""Skill: observability — 可观测性数据查询与分析（指标/链路/拓扑/服务）"""
from skill_registry import SkillDef, SkillRegistry, ToolRegistry


def register_observability_skill():
    # 注册工具（如未注册）
    try:
        from tools import query_metrics, query_traces, query_topology, get_service_list
        if not ToolRegistry.get("query_metrics"):
            ToolRegistry.register(name="query_metrics",
                                  description="查询服务 RED 指标（请求量/错误率/延迟）",
                                  category="metrics",
                                  params={"service": {"type": "string", "required": True, "default": "", "desc": "服务名"}})(query_metrics)
        if not ToolRegistry.get("query_traces"):
            ToolRegistry.register(name="query_traces",
                                  description="查询 Trace 调用链数据",
                                  category="trace",
                                  params={"service": {"type": "string", "required": False, "default": "", "desc": "服务名（空为全部）"},
                                          "limit": {"type": "int", "required": False, "default": 5, "desc": "返回条数"}})(query_traces)
        if not ToolRegistry.get("query_topology"):
            ToolRegistry.register(name="query_topology",
                                  description="查询全局服务拓扑图",
                                  category="trace")(query_topology)
        if not ToolRegistry.get("get_service_list"):
            ToolRegistry.register(name="get_service_list",
                                  description="获取所有服务列表及概览（含调用量/延迟/错误率）",
                                  category="infra")(get_service_list)
    except Exception as e:
        print(f"[skills.observability] 工具注册失败: {e}")

    SkillRegistry.register(SkillDef(
        name="skill.observability",
        title="可观测性分析",
        description="查询和分析服务 RED 指标（请求量/错误率/延迟）、Trace 调用链、全局服务拓扑与列表",
        intent_keywords=["指标", "延迟", "错误率", "调用量", "链路", "trace", "拓扑", "服务", "请求量", "qps", "red"],
        tools=["query_metrics", "query_traces", "query_topology", "get_service_list"],
        system_prompt=(
            "你擅长可观测性数据分析。基于已采集的 RED 指标、Trace 调用链、拓扑关系进行分析，"
            "直接给出数据解读和结论，不要输出调用工具的步骤。"
        ),
    ))
