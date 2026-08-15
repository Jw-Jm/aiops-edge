"""SKILL.md 加载器 — 将 skills/<name>/SKILL.md 解析为 SkillFile 并校验。

- 必填字段: name / description / when_to_use
- tools 只能引用 ToolRegistry 中已注册的工具名（安全红线: 外部 skill 不执行外部代码）
- 目录加载顺序: 后出现的目录覆盖同名 skill（用户目录覆盖 builtin）
"""
import os
import re
from dataclasses import dataclass, field
from typing import Dict, List

from md_meta import split_frontmatter

_REQUIRED_FIELDS = ("name", "description", "when_to_use")


@dataclass
class SkillFile:
    name: str
    description: str
    when_to_use: str
    system_prompt: str
    tools: List[str]
    activation_keywords: List[str]
    description_keywords: List[str]
    title: str = ""
    trigger_actions: List[str] = field(default_factory=list)
    version: str = ""
    source: str = ""


def builtin_skills_dir() -> str:
    """内置 skills 目录（ai-orchestrator/skills）。"""
    return os.path.join(os.path.dirname(os.path.abspath(__file__)), "skills")


def user_skills_dir() -> str:
    """用户 skills 目录（AIOPS_DATA_DIR/skills，默认 /data/skills）。"""
    data_dir = os.environ.get("AIOPS_DATA_DIR", "/data")
    return os.path.join(data_dir, "skills")


def _derive_description_keywords(text: str) -> List[str]:
    """从 when_to_use 推导描述关键词，用于 match() 的 description 兜底匹配。"""
    tokens = re.split(r"[\s，。、；;：:（）()/,!?]+", (text or "").strip())
    seen, out = set(), []
    for t in tokens:
        t = t.strip().lower()
        if len(t) >= 2 and t not in seen:
            seen.add(t)
            out.append(t)
    return out


def _parse_skill_file(path: str) -> SkillFile:
    try:
        with open(path, "r", encoding="utf-8") as f:
            text = f.read()
    except OSError as e:
        raise ValueError(f"{path}: 无法读取 SKILL.md: {e}")

    meta, body = split_frontmatter(text)

    for req in _REQUIRED_FIELDS:
        if not meta.get(req):
            raise ValueError(f"{path}: 缺少必填字段 {req}")

    activation = meta.get("activation") or {}
    activation_keywords = [str(k) for k in (activation.get("keywords") or [])]

    tools = []
    for t in meta.get("tools") or []:
        name = t.get("name") if isinstance(t, dict) else t
        if name:
            tools.append(str(name))

    # 安全红线: 外部 skill 的 tools 只能引用已有注册工具名
    from skill_registry import ToolRegistry
    registered = {td.name for td in ToolRegistry.list_all()}
    for tn in tools:
        if tn not in registered:
            raise ValueError(f"{path}: 引用了未注册工具 {tn}")

    when_to_use = str(meta["when_to_use"])
    return SkillFile(
        name=str(meta["name"]),
        title=str(meta.get("title") or meta["name"]),
        description=str(meta["description"]),
        when_to_use=when_to_use,
        system_prompt=(body or "").strip(),
        tools=tools,
        activation_keywords=activation_keywords,
        description_keywords=_derive_description_keywords(when_to_use),
        trigger_actions=[str(a) for a in (meta.get("trigger_actions") or [])],
        version=str(meta.get("version") or ""),
        source=path,
    )


def load_skills(*dirs) -> Dict[str, SkillFile]:
    """从若干目录递归加载全部 SKILL.md，返回 {skill_name: SkillFile}。

    参数 dirs 为空时默认扫描 builtin + 用户目录；后出现的目录覆盖同名 skill。
    """
    merged: Dict[str, SkillFile] = {}
    scan_dirs = list(dirs) or [builtin_skills_dir(), user_skills_dir()]
    for d in scan_dirs:
        if not os.path.isdir(d):
            continue
        for root, _dirs, files in os.walk(d):
            if "SKILL.md" not in files:
                continue
            sf = _parse_skill_file(os.path.join(root, "SKILL.md"))
            merged[sf.name] = sf
    return merged
