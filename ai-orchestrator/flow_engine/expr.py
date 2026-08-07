# flow_engine/expr.py
import re
from dataclasses import dataclass, field


@dataclass
class RunContext:
    trigger: dict = field(default_factory=dict)
    nodes: dict = field(default_factory=dict)
    vars: dict = field(default_factory=dict)


def get_path(obj, path: str):
    """取 a.b.c[0].d 路径值；任一段缺失返回 None。"""
    cur = obj
    for part in re.split(r"\.|\[", path.replace("]", "")):
        if part == "":
            continue
        if isinstance(cur, (dict,)):
            cur = cur.get(part)
        elif isinstance(cur, (list,)) and part.isdigit():
            cur = cur[int(part)]
        else:
            return None
        if cur is None:
            return None
    return cur


_TEMPLATE = re.compile(r"\{\{([^}]+)\}\}")


def _lookup(ref: str, ctx: RunContext):
    ref = ref.strip()
    if ref.startswith("trigger."):
        return get_path(ctx.trigger, ref[len("trigger."):])
    if ref.startswith("nodes."):
        rest = ref[len("nodes."):]
        parts = re.split(r"\.|\[", rest.replace("]", ""))
        node_id, sub = parts[0], ".".join(parts[1:])
        node = ctx.nodes.get(node_id)
        if node is None:
            return None
        return get_path(node, sub)
    if ref.startswith("vars."):
        return ctx.vars.get(ref[len("vars."):])
    return None


def resolve_template(text: str, ctx: RunContext) -> str:
    def _sub(m):
        val = _lookup(m.group(1), ctx)
        return str(val) if val is not None else m.group(0)
    return _TEMPLATE.sub(_sub, text)


def resolve_value(val, ctx: RunContext):
    if isinstance(val, str):
        return resolve_template(val, ctx)
    return val


def eval_condition(expr: str, ctx: RunContext) -> bool:
    """从顶层找操作符。支持 == != > >= < <= contains。"""
    resolved = resolve_template(expr, ctx)
    for op in (">=", "<=", "==", "!=", ">", "<"):
        if op in resolved:
            left, right = resolved.split(op, 1)
            return _numcmp(op, left.strip(), right.strip())
    if " contains " in resolved:
        left, right = resolved.split(" contains ", 1)
        return right.strip() in left.strip()
    return False


def _numcmp(op, left, right):
    if op == "==":
        return left == right
    if op == "!=":
        return left != right
    try:
        l, r = float(left), float(right)
    except ValueError:
        return False
    return {"<": l < r, ">": l > r, "<=": l <= r, ">=": l >= r}[op]
