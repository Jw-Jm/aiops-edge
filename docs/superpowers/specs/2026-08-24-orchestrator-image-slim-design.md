# AIOps ai-orchestrator 镜像瘦身 — Design Spec

```text
CONTRACT        = V9.3 FINAL ACCEPTANCE DoD #72（五镜像总大小 ≤ baseline×0.80 = 6,973MB）
BASE            = AIOPS_IMAGE_SIZE_REPORT.md（P15.7 + P20 实测：合计 ≈8,677MB，FAIL）
               + AIOPS_FINAL_ACCEPTANCE_REPORT.md（#72 FAIL，拆独立专项，需单独 Design + 授权）
VERSION         = v0.1
STATUS          = DESIGN APPROVED（2026-08-24，决策冻结：**仅做 P0，P1/P2 均不做**）
GIT_ACTION      = COMMIT（本设计随分支收口，仅文档，不触发重建/部署）
DATE            = 2026-08-24
```

本 Spec 针对唯一遗留 DoD **#72（镜像体积 ≤80% 基线）** 产出瘦身 Design。核心目标是把
orchestrator 镜像从 **8.26GB** 降到 ≤6.973GB 总目标范围内。

**决策（2026-08-24 冻结）**：**仅做 P0**（`.dockerignore` 排除误带入的开发 venv，预计
8.26GB → ≈5.6GB，已达标）。**P1（移除 torch/transformers 改 ONNX 中文 embedding）与
P2（裁剪 pyarrow/lancedb）均明确不采纳**——P0 已足够达标，P1/P2 代价高且涉及功能/精度权衡。

---

## 0. 范围与红线

- 目标镜像：`ai-orchestrator`（占五镜像合计 94.7%，8.26GB 是唯一瓶颈）。
- 目标：五镜像 `FINAL_TOTAL ≤ BASELINE×0.80 = 6,973MB`。orchestrator 需从 8.26GB 至少降到
  ≈6.8GB 以内（其余四镜像 ≈247MB 已达标）。
- 红线（继承 #72 专项边界，设计 §3）：
  - **不得为体积删 RCA/知识库运行时硬依赖的语义能力**（bge-small-zh 中文检索、Chroma 知识库）。
  - 不删除 CA/timezone/runtime libraries/WAL/health/recovery。
  - F1-F5 红线保持；不触碰其它 DoD。

---

## 1. 体积分析（本机实测，2026-08-24）

### 1.1 镜像各层体积（`docker history` + `du`/`tar` 实测）

| 层 | 体积 | 说明 |
|----|------|------|
| **COPY . .（应用 + .venv 误带入）** | **≈2.6GB** | **根因见 §1.2，零风险可消除** |
| RUN 解压 `sp.tar.gz`（site-packages） | ≈1.6GB | 含 torch 549MB 等，见 §1.3 |
| `COPY bin/sp.tar.gz` | 423MB | 压缩包层 |
| RUN 解压 hf + chroma 模型缓存 | 271MB | bge-small-zh + onnx 模型 |
| `COPY bin/k8sgpt` | 116MB | 运行时 k8sgpt analyze 需要 |
| `COPY bin/chroma.tar.gz` | 166MB | 压缩包层 |
| apt 系统工具 | 166MB | curl/git/procps/iproute2 等 |
| `COPY bin/kubectl` | 56MB | 运行时需要 |
| `COPY bin/hf.tar.gz` | 56MB | 压缩包层 |
| base `python:3.12-slim` | ≈44MB | 基准 |

### 1.2 【根因】`COPY . .` 误带入开发虚拟环境（零风险项）

- `.dockerignore` 只排除 `.venv`/`venv`，**未排除项目实际使用的 `.venv-312`（1.7GB）和
  `.venv312`（1.7GB）**。
