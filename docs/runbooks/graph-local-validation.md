# Local graph validation

The validation profile uses OrbStack Kubernetes, namespace `observability`, and
the only mutation canary namespace `aiops-canary`. It deploys Apache HugeGraph
1.7.0 with its RocksDB data directory at `/var/lib/hugegraph/data`.

```bash
LLM_PROVIDER_KEYS='deepseek:<real-key>' \
  bash deploy/scripts/local-validation.sh --skip-deepflow
```

The command performs Helm contract checks, MySQL/graph schema migration,
runtime readiness, executor-disabled/RBAC checks and graph recovery evidence.
No fake provider key is generated. If the local cluster or a real provider is
not reachable, the result is explicitly `BLOCKED_BY_ENV` rather than an empty
success state.

Read-only follow-ups:

```bash
bash deploy/scripts/graph-recovery-test.sh
bash deploy/scripts/validate-local-stack.sh
```
