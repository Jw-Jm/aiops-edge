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

## 6. 故障注入 / 自愈（P4-6 部分）✅

- 候选集群删除 1/2 `ai-investigation-worker` pod（=pod delete 驱逐语义）→ ReplicaSet 25s 内重建，**2/2 Ready**。
- kill -9 容器级 / 节点驱逐 / 备份恢复 / rollback 深测：见 §7 未完成（建议在正式候选窗口执行，含迁移前后备份对比）。

## 7. 未完成 / 待判定

- release manifest（P4-7）：`collect-release-evidence.sh` 已生成 `deploy/evidence/release-evidence.json`（git_commit=43cefc9，commands 全部 exit 0，**publishable=false** 为正确初态）。
- `publishable=true` 需满足：最终 clean commit 重新 collect（dirty=false）+ 破坏性深测证据（rollback、backup/restore、kill-9、节点驱逐）+ real observability evidence（当前候选 revision 63 运行中，metrics/日志证据可另采集）。
- 候选集群运行的是 local 定制 values（`environment: local`、llmMock、digest 非强制）——正式 production manifest 需 release system 注入真实 registry digest + 真实 secret。
