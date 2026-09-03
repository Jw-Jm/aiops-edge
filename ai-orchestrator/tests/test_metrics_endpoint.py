"""Production metrics endpoint must remain healthy without the legacy task store."""

from metrics import update_task_metrics


def test_update_task_metrics_accepts_disabled_legacy_store():
    """Production composition disables the legacy in-memory task owner."""

    update_task_metrics(None)
