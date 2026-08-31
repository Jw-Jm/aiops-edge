# 运行时预算与发布门禁

## 默认预算

| 调用 | connect | header | overall | retry |
|---|---:|---:|---:|---|
| 内部 HTTP | 1s | 5s | 15s | 仅幂等，最多 2 次 |
| Action execute | 2s | 10s | 60s | 不自动重放，先 reconcile |
| LLM | 3s | 15s | 120s | 仅 429/未开始，最多 2 次 |
| 存储只读 | 2s | — | 30s | 瞬态最多 1 次 |

所有服务必须传播取消、记录统一 trace/request/run/session ID，并以队列高水位背压；禁止
drop-and-ack、无限 timeout、空 catch 后返回成功。

## 发布门禁

本机零失败的单元/静态测试只是必要条件。候选环境还必须提供：迁移/回滚、双副本重启、
依赖中断、Action drift/reconcile、WAL replay、LLM 限流取消、Graph gate、备份恢复和
证书/签名 key 轮换证据。证据由 `deploy/scripts/collect-release-evidence.sh` 生成并绑定版本。
