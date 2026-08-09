"""统一工具执行审批闸门（C1）。

按 ToolDef.cls 分级（safe/mutating/dangerous）+ requires_approval 决定工具是否可执行：
- safe 且无审批标记 → 直接执行（只读）
- mutating/dangerous 或 requires_approval → 必须人工审批（approved=True）后才可执行

供 Agent 工具调用层 / 前端执行表单在真正执行前调用，形成统一的安全闸门。
"""
from typing import Optional

from skill_registry import ToolDef

# 需要审批的分级集合
APPROVAL_REQUIRED_CLASSES = {"mutating", "dangerous"}


def check_tool_executable(tool: Optional[ToolDef], approved: bool = False):
    """返回 (allowed, reason)。

    allowed=True 表示可执行；False 表示被闸门拦截，reason 说明原因。
    """
    if tool is None:
        return False, "工具未注册或不存在"

    needs_approval = tool.requires_approval or tool.cls in APPROVAL_REQUIRED_CLASSES

    if not needs_approval:
        # safe 且无审批标记 → 直接执行
        return True, ""

    if not approved:
        if tool.cls in APPROVAL_REQUIRED_CLASSES:
            return False, f"工具 {tool.name} 分级为 {tool.cls}，必须人工审批后执行"
        return False, f"工具 {tool.name} 需要人工审批后执行"

    return True, ""
