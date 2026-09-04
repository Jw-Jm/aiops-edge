# Phase 4 交付记录：最终生产验证（2026-09-03/04 进行中）

> 依据最终报告 §24 Phase 4。当前 main = `43cefc9`（含 PR#3/PR#4 缺陷修复）。本记录覆盖已完成项；破坏性深测与 publishable 判定见 §7。

## 1. clean build（P4-1）✅

- `IMAGE_TAG=git-da9e85b deploy/scripts/build-images.sh all` 构建 **12 组件镜像**全部成功（查询/摄入/采集/编排/investigation-worker(复用 ai-orchestrator)/frontend/executor/broker/proxy/migrator ×3/ipmi）。
- 途中 daocloud 源 `golang:1.26.6-alpine` 连续 EOF → 从 docker.io 拉取并本地 tag 同形引用，重试成功（frontend 层缓存命中）。
- 镜像 config digest 收集并用于生产 render（见下）。

## 2. image digest（P4-2）✅ + SBOM/SCA（CI）

- 本地镜像 config digest 已收集（12 个 `sha256:<64hex>`）。
- 生产 Helm render 断言（真实 digest pin）：**14 处 `@sha256` 引用、0 mutable tag**（`deployments=10, statefulsets=3`）。
- SCA/SBOM：由 CI `supply-chain-tests` job 持续门禁（PR#2/#3/#4 均 pass）。registry digest 需 push 后由 release system 解析（本候选未推送 registry）。

## 3. Helm prod digest render（P4-3）✅

见上。渲染产物 `/tmp/p4-render.yaml`（14 digest refs / 0 tag refs）。

## 4. candidate deploy（P4-4）✅（rev 63，含 2 个缺陷修复）

- 候选集群升级至 `git-da9e85b`，release revision 59→63，全部 11 Deployments Ready。
- **缺陷 #1（运行阻断）**：P2-SEC 基线 `drop: ALL` 使 frontend nginx root-master 无法 `chown(/var/cache/nginx/...)`（nginx 需要 CHOWN/SETUID/SETGID）→ 每次新 rollout frontend CrashLoopBackOff。修复：chart 保留 drop-ALL 并显式 add 三能力 → **PR #3 merged (b6d1329)**。
- **环境（非代码）**：local-validation 自签 mTLS（2 天有效期）当日过期导致全部内部 TLS/探针失败。轮换为 30 天证书（CN=aiops-local-internal，SAN 12 DNS + 127.0.0.1）后升级成功。已纳入运维注意（证书 ≤2d 有效期不适用于长期候选）。

## 5. migration（P4-5）✅ Gate 4 PASS（真实 MySQL + MinIO）

- `phase4-gate.sh`（P4.9 Gate 4）在真实 MySQL 8（13306）+ MinIO（19000）上 **PASS**：
  - A/B/C：`TestMigratedSchemaCoversLegacyEnsureSchema`（空初始化/幂等/legacy 列覆盖）真库 PASS；
  - D 受限账号：SKIP（容器无 aiops_app，P4.8 已证）；
  - G Object Store：bucket bootstrap 幂等 + readiness PASS。
- **缺陷 #2（真实缺口）**：Gate A/B/C 首次真跑暴露 authoritative baseline `auth_sessions` 缺 legacy 的 `updated_at` 列 → 新增迁移 **0018** → **PR #4 merged (43cefc9)**。修复后 coverage + `internal/store/...` 真库全套 PASS。

## 6. 故障注入 / 自愈 / 深测（P4-6）✅（2026-09-04）

- **pod delete（驱逐语义）**：删 1/2 `ai-investigation-worker` pod → ReplicaSet 25s 重建 **2/2 Ready**。
- **kill -9 容器级**：对 worker 主进程 `kill -9 1` **未触发**（容器以非 root 65532 运行 + drop-ALL 无 CAP_KILL——安全加固符合预期）。结论：容器内 SIGKILL 不可行是 drop-ALL 的预期副作用；以 pod delete 作为强终止/恢复验证。
- **helm rollback**：rev 65 → rollback rev 62（旧 chart + git-b387869 镜像，DB schema 已领先含 0018）→ 全部关键组件正常 Running，**rollback 可用、schema 领先未阻断旧代码启动**；随后 upgrade 回最终（rev 67，git-7b82fd7 + 0018 + frontend caps）。
- **backup/restore 演练**：mysqldump 生产 `aiops`（71MB，--single-transaction）→ 恢复到隔离库 `aiops_restore_verify` → 行数一致（users 1=1、auth_sessions 34=34）→ 清理验证库。**备份可恢复**。
- **real observability evidence**：query-api `/metrics`（HTTPS）返回 `aiops_build_info{service="query-api",go_version="go1.26.6"}`；orchestrator 日志 `/metrics`/`/health`/`/readyz` 200；query-api log-shipper 实际搬运日志（198/175 logs，22-23 pods）。VictoriaMetrics up=3（候选 deepflow disabled，自研 scrape 另配）。
- **节点驱逐**：候选为单节点，节点驱逐=整集群宕机，**SKIP**（需多节点候选）。
- **RCA/Action/Approval/Reconcile 实链**：候选 `llmMock=true` 且无真实 provider key；正式链路正确性由 workflow-contract Go 真 MySQL 测试（9/9，14 场景）作为发布门禁。真实 LLM E2E 触发建议在正式候选窗口执行。

## 7. 未完成 / 待判定

- release manifest（P4-7）：`deploy/evidence/release-evidence.json` 已重新生成绑定最终 commit（commands 全部 exit 0，**publishable=false** 为正确初态）。
- `publishable=true` 需满足：最终 clean commit 重新 collect（dirty=false）+ real observability 正式采集策略 + 多节点候选（节点驱逐）+ 真实 LLM 链路 RCA/Action/Approval/Reconcile evidence。
- 候选集群运行的是 local 定制 values（`environment: local`、llmMock、digest 非强制）——正式 production manifest 需 release system 注入真实 registry digest + 真实 secret。
- **运维提醒**：local-validation mTLS 自签证书仅 2 天有效期（曾于 2026-09-03 过期阻断全部 mTLS），建议调长有效期或接 cert-manager。
