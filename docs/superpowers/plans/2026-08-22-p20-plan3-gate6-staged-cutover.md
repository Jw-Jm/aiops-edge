# P20 Plan 3：独立 Gate 6 Staged Cutover

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 按设计 v0.2 §6 执行 Gate 6 staged cutover（F5 的唯一例外）：先写后读、writer/reader 分阶段切换、影子对账、precheck、成功/停止阈值、回滚、观察窗口、不可变 manifest hash；只停流量，不删除 legacy 数据或证据。

**Architecture:** writer 侧在 `ai-apm-ingest-go`（`TELEMETRY_WRITER_MODE`：legacy/new 双写/切换），reader 侧在 `ai-apm-query-go`（`QUERY_READER_MODE` + `SourceRouter.ReaderFor`）。当前生产为 legacy writer+reader ACTIVE、new 链已就绪未激活（P6.5 冻结）。本计划在 orbstack acceptance 执行 staged 切换，**只停流量不删数据**。

**Tech Stack:** Helm（query-api/ingest env）、kubectl、orbstack acceptance（namespace `observability`）、VictoriaMetrics/VictoriaLogs/ClickHouse、manifest hash（SHA-256）。

## Global Constraints

- GIT_ACTION = NONE：只记录变更，不 commit/push。
- **这是 F5 的唯一例外**：只做 writer/reader 流量切换；**不触发真实业务执行变更**。
- **只停流量，不删除 legacy 数据/证据**：全程不 TRUNCATE、不 DELETE、不 reset。
- **每阶段单独 Go/No-Go**：每个 writer/reader 阶段需用户单独执行授权；不能一次批量放行。
- **不可变 manifest hash 是唯一部署授权对象**：每阶段生成 manifest + SHA-256，授权引用 hash；字段变化→hash 变→须重新授权。
- canonical 身份：tenant `7ed01afc-cc79-4ecd-8767-a2befa6168ad`、主集群 `91771a6e-9c2d-11f1-8271-bea176fe9f9f`。
- 观察窗口建议 15-30min/阶段（按实际调整，记录）。

---

## Task 1: S0 PRECHECK（每阶段前统一预检）

**Files:**
- 无新增（运行验证）

**Interfaces:**
- Produces: 满足执行条件的确证（canonical 身份、new backend 可写、schema-migrator 就绪、迁移版本、manifest 模板）。

- [ ] **Step 1: 确认 canonical 身份**

确认 tenants/clusters/tenant_clusters 中 canonical tenant + cluster 归属正确（MySQL 查询）。

- [ ] **Step 2: 确认 new backend 可写**

验证 VM/VLogs 可达且可写（fresh telemetry 受控注入 + 查询回读），CH legacy 仍可读。

- [ ] **Step 3: 确认 schema-migrator 与迁移版本**

确认 `aiops_schema_migrations` 表存在、`RequireCurrentVersion` 通过、迁移版本正确（query-api 当前镜像内置迁移集）。

- [ ] **Step 4: 生成 PRECHECK manifest + hash**

```bash
# 生成 S0 manifest（字段：cluster/tenant/镜像tag/阶段/期望状态）
cat > /tmp/p20_g6_s0_manifest.json <<'EOF'
{
  "phase": "S0_PRECHECK",
  "environment": "orbstack-local-acceptance",
  "cluster_id": "91771a6e-9c2d-11f1-8271-bea176fe9f9f",
  "tenant_id": "7ed01afc-cc79-4ecd-8767-a2befa6168ad",
  "writer_mode": "legacy+new_shadow",
  "reader_mode": "legacy",
  "expected": "all_prechecks_pass"
}
EOF
sha256sum /tmp/p20_g6_s0_manifest.json
```

- [ ] **Step 5: 用户授权 S0 进入 S1**

展示 manifest + hash，请用户授权（引用 hash）后进入 S1。

---

## Task 2: S1 Writer 影子写 + S2 Writer 影子对账

**Files:**
- Modify: `deploy/helm/aiops/values.yaml` 或 deployment env（ingest `TELEMETRY_WRITER_MODE`）
- Run: `helm upgrade` 或 `kubectl set env`

**Interfaces:**
- Consumes: S0 PRECHECK 通过。
- Produces: new writer 启用（影子双写），legacy writer 保留；影子对账通过。

- [ ] **Step 1: S1 manifest + 授权**

生成 S1 manifest（writer_mode=legacy+new_shadow，期望 new 数据在 VM/VLogs）+ hash；请用户授权（引用 hash）后执行。

- [ ] **Step 2: 启用 new writer 影子写**

将 ingest `TELEMETRY_WRITER_MODE` 设为 new（legacy 保留，双写），rollout。

- [ ] **Step 3: S1 成功阈值**

确认 VM 有 `call_total{tenant_id=canonical,cluster_id=canonical}`、VLogs 有 canonical log、CH legacy 仍写。任一失败 → 停止 new writer，回滚到 legacy-only。

- [ ] **Step 4: S2 影子对账**

对比 VM/VLogs/CH 同源受控数据，校验标签契约（`tenant_id`/`cluster_id`/`service_name`/`level`）。数据一致/差异可解释 → 通过；不一致 → 回滚。

