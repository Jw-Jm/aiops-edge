"""markdown frontmatter 解析共享工具 (persona / SKILL.md / playbook 共用)"""
import yaml


def split_frontmatter(text: str):
    """解析 markdown 的 YAML frontmatter。

    Returns:
        (meta: dict, body: str) — frontmatter 字典与正文
    Raises:
        ValueError: 缺少或未闭合 frontmatter
    """
    lines = text.splitlines()
    if not lines or lines[0].strip() != "---":
        raise ValueError("缺少 YAML frontmatter (--- 开头)")
    end = None
    for i in range(1, len(lines)):
        if lines[i].strip() == "---":
            end = i
            break
    if end is None:
        raise ValueError("frontmatter 未闭合 (缺少结尾 ---)")
    meta = yaml.safe_load("\n".join(lines[1:end])) or {}
    return meta, "\n".join(lines[end + 1:])
