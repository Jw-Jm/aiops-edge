# 拓扑页面视觉修复进展总结（2026-08-08）

> **换账号后从这里继续开发。** 本文件记录 `/topology` 页面视觉问题修复的完整进展，包括已修复的问题、当前状态、未决项和下一步建议。

## 当前状态：已部署验证

所有修复已完成并部署至 K8s `observability` 命名空间。前端镜像 `observability-frontend:latest` 已更新。

## 已修复的问题

### 1. 节点不可见（opacity: 0.12 bug）— 已修复 ✅

**根因：** `CustomTopologyNode` 的 `dimmed` 逻辑在没有 hover 任何节点时（`hoverActive=false` 且 `hoverRelated=false`），所有节点 `dimmed=true` → opacity 0.12，导致全部节点不可见。

**修复：** 在 data 中新增 `globalHovering` 标志（来自 `layoutGraph` 的 `hoveredNum != null`），dimmed 逻辑改为：
```typescript
const dimmed = data.globalHovering && !data.hoverActive && !data.hoverRelated
```

### 2. 对比度差（节点背景太暗）— 已修复 ✅

**修复：** `NODE_COLORS` 提亮至 WCAG AA 达标（service `#4f46e5`=3.34, cluster `#047857`=3.83）。常量见 `TopologyGraph.tsx` 第 40+ 行。

### 3. 平行边重叠 / 错开效果差 — 已重写 ✅

**两次迭代：**
- 第一次用 `pathOptions.offset` 平移路径 → 根部仍交叉重叠，效果差
- 第二次重写为**多 handle 平行通道**：

每个节点渲染 16 个隐藏 handle（上下各 4 source + 4 target），沿边缘均匀分布：
```typescript
const MAX_PARALLEL = 4
const handleSlot = (base: string, idx: number) => `${base}__${idx}`
```
边按 `(src→dst, direction, idx)` 选择对应 handle 槽位，从节点不同位置出发形成清晰平行通道。

## 关键文件

| 文件 | 改动 |
|------|------|
| `observability-frontend/src/components/topology/TopologyGraph.tsx` | 核心图谱组件：NODE_COLORS、dimmed 逻辑、多 handle 平行边、tier 标签、hover 高亮 |
| `observability-frontend/src/pages/Topology/index.tsx` | 页面：工具栏、摘要条、人话详情、只看异常、聚焦 |
| `observability-frontend/src/api/client.ts` | API：`topoList*`, `getTopology`, `getAlertEvents` |
| `ai-apm-query-go/internal/api/topology_graph.go` | 后端：`SyncTopologyCatalog` handler |
| `ai-apm-query-go/internal/store/mysql.go` + `topology.go` | 后端：4 表 schema + seed |

## 部署命令

```bash
cd aiops/observability-frontend
docker build --platform linux/arm64 -t observability-frontend:latest .
kubectl -n observability rollout restart deploy/frontend
kubectl -n observability rollout status deploy/frontend --timeout=60s
```

访问：`http://localhost:30253/topology`（登录 admin/admin123）

## 验证方法（agent-browser）

```bash
agent-browser open "http://localhost:30253/topology?t=$(date +%s)"
# 如需强制刷新缓存：URL 加 ?t=timestamp
agent-browser screenshot
```

检查项：
- 所有节点 opacity = 1.0（非 0.12）
- 节点背景色明亮可辨（紫/绿/橙）
- 平行边从节点不同位置出发，不重叠
- 无 hover 时所有节点正常显示

## 未决项 / 可改进

1. **平行边 label 重叠** — 多条平行边共用 `pairKey` 只显示第一条的 label，其余边无类型标注。可考虑：hover 时显示该边 label，或给每条边独立 label。
2. **MAX_PARALLEL = 4 限制** — 若某对节点间有 >4 条平行关系，超出部分会回退到第 4 槽位（重叠）。当前数据无此情况，但可动态计算最大值。
3. **节点微指标** — 仅显示错误率%，延迟/吞吐在详情页。可考虑在 hover 时显示简要指标。
4. **移动端/小屏适配** — 节点 192×86 在小屏可能拥挤。

## 下一步建议

- 若有新视觉问题，按 superpowers 流程：先 `using-superpowers` → `systematic-debugging` 排查根因 → 修复 → 部署验证
- 截图验证前务必加 `?t=timestamp` 防止浏览器缓存旧 JS bundle
