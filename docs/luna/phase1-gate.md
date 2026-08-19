# Phase 1 Gate 1 验收记录

## 范围

Phase 1 只完成 ownership、数据模型和跨语言契约冻结；没有切换生产路由、没有启用新 writer、没有删除旧 schema/页面/依赖、没有读取真实 Kubernetes 凭据。

## Gate 1A：所有权与数据模型

结论：**PASS**。

- `docs/SCHEMA_OWNERSHIP.md` 明确业务域所有者、物理写入者、读写边界和已知冲突。
- `audit_logs` 与 `platform_audit_logs` 的语义边界已冻结。
- MySQL 动态授权唯一权威、Control Plane Persistence、canonical UUID `cluster_id`、`credential_ref` only、VictoriaLogs/ClickHouse/VictoriaMetrics 职责已冻结。
- Phase 0 的当前冲突被显式保留到后续 cutover，不在 Phase 1 偷改。

## Gate 1：跨语言契约

结论：**PASS**。

### Python

命令：

```text
cd ai-orchestrator
python3 -m pytest -q tests/test_contracts.py
PYTHONPYCACHEPREFIX=/tmp/aiops-phase1-pyc python3 -m compileall -q contracts.py tests/test_contracts.py
```

结果：`7 passed`，compileall 通过。测试覆盖：

- TrustedRequestContext 必填 UUID、60 秒生命周期和拒绝 roles/allowed clusters。
- 同名 resource 在不同 canonical cluster UUID 下隔离。
- ToolResult status 枚举和 success/error 语义。
- Evidence、Hypothesis、OpsAction、VerificationResult 边界。
- 稳定 `contract_validation_error` 和字段路径。

### Go

命令：

```text
cd ai-apm-query-go
go test ./...
```

结果：通过。新增 `internal/contract` 覆盖严格未知字段拒绝、UUID-only cluster_id、共享 fixture、ToolResult 状态和 JSON round-trip；`ResourceRef.namespace` 保持 nullable JSON 语义。

### TypeScript / Frontend

命令：

```text
cd observability-frontend
npm ci --ignore-scripts
npm run build
```

结果：通过。`tsc` 对 `src/api/contracts.ts` 与 `contracts.test-fixtures.ts` 完成类型检查，Vite production build 完成。构建仅产生被忽略的本地 `node_modules`/`dist`，未进入提交。

## 共享 fixture 与安全检查

- `docs/contracts/contract-fixtures.json` 被 Python 和 Go 测试读取；前端拥有同结构 `satisfies` fixture。
- fixture 包含两个 cluster 中同名 `orders` service，canonical resource ID 不同。
- TrustedRequestContext 没有 roles、permissions、allowed clusters、credential_ref、Secret 内容或 approval 结论。
- Phase 1 变更文件仅限文档、fixture、契约类型和契约测试；未修改生产 route、handler、writer 或 Kubernetes client 路径。
- `git diff --check` 通过，工作树干净。

## 提交记录

- `7bfc873`：Phase 1 实施计划
- `ba22bb4`：ownership / architecture / data model
- `bb59a21`：Python control plane contracts
- `a18acc0`：Go request context types
- `49d84c2`：frontend contract types
- `22a2c1b`：nullable namespace JSON 语义修正

## 进入 Phase 2 的前置条件

Gate 1 只批准进入 Phase 2，不代表生产路径已经切换。Phase 2 必须继续处理 Cluster Resolver、TrustedRequestContext 签名验证、query-api 授权回查和 Kubernetes Access Boundary；不得因为本 Gate 的 fixture 通过而把 fixture/mock 当作真实集群验收。

