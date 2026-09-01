from __future__ import annotations

import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

from kg.builders.trace import TraceBuilder


def test_trace_builder_drops_self_observations_but_keeps_cross_service_edges():
    batch = TraceBuilder("tenant-1", "cluster-1").build([
        {"source_service": "ingest", "target_service": "ingest"},
        {"source_service": "api", "target_service": "db"},
    ])

    assert len(batch.vertices) == 3
    assert len(batch.edges) == 1
    assert batch.edges[0]["relation_type"] == "DEPENDS_ON"