- [ ] **Step 5: 记录变更（不 commit）**

追加主 Evidence 文档 Phase 20 章节（S1/S2 结果 + hash + 授权记录）。

---

## Task 3: S3 Writer 流量切换（停 legacy writer 流量）

**Files:**
- Modify: ingest deployment env（停 legacy writer）
- Run: `helm upgrade` / `kubectl set env`

**Interfaces:**
- Consumes: S2 影子对账通过。
- Produces: new writer 全量承接；legacy writer 停流量（legacy 数据保留）。

- [ ] **Step 1: S3 manifest + 授权**

生成 S3 manifest（writer_mode=new，legacy 停流量不删数据）+ hash；请用户授权后执行。

- [ ] **Step 2: 停 legacy writer 流量**

停用 legacy writer（保留 legacy 数据，不删除）。new writer 全量承接。

- [ ] **Step 3: S3 成功/停止阈值**

新写数据仅出现在 VM/VLogs，CH 不再新增（legacy 停）；写失败率 0。写失败率 > 阈值 → 回滚 writer 到 legacy+new 双写。

- [ ] **Step 4: 记录变更（不 commit）**

追加主 Evidence 文档 Phase 20 章节（S3 结果 + hash）。

---

## Task 4: S4 Reader 影子读 + S5 Reader 流量切换

**Files:**
- Modify: query-api deployment env（`QUERY_READER_MODE`）
- Run: `helm upgrade` / `kubectl set env`

**Interfaces:**
- Consumes: S3 writer 切换通过。
- Produces: new reader 启用（影子读，legacy reader 保留）；S5 切 new reader 全量；legacy reader 停流量。

- [ ] **Step 1: S4 manifest + 授权**

生成 S4 manifest（reader_mode=legacy+new_shadow，new reader 影子读）+ hash；请用户授权后执行。

- [ ] **Step 2: 启用 new reader 影子读**

`QUERY_READER_MODE=new`（legacy reader 保留）。`SourceRouter.ReaderFor` 将 logs/metrics 路由到 VM/VLogs。

- [ ] **Step 3: S4 成功阈值**

/read 返回 VM/VLogs 数据（新 SoT）；legacy reader 仍可读（影子）。读取异常 → 停 new reader。

- [ ] **Step 4: S5 manifest + 授权**

生成 S5 manifest（reader_mode=new，legacy reader 停流量不删数据）+ hash；请用户授权后执行。

- [ ] **Step 5: 停 legacy reader 流量**

legacy reader 停流量（legacy 数据保留）。new reader 全量承接。

- [ ] **Step 6: S5 语义验证（403/no_data 确定性）**

未授权（错误 tenant/cluster/capability）→ 403；已授权空 → no_data。语义测试/实测区分。任一异常 → 回滚 reader 到 legacy。

- [ ] **Step 7: 记录变更（不 commit）**

追加主 Evidence 文档 Phase 20 章节（S4/S5 结果 + hash）。

---

## Task 5: S6 观察窗口 + 回滚预案验证

**Files:**
- 无新增（运行验证）

**Interfaces:**
- Consumes: S5 通过。
- Produces: 观察期无回归；回滚路径验证可执行。

- [ ] **Step 1: 观察窗口**

观察 N 分钟（建议 15-30min）：指标/日志/查询无回归、无错误、无写失败。

- [ ] **Step 2: 回滚路径验证（dry 确认不实际回滚）**

确认回滚命令可用：`helm rollback aiops <prev_revision> -n observability` + 恢复 `QUERY_READER_MODE`/`TELEMETRY_WRITER_MODE`。验证回滚命令存在且可执行（不实际触发回滚）。

- [ ] **Step 3: 记录变更（不 commit）**

追加主 Evidence 文档 Phase 20 章节（S6 观察窗口结果 + 回滚预案）。

---

## Task 6: Gate 6 cutover 验收

**Files:**
- 无新增（验证汇总）

**Interfaces:**
- Consumes: S0-S6 全部完成。
- Produces: Gate 6 cutover 验收证据（分阶段 Go/No-Go + manifest hash + 只停流量不删数据）。

- [ ] **Step 1: 汇总每阶段授权记录**

整理 S0-S6 每阶段的 manifest hash + 用户授权记录 + 成功/停止阈值结果，形成完整 cutover 审计链。

- [ ] **Step 2: 确认 legacy 数据保留**

确认 legacy CH 数据全程未删除（TRUNCATE/DELETE 0 执行），仅停流量。

- [ ] **Step 3: 确认新 SoT 全量承接**

确认 VM/VLogs 为 logs/metrics 的 ACTIVE SoT，CH 为保留的 legacy 数据（traces/edge 仍 CH 不变）。

- [ ] **Step 4: 记录变更（不 commit）**

追加主 Evidence 文档 Phase 20 章节（Gate 6 cutover 完整证据 + 审计链）。

**Plan 3 完成标准：** 6 阶段（S0-S6）全部执行且每阶段有单独 Go/No-Go + manifest hash + 授权记录；writer 先切后 reader 切（先写后读）；legacy 数据全程保留（只停流量不删数据）；403/no_data 语义确定；回滚预案验证可执行；最终 VM/VLogs 为 logs/metrics ACTIVE SoT。
