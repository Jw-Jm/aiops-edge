# AIOps Phase 0 Baseline Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Freeze the confirmed architecture decisions and produce a reproducible Phase 0 baseline for the AIOps Agentic refactor without changing production behavior or runtime data.

**Architecture:** Treat `aiops-agentic.md` as the governing specification. Phase 0 writes only decision constraints and evidence documents: `BEFORE_BASELINE.md` plus code, API, data, page, and dependency maps. Environment capabilities are probed, never inferred; unavailable capabilities block their later Gate.

**Tech Stack:** Git, shell probes, Python/Go/Node/Helm/Docker/Kubernetes tooling when available, repository inspection with `rg`, and Markdown evidence artifacts.

**Spec:** `aiops-agentic.md` sections 0–5, especially Tasks 0.1 and 0.2.

## Global Constraints

- Do not modify production source, deployment configuration, dependencies, or runtime data in Phase 0.
- Do not print Secret, kubeconfig, token, private key, password, or LLM API key values.
- Record command, exit code, version, status, and failure reason for every baseline probe.
- Do not infer availability from configuration files; use a real command/API/health check.
- Preserve all existing user files, backups, bundles, binaries, and untracked files.
- Any `UNKNOWN` deletion target remains protected and cannot be cleaned.

### Task 1: Freeze the confirmed decision constraints

**Files:**
- Modify: `aiops-agentic.md:0.5`

- [x] Add the confirmed tenant/RBAC, UUID cluster identity, Kubernetes credential boundary, internal authentication, control-plane ownership, storage ownership, Planner budget, RCA thresholds, risk policy, cleanup guard, and Phase 0 discovery rules.
- [ ] Review the inserted constraints for contradictions with later sections and record any required follow-up edits.

### Task 2: Capture repository and Git baseline

**Files:**
- Create: `BEFORE_BASELINE.md`
- Create: `docs/luna/phase0-code-map.md`
- Create: `docs/luna/phase0-api-map.md`
- Create: `docs/luna/phase0-data-map.md`
- Create: `docs/luna/phase0-page-map.md`
- Create: `docs/luna/phase0-dependency-map.md`

- [ ] Record absolute workspace, branch, SHA, worktree status, recent commit, and current untracked/modified files.
- [ ] Inventory services, entrypoints, routes, pages, storage schemas, writers/readers, dependencies, Dockerfiles, Helm templates, scripts, and tests.
- [ ] Record current call chains and ownership conflicts without fixing them.

### Task 3: Probe the local execution environment

**Files:**
- Modify: `BEFORE_BASELINE.md`

- [ ] Probe tool versions for Git, Python, Go, Node/npm, Helm, Docker, kubectl, and Playwright/browser availability.
- [ ] Probe local Kubernetes access without printing credentials; record contexts, API reachability, and permissions at metadata level only.
- [ ] Probe configured ClickHouse, VictoriaMetrics, VictoriaLogs, MySQL, ChromaDB, MinIO, and LLM endpoints using safe health/read-only checks when available.
- [ ] Probe K8sGPT installation and adapter wiring; record `AVAILABLE`, `UNAVAILABLE`, or `UNKNOWN`.

### Task 4: Run the Phase 0 baseline test matrix

**Files:**
- Modify: `BEFORE_BASELINE.md`

- [ ] Run the exact commands from `aiops-agentic.md` Task 0.1 where the environment supports them.
- [ ] Record command, exit code, duration, and concise failure reason for missing tools, dependency failures, test failures, build failures, Docker failures, and Kubernetes failures.
- [ ] Do not repair failures in Phase 0; classify them as baseline facts and preserve their evidence.

### Task 5: Gate 0 review and commit

**Files:**
- Modify: all Phase 0 evidence files only.

- [ ] Verify that no production code, deployment configuration, dependency file, or runtime data changed.
- [ ] Verify all five maps and `BEFORE_BASELINE.md` answer the Gate 0 questions.
- [ ] Run `git diff --check` and a secret/path safety review.
- [ ] Commit only the Phase 0 decision/spec and baseline artifacts with message `docs(baseline): freeze aiops phase 0 evidence`.
