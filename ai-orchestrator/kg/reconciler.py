from __future__ import annotations
from dataclasses import dataclass
from typing import Any, Callable

STALE_GRACE_SECONDS = {"kubernetes": 900, "kubevirt": 300, "hardware": 86400,
                       "trace": 1800, "middleware": 1800, "network": 3600}


@dataclass
class ReconcileResult:
    source: str
    generation: int
    applied: int
    stale_marked: bool


class GraphReconciler:
    def __init__(self, *, generation_store: Any, lease: Any, projector: Any, builder: Callable):
        self.generation_store, self.lease, self.projector, self.builder = generation_store, lease, projector, builder

    def run(self, source: str, records: Any) -> ReconcileResult:
        self.lease.check_active()
        generation = int(self.generation_store.next(source))
        batch = self.builder(records, generation=generation)
        stats = self.projector.project([batch])
        # Stale marking is intentionally after every batch succeeds.
        self.generation_store.mark_stale(source, generation)
        self.generation_store.commit(source, generation)
        return ReconcileResult(source, generation, stats.applied, True)
