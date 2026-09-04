# AIOps 后续治理任务清单

> 只记录当前代码仍存在、但不阻断当前单副本产品形态的工程治理项。
> 已关闭问题和历史整改过程不重复记录。

## A. 代码与安全加固

### P2-SEC1 容器最小权限

- 自研运行镜像尽可能使用非 root USER；
- 可开启的组件完成 readOnlyRootFilesystem；
- 必须写入路径使用 emptyDir / PVC；
- 不增加 Linux capabilities。

### P2-A2 前端逐 API scope

- 每个需要授权的 API 显式声明 scope/action；
- interceptor 只消费明确声明；
- tenant/cluster/namespace/resource/action 边界不得退化。

### P2-CODE Orchestrator / Query API 物理拆包

- 不改变 production surface；
- 先补契约测试再移动代码；
- 不重新引入 legacy runtime；
- 属于可维护性治理，不是当前生产部署阻断。

## B. CI 与供应链

### P2-CI migration coverage 进入 required CI

- 使用真实 MySQL；
- migration 缺字段时 CI 必须失败；
- 不允许 source-string 伪测试替代真实数据库测试。

### P2-SUP 工具链精确版本

- Go 从 `1.26.x` 固定到验证过的精确 patch；
- `govulncheck` 不使用 `@latest`；
- `pip-audit` 固定版本；
- 记录 scanner/tool version。

## C. 运维自动化

### P2-OPS Secret / mTLS 轮换

- 生产 Secret 轮换有明确 Runbook；
- mTLS 证书到期可观测；
- 私钥不得提交 Git。

## D. 功能验收边界

### 真实 LLM Provider E2E

仅当生产环境启用并对外声明真实 LLM/RCA 功能时，执行真实 provider 链路验收。

- 不得以 mock 冒充真实 provider；
- 该项是对应功能验收条件，不是平台 Helm 部署统一阻断条件。

## 已接受、不进入整改

- 所有自研中心服务单副本；
- 不要求服务级多副本 HA；
- `publishable` / release signature 不作为生产部署强制前置；
- release evidence 用于正式版本审计和供应链追踪。
