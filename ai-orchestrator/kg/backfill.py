from __future__ import annotations
from typing import Any, Callable

BACKFILL_ORDER = ("catalog", "hardware", "kubernetes", "kubevirt", "middleware", "trace", "change", "network")


def run_backfill(*, sources: dict[str, Callable[[], Any]], reconcile: Callable[[str, Any], Any]) -> list[Any]:
    results = []
    for source in BACKFILL_ORDER:
        if source in sources:
            results.append(reconcile(source, sources[source]()))
    return results
