# AIOps 代码与依赖清理报告（Phase 21 P21.4）

Status: **REPORT**（量化删除 + 依赖变化 + 安全能力未因瘦身丢失）
Date: 2026-08-23
GIT_ACTION: NONE

## 一、代码删除（量化）

### P14（删旧，Legacy Removal，选项 D 依赖倒置）
| 项 | 删除内容 | 量化 |
|----|---------|------|
| orchestrator | `/api/v1/ai/flows/{key}/run-legacy` 端点（main.py ai_flow_run） | 1 端点 |
| ingest-go | `internal/clickhouse/` 全包（6 文件）+ main.go legacy gate（newLegacyWriters/legacyWriterEnabledFromEnv/validateLegacyGate）+ healthCache CH 探针 + Helm LEGACY_WRITER_ENABLED 注入/values 字段 | 6 文件 + 3 函数 + 配置 |
| frontend | `/ai/workflows*`、`/ai/tools`、`/knowledge`、`/kg`、`/slo` 5 页面 + `api/workflows.ts`/`api/marketplace.ts` 2 API 模块 | 5 页面 + 2 模块 |
| deploy | legacyWriterEnabled 字段清理 | 配置 |

### P12（前端收敛）
- 六大导航去顶层 AI Chat/Tool/Workflow/Graph（保留路由 deep-link 非顶层）。

### P20（缺陷收口）
- `investigation_state.Hypothesis` 平行 dataclass 删除 → 组合权威 `contracts.Hypothesis`。
- 前端移除 DEMO_RUNS/DEMO_DETAIL 占位（真实数据源 + 空态）。

## 二、依赖变化

### P15（依赖精简）
| 对象 | 变化 | 量化 |
|------|------|------|
| frontend | 移除 7 个死依赖（html2canvas/react-grid-layout/@dagrejs/dagre/@xyflow/react/@antv/g6/@xterm/xterm/@xterm/addon-fit + @types/react-grid-layout） | `npm install` 移除 **117 个传递包** |
| orchestrator | requirements.txt 仅 runtime 依赖；新增 requirements-dev.txt 规范 dev/test 分离 | 无死依赖 |
| Go 服务 | Dockerfile 加 `-trimpath -ldflags="-s -w"` | 去除符号表/DWARF |

### 安全能力未因瘦身丢失（合同要求）
- **移除的都是死依赖/未使用路径**，非安全组件：
  - legacy CH writer 是已 cutover 到 new SoT 的废弃路径（VM/VLogs ACTIVE），删除不丢失数据写能力。
  - 前端死依赖（g6/dagre/xyflow 图库等）未被任何活跃页面使用，移除不影响功能。
  - orchestrator runtime 库（torch/chromadb/sentence-transformers）**保留**（RCA/知识库运行时硬依赖，非瘦身对象）。
- **安全能力保持**：RBAC 集群写权限撤销（orchestrator 只读）；无 DB 凭据直连；Agent 无 execute/credential/kubeconfig；红线 F1-F5 保持。
- **验证**：删除后全量测试 PASS（P20 本轮窗口 orchestrator 335 + 3 Go PASS + frontend tsc exit 0），无功能回归。

## 三、镜像 baseline/final/delta

见 `AIOPS_IMAGE_SIZE_REPORT.md`（P15 报告）+ P20 本轮实测：

| 镜像 | Baseline | P20 Final | Δ |
|------|----------|-----------|---|
| ai-orchestrator | 8.25 GB | 8.26 GB | ~0%（未瘦身，独立专项）|
| query-api | 106 MB | 99.9 MB | -5.8% |
| observability-frontend | 85.6 MB | 81.1 MB | -5.3% |
| event-collector | 39.9 MB | 35.3 MB | -11.5% |
| ingest-pipeline | 30.9 MB | 25.4 MB | -17.8% |
| **合计** | **8,716 MB** | **≈8,677 MB** | **-0.45%** |

> orchestrator 8.26GB 占 95.2% 主导；80% 目标（6,973MB）未达成，瓶颈为 orchestrator 瘦身专项。

## 四、结论
- 代码/依赖清理量化完成；安全能力未因瘦身丢失（只删死依赖/废弃路径，runtime 安全组件保留）。
- GIT_ACTION=NONE。
