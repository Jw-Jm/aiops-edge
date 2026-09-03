# P1-SCA1 交付记录：供应链漏洞门禁（2026-09-03）

> 整改分支 `remediation/p1-release-blockers-20260903`。依据审核报告 §10。

## 1. 修改文件清单

| 文件 | 变更 |
|---|---|
| `deploy/scripts/dependency-sca.sh` | **新增**：SCA 门禁（npm production critical/high=0 阻断 + pip-audit + govulncheck；工具缺失=fail 非 skip；无 `\|\| true` 掩盖） |
| `deploy/scripts/sca-allowlist.txt` | **新增**：可审计例外清单（ID/expiry/理由，过期即失效） |
| `ai-apm-ingest-go/go.mod`、`go.sum` | grpc v1.74.2 → **v1.82.1**（protobuf/genproto 连带升级） |
| 6 个 Go 仓库 `go.mod` | 新增 `toolchain go1.26.6` |
| 8 个 Dockerfile | golang 基础镜像 1.23/1.25-alpine → **1.26.6-alpine**（保留 daocloud 国内源） |
| `.github/workflows/aiops-workflow-gates.yml` | 新增 `supply-chain-tests` job 并纳入 release-gate needs；setup-go 1.25.x→1.26.x |
| `docs/remediation/2026-09-03/phase1-p1sca1/SCA_TRIAGE_LEDGER.md` | **新增**：完整 triage 台账（含依赖路径/可达性/修复/例外） |

## 2. Triage 结果（npm）

| 范围 | critical | high | moderate |
|---|---|---|---|
| 全量（含 dev） | 1（vitest RCE，dev-only） | 2（nanoid/vite，dev-only） | 6 |
| **仅生产（--omit=dev）** | **0** | **0** | 3（echarts/react-router 系，major 才能修） |

- 门禁标准（production critical/high=0）**满足**。
- 生产 moderate ×3：书面登记 + exception_expiry=2026-12-31，修复需 major 升级（react-router 6→7、echarts 5→6），列入后续前端治理（含功能回归）。
- dev 链 critical/high 不进生产 bundle，登记台账。
- `npm audit fix`（非 force）已执行：无可安全自动修复项。

## 3. Python（pip-audit）

- `chromadb 1.5.9`：4 个已知漏洞（PYSEC-2026-311、CVE-2026-45830/31/33）。**上游无修复版本**（1.5.9 已是最新发布版）。
- 可达性：全部针对 Chroma **REST server 模式**；本项目 in-process 嵌入（RAG 知识库，PVC），不暴露 Chroma server，egress NetworkPolicy default-deny → 不可达。
- 处置：`sca-allowlist.txt` 书面例外（expiry=2026-12-31，含缓解说明），到期门禁自动失效重红。

## 4. Go（govulncheck）——发现即修复（9 个可达漏洞）

| 仓库 | 修复前 | 修复 |
|---|---|---|
| ai-apm-query-go | 6 个可达（net/url、crypto/tls、net/http、encoding/asn1，标准库） | toolchain 1.26.6 → **0 可达** |
| ai-action-executor | 6 个可达（同上） | 同上 → **0 可达** |
| ai-apm-ingest-go | grpc GO-2026-6061（xDS RBAC/HTTP2） | grpc v1.74.2→**v1.82.1** + toolchain → **0 可达** |
| credential-broker / egress-proxy / event-collector | — | toolchain → **No vulnerabilities** |

标准库漏洞（Fixed in go1.26.5/1.26.6）经 toolchain 指令 + Dockerfile 基础镜像同步修复。

## 5. 验证命令与结果

```bash
npm audit --registry=https://registry.npmjs.org/ --omit=dev --json
# → critical=0 high=0

pip-audit --disable-pip --no-deps --format json -r ai-orchestrator/requirements-lock.txt
# → chromadb×4（allowlist 后通过）

govulncheck ./...（6 仓库）
# → 全部 "No vulnerabilities found / Your code is affected by 0 vulnerabilities"

go test ./... -count=1（6 仓库，升级依赖后回归）  # → 全 PASS

bash deploy/scripts/dependency-sca.sh             # → exit=0 全绿
```

## 6. 架构契约 / 兼容性

- 无架构契约变更。grpc v1.82.1（ingest 经 go mod tidy 全量回归 PASS）；toolchain 1.26.6 向后兼容 go.mod 的 `go 1.2x` 指令。
- CI：`supply-chain-tests` job 阻断 release-gate；扫描器缺失 = fail（不静默跳过）。
- Dockerfile 基础镜像升级在 P1-REL1 阶段统一重建镜像生效（代码/配置层面已完成）。

## 7. 关闭结论：CLOSED（本项范围）

- ✅ critical/high triage 完成并有台账；
- ✅ production critical/high = 0 门禁落地（npm）；
- ✅ Python/Go 扫描接入（镜像 Trivy/SBOM 绑定 image digest 部分 → P1-REL1/P1-SUP2 阶段 production-image job）；
- ✅ 可达 Go 漏洞全部修复；
- ✅ chromadb 无修复版漏洞走正式例外（可审计、未过期）。
