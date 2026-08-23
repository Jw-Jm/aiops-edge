# AIOps V9.2 Phase 6 — Gate 6 证据（Final State Rebuild）

```text
CONTRACT      = V9.2 FINAL R2 Phase6 Final State Rebuild (CLEAN REBUILD)
PHASE         = 6
STATUS        = PASS
MODE          = FINAL REBUILD
V9.3          = NOT_STARTED
DATE          = 2026-08-20
```

## 执行摘要

在**非生产验证环境**执行 CLEAN REBUILD：废弃 legacy 链路，通过声明式重建（helm install）得到
V9.2 Phase 6 Final State，重新生成 Gate6 全量证据。重建过程中修正了 3 个重建可复现性缺陷
（见下），未改动 Go 代码 / MySQL schema / WAL / Auth 模型 / 未 rotate key。

## 重建修复记录（Rebuild Prerequisites）

| 项 | 问题 | 修复 |
|----|------|------|
| trusted context 公钥 | `ORCHESTRATOR_TO_QUERY_VERIFY_KEYS` 为空 → verifier disabled | 恢复 `fQmL5...`（trust root 未变，不 rotate） |
| schema-migrator 镜像 | build-images.sh 不构建，本地缺失 → init Job ImagePullBackOff | 构建 `schema-migrator:rebuild-20260820`（cmd/schema-migrator 源码） |
| MySQL 用户密码 heredoc | `users-init-job.yaml` 用 `<<'SQL'`（quoted heredoc）不展开变量 → 密码被设成字面量 → Access denied | 改为 `<<SQL`（允许展开） |
| MySQL users-init host | 独立 hook Job 连 `127.0.0.1`（自身无 MySQL）→ 连接失败 | 改为 `mysql.observability.svc.cluster.local` |

## Gate 6 证据

### G6-A New Writer — PASS

fresh telemetry（canonical tenant / cluster）写入验证：

| 后端 | 证据 |
|------|------|
| VictoriaMetrics | `call_total{tenant_id="7ed01afc-cc79-4ecd-8767-a2befa6168ad", cluster_id="91771a6e-9c2d-11f1-8271-bea176fe9f9f", service_name="checkout"} = 1` |
| VictoriaLogs | `_msg="g6a-fresh-log-..."` `_time=2026-08-20T13:12:30Z` `tenant_id="7ed01afc..."` `cluster_id="91771a6e..."` `service_name="checkout"` `level="INFO"` |

canonical 标签符合 Phase 4/5 契约。

### G6-B New Reader — PASS

`/internal/v1/query/*`（TrustedRequestContext V2，EdDSA fQmL5 验签）：

| 端点 | 结果 |
|------|------|
| `/internal/v1/query/logs` | HTTP 200，读到 VLogs fresh log `g6a-fresh-log-...` |
| `/internal/v1/query/metrics` | HTTP 200，2 个 VM 采样点（`call_total`） |

数据来自 VictoriaLogs / VictoriaMetrics（非 ClickHouse）。`CallCount=0` 为 `rate()[5m]` counter 取整精度，非链路问题。

### G6-C Isolation — PASS

| 用例 | 结果 |
|------|------|
| 错误 tenant + canonical cluster | HTTP 200 `NO_DATA`（鉴权通过，数据不可见） |
| canonical tenant + 错误 cluster | HTTP 200 `NO_DATA` |
| 错误 capability（logs.read 调 metrics） | HTTP 403 `TENANT_ACCESS_DENIED` |

### G6-D Semantic — PASS

| 用例 | 结果 |
|------|------|
| 无数据 service 查 logs | HTTP 200 `NO_DATA`（no CH fallback） |
| 无数据 service 查 metrics | HTTP 200 `NO_DATA` |
| CH 写入验证 | ClickHouse observability 遥测表保持 0 行（new 链不写 CH） |

NO_DATA 语义正确；reader 无 CH fallback。

### G6-E Runtime Legacy Absent — PASS

| 项 | 状态 |
|----|------|
| legacy writer runtime | DISABLED（`LEGACY_WRITER_ENABLED=false`，日志 "legacy ClickHouse writer DISABLED: CH writers nil"） |
| new writer runtime | ACTIVE（`TELEMETRY_WRITER_MODE=new`，日志 "telemetry new backend ACTIVE: VM=true VLogs=true"） |
| legacy reader runtime | DISABLED（`QUERY_READER_MODE=new`） |
| fallback | ABSENT（G6-B/D 证明无 CH fallback） |
| legacy adapter | ABSENT（无 legacy deployment） |

### G6-R Rebuild Reproducibility — PASS

| 项 | 状态 |
|----|------|
| clean install（helm install） | 成功（REVISION 3, deployed） |
| secret 恢复 | 成功（aiops-secrets 18 keys，trust root 保持） |
| schema-migrator（mysql-init Job） | Complete 1/1（schema checksum 校验通过） |
| identity 恢复 | tenant `7ed01afc...` / cluster `91771a6e...` 保留 |
| new writer | PASS（G6-A） |
| new reader | PASS（G6-B） |

## 最终状态

```text
legacy writer      ABSENT (runtime disabled)
legacy reader      ABSENT (runtime disabled)
fallback           ABSENT
new writer         ACTIVE
new reader         ACTIVE
VM                 ACTIVE
VLogs              ACTIVE
canonical identity ACTIVE
Gate6              PASS
V9.3               READY
```

## 边界

- 未开始 V9.3 Phase7 / Tool Registry / Evidence Hub / Planner / Agent
- 未改 MySQL SoT 模型 / WAL / Auth 模型 / capability 模型
- 未 rotate Ed25519 key / 未改 trust root
- 未删除 legacy 代码（仅 runtime disabled，代码保留维护能力）
- GIT_ACTION = NONE（未 commit/push）
