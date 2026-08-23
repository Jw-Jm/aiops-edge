# Chroma Collection Contract（V9.2 Phase 4 P4.7）

ChromaDB vector index 的初始化契约。collection 的创建/校验从 orchestrator runtime 移入 bootstrap（V9.2 Phase 4：runtime 只 `get_collection`，缺失 → readiness 失败，不再 `get_or_create`）。

## Collections

| collection | embedding | metadata | 用途 |
|---|---|---|---|
| `ops_cases` | `bge-small-zh-v1.5` | `{"hnsw:space": "cosine"}` | 历史故障案例知识（type=case/knowledge）|
| `ops_playbooks` | `bge-small-zh-v1.5` | `{"hnsw:space": "cosine"}` | 运维 playbook |

## 初始化分工

```text
bootstrap（rag_bootstrap.py）
  → create_or_validate collection ops_cases / ops_playbooks

orchestrator runtime（rag.py）
  → get_collection(...) only
  → collection 缺失 → startup/readiness FAIL CLOSED（不再 create_collection）
```

## 常量

- `CASE_COLLECTION = "ops_cases"`
- `PLAYBOOK_COLLECTION = "ops_playbooks"`
- `EMBEDDING_FN = "bge-small-zh-v1.5"`

## 约束

- runtime 不得创建 collection。
- collection 命名与 embedding 以本契约为唯一来源。
