# 安全基线

这是平台当前生产安全契约，代码、Helm 和测试不得绕过这些约束。

1. MySQL 是用户、角色、租户、集群授权、会话作用域、Run、Chat 和 Action 的唯一权威来源。JWT 只含 `sub/sid/iat/exp/jti/token_version`；浏览器不保存 token、role、tenant 或 cluster，也不发送 `X-Tenant-ID`。
2. 所有服务间特权请求都使用方向独立的服务身份和签名 TrustedRequestContext/Action envelope；nonce 必须持久化消费，TTL 不超过 60 秒（Kubernetes lease token 不超过 300 秒）。
3. 授权同时绑定 tenant、cluster、namespace、resource 和 action。缺少作用域、默认值、`all` 或 slug 均拒绝。
4. Kubernetes 凭据只通过 `credential_ref` 和 Credential Broker 的预登记 capability profile 获取短时 TokenRequest；业务 Pod 不挂长期写权限 ServiceAccount。
5. LLM Provider key 只存在 egress proxy；调用方只能传 `provider_id/model`。Proxy 必须执行 scheme、DNS、IP、redirect 和预算校验。
6. Event Collector、K8s/硬件采集器只向 unified ingest 发送 Envelope；ClickHouse 由 ingest 单一写入。生产事件确认必须先写 WAL 并 fsync。
7. 生产工作负载禁止 privileged、host device、明文 secret、root DB、通配 RBAC 和默认作用域。日志不得包含 Cookie、Authorization、provider key、Kubernetes token、DSN 或原始 prompt。

每次发布必须运行：

```bash
bash deploy/scripts/test-production-architecture-contracts.sh
bash deploy/scripts/test-deployment-contracts.sh
bash deploy/scripts/secret-format-test.sh
```

发现安全边界无法验证时，状态为“未验证”，不能升级为通过。
