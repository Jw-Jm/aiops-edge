# Object Store Contract（V9.2 Phase 4 P4.7）

V9.2 冻结的 Object Store SoT = **MinIO（或正式 Erratum 批准的其他 S3-compatible 存储）**，用于 large Evidence objects + Knowledge objects。

> 任何改用其他 S3-compatible 存储的决策，必须先产出正式 `V9.2 ARCHITECTURE ERRATUM`（MinIO → S3-compatible）。不得在 Phase 4 Implementation Plan 中静默改变 SoT。
>
> **P4.7 部署状态：** 当前仓库已移除 MinIO deployment/runtime dependency（AGPLv3 停更）。因此本 Phase 只冻结 Object Store **契约**（bucket/prefix/key/retention）；真实 bootstrap Job 受 `BLOCKED_OBJECT_STORE_RUNTIME_MISSING` 限制（无可用 MinIO/S3 endpoint），在具备 S3-compatible endpoint 后按本契约补建 bootstrap。

## Bucket 契约

| bucket | 用途 | 生命周期 |
|---|---|---|
| `aiops-evidence` | large Evidence objects（如大 JSON 证据、脚本、拓扑快照） | 与 `ai_evidence` 记录生命周期一致 |
| `aiops-knowledge` | Knowledge objects（知识库文档、playbook） | 与对应 knowledge 记录一致 |

## Object Key 命名（tenant isolation 强制）

```text
evidence bucket:
  <tenant_id>/<cluster_id>/<run_id>/<evidence_id>          # 大证据对象
knowledge bucket:
  <tenant_id>/<cluster_id>/<doc_id>                        # 知识对象
```

- **每条 object key 强制含 `tenant_id`**（tenant isolation）；次之 `cluster_id`（canonical UUID）。
- 不允许省略 tenant_id 的裸对象 key。

## Evidence 关联

- `ai_evidence.raw_ref` 指向 `aiops-evidence/<tenant_id>/<cluster_id>/<run_id>/<evidence_id>`。
- `ai_evidence.raw_digest_sha256` = 对象内容 SHA256，用于完整性校验（不信任对象元数据）。

## Retention / Reference 语义

- Evidence/Knowledge 对象与对应 DB 记录同生命周期；DB 记录删除时，对象由回收任务清理（Phase 11 或后续）。
- 禁止仅写对象不写 DB 记录（对象必须可被 DB 引用）。

## bootstrap

```text
object-store bootstrap Job（bootstrap 阶段）
  → create_or_validate bucket aiops-evidence
  → create_or_validate bucket aiops-knowledge
  → 二次 bootstrap 幂等（bucket exists → OK）
runtime：不负责 CreateBucket
```

bootstrap 需要 S3-compatible endpoint + credentials；当前因无可用 endpoint 标记 `BLOCKED_OBJECT_STORE_RUNTIME_MISSING`。
