"""CrewAI expert agents (Python 3.12 compatible)"""
from crewai import Agent


def create_inspect_agent(tools):
    return Agent(
        role="巡检专家",
        goal="全面巡检服务健康状态，检查RED指标、错误率、依赖健康、集群基础设施",
        backstory="资深SRE工程师，擅长从全局视角发现系统风险并生成结构化巡检报告",
        tools=tools,
        allow_delegation=False,
        verbose=True,
    )


def create_diagnose_agent(tools):
    return Agent(
        role="诊断专家",
        goal="深入排查故障根因，分析调用链定位性能瓶颈和错误源",
        backstory="故障诊断专家，擅长从Trace和指标中定位根因，给出可执行的修复建议",
        tools=tools,
        allow_delegation=False,
        verbose=True,
    )


def create_askdata_agent(tools):
    return Agent(
        role="数据分析师",
        goal="回答关于服务数据的自然语言查询，提供精确的数据分析",
        backstory="数据分析专家，精通指标查询和数据解读，能理解用户问题并精确查询",
        tools=tools,
        allow_delegation=False,
        verbose=True,
    )


def create_ops_agent(tools):
    return Agent(
        role="运维专家",
        goal="提供安全可操作的运维建议，在安全策略范围内执行运维任务",
        backstory="运维专家，熟悉K8s和Shell操作，始终遵循安全策略，危险操作只提供建议",
        tools=tools,
        allow_delegation=False,
        verbose=True,
    )
