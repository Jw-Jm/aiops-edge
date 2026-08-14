"""ChromaDB RAG — bge-small-zh-v1.5 中文 embedding + 反馈闭环"""
import os
import json
import time
import threading

# 延迟加载 embedding 模型，避免启动时下载阻塞
_EF = None
_EF_LOCK = threading.Lock()

# 强制离线：禁止 HuggingFace 联网检查/下载模型（避免 429 限流阻塞 Chat）
os.environ.setdefault("TRANSFORMERS_OFFLINE", "1")
os.environ.setdefault("HF_HUB_OFFLINE", "1")
os.environ.setdefault("HF_DATASETS_OFFLINE", "1")


def _get_ef():
    """加载 embedding 模型，完全离线模式，绝不联网、绝不长时间阻塞。

    优先使用 ChromaDB 内置 ONNX 模型 (all-MiniLM-L6-v2, 已预缓存)，它走本地
    ONNX Runtime 推理，零联网依赖，避免了 sentence_transformers 加载时
    访问 HuggingFace 被 429 限流导致卡死的问题。
    """
    global _EF
    if _EF is not None:
        return _EF
    # 非阻塞获取锁：如果别的线程正在加载，直接返回 None（降级），不等待
    if not _EF_LOCK.acquire(blocking=False):
        print("[RAG] embedding 模型正在加载中, 本次降级跳过")
        return None
    try:
        if _EF is not None:
            return _EF
        # 首选中文 embedding 模型 bge-small-zh-v1.5 (sentence-transformers, 本地缓存, 零联网)
        # 显著提升中文故障描述 (k8s/Kylin/内核/kubevirt/ceph) 的向量检索精度
        try:
            from chromadb.utils import embedding_functions
            _EF = embedding_functions.SentenceTransformerEmbeddingFunction(
                model_name="BAAI/bge-small-zh-v1.5",
                device="cpu",
            )
            # 用本地缓存路径，避免联网 HuggingFace
            import os
            _HF_HOME = os.path.join(os.path.expanduser("~"), ".cache", "huggingface")
            _EF._model_kwargs = getattr(_EF, "_model_kwargs", {}) or {}
            _EF._model_kwargs["cache_folder"] = _HF_HOME
            _EF._model_kwargs["local_files_only"] = True
            # 预热一次，确认模型可加载
            _EF(["中文检索预热"])
            print("[RAG] embedding 使用 bge-small-zh-v1.5 (中文模型, 本地缓存)")
            return _EF
        except Exception as e:
            print(f"[RAG] bge-small-zh 模型加载失败: {e}")
        # 降级：ChromaDB 内置 ONNX 模型（离线安全）
        try:
            from chromadb.utils import embedding_functions
            _EF = embedding_functions.ONNXMiniLM_L6_V2(
                preferred_providers=["CPUExecutionProvider"])
            print("[RAG] embedding 降级使用 ONNXMiniLM_L6_V2 (离线)")
            return _EF
        except Exception as e:
            print(f"[RAG] ONNX 模型加载失败: {e}")
        # 再降级：all-MiniLM-L6-v2 sentence-transformers
        try:
            from chromadb.utils import embedding_functions
            _EF = embedding_functions.SentenceTransformerEmbeddingFunction(
                model_name="sentence-transformers/all-MiniLM-L6-v2",
                device="cpu",
            )
            print("[RAG] embedding 降级使用 sentence-transformers all-MiniLM-L6-v2")
            return _EF
        except Exception as e:
            print(f"[RAG] sentence-transformers 模型加载失败: {e}")
        print("[RAG] 所有 embedding 模型加载失败, RAG 降级为不可用")
        _EF = None
        return None
    finally:
        _EF_LOCK.release()


