"""专家定义 — 通过组合多个 Skill 构建专家能力"""
from skill_registry import ExpertRegistry


def register_experts():
    """注册专家（组合多个标准 Skill）"""
    # 巡检专家：全面健康巡检 = 可观测性 + 基础设施 + 告警
    ExpertRegistry.register(
        name="inspection", role="巡检专家",
        goal="全面巡检服务健康状态，检查 RED 指标、错误率、依赖健康、集群基础设施与告警态势",
        backstory="资深SRE工程师，擅长从全局视角发现系统风险并生成结构化巡检报告",
        intent_keywords=["巡检", "检查", "健康", "状态", "扫描", "inspection"],
        skills=["skill.observability", "skill.infra", "skill.alert_ops"],
        tools=[],
    )
    # 诊断专家：故障根因 = 可观测性 + RCA + 案例
    ExpertRegistry.register(
        name="diagnosis", role="诊断专家",
        goal="深入排查故障根因，结合指标/链路/RCA引擎/历史案例定位性能瓶颈和错误源",
        backstory="故障诊断专家，擅长从 Trace 和指标中定位根因，给出可执行的修复建议",
        intent_keywords=["诊断", "慢了", "报错", "错误", "异常", "故障", "根因", "为什么", "原因", "diagnos"],
        skills=["skill.observability", "skill.rca", "skill.rag_cases"],
        tools=[],
    )
    # 运维专家：操作执行 = 可观测性 + 基础设施 + 自动化 + 虚拟机
    ExpertRegistry.register(
        name="ops", role="运维专家",
        goal="提供安全可操作的运维建议，在安全策略和人工审批范围内执行运维任务（含 K8s 与 KubeVirt VM）",
        backstory="运维专家，熟悉 K8s/Shell/KubeVirt 操作，始终遵循安全策略，危险操作需审批",
        intent_keywords=["重启", "扩容", "缩容", "部署", "回滚", "执行", "操作", "运维", "restart", "scale"],
        skills=["skill.observability", "skill.infra", "skill.automation", "skill.vm_ops"],
        tools=[],
    )
    # 问数专家：数据分析 = 可观测性 + 告警 + 案例
    ExpertRegistry.register(
        name="query", role="数据分析师",
        goal="回答关于服务数据的自然语言查询，提供精确的数据分析与趋势洞察",
        backstory="数据分析专家，精通指标查询和数据解读，能理解用户问题并精确查询",
        intent_keywords=["查询", "数据", "统计", "排名", "多少", "分析", "query"],
        skills=["skill.observability", "skill.alert_ops", "skill.rag_cases"],
        tools=[],
    )
