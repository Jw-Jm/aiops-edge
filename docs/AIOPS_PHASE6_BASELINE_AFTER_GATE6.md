# V9.2 Phase6 — Baseline After Gate6（V9.3 Phase7 起点基线）

```text
CONTRACT      = V9.2 FINAL R2 Phase6 Final State Rebuild
BASELINE      = V9.2_BASELINE_AFTER_GATE6
Gate6         = PASS
V9.3          = READY / NOT_STARTED
DATE          = 2026-08-20
```

本文件冻结 V9.2 Phase6 关闭后的基线，作为 V9.3 Phase7 规划的起点。记录镜像版本、
配置指纹、canonical identity、Gate6 证据索引与当前禁止变更项。

## 1. 镜像版本（deployed）

| 服务 | 镜像 | 说明 |
|------|------|------|
| ingest | `ingest-pipeline:v1.1.5-b2.20260820113654` | new writer ACTIVE |
| query-api | `query-api:v1.1.4-dirty.20260820083624` | new reader ACTIVE |
| ai-orchestrator | `ai-orchestrator:v1.1.3-dirty.20260819005955` | |
| frontend | `observability-frontend:v1.1.3-dirty.20260819005955` | |
| event-collector | `event-collector:v1.1.3-dirty.20260819005955` | canonical identity |
| schema-migrator | `schema-migrator:rebuild-20260820` | init Job（G6-R） |
| VictoriaMetrics | `victoriametrics/victoria-metrics:v1.101.0` | |
| VictoriaLogs | `victoriametrics/victoria-logs:v1.52.0` | |

## 2. 配置指纹

| 项 | 值 |
|----|----|
| Helm values 源文件 | `bd05b6d7922e977d6069f9537e0761059a995d02e074d0dcb50260963e671976`（deploy/helm/aiops/values.yaml） |
| Secret identity（不含明文） | `d9340b9c74b05a5e4b79bb9f011156dad30399db1023352d7054983ab92ae984`（aiops-secrets data hash） |
| 运行模式 | ingest `TELEMETRY_WRITER_MODE=new` + `LEGACY_WRITER_ENABLED=false`；query-api `QUERY_READER_MODE=new` |

## 3. Canonical Identity

```text
tenant_id : 7ed01afc-cc79-4ecd-8767-a2befa6168ad
cluster_id: 91771a6e-9c2d-11f1-8271-bea176fe9f9f
```

Trusted Context（冻结，禁止 rotate）：
```text
public_key: fQmL5KNdga4wKAge7P0Yns15Ha1gRliqVyxYmIGD660
issuer    : ai-orchestrator
audience  : ai-apm-query-go
signing_key_origin: Authorization A key（/tmp/p65_sign_ctx.py，已验证匹配）
```

## 4. Gate6 Evidence Index

完整证据见 `docs/AIOPS_PHASE6_GATE.md`。

| Gate | 结果 | 关键证据 |
|------|------|---------|
| G6-A New Writer | PASS | VM `call_total` + VLogs fresh log，canonical 标签 |
| G6-B New Reader | PASS | `/internal/v1/query/*` HTTP 200，TrustedRequestContext V2（fQmL5） |
| G6-C Isolation | PASS | wrong tenant/cluster → NO_DATA；wrong capability → 403 |
| G6-D Semantic | PASS | NO_DATA 语义，no CH fallback |
| G6-E Legacy Absent | PASS | legacy writer/reader/fallback/adapter runtime absent |
| G6-R Reproducibility | PASS | clean install / secret restore / schema-migrator / identity / writer / reader |

## 5. 重建修复记录（进入 V9.3 前保留）

| 项 | 修复 |
|----|------|
| trusted context 公钥 | 恢复 `fQmL5...`（不 rotate） |
| schema-migrator 镜像 | 构建 `schema-migrator:rebuild-20260820` |
| MySQL users-init heredoc | `<<'SQL'` → `<<SQL`（变量展开） |
| MySQL users-init host | `127.0.0.1` → `mysql.observability.svc.cluster.local` |

## 6. 当前禁止变更项（V9.3 起点红线）

禁止在 V9.3 Phase7 启动前/启动中擅自改动：

- ❌ legacy 代码删除（仅 runtime disabled，代码保留维护能力）
- ❌ MySQL SoT 模型变更
- ❌ WAL / recovery 代码变更
- ❌ Auth / capability 模型变更
- ❌ query fallback 逻辑变更
- ❌ Trusted Context key rotation（保持 `fQmL5...`）
- ❌ 重新引入 legacy writer/reader runtime

## 7. V9.3 状态

```text
V9.3_STATUS   = READY
V9.3_PHASE7   = NOT_STARTED
```

禁止提前启动：Tool Registry / Evidence Hub / Planner / Agent Runtime / Learning Engine / Auto Run。
