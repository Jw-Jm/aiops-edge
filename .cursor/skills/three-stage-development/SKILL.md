---
name: three-stage-development
description: Use when a task should be handled as a staged architecture, planning, and implementation workflow with separate Sol, Terra, and Luna roles.
disable-model-invocation: true
---

# Three-Stage Development

Use this skill as the orchestration contract for the Sol → Terra → Luna workflow.

## Roles

- **Sol (`gpt-5.6-sol`)**: inspect the request and repository context, design the architecture, identify tradeoffs, risks, and acceptance criteria. Sol does not implement.
- **Terra (`gpt-5.6-terra`)**: consume Sol's architecture, turn it into an executable implementation plan, split the work into concrete tasks, and define verification steps. Terra does not implement.
- **Luna (`gpt-5.6-luna`)**: consume both handoffs, modify the repository, run relevant tests, and report changed files, verification results, and remaining risks.

## Execution contract

1. Preserve the user's manually selected model for the main orchestrator. Route only the three child roles to their named models; set each child model and reasoning effort explicitly.
2. Run the stages sequentially because each stage depends on the previous handoff. Do not ask Luna to implement before Terra has produced a plan.
3. Give Sol the original request plus only the context needed to reason about the architecture. Record its output as `architecture_handoff`.
4. Give Terra the original request and `architecture_handoff`. Record its output as `implementation_handoff`.
5. Give Luna the original request, both handoffs, the repository path, and explicit instructions to implement and verify. Keep file ownership clear and do not duplicate implementation work in the orchestrator.
6. If a child reports a blocking ambiguity, surface it to the user rather than silently changing requirements. Otherwise continue through verification.

## Required handoff contents

Sol must provide: goals and non-goals, current-state observations, proposed architecture, interfaces/data flow, key decisions and alternatives, risks, and acceptance criteria.

Terra must provide: ordered implementation tasks, files or modules likely affected, dependencies, test plan, rollout/migration notes, and definition of done.

Luna must provide: summary of changes, files changed, commands/tests run with results, and unresolved risks or follow-ups.

## Final response

Summarize the three handoffs in order, then report Luna's implementation and verification. Do not claim completion if verification failed or Luna did not finish.

