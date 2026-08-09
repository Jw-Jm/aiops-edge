# 容量预测（全维度资源预测）设计

**日期**: 2026-08-09
**性质**: 设计文档（需求已与用户对齐）
**目标**: 在 `/capacity` 页对 node 级资源（CPU/内存/磁盘/网络）做**全维度容量预测**：线性回归（最小二乘）+ EWMA 指数平滑**双算法**并列，输出历史曲线 + 预测曲线 + 预计达到阈值时间（ETT）。
**落点**: Go 侧（ai-apm-query-go）实现算法与 API，前端新增 `/capacity` 页。

---

## 0. 已对齐的关键决策

| # | 决策 | 选择 |
|---|---|---|
| 1 | 预测算法与落点 | **线性回归（最小二乘）+ EWMA 指数平滑双算法，Go 侧实现**，复用 `vmRangeQuery` |
| 2 | 预测维度 | **全维度**：CPU / 内存 / 磁盘 / 网络，每维度独立预测曲线 |
| 3 | 成熟方案调研 | GitHub 无"开箱即用"轻量方案；**最接近的是 Prometheus `predict_linear`**（内部即最小二乘线性回归做预测告警）。本设计是其 Go 侧等价实现 + 超越（画完整预测曲线、双算法对比、ETT）。Facebook prophet 对 node 级小数据过重，不采用 |
| 4 | 预计达阈值时间（ETT） | 需实现（PromQL `predict_linear` 只算单点告警，不能画曲线/给 ETT） |
| 5 | 环比趋势提示 | **加上**：展示"当前值较 N 小时前上升/下降百分比" |

---

## 1. 现状与差距（代码实际）

| 模块 | 现状 | 差距 |
|---|---|---|
| **历史查询层** | `vmRangeQuery`（`internal/api/victoriametrics.go`）已实现：打 `/api/v1/query_range`，返回 `[]float64` 平铺序列，自动 URL 转义 | 可直接复用 |
| **资源指标 PromQL** | `devices.go` 已含 node-exporter 模板：`node_cpu_seconds_total`/`node_memory_MemAvailable_bytes`/`node_filesystem_*`/`node_network_*`（即时查询 `vmInstantQuery`） | 缺 range 版 + 磁盘使用率补全 + 网络带宽聚合 |
| **预测算法** | **无**。只有 zscore/MAD 统计检测（`anomaly.go`）+ 简单线性偏差的伪 forecast（`alerts.go`），无真正回归/平滑 | 需新写（纯标准库，项目无数学库） |
| **API 路由** | `main.go` 用 `http.NewServeMux` + `mux.HandleFunc`，响应用 `respondJSON`/`respondError` | 需新增 `/api/v1/capacity/forecast` |
| **前端** | `client.ts`（axios 封装+内联 interface）、`App.tsx`（menuGroups + Route + lazy）、Monitor 页 echarts 模式 | 需新增 `getCapacityForecast` + `/capacity` 路由/菜单/页 |

---

## 2. 范围

### 2.1 后端预测算法（Go 纯标准库）

新增 `internal/api/capacity.go`，三个纯函数 + 一个 handler：

**① 线性回归（最小二乘）**
```go
// LinearRegression 对 (x=0..n-1, y) 拟合 y = a + b*x，返回 (slope, intercept)
func LinearRegression(series []float64) (slope, intercept float64)
```
- 公式：`b = (n*Σxy - Σx*Σy) / (n*Σx² - (Σx)²)`，`a = (Σy - b*Σx) / n`，`n = len(series)`
- 边界：`n < 2` 返回 (0, y0)；分母为 0 返回 (0, mean)
- 预测未来第 k 步：`y = a + b*(n-1+k)`

**② EWMA 指数平滑**
```go
// EWMA 对序列做指数加权平滑，返回平滑后序列（长度同输入）。
// s[0]=y[0]，s[t]=alpha*y[t] + (1-alpha)*s[t-1]
func EWMA(series []float64, alpha float64) []float64
```
- alpha 默认 0.3，`alpha<=0||alpha>1` 时回退 0.3
- 外推：取平滑序列末段（如最后 1/3）做线性回归斜率，从平滑序列末值沿斜率外推 horizon 步

**③ 预计达到阈值时间 ETT**
```go
// EstimateTimeToThreshold 沿线性回归预测曲线求 y>=threshold 的最早未来步数 k（k>=1）。
// 返回 (k, ok)。ok=false 表示预测期内不会达到（含已超阈值情形由调用方区分）。
func EstimateTimeToThreshold(slope, intercept float64, n, horizon int, threshold float64) (int, bool)
```
- 已超（当前值>=threshold）→ 调用方标记 `already_breached`，返回 false
- 预测期内 k<=horizon 达到 → (k, true)
- 未达到 → (false)

### 2.2 后端预测 API

**路由**（`main.go` 新增）：
```go
mux.HandleFunc("/api/v1/capacity/forecast", handler.CapacityForecast)
```