- 两个 venv 合计 **≈3.4GB**，被 `COPY . .` 带入镜像 → 对应 COPY 层 **≈2.6GB**。
- 这是**构建缺陷**，非功能依赖：venv 是纯开发产物，绝不应进运行时镜像。

### 1.3 site-packages 体积构成（`sp.tar.gz` 解压后，>20MB）

| 包 | 解压体积 | 是否 orchestrator 硬依赖 |
|----|---------|--------------------------|
| **torch** | **549MB** | ✗（仅 rag.py 中文 embedding 间接依赖） |
| pyarrow | 136MB | 待核实（chromadb/内存表类依赖） |
| lancedb | 100MB | 待核实（chromadb 1.x 依赖） |
| scipy | 86MB | chromadb/sklearn 间接依赖 |
| chromadb_rust_bindings | 46MB | ✓ chromadb 核心 |
| transformers | 45MB | ✗（sentence-transformers 依赖，随 torch 方案） |
| onnxruntime | 45MB | ✓ chromadb ONNX embedding 需要 |
| kubernetes | 38MB | ✓ 运行时 K8s 交互 |
| sklearn | 38MB | chromadb/sklearn 依赖 |
| scipy.libs / numpy.libs | 57MB | 间接依赖 |
| sympy / numpy / zstandard | 71MB | 间接依赖 |
| **site-packages 合计** | **≈1,446MB** | — |

**关键判定**：`chromadb-1.1.1.dist-info/METADATA` 的 `Requires-Dist` **不含 torch、不含
sentence-transformers**（只依赖 onnxruntime/tokenizers/kubernetes/numpy 等）。因此：

- **torch（549MB）+ transformers（45MB）是 `requirements.txt` 显式声明
  `sentence-transformers>=3.0` 引入的额外依赖**，并非 Chroma 知识库硬依赖。
- 代码中对 ST/torch 的唯一使用点是 `rag.py` 的
  `SentenceTransformerEmbeddingFunction("BAAI/bge-small-zh-v1.5")`（中文 embedding）。
- `rag.py` 已内置 ONNX 降级路径（`ONNXMiniLM_L6_V2`，走 onnxruntime，**不依赖 torch**）。

---

## 2. 瘦身方案（分层，按风险/收益排序）

### P0 层 — 零功能风险，立即可做（预计 -2.6GB）

**修 `.dockerignore`**，排除 `.venv-312`/`.venv312`（或泛化为 `.*venv*`）：

```dockerignore
# 现有条目之外追加
.venv-312
.venv312
```

- **收益**：`COPY . .` 层从 ≈2.6GB → 应用本体（≈几十 MB）。orchestrator 镜像 8.26GB → **≈5.6GB**。
- **风险**：无功能风险。venv 为纯开发产物，不影响运行时。`COPY bin/*.tar.gz`（显式 COPY）不受影响。
- **验证**：重建镜像后 `docker history` 确认 COPY 层收缩；`python3 -c "import main"` 正常；全量测试通过。

> P0 后 orchestrator ≈5.6GB，**已低于目标阈值**，理论上 #72 可达标（五镜像合计
> ≈5.6 + 0.25 = ≈5.85GB ≤ 6,973MB）。

### P1 层 — 涉及功能权衡，**明确不采纳**（2026-08-24 决策）

**评估移除 torch + transformers + sentence-transformers（≈594MB）**，改用纯 ONNX 中文 embedding。

**实测兼容性判定（2026-08-24）——P1 硬伤，导致不采纳**：

- 当前 `bge-small-zh`（ST/torch 路径）embedding 维度 = **512**。
- chroma 内置 `ONNXMiniLM_L6_V2` embedding 维度 = **384**。
- chromadb 的 ONNX embedding 类（`ONNXMiniLM_L6_V2`）**固定英文模型，不支持加载自定义 ONNX**，
  且内置无中文 ONNX embedding。
