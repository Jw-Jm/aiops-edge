from __future__ import annotations

from dataclasses import dataclass
from typing import Any, Iterable


@dataclass
class ProjectorStats:
    applied: int = 0
    failed: int = 0
    dead: int = 0


class GraphProjector:
    """Lease-aware orchestration-side adapter for deterministic mutation batches."""
    def __init__(self, sink: Any, lease: Any):
        self.sink, self.lease = sink, lease

    def project(self, batches: Iterable[Any]) -> ProjectorStats:
        stats = ProjectorStats()
        for batch in batches:
            self.lease.check_active()
            try:
                self.sink.apply(batch)
                stats.applied += len(getattr(batch, "mutations", ()))
            except Exception:
                stats.failed += 1
                raise
        return stats
