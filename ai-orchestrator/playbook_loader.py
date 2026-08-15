"""playbook 加载器 — 内置运维 playbook 切块 → 向量化 → ops_playbooks 集合。

对齐 ongrid builtin_vault 模式: playbook = YAML frontmatter (title/tags/alert_keys/applies_to)
+ 四段正文 (What this means / Immediate checks / Likely causes / Escalation criteria)。
按 `## ` 标题切 chunk (单块 ≤600 字符), doc_id = `{relpath}#{i}`, upsert 幂等。
"""
import os

from md_meta import split_frontmatter

# 检索建议阈值: score ≥ 0.6 才按 playbook 作答 (persona 约定, 工具不硬过滤)
PLAYBOOK_SCORE_THRESHOLD = 0.6
# preview 最大长度
PREVIEW_MAX_CHARS = 800
# 单块最大字符数
CHUNK_MAX_CHARS = 600


def _default_playbooks_dir() -> str:
    """内置 playbook 目录 (data/playbooks, 随镜像分发)。"""
    return os.path.join(os.path.dirname(os.path.abspath(__file__)), "data", "playbooks")


def _split_sections(body: str) -> list:
    """按 `## ` 标题把正文切成 section (标题含 `## ` 前缀, 保留标题行)。"""
    lines = body.split("\n")
    sections = []
    current = []
    for ln in lines:
        if ln.startswith("## "):
            if current:
                sections.append("\n".join(current))
            current = [ln]
        else:
            current.append(ln)
    if current:
        sections.append("\n".join(current))
    return [s.strip() for s in sections if s.strip()]


def _chunkify(sections: list, max_chars: int = CHUNK_MAX_CHARS) -> list:
    """section 超长时按行再切分, 保证每块 ≤ max_chars 字符。"""
    chunks = []
    for sec in sections:
        if len(sec) <= max_chars:
            chunks.append(sec)
            continue
        buf = ""
        for ln in sec.split("\n"):
            if buf and len(buf) + len(ln) + 1 > max_chars:
                chunks.append(buf)
                buf = ln
            else:
                buf = f"{buf}\n{ln}" if buf else ln
        if buf:
            chunks.append(buf)
    return chunks


def _norm_meta_list(values):
    """metadata 值归一化: 非空列表存列表(chromadb $contains 成员匹配), 空列表存空串(空 list 会被 chromadb 拒绝)。"""
    v = [str(x) for x in (values or [])]
    return v if v else ""


def load_playbooks(store, playbooks_dir: str = None) -> int:
    """扫描 playbooks_dir 下所有 .md, 解析 frontmatter 并按 `## ` 切块入库。

    Args:
        store: RAGStore 实例 (需支持 upsert_playbook_chunk)
        playbooks_dir: playbook 目录, 缺省为内置 data/playbooks

    Returns:
        写入的 chunk 总数 (幂等: 重复调用不产生重复 chunk)。
    """
    if playbooks_dir is None:
        playbooks_dir = _default_playbooks_dir()
    added = 0
    if not os.path.isdir(playbooks_dir):
        return 0
    for root, _dirs, files in sorted(os.walk(playbooks_dir)):
        for fn in sorted(files):
            if not fn.endswith(".md"):
                continue
            path = os.path.join(root, fn)
            relpath = os.path.relpath(path, playbooks_dir)
            with open(path, encoding="utf-8") as f:
                text = f.read()
            meta, body = split_frontmatter(text)
            category = os.path.dirname(relpath)
            title = meta.get("title") or os.path.splitext(fn)[0]
            # tags/alert_keys/applies_to 存为列表 (chromadb 1.x $contains 按数组成员匹配);
            # 空列表归一化为空串, 否则 chromadb 拒绝空 list metadata
            m_tags = _norm_meta_list(meta.get("tags"))
            m_alert_keys = _norm_meta_list(meta.get("alert_keys"))
            m_applies_to = _norm_meta_list(meta.get("applies_to"))
            for i, chunk_text in enumerate(_chunkify(_split_sections(body))):
                doc_id = f"{relpath}#{i}"
                if store.upsert_playbook_chunk(doc_id, chunk_text, {
                        "path": relpath,
                        "category": category,
                        "title": title,
                        "tags": m_tags,
                        "alert_keys": m_alert_keys,
                        "applies_to": m_applies_to,
                }):
                    added += 1
    return added


def _default_store():
    """模块级 RAGStore 单例 (与 skills/rag_skill.py 的 case_search 同一实例)。"""
    from rag import rag
    return rag


def query_knowledge(query: str, path_prefix: str = None, tags=None,
                    max_results: int = 5, store=None) -> dict:
    """内置运维知识库检索工具 (playbook + 历史案例两组分合并)。

    Args:
        query: 检索关键词
        path_prefix: 仅检索指定 playbook 分类 (diagnostics/alerts/concepts/reference)
        tags: 标签过滤 (逗号分隔字符串或列表, 全部命中才返回)
        max_results: 返回条数上限
        store: 内部/测试用注入的 RAGStore; 缺省用模块级 rag 单例

    Returns:
        {"items": [{"title","path","category","tags","score","preview",...}]}
        preview ≤ 800 字符; score 供 persona 按 ≥0.6 阈值判断是否作答。
    """
    if not query:
        return {"items": []}
    store = store or _default_store()
    tag_list = None
    if isinstance(tags, str):
        tag_list = [t.strip() for t in tags.split(",") if t.strip()]
    elif tags:
        tag_list = [str(t) for t in tags]

    items = []
    # 分组 1: playbook (ops_playbooks 集合)
    for pb in store.search_playbooks(query, limit=max_results,
                                     path_prefix=path_prefix, tags=tag_list):
        items.append({
            "source": "playbook",
            "title": pb["title"],
            "path": pb["path"],
            "category": pb["category"],
            "tags": pb["tags"],
            "score": pb["score"],
            "preview": (pb["content"] or "")[:PREVIEW_MAX_CHARS],
        })
    # 分组 2: 历史案例 (ops_cases 集合, path_prefix 为 playbook 专属过滤)
    if not path_prefix:
        for c in store.search(query, limit=max_results):
            items.append({
                "source": "case",
                "title": (c.get("title") or c.get("symptom", ""))[:80],
                "path": "cases/" + c.get("case_id", ""),
                "category": c.get("type", "case"),
                "tags": c.get("tags", ""),
                "score": c.get("score", 0),
                "preview": (c.get("symptom", "") or "")[:PREVIEW_MAX_CHARS],
            })
    items.sort(key=lambda x: x["score"], reverse=True)
    return {"items": items[:max_results]}