class RAGStore:
    """ChromaDB-backed case store for ops experience retrieval with feedback loop."""

    def __init__(self, persist_dir=None):
        # 统一数据目录: 由 AIOPS_DATA_DIR 环境变量控制 (helm 挂载 PVC 到 /var/lib/aiops)
        if persist_dir is None:
            data_dir = os.environ.get("AIOPS_DATA_DIR", "/var/lib/aiops")
            persist_dir = os.path.join(data_dir, "ops-cases")
        os.makedirs(persist_dir, exist_ok=True)
        self._ready = False
        self._init_error = None
        self._init_lock = threading.Lock()
        self._persist_dir = persist_dir
        # 延迟初始化：首次使用时在后台线程中初始化，最多等待 10s
        # 避免阻塞 API 请求

    def _ensure_init(self) -> bool:
        """延迟初始化 ChromaDB，最多等 8s，失败则降级为无 RAG 模式，绝不影响主流程"""
        if self._ready:
            return True
        if self._init_error:
            return False
        # 在独立线程中初始化并设置超时，避免 HuggingFace/ChromaDB 联网卡死
        result = {}

        def _do_init():
            try:
                import chromadb
                from chromadb.config import Settings
                self.client = chromadb.PersistentClient(
                    path=self._persist_dir, settings=Settings(anonymized_telemetry=False))
                ef = _get_ef()
                try:
                    self.collection = self.client.get_collection(
                        "ops_cases", embedding_function=ef)
                except Exception:
                    ef = _get_ef()
                    self.collection = self.client.create_collection(
                        "ops_cases", embedding_function=ef,
                        metadata={"hnsw:space": "cosine"})
                result["ok"] = True
                self._ready = True
                print(f"[RAG] ChromaDB 初始化成功, 当前案例数: {self.collection.count()}")
            except Exception as e:
                result["err"] = str(e)

        with self._init_lock:
            if self._ready:
                return True
            if self._init_error:
                return False
            t = threading.Thread(target=_do_init, daemon=True)
            t.start()
            # 首次冷启动加载中文 embedding 模型 + 打开 ChromaDB 需要时间，给足 60s
            t.join(timeout=60)
            if result.get("ok"):
                return True
            if t.is_alive():
                # 线程还在跑，本次降级返回 False，但不永久标记失败：
                # 后台线程完成后仍会置 _ready，后续请求可正常使用 RAG
                print("[RAG] 初始化进行中，本次检索降级跳过 (后台线程继续加载)")
                return False
            self._init_error = result.get("err", "unknown init error")
            print(f"[RAG] ChromaDB 初始化失败 (将降级运行): {self._init_error}")
            return False

    def add_case(self, case: dict) -> str:
        """Add a case. case must have: case_id, symptom, root_cause, plan, outcome."""
        if not self._ensure_init():
            return case.get("case_id", "")
        try:
            # 去重检查：相似度 > 0.92 的不再添加
            existing = self.dedup_check(case.get("symptom", ""), threshold=0.92)
            if existing:
                return existing  # 返回已有 case_id
            meta = {
                "service": case.get("service", ""),
                "root_cause": case.get("root_cause", ""),
                "plan": case.get("plan", ""),
                "outcome": case.get("outcome", "success"),
                "report": case.get("report", "")[:500],
                "validated": "pending",
                "weight": "1.0",
                "created_at": str(time.time()),
                "decay_factor": "1.0",
                "type": case.get("type", "case"),  # case=故障案例 | knowledge=知识条目
            }
            if meta["type"] == "knowledge":
                # 知识条目：tags/source 存入 metadata，搜索命中后供展示
                meta["tags"] = case.get("tags", "")
                meta["source"] = case.get("source", "manual")
            self.collection.add(
                ids=[case["case_id"]],
                documents=[case.get("symptom", "")],
                metadatas=[meta],
            )
            return case["case_id"]
        except Exception:
            return case.get("case_id", "")

    def dedup_check(self, symptom: str, threshold: float = 0.92) -> str | None:
        """返回已有 case_id 如果相似度 > threshold，避免重复存储"""
        if not self._ensure_init():
            return None
        try:
            results = self.collection.query(query_texts=[symptom], n_results=1)
            if results["ids"] and results["ids"][0] and results["distances"]:
                score = 1 - results["distances"][0][0]
                if score > threshold:
                    return results["ids"][0][0]
        except:
            pass
        return None

    def validate_case(self, case_id: str, outcome: str):
        """人工反馈: outcome=success → 提权, outcome=failed → 降权"""
        if not self._ensure_init():
            return
        try:
            meta = self.collection.get(ids=[case_id])
            if meta and meta["metadatas"]:
                m = dict(meta["metadatas"][0])
                if outcome == "success":
                    m["validated"] = "true"
                    m["weight"] = str(min(float(m.get("weight", 1.0)) * 1.5, 3.0))
                elif outcome == "failed":
                    m["validated"] = "false"
                    m["weight"] = str(max(float(m.get("weight", 1.0)) * 0.3, 0.1))
                self.collection.update(ids=[case_id], metadatas=[m])
        except:
            pass

    def decay_scores(self):
        """衰减：超过30天的案例权重 × 0.8/30天"""
        if not self._ensure_init():
            return
        try:
            now = time.time()
            all_cases = self.collection.get()
            if not all_cases["ids"]:
                return
            for i, cid in enumerate(all_cases["ids"]):
                meta = dict(all_cases["metadatas"][i]) if all_cases["metadatas"] else {}
                created = float(meta.get("created_at", str(now)))
                days = (now - created) / 86400
                if days > 30:
                    factor = max(0.2, 0.8 ** (days / 30))
                    self.collection.update(ids=[cid], metadatas=[{"decay_factor": str(factor)}])
        except:
            pass

    def search(self, query: str, limit: int = 3) -> list:
        """带反馈权重的向量检索。按 weight * decay * (1-distance) 排序"""
        if not self._ensure_init():
            return []
        try:
            results = self.collection.query(query_texts=[query], n_results=max(limit * 2, 5))
            cases = []
            if not results["ids"] or not results["ids"][0]:
                return cases
            for i, case_id in enumerate(results["ids"][0]):
                meta = results["metadatas"][0][i] if results["metadatas"] else {}
                distance = results["distances"][0][i] if results["distances"] else 1.0
                similarity = 1 - distance
                weight = float(meta.get("weight", 1.0))
                decay = float(meta.get("decay_factor", 1.0))
                adjusted_score = similarity * weight * decay
                cases.append({
                    "case_id": case_id,
                    "service": meta.get("service", ""),
                    "symptom": results["documents"][0][i] if results["documents"] else "",
                    "root_cause": meta.get("root_cause", ""),
                    "plan": meta.get("plan", ""),
                    "outcome": meta.get("outcome", "success"),
                    "validated": meta.get("validated", "pending"),
                    "report": meta.get("report", "")[:300],
                    "score": round(adjusted_score, 4),
                    "raw_similarity": round(similarity, 4),
                })
            cases.sort(key=lambda c: c["score"], reverse=True)
            return cases[:limit]
        except Exception:
            return []

    def count(self) -> int:
        if not self._ensure_init():
            return 0
        try:
            return self.collection.count()
        except Exception:
            return 0

    def list_all(self, type_filter: str = "", q: str = "", limit: int = 200, offset: int = 0) -> list:
        """列出所有条目（统一：case + knowledge），支持 type 过滤、关键词过滤、分页。
        返回条目字典列表，调用方负责分页切片。"""
        if not self._ensure_init():
            return []
        try:
            all_cases = self.collection.get()
            result = []
            if not all_cases["ids"]:
                return result
            ql = q.lower() if q else ""
            for i, cid in enumerate(all_cases["ids"]):
                meta = dict(all_cases["metadatas"][i]) if all_cases["metadatas"] else {}
                typ = meta.get("type", "case")
                if type_filter and typ != type_filter:
                    continue
                doc = (all_cases["documents"][i] or "") if all_cases["documents"] else ""
                if ql:
                    hay = (doc + " " + meta.get("root_cause", "") + " " + meta.get("tags", "")).lower()
                    if ql not in hay:
                        continue
                result.append({
                    "id": cid,
                    "type": typ,
                    "title": meta.get("title") or doc[:60],
                    "content": doc,
                    "service": meta.get("service", ""),
                    "root_cause": meta.get("root_cause", ""),
                    "plan": meta.get("plan", ""),
                    "outcome": meta.get("outcome", "success"),
                    "validated": meta.get("validated", "pending"),
                    "weight": float(meta.get("weight", 1.0)),
                    "decay": float(meta.get("decay_factor", 1.0)),
                    "created_at": meta.get("created_at", ""),
                    "tags": meta.get("tags", ""),
                    "source": meta.get("source", ""),
                })
            # 按创建时间倒序（created_at 为 epoch 秒字符串）
            result.sort(key=lambda x: float(x.get("created_at") or 0), reverse=True)
            return result[offset:offset + limit]
        except:
            return []

    def delete(self, item_id: str) -> bool:
        """按 id 删除条目（统一删除入口）。"""
        if not self._ensure_init():
            return False
        try:
            self.collection.delete(ids=[item_id])
            return True
        except Exception:
            return False

    def add_knowledge(self, title: str, content: str, source: str = "manual",
                      tags: str = "", service: str = "") -> str:
        """新增知识条目（type=knowledge，文档=标题+内容，便于语义检索）。"""
        import uuid
        kid = "kn-" + uuid.uuid4().hex[:12]
        return self.add_case({
            "case_id": kid,
            "type": "knowledge",
            "symptom": f"{title}\n{content}",  # 检索用全文
            "service": service,
            "root_cause": content,
            "plan": "",
            "outcome": "success",
            "report": "",
            "tags": tags,
            "source": source,
            "title": title,
        })


rag = RAGStore()

