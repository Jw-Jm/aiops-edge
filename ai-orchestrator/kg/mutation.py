"""Compatibility import for the scheme's explicit mutation module."""
from .models import GraphMutation, GraphMutationBatch
from .identity import mutation_uuid

__all__ = ["GraphMutation", "GraphMutationBatch", "mutation_uuid"]
