# P1-SCA1 漏洞 Triage 台账（2026-09-03）

> 数据来源：`npm audit --registry=https://registry.npmjs.org/ --json`（含 dev）与 `--omit=dev --json`（仅生产）。
> 门禁标准（报告 §10.2）：**production critical = 0 且 high = 0**，否则阻断（例外需书面 risk acceptance + 失效日期，CI 读 allowlist，禁 `|| true`）。

## 总量

| 范围 | critical | high | moderate | 结论 |
|---|---|---|---|---|
| 全量（含 dev） | 1 | 2 | 6 | dev 工具链未 triage 前不可作为发布依据 |
| **仅生产依赖（--omit=dev）** | **0** | **0** | 3 | **达到门禁标准** |

## 生产依赖漏洞台账（moderate，不阻断但必须记录）

| 包 | 版本 | 漏洞 | 依赖路径 | 生产可达 | 修复 | 处置 |
|---|---|---|---|---|---|---|
| echarts | ^5.5.0 | XSS（Apache ECharts） | direct | 是（前端图表渲染层；仅当渲染不可信数据时可利用） | 仅 6.1.0（**major**，API breaking） | **例外保留**：需前端功能回归的 major 升级，单独任务；登记 exception_expiry=2026-12-31，到期必须升级或续批 |
| react-router | 传递（经 react-router-dom） | open redirect via backslash（CVE-2025-68470 bypass）+ constructor injection（SSR） | react-router-dom@^6.28.0 | 是（路由层） | 7.18.3（**major**，6→7） | **例外保留**：同上，与 react-router-dom 一并升级；exception_expiry=2026-12-31 |
| react-router-dom | ^6.28.0 | open redirect leading to XSS | direct | 是（路由层） | 7.18.3（major） | 同上 |

**允许清单（CI allowlist）**：当前 production critical/high = 0，无需例外条目。上表 3 项 moderate 如未来被门禁升级覆盖，需先在 `deploy/scripts/sca-allowlist.txt` 登记并给出 expiry。

## Dev 工具链漏洞台账（不进生产 bundle，不阻断）

| 包 | 严重度 | 漏洞 | 生产可达 | 修复 |
|---|---|---|---|---|
| vitest ≤3.2.5（devDep ^2.1.8） | **critical** | Vitest API server RCE（访问恶意网站时） | **否**（仅本地测试进程；生产 bundle 不含 vitest） | 3.2.6+（major 2→3） |
| vite ≤6.4.2（devDep ^6.0.0） | **high** | 优化依赖 path traversal；launch-editor NTLMv2 hash 泄漏（Windows） | **否**（构建期工具） | vite 6.4.3+/7（部分 major） |
| nanoid <3.3.18 | high | 自定义 generator 死循环 | **否**（仅 dev 树可达，--omit=dev 结果 high=0 证实） | 3.3.18（传递依赖版本锁定） |
| esbuild ≤0.24.2 | moderate | 开发服务器跨站读响应 | 否 | 随 vite 升级 |
| @vitest/mocker / vite-node | moderate | 随 vite | 否 | 随 vitest/vite 升级 |

dev 链修复（vitest 3 / vite 7 major）列入后续治理，不影响生产发布门禁。

## Triage 结论

1. **CI 阻断门禁**：`npm audit --omit=dev` 的 critical/high 计数 = 0 → PASS（门禁实现见 `deploy/scripts/dependency-sca.sh` + workflow `supply-chain-tests` job）。
2. 生产 moderate ×3 已书面登记（含可达性分析与 expiry），不构成 §10.2 阻断条件。
3. `npm audit fix`（非 force）已执行：无可安全自动修复项（剩余全部需 major）。

## Python / Go / 镜像扫描（报告 §10.3）

- Python：`pip-audit`（接入 supply-chain job，对 requirements-lock.txt）。
- Go：`govulncheck ./...`（query-go / executor 等）。
- 镜像：Trivy/Grype + SBOM（Syft）——依赖 production-image-build job（P1-SUP2/P1-REL1 阶段接入，扫描结果绑定最终 image digest）。
