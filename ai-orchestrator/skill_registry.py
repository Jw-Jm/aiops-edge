"""Skill 体系基类与注册机制 — 将智能运维能力整合为标准化 Skill 模块。

每个 Skill 封装一组相关工具 + 意图关键词 + 系统提示词模板，
专家 (Expert) 通过引用多个 Skill 组合能力，实现"工具分层、专家编排"。
"""
import os
import json
from dataclasses import dataclass, field
from typing import Callable, Optional, List


# ═══════════════════════════════════════════════════════
#  Tool (工具)
# ═══════════════════════════════════════════════════════

@dataclass
class ToolDef:
    name: str
    description: str
    func: Callable
    params: dict = field(default_factory=dict)  # param_name -> type
    category: str = "general"                   # metrics | trace | infra | vm | k8s | automation
    requires_approval: bool = False             # 是否需人工审批后才能执行


class ToolRegistry:
    _tools: dict[str, ToolDef] = {}

    @classmethod
    def register(cls, name: str = None, description: str = "",
                 category: str = "general", requires_approval: bool = False,
                 params: dict = None):
        """装饰器: 注册工具函数

        params: param_name -> {"type","required","default","desc"}，供前端渲染执行表单。
        """
        def decorator(func):
            tool_name = name or func.__name__
            cls._tools[tool_name] = ToolDef(
                name=tool_name, description=description,
                func=func, category=category,
                requires_approval=requires_approval,
                params=params or {},
            )
            return func
        return decorator

    @classmethod
    def get(cls, name: str) -> Optional[ToolDef]:
        return cls._tools.get(name)

    @classmethod
    def list_by_category(cls, category: str) -> list[ToolDef]:
        return [t for t in cls._tools.values() if t.category == category]

    @classmethod
    def list_all(cls) -> list[ToolDef]:
        return list(cls._tools.values())

    @classmethod
    def describe_for_llm(cls, category: str = None) -> str:
        """生成 LLM 可用的工具描述"""
        tools = cls.list_by_category(category) if category else cls.list_all()
        if not tools:
            tools = cls.list_all()
        return "\n".join(
            f"- {t.name}: {t.description}" + (" (需审批)" if t.requires_approval else "")
            for t in tools
        )


# ═══════════════════════════════════════════════════════
#  Skill (技能)
# ═══════════════════════════════════════════════════════

@dataclass
class SkillDef:
    name: str                                   # e.g. skill.observability
    title: str                                  # 中文标题
    description: str                            # 技能描述（供 LLM/路由理解）
    intent_keywords: List[str]                  # 意图关键词
    tools: List[str]                            # 关联工具名
    system_prompt: str = ""                     # 该技能的领域系统提示词
    trigger_actions: List[str] = field(default_factory=list)  # 可触发的动作（如审批执行）

    def to_summary(self) -> dict:
        """导出技能元数据，供 /ai/skills 接口与前端渲染。"""
        tools = []
        for tn in self.tools:
            t = ToolRegistry.get(tn)
            tools.append({
                "name": tn,
                "description": t.description if t else "",
                "category": t.category if t else "general",
                "requires_approval": t.requires_approval if t else False,
                "params": list((t.params or {}).keys()),
            })
        return {
            "key": self.name,
            "name": self.title,
            "description": self.description,
            "intent_keywords": self.intent_keywords,
            "tools": tools,
            "system_prompt": self.system_prompt,
        }


class SkillRegistry:
    _skills: dict[str, SkillDef] = {}

    @classmethod
    def register(cls, skill: SkillDef):
        cls._skills[skill.name] = skill
        return skill

    @classmethod
    def get(cls, name: str) -> Optional[SkillDef]:
        return cls._skills.get(name)

    @classmethod
    def match(cls, user_message: str) -> List[SkillDef]:
        """按意图关键词匹配最相关的 Skill 列表（按命中数降序）"""
        msg = (user_message or "").lower()
        scored = []
        for s in cls._skills.values():
            score = sum(1 for kw in s.intent_keywords if kw in msg)
            if score > 0:
                scored.append((score, s))
        scored.sort(key=lambda x: x[0], reverse=True)
        return [s for _, s in scored]

    @classmethod
    def list_all(cls) -> list[SkillDef]:
        return list(cls._skills.values())

    @classmethod
    def describe_all(cls) -> str:
        """生成全部 Skill 的描述清单，供路由使用"""
        return "\n".join(
            f"- {s.name} ({s.title}): {s.description}" for s in cls._skills.values()
        )

    @classmethod
    def execute_skill(cls, key: str, params: dict) -> dict:
        """执行技能：遍历其 tools，调用对应工具函数。

        - 需审批工具（requires_approval）返回提示，不执行。
        - 参数按工具 params schema 过滤后调用 func；缺失参数用默认值。
        """
        skill = cls._skills.get(key)
        if not skill:
            raise KeyError(f"skill not found: {key}")
        out = {}
        for tn in skill.tools:
            t = ToolRegistry.get(tn)
            if not t:
                out[tn] = {"error": "tool not found"}
                continue
            if t.requires_approval:
                out[tn] = {"requires_approval": True, "tool": tn}
                continue
            try:
                schema = t.params or {}
                tool_params = {k: v for k, v in (params or {}).items() if k in schema}
                result = t.func(**tool_params) if tool_params else t.func()
                out[tn] = {"result": str(result)[:1000]}
            except Exception as e:
                out[tn] = {"error": str(e)}
        return {"skill": key, "outputs": out}


