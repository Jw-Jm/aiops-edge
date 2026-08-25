# Workflow contract gate

These tests are deterministic, offline contract gates. They model the durable
approval/outbox/reconcile boundaries and assert the checked-in service code
keeps one Action authority. The service-specific suites run alongside them via
the root `Makefile`; no real Kubernetes cluster or provider key is used.
