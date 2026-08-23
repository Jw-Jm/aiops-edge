# P19.6 Chat Canonical 重构 — 历史无效旧代码清单

- **STATUS**: IMPLEMENTED (P19.6 chat canonical-protected refactor)
- **日期**: 2026-08-22
- **GIT_ACTION**: NONE

本清单记录在 P19.6 迁移过程中识别为**历史无效/错误接线**的旧代码，以及本次处理结论。判定原则：`docs/V9.3_P19_CHAT_CANONICAL_REFACTOR_DESIGN.md` 的 1.2 节架构判断（对话 ≠ Investigation Run）。

---

## H1. query-go `ProxyAI` chat 分支转发目标错误接线（已修）

- **位置**: `ai-apm-query-go/internal/api/settings.go` 旧 `ProxyAI` chat 分支（原 L706）
- **内容**: `/api/v1/ai/chat` 转发到 orchestrator `/internal/v1/run-invocations`（调查入口）。
- **为何无效**: 前端 `AiChat.tsx` 是**对话型**（intent=diagnosis，SSE streaming，suggestion/report），不是调查 Run。把对话接到 run-invocations 会导致：
  1. 被要求 `ai.investigate` capability（viewer 无此 cap → 对话失败）
  2. 触发 ManualBoundary 建 Run 语义（对话不应建 Run）
  3. run-invocations 返回 `{run_id, events}` JSON，前端 `stream:true` 的 SSE 帧永远得不到
- **处理**: 拆出独立 `ProxyChat` handler，转发到新 `/internal/v1/chat`（对话型，`ai.chat` capability，SSE 流式透传）。

## H2. query-go `isCanonicalProtectedRoute` 未含 `/api/v1/ai/chat`（已修）

- **位置**: `ai-apm-query-go/internal/api/auth.go` `isCanonicalProtectedRoute`（原 L557）
- **内容**: `/api/v1/ai/chat` 不在 canonical-protected 白名单 → `AuthMiddleware` 在 L644 直接 403，请求到不了 ProxyAI。
- **为何无效**: 这是 P19.6 阻断点。已加入白名单（canonical-protected，非公开放行，仍要求 JWT + canonical tenant + 成员）。

## H3. query-go `ProxyChat` body cluster 字段兼容（已修）

- **位置**: `ai-apm-query-go/internal/api/settings.go` `ProxyChat`
- **内容**: 原 `ProxyAI` 只读 `body["cluster"]`（slug），但前端 `AiChat.tsx` 发送 `body["cluster_id"]`（canonical UUID）。
- **为何无效**: 字段不匹配 → 即使白名单放开，chat 也会 CLUSTER_ACCESS_DENIED。
- **处理**: `ProxyChat` 兼容 `cluster`（slug/name/UUID）与 `cluster_id`（canonical UUID）两种字段，最终 canonical cluster 一律由服务端 `ResolveRef` 决定。

## H4. orchestrator 公开 `/api/v1/ai/chat`（降级 legacy，未删）

- **位置**: `ai-orchestrator/main.py` `/api/v1/ai/chat`（原 L633）
- **内容**: 公开 chat 端点，浏览器可直连 orchestrator（若前端直接调 orchestrator 而非 query-api，则绕过 query-api 的 JWT/tenant/cluster 解析）。
- **为何无效/保留**: P14 已列移除目标；P19 前端统一走 query-api canonical-protected 路径。本设计**新增** `/internal/v1/chat` 流式 trusted ingress 作为 query-api→orchestrator 的对话入口，旧公开端点不再作为前端主路径，保留为诊断兼容（不动它，避免破坏既有兼容）。

## H5. frontend `AiChat.tsx` 默认 `cluster_id='all'`（行为正确，无需改）

- **位置**: `observability-frontend/src/pages/ai/AiChat.tsx` L165
- **内容**: 未选择集群时 `clusterId='all'`。
- **判定**: `ProxyChat` 对 `cluster_id='all'` fail-closed（CLUSTER_ACCESS_DENIED），符合设计"无默认/current-cluster 回退"。用户需显式选择 canonical cluster 才能对话——正确行为，不改。

## H6. frontend `chatWithAI`（未使用主路径，保留）

- **位置**: `observability-frontend/src/api/client.ts` L153-157
- **内容**: 非流式 POST `/ai/chat`（`responseType:'text'`）。
- **判定**: `AiChat.tsx` 用自带 `fetch` 做 SSE 流式，`chatWithAI` 非主路径。保留（兼容其它调用点），不删。

---

## 总结

| # | 位置 | 判定 | 处理 |
|---|------|------|------|
| H1 | settings.go ProxyAI chat→run-invocations | 错误接线 | 拆 ProxyChat → /internal/v1/chat |
| H2 | auth.go 白名单缺 chat | 阻断点 | 加入 canonical-protected |
| H3 | ProxyChat body 字段不匹配 | 无效 | 兼容 cluster + cluster_id |
| H4 | orchestrator 公开 /api/v1/ai/chat | legacy | 新增 /internal/v1/chat，旧端点保留 |
| H5 | frontend cluster_id='all' | 行为正确 | 不改（fail-closed 正确）|
| H6 | frontend chatWithAI | 非主路径 | 保留 |
