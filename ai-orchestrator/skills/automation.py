"""Skill: automation — 自动化运维执行（安全 Shell/K8s 命令 + 人工审批闭环）"""
from skill_registry import SkillDef, SkillRegistry, ToolRegistry


def register_automation_skill():
    try:
        from tools import execute_shell
        if not ToolRegistry.get("execute_shell"):
            ToolRegistry.register(name="execute_shell",
                                  description="执行 Shell/K8s 命令（受安全策略管控，需人工审批）",
                                  category="automation",
                                  requires_approval=True,
                                  params={"command": {"type": "string", "required": True, "default": "", "desc": "要执行的命令"},
                                          "timeout": {"type": "int", "required": False, "default": 30, "desc": "超时秒数"}})(execute_shell)
    except Exception as e:
        print(f"[skills.automation] 工具注册失败: {e}")

    SkillRegistry.register(SkillDef(
        name="skill.automation",
        title="自动化运维执行",
        description="生成并执行安全的运维操作（Shell/K8s 命令、虚拟机操作），所有操作经安全策略校验并需人工审批后才真正执行",
        intent_keywords=["执行", "重启", "扩容", "缩容", "部署", "回滚", "操作", "命令", "kubectl", "执行操作"],
        tools=["execute_shell"],
        system_prompt=(
            "你负责自动化运维执行。所有操作命令都受 ShellPolicy 安全策略管控，"
            "会生成待审批任务，人工确认后才真正执行。只建议安全、可回滚的操作。"
        ),
        trigger_actions=["execute", "restart", "scale", "deploy", "rollback"],
    ))
