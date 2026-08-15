"""persona 注册表：从 markdown frontmatter 文件加载 Persona（对齐 ongrid persona 目录）。

persona = markdown frontmatter 文件（name/when_to_use/tools/permission_mode/max_turns），
正文即 worker 的 system prompt。load_personas 支持多个目录（builtin + 用户覆盖），
build_catalog 生成 coordinator 目录注入文本（B5 复用）。
"""
import os
from dataclasses import dataclass, field
from typing import List

from md_meta import split_frontmatter

# 合法 permission_mode：read-only（工具集 ∩ 只读白名单）/ read-write（persona 自身白名单）
VALID_PERMISSION_MODES = {"read-only", "read-write"}

# builtin persona 目录（包内 personas/）；用户目录由 main.py 按 AIOPS_DATA_DIR 拼接
PERSONAS_BUILTIN_DIR = os.path.join(os.path.dirname(os.path.abspath(__file__)), "personas")
USER_PERSONAS_DIR = os.path.join(os.environ.get("AIOPS_DATA_DIR", "/data"), "personas")


@dataclass
class Persona:
    name: str
    description: str = ""
    when_to_use: str = ""
    system_prompt: str = ""
    tools: List[str] = field(default_factory=list)
    disallowed_tools: List[str] = field(default_factory=list)
    permission_mode: str = "read-only"      # read-only / read-write
    max_turns: int = 20                     # 工具调用轮数上限
    background: bool = False                # 默认是否后台执行
    model: str = ""                         # 可选：指定模型
    source: str = "builtin"                 # builtin / user

    def to_dict(self) -> dict:
        return {
            "name": self.name,
            "description": self.description,
            "when_to_use": self.when_to_use,
            "tools": self.tools,
            "permission_mode": self.permission_mode,
            "max_turns": self.max_turns,
            "source": self.source,
        }


def load_personas(*dirs) -> dict:
    """加载全部 persona（*.md frontmatter）。

    目录顺序决定覆盖优先级与 source：首个目录视为 builtin，其后为 user（同名覆盖）。
    缺 when_to_use 或非法 permission_mode 抛 ValueError。
    """
    personas = {}
    for idx, d in enumerate(dirs):
        if not d or not os.path.isdir(d):
            continue
        source = "builtin" if idx == 0 else "user"
        for fn in sorted(os.listdir(d)):
            if not fn.endswith((".md", ".markdown")):
                continue
            path = os.path.join(d, fn)
            with open(path, encoding="utf-8") as f:
                text = f.read()
            meta, body = split_frontmatter(text)
            when_to_use = str(meta.get("when_to_use", "") or "").strip()
            if not when_to_use:
                raise ValueError(f"persona {path} 缺少 when_to_use 字段")
            permission_mode = str(meta.get("permission_mode", "read-only")).strip()
            if permission_mode not in VALID_PERMISSION_MODES:
                raise ValueError(
                    f"persona {path} 非法 permission_mode: {permission_mode} "
                    f"(可选 {sorted(VALID_PERMISSION_MODES)})")
            p = Persona(
                name=str(meta.get("name", fn[: -len(".md")])),
                description=str(meta.get("description", "") or ""),
                when_to_use=when_to_use,
                system_prompt=body.strip(),
                tools=[str(t) for t in (meta.get("tools") or [])],
                disallowed_tools=[str(t) for t in (meta.get("disallowed_tools") or [])],
                permission_mode=permission_mode,
                max_turns=int(meta.get("max_turns", 20) or 20),
                background=bool(meta.get("background", False)),
                model=str(meta.get("model", "") or ""),
                source=source,
            )
            personas[p.name] = p  # 后续目录覆盖同名
    return personas


def build_catalog(personas: dict) -> str:
    """生成 coordinator 目录注入文本：`- name: description | when_to_use 首行`。

    排除 reviewer/reporter（非 specialist，不参与 coordinator 路由）。
    """
    lines = []
    for name in sorted(personas):
        if name in ("reviewer", "reporter"):
            continue
        p = personas[name]
        first_line = (p.when_to_use or "").strip().splitlines()[0] if (p.when_to_use or "").strip() else ""
        lines.append(f"- {p.name}: {p.description} | {first_line}")
    return "\n".join(lines)
