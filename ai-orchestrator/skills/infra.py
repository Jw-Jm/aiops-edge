"""Skill: infra — K8s 基础设施与网络可观测性（节点/Pod/Deployment/DeepFlow/K8sGPT）"""
from skill_registry import SkillDef, SkillRegistry, ToolRegistry


def register_infra_skill():
    try:
        from tools import get_infrastructure, deepflow_status, k8sgpt_diagnose
        if not ToolRegistry.get("get_infrastructure"):
            ToolRegistry.register(name="get_infrastructure",
                                  description="获取 K8s 基础设施（节点/Pod/Deployment）",
                                  category="infra",
                                  params={})(get_infrastructure)
        if not ToolRegistry.get("deepflow_status"):
            ToolRegistry.register(name="deepflow_status",
                                  description="检查 DeepFlow eBPF 采集状态",
                                  category="infra",
                                  params={})(deepflow_status)
        if not ToolRegistry.get("k8sgpt_diagnose"):
            ToolRegistry.register(name="k8sgpt_diagnose",
                                  description="使用 K8sGPT 诊断集群问题",
                                  category="k8s",
                                  params={"namespace": {"type": "string", "required": False, "default": "observability", "desc": "命名空间"}})(k8sgpt_diagnose)
    except Exception as e:
        print(f"[skills.infra] 工具注册失败: {e}")

    SkillRegistry.register(SkillDef(
        name="skill.infra",
        title="基础设施巡检",
        description="巡检 K8s 集群基础设施（节点/Pod/Deployment 状态）、DeepFlow eBPF 网络可观测性、K8sGPT 集群问题诊断",
        intent_keywords=["节点", "pod", "deployment", "基础设施", "集群", "k8s", "网络", "deepflow", "资源", "namespace"],
        tools=["get_infrastructure", "deepflow_status", "k8sgpt_diagnose"],
        system_prompt=(
            "你擅长 K8s 基础设施与网络巡检。基于已采集的节点/Pod/Deployment 状态、DeepFlow 网络数据、"
            "K8sGPT 诊断结果进行分析，直接给出基础设施健康结论和风险点，不要输出调用工具的步骤。"
        ),
    ))
