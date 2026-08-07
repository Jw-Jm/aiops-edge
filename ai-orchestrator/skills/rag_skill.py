"""Skill: rag_cases — 历史案例检索与反馈闭环（ChromaDB RAG）"""
from skill_registry import SkillDef, SkillRegistry, ToolRegistry


def _case_search(query: str = "", limit: int = 5):
    """检索相似历史案例"""
    try:
        from rag import rag
        cases = rag.search(query, limit=limit)
        if not cases:
            return "未检索到相似案例"
        lines = []
        for c in cases:
            lines.append(f"- [{c['outcome']}] {c['service']}: {c['symptom'][:80]} | 根因:{c['root_cause'][:60]} | 方案:{c['plan'][:60]}")
        return "\n".join(lines)
    except Exception as e:
        return f"案例检索失败: {e}"


def _case_feedback(case_id: str, outcome: str = "success"):
    """人工反馈案例有效性"""
    try:
        from rag import rag
        rag.validate_case(case_id, outcome)
        return f"案例 {case_id} 反馈已记录 (outcome={outcome})"
    except Exception as e:
        return f"反馈失败: {e}"


def register_rag_skill():
    if not ToolRegistry.get("case_search"):
        ToolRegistry.register(name="case_search",
                              description="检索相似历史运维案例（含根因/方案/处理结果）",
                              category="knowledge",
                              params={"query": {"type": "string", "required": False, "default": "", "desc": "检索关键词"},
                                      "limit": {"type": "int", "required": False, "default": 5, "desc": "返回条数"}})(_case_search)
    if not ToolRegistry.get("case_feedback"):
        ToolRegistry.register(name="case_feedback",
                              description="对历史案例提交有效性反馈 (success/failed)，用于案例权重调整",
                              category="knowledge",
                              params={"case_id": {"type": "string", "required": True, "default": "", "desc": "案例 ID"},
                                      "outcome": {"type": "string", "required": False, "default": "success", "desc": "反馈结果(success/failed)"}})(_case_feedback)

    SkillRegistry.register(SkillDef(
        name="skill.rag_cases",
        title="历史案例检索",
        description="从历史运维案例库中检索相似故障的处理经验（症状/根因/方案/结果），支持反馈闭环优化案例权重",
        intent_keywords=["案例", "历史", "经验", "曾经", "相似问题", "之前", "案例库", "反馈"],
        tools=["case_search", "case_feedback"],
        system_prompt=(
            "你擅长利用历史运维案例经验。基于检索到的相似案例（含历史根因、处理方案、结果），"
            "参考过往成功经验给出当前问题的处理建议。"
        ),
    ))
