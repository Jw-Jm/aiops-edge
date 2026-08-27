from __future__ import annotations
import os

SCHEDULE_DEFAULTS = {
    "outbox": 2, "kubernetes": 300, "kubevirt": 60, "hardware": 600,
    "trace": 60, "middleware": 60, "network": 300, "audit": 3600,
}


def interval_seconds(source: str) -> int:
    key = f"GRAPH_{source.upper()}_RECONCILE_INTERVAL_SECONDS" if source != "outbox" else "GRAPH_OUTBOX_POLL_INTERVAL_SECONDS"
    return int(os.getenv(key, SCHEDULE_DEFAULTS.get(source, 60)))
