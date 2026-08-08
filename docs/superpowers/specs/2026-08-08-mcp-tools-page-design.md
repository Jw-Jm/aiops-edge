# MCP 工具目录 + 调用测试页

**日期**: 2026-08-08
**范围**: observability-frontend（React，纯前端）+ 复用 ai-orchestrator 现有 MCP 接口
**驱动**: 仿照 ongrid MCP 页
**定位**: 轻量化——当前自研 MCP 为本地工具集，本次只做工具目录 + 调用测试页，**不新增后端/表**；扩展点留待后续（接远程 server 时在后端注册机制加适配器，前端零改动）

---

## 1. 决策汇总（已与用户确认）

| 项 | 决策 |
|----|------|
| 本次范围 | 只做前端 MCP 页：**工具目录 + 调用测试** |
| 后端 | **零新增**，复用现有 `/api/v1/mcp/tools` + `/api/v1/mcp/call` |
| 扩展点 | 留待后续：后端 `mcp_server.py` 注册机制加"本地/远程工具源适配器"，前端无需改 |
| 不含 | server 注册 CRUD、认证凭据库、远程 server 管理 |

---

## 2. 现有后端接口（复用）

| 端点 | 方法 | 功能 |
|------|------|------|
| `/api/v1/mcp/tools` | GET | 返回 MCP 工具列表（名称/描述/参数 schema）|
| `/api/v1/mcp/call` | POST | 调用 MCP 工具，body `{tool, arguments}` |

> 已在 `main.py` (L437/L443) 和 `server.py` (L39/L67) 实现。

---

## 3. 前端页面

### 3.1 路由与页面

- 新增 `/mcp` 路由 + `pages/Mcp/index.tsx`（AntD）
- 挂"智能运维"分组菜单

### 3.2 页面结构

**两区布局：**

1. **工具目录**（左侧/上半）：工具列表表格
   - 列：工具名 / 描述 / 参数个数 / 操作（调用）
   - 数据源：`GET /mcp/tools`
   - 点击"调用"→ 打开调用抽屉，根据参数 schema 渲染动态表单

2. **调用测试**（抽屉/下半）：选定工具后
   - 按工具参数 schema 渲染输入表单（复用 Skills 页已有"按 schema 渲染表单"模式）
   - 填写参数 → `POST /mcp/call` → 展示返回结果（JSON 格式化）

### 3.3 client.ts 新增 API

```ts
export const getMcpTools = () => api.get('/mcp/tools')
export const callMcpTool = (data: { tool: string; arguments: Record<string, any> }) => api.post('/mcp/call', data)
```

---

## 4. 空态处理

- 工具列表为空 → 显示 antd `Empty` + "暂无 MCP 工具"（复用全站空态风格）

---

## 5. 测试

- 前端：`tsc --noEmit` + `npm run build`
- 冒烟：打开 `/mcp` 看到工具列表 → 选工具填参 → 调用返回结果

---

## 6. 后续扩展（不在本次）

接远程 MCP server 时：在 `ai-orchestrator/mcp_server.py` 增加"远程 server 注册/拉取 tools/转发调用"适配器，注册进 `/mcp/tools` 和 `/mcp/call` 逻辑；前端 `/mcp` 页零改动即可列出并调用远程工具。

---

## 7. 自审

- [x] 无 TBD/TODO
- [x] 轻量化：纯前端，零新增后端
- [x] 复用现有 `/mcp/tools` + `/mcp/call`
- [x] 扩展点明确（后端注册机制适配器），本次不提前造表