**请求参数**（query string）：

| 参数 | 说明 | 默认 |
|---|---|---|
| `metric` | cpu \| memory \| disk \| network | 必填 |
| `instance` | node 实例（label filter） | 缺省取首个匹配 |
| `hours` | 历史回看窗口（小时） | 24 |
| `step` | 采样步长（秒） | 300 |
| `horizon` | 未来预测数据点数 | 12 |
| `threshold` | 目标阈值；对 cpu/memory/disk 为百分比(0-100)，对 network 为 bps | cpu/memory/disk 默认 80，network 必填 |

**PromQL 映射**（`capacityPromQL(metric) string`，复用 `devices.go` 思路改 range 版）：
```go
// cpu:    100 - avg(rate(node_cpu_seconds_total{mode="idle"}[5m])) * 100
// memory: 100 * (1 - node_memory_MemAvailable_bytes / node_memory_MemTotal_bytes)
// disk:   avg(1 - node_filesystem_avail_bytes / node_filesystem_size_bytes) * 100
// network:rate(node_network_receive_bytes_total[5m]) + rate(node_network_transmit_bytes_total[5m])
```
- `instance` 传入时拼 `{instance="..."}` label filter
- 用 `vmRangeQuery(promQL, start, end, step)` 拉历史 `[]float64`

**响应 JSON**（`respondJSON`，遵循现有无统一 code 包装）：
```json
{
  "metric": "cpu",
  "instance": "node-1",
  "threshold": 80,
  "current": 45.2,
  "change_pct": 15.3,
  "timestamps": [1700000000, 1700000300, ...],
  "history": [30, 35, ...],
  "forecasts": {
    "linear": { "values": [46, 48, ...], "ett_seconds": 86400, "within_horizon": true, "already_breached": false },
    "ewma":   { "values": [45, 46, ...], "ett_seconds": 90000, "within_horizon": true, "already_breached": false }
  }
}
```
- `timestamps` 为等步长时间戳序列，**覆盖历史 + 预测完整范围**，长度 = `n + horizon` 个点：第 i 点 = `start + i*step`（i=0..n+horizon-1）。`history` 对应前 n 个点，`forecasts.*.values` 对应第 n 个点起（`n-1+1` 到 `n-1+horizon`，即未来 horizon 个点）。前端用同一 `timestamps` 数组渲染三组曲线
- `ett_seconds = k * step`（k 为达到阈值的未来步数）
- `change_pct`：`(current - history[0]) / history[0] * 100`（环比趋势）
- 错误：参数非法 → `respondError(w, 400, ...)`；`vmRangeQuery` 失败 → 500

### 2.3 前端 `/capacity` 页

- **client.ts**：新增 `CapacityForecast`、`ForecastSeries` 等 interface + `getCapacityForecast(params)`
- **App.tsx**：新增 lazy import `<Capacity />` + `<Route path="/capacity">` + `menuGroups` 增加「容量预测」项（图标 `LineChartOutlined`）
- **页面** `pages/Capacity/index.tsx`：
  - 顶部维度切换 tab（CPU/内存/磁盘/网络）
  - 每个维度用 `echarts-for-react` 渲染一张图（参照 Monitor `buildOption` 模式）：历史实线 + 线性预测线 + EWMA 预测虚线 + 阈值 `markLine`
  - 每维度卡片显示：当前值、环比趋势（↑/→/↓ + 百分比）、**预计达阈值时间**（如"约 3.2 天后" / "预测期内不会达到" / "已超过阈值"）
  - 历史窗口（hours）+ 预测长度（horizon）可选控件

---

## 3. 测试（TDD）

- **算法单测**（`capacity_test.go`）：
  - `LinearRegression`：合成 `y = 2x+1` 序列断言 slope≈2、intercept≈1；n<2、常数列边界
  - `EWMA`：常数列输出不变、单调上升序列平滑收敛、alpha 越界回退
  - `EstimateTimeToThreshold`：斜率正达到/预测期内未达/已超阈值三情形
- **Handler 单测**：mock `vmRangeQuery` 固定序列，断言响应 JSON 结构、参数校验（缺 metric、network 缺 threshold → 400）
- **前端**：tsc 通过 + 手动验证渲染

## 4. 数据/合规

- 全自研，不复制 ongrid；组件最小化（纯标准库算法、不引数学库、前端复用 echarts）
- 只读预测，不触发任何变更动作
- 指标来源：node-exporter（已采集）+ VictoriaMetrics

## 5. 自审

- [x] 全维度（CPU/内存/磁盘/网络）覆盖
- [x] 双算法（线性回归 + EWMA）覆盖
- [x] ETT + 环比趋势 + 阈值 markLine 覆盖
- [x] 复用 `vmRangeQuery` + `devices.go` PromQL + echarts，组件最小化
- [x] 算法/边界/参数校验均有单测
