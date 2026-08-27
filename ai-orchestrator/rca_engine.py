"""Compatibility source marker for deployments that still enumerate rca_engine.py.

The importable implementation lives in the sibling ``rca_engine/`` package;
this file is retained so older packaging/security manifests keep the same
path. Python package discovery prefers the package directory.
"""