- → 维度不同（512 vs 384）是**硬性不兼容**：切到 384 维 ONNX 后，查询向量与已持久化的
  512 维向量维度不匹配，Chroma 直接报错，**现有 ops_cases / ops_playbooks 检索整体失效**，
  而非仅"精度下降"。要修复必须**重建整个知识库向量**（重新嵌入全部案例），并自研
  ONNX 中文 embedding 函数（onnxruntime 加载 bge-small-zh ONNX 量化版）+ 打包新模型资产。

**结论（决策：不做 P1）**：

- **收益不必要**：P0 已把镜像降至 ≈5.6GB，**已达成 DoD #72（≤6.8GB）**，P1 的 594MB 对达标
  无增量价值。
- **代价过高**：向量库重建 + 中文检索精度风险（MiniLM 英文为主，中文故障词召回变差）+
  自研 ONNX 中文 embedding 开发 + 模型资产维护。
- **违反红线**：为体积牺牲中文 RAG 语义能力，与 #72 专项红线"不得为体积删 RCA/知识库
  运行时硬依赖语义能力"冲突。
- **保留现状**：torch + sentence-transformers + bge-small-zh（512 维中文 embedding）作为
  RCA / 知识库的运行时硬依赖，**完整保留**。

### P2 层 — 低收益/需进一步核实（不建议本期做）

- pyarrow（136MB）+ lancedb（100MB）：chromadb 1.x 依赖，需确认是否可裁剪（可能影响 chroma
  内部索引）。收益不确定，风险高，**明确排除（不采纳）**。
- k8sgpt（116MB）/ kubectl（56MB）：运行时工具，**保留**。

---

## 3. Gate 判定标准（PASS 条件）

**决策（2026-08-24）**：**仅做 P0，P1/P2 均不做**。P0 已足够达成 DoD #72，无需进一步瘦身。

1. **P0 落地**：`.dockerignore` 修复 + orchestrator 镜像重建实测 **≤6.8GB**（或五镜像合计 ≤6,973MB）。
2. 镜像启动：`import main` + readiness `/health` OK；`uvicorn` 正常拉起。
3. 回归：orchestrator **全量 pytest 通过**（无 collect error）；RCA / RAG / Chat 主路径 smoke 通过。
4. 真实环境：`observability` 命名空间 rollout 新镜像，query-api/前端联通正常；RAG 检索返回非空。
5. 五镜像 `docker images` 实测合计满足 ≤80% 基线。
6. **P1/P2 明确不执行**：torch / sentence-transformers / bge-small-zh（512 维中文 embedding）
   作为 RCA / 知识库运行时硬依赖完整保留；pyarrow / lancedb / k8sgpt / kubectl 均保留。

→ 全部 PASS 后：DoD #72 = PASS，`AIOPS_AGENTIC_REFACTOR_COMPLETE` 满足。

## 4. 边界与最后确认（决策冻结）

- **仅 P0**：`.dockerignore` 排除开发 venv（`.venv-312`/`.venv312`）。这是构建缺陷修复，零功能风险。
- **P1 不采纳**：不移除 torch/transformers。实测 512(中文 bge) vs 384(ONNX) 维度硬不兼容，
  切 ONNX 需重建向量库 + 中文精度下降 + 自研 ONNX 中文 embedding，代价远超达标所需的体积收益。
- **P2 不采纳**：不裁剪 pyarrow/lancedb（chromadb 依赖，风险高收益低）。
- 不引入 Vault / 不改 RCA 引擎 / 不删运行时工具（k8sgpt/kubectl）。
- 未触碰其它 DoD / 红线 F1-F5。

## 5. 交付物

- 代码：`.dockerignore` 追加 `.venv-312`/`.venv312`（P0，唯一实施项）。
- 测试：P0 后重建镜像 + 全量回归 + RAG smoke（验证 bge-small-zh 中文检索正常）。
- 文档：本 Design + 执行后 Evidence（镜像实测、回归、Gate 判定）。
- Git：本 Spec 随分支收口提交。
