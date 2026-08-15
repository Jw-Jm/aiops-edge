"""md_meta.split_frontmatter 单元测试"""
import pytest

from md_meta import split_frontmatter


def test_parse_valid_frontmatter():
    text = """---
name: specialist-sre
when_to_use: 涉及 pod 问题
tools: [query_metrics]
---
正文内容
"""
    meta, body = split_frontmatter(text)
    assert meta["name"] == "specialist-sre"
    assert meta["when_to_use"] == "涉及 pod 问题"
    assert meta["tools"] == ["query_metrics"]
    assert body == "正文内容"


def test_missing_frontmatter_raises():
    with pytest.raises(ValueError, match="frontmatter"):
        split_frontmatter("没有 frontmatter 的纯文本")


def test_unclosed_frontmatter_raises():
    with pytest.raises(ValueError, match="未闭合"):
        split_frontmatter("---\nname: x\n正文没有结尾分隔")


def test_empty_meta_returns_empty_dict():
    meta, body = split_frontmatter("---\n---\nbody")
    assert meta == {}
    assert body == "body"
