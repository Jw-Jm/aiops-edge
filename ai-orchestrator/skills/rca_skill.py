"""Skill: rca — 根因分析（确定性引擎 + 假设引擎）"""
from skill_registry import SkillDef, SkillRegistry, ToolRegistry


def _rca_run(service: str = ""):
    """执行 RCA 分析（确定性 + 假设引擎）"""
    try:
        from rca import diagnose_root_cause, full_rca_analysis
        try:
            result = full_rca_analysis(service)
            mode = result.get("mode", "deterministic")
            detail = result.get("result", {})
            return f"RCA模式: {mode}\n结论: {str(detail)[:800]}"
        except Exception:
            result = diagnose_root_cause(service)
            return f"RCA(确定性): {str(result)[:800]}"
    except Exception as e:
        return f"RCA 分析失败: {e}"


def register_rca_skill():
    if not ToolRegistry.get("rca_analyze"):
        ToolRegistry.register(name="rca_analyze",
                              description="执行根因分析（确定性+假设引擎），定位故障根因",
                              category="analysis")(_rca_run)

    SkillRegistry.register(SkillDef(
        name="skill.rca",
        title="根因分析",
        description="对服务故障执行根因分析，结合确定性规则引擎与 LLM 假设引擎定位根本原因，输出证据链和置信度",
        intent_keywords=["根因", "为什么", "原因", "定位", "排查", "rca", "故障根因", "怀疑", "假设"],
        tools=["rca_analyze"],
        system_prompt=(
            "你擅长故障根因分析。基于 RCA 引擎的根因结论、证据链和置信度，"
            "给出清晰的根因判断和下一步排查方向，直接输出结论不要输出调用步骤。"
        ),
    ))