# ═══════════════════════════════════════════════════════
#  Expert (专家 = 多个 Skill 的组合编排)
# ═══════════════════════════════════════════════════════

@dataclass
class ExpertDef:
    name: str
    role: str
    goal: str
    backstory: str
    intent_keywords: list[str]
    skills: list[str]            # 关联的 Skill 名称列表
    tools: list[str]             # 可直接调用的工具（兼容旧版）
    system_prompt_template: str = ""


class ExpertRegistry:
    _experts: dict[str, ExpertDef] = {}

    @classmethod
    def register(cls, name: str, role: str, goal: str, backstory: str,
                 intent_keywords: list[str], skills: list[str] = None,
                 tools: list[str] = None, system_prompt_template: str = ""):
        cls._experts[name] = ExpertDef(
            name=name, role=role, goal=goal, backstory=backstory,
            intent_keywords=intent_keywords,
            skills=skills or [],
            tools=tools or [],
            system_prompt_template=system_prompt_template,
        )

    @classmethod
    def get(cls, name: str) -> Optional[ExpertDef]:
        return cls._experts.get(name)

    @classmethod
    def match_intent(cls, user_message: str) -> Optional[ExpertDef]:
        """根据用户消息匹配最合适的专家"""
        msg_lower = (user_message or "").lower()
        best_score = 0
        best_expert = None
        for expert in cls._experts.values():
            score = sum(1 for kw in expert.intent_keywords if kw in msg_lower)
            if score > best_score:
                best_score = score
                best_expert = expert
        return best_expert

    @classmethod
    def list_all(cls) -> list[ExpertDef]:
        return list(cls._experts.values())

    @classmethod
    def skills_of(cls, expert_name: str) -> List[SkillDef]:
        """返回专家关联的所有 Skill 定义"""
        expert = cls._experts.get(expert_name)
        if not expert:
            return []
        return [SkillRegistry.get(s) for s in expert.skills if SkillRegistry.get(s)]

    BUILTIN_EXPERTS = {"inspection", "diagnosis", "ops", "query"}

    @classmethod
    def update(cls, name: str, **fields) -> bool:
        """更新专家字段（role/goal/backstory/intent_keywords/skills/tools/system_prompt_template）。"""
        expert = cls._experts.get(name)
        if not expert:
            return False
        for k, v in fields.items():
            if hasattr(expert, k):
                setattr(expert, k, v)
        cls.save_custom_store()
        return True

    @classmethod
    def delete(cls, name: str) -> bool:
        """删除用户自定义专家；内置专家不可删。"""
        if name in cls.BUILTIN_EXPERTS:
            return False
        if name in cls._experts:
            del cls._experts[name]
            cls.save_custom_store()
            return True
        return False

    @classmethod
    def _store_path(cls) -> str:
        return os.environ.get("EXPERTS_STORE", "/tmp/expert_store.json")

    @classmethod
    def save_custom_store(cls):
        """将用户自定义专家（非内置）持久化到 JSON。"""
        custom = {}
        for k, v in cls._experts.items():
            if k in cls.BUILTIN_EXPERTS:
                continue
            custom[k] = {
                "name": v.name, "role": v.role, "goal": v.goal, "backstory": v.backstory,
                "intent_keywords": v.intent_keywords, "skills": v.skills, "tools": v.tools,
                "system_prompt_template": v.system_prompt_template,
            }
        try:
            with open(cls._store_path(), "w") as f:
                json.dump(custom, f, ensure_ascii=False, indent=2)
        except Exception as e:
            print(f"[skill_registry] 保存自定义专家失败: {e}")

    @classmethod
    def load_custom_store(cls):
        """启动时加载用户自定义专家（与内置专家合并）。"""
        try:
            with open(cls._store_path()) as f:
                data = json.load(f)
        except (FileNotFoundError, json.JSONDecodeError):
            return
        for k, v in data.items():
            if k in cls.BUILTIN_EXPERTS:
                continue
            cls._experts[k] = ExpertDef(**v)


# ═══════════════════════════════════════════════════════
#  初始化（在 orchestrator 中调用）
# ═══════════════════════════════════════════════════════

def _init_defaults():
    """初始化全部 Skill 与专家。幂等，可重复调用。"""
    if SkillRegistry.list_all():
        return  # 已初始化
    from skills import init_skills, init_experts
    init_skills()
    init_experts()
    ExpertRegistry.load_custom_store()  # 加载用户自定义专家
