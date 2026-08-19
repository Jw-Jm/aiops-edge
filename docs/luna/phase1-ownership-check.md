# Phase 1 Ownership Check

本检查用于 Gate 1A，验证文档已冻结而不是宣称生产代码已切换。

## 必须成立

- [x] `docs/SCHEMA_OWNERSHIP.md` 区分业务域所有者与物理写入者。
- [x] MySQL 动态授权唯一权威为 query-api / Control Plane Persistence。
- [x] AI Runtime 表由 query-api 物理写入，orchestrator 不直连 MySQL。
- [x] `audit_logs` 与 `platform_audit_logs` 的边界已定义。
- [x] VictoriaLogs raw logs、ClickHouse derived observability、VictoriaMetrics metrics 的职责已定义。
- [x] canonical UUID `cluster_id`、tenant 隔离、`credential_ref` only 已定义。
- [x] Phase 1 不切生产路由、不启用新 writer、不迁移历史、不删除旧实现。

## 现状冲突必须显式保留

- [x] 当前 `clusters.kubeconfig` 仍存在，留给 Kubernetes Access Phase 处理。
- [x] 当前旧 `audit_logs` writer/DDL 冲突已记录，留给 Control Plane Persistence 收敛。
- [x] 当前字符串/default cluster schema 冲突已记录，留给新 schema cutover。

## Gate 结论

Gate 1A 文档条件满足。Gate 1 还必须通过 Python/Go/TypeScript contract fixtures、负向校验、JSON round-trip 和三端 schema 一致性测试后，才能进入 Phase 2。

