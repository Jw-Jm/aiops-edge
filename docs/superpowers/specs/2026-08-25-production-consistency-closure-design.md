# Production Consistency Closure Design

**Date:** 2026-08-25

## Goal

Close the production-blocking consistency failures identified in the repository audit: no false action success, no cross-service signing-domain mismatch, no false Query API readiness, no silent log loss after an ingest ACK, and no malformed LLM proxy upstream path.

## Scope

This design covers the first remediation phase only:

- `ai-action-executor`: fail closed when approved execution cannot perform a Kubernetes mutation; use a dedicated verify key domain.
- `ai-apm-query-go`: expose process liveness separately from dependency readiness and make production startup fail fast when MySQL is unavailable.
- `ai-apm-ingest-go`: return a retryable error when a required log sink write fails.
- `ai-llm-egress-proxy`: strip the provider segment before forwarding and test the upstream path.
- Helm: wire the dedicated executor verify key, the new health endpoints, and the LLM proxy settings without restoring database credentials to Orchestrator.

Persistence Ownership, cold run recovery, lease release/time semantics, scheduler HA, and frontend release gates remain separate follow-up phases.

## Invariants

1. `success` means the requested mutation was applied and verified by the data plane.
2. A service is Ready only when its authoritative MySQL dependency and required bootstrap state are usable.
3. An ingest client receives a successful ACK only after the required log batch is durably accepted by the configured sink.
4. The LLM provider name is routing metadata and never appears in the provider API path.
5. Query API → Action Executor signing keys are independent from Query API → Orchestrator keys.

## Verification

Each behavior is implemented test-first. Targeted unit tests run before package tests; Go modules are tested separately because the repository contains multiple modules. Helm templates are rendered with the repository's existing chart tooling where available.
