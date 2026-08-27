from __future__ import annotations
from typing import Iterable
from .models import GraphMutationBatch


class MutationSink:
    """Small protocol adapter used by reconcile/projector tests and runtime."""
    def __init__(self, graph_client):
        self.graph_client = graph_client

    def apply(self, batch: GraphMutationBatch):
        return self.graph_client.batch_mutate(batch.to_dict())


def apply_batches(sink: MutationSink, batches: Iterable[GraphMutationBatch]) -> int:
    count = 0
    for batch in batches:
        sink.apply(batch); count += len(batch.mutations)
    return count
