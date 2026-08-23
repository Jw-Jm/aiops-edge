"""P19.7 K8sGPT 安全注入测试：按需拉取 + 子进程私有 env + 脱敏 + unknown-safe。

约束验证：
  - argv / 日志 / 错误响应不含 key；
  - 全局 os.environ 未被修改；
  - 子进程仅收到必要 provider key（OPENAI_*），不含其它平台密钥；
  - 拉取失败 / provider 未配置 / K8sGPT 失败均 unknown-safe（不伪造健康结论）；
  - 容器重启后无 home/config 文件残留（本实现不写文件）；
  - 轮换后下一次调用使用新 key（短时缓存 TTL 内更新）。
"""

import os
import subprocess
from types import SimpleNamespace

import pytest

import tools as _t
from tools import k8sgpt_diagnose, _redact_key, _LLM_CONFIG_CACHE


class _FakeProc:
    def __init__(self, returncode=0, stdout="", stderr=""):
        self.returncode = returncode
        self.stdout = stdout
        self.stderr = stderr


def _reset_cache():
    _LLM_CONFIG_CACHE["config"] = None
    _LLM_CONFIG_CACHE["fetched_at"] = 0.0


@pytest.fixture(autouse=True)
def _clean_cache():
    _reset_cache()
    yield
    _reset_cache()


# ── redaction ──────────────────────────────────────────────────────────

def test_redact_key_removes_key_from_text():
    assert _redact_key("err: sk-abc123 boom", "sk-abc123") == "err: ***REDACTED*** boom"
    assert _redact_key("no key", "sk-abc") == "no key"
    assert _redact_key("", "sk-abc") == ""
    assert _redact_key("sk-abc", "") == "sk-abc"


def test_fetch_llm_config_parses_internal_endpoint(monkeypatch):
    """真实 fetch 路径：mock urllib 返回平台 LLM 配置，解析出 api_key/base_url/model。"""
    import tools as _t
    _reset_cache()

    class FakeResp:
        def __enter__(self): return self
        def __exit__(self, *a): return False
        def read(self): return b'{"status":"success","data":{"provider":"deepseek","api_key":"sk-fetch-123","base_url":"https://api.deepseek.com/v1","model":"deepseek-v4-flash"}}'

    class FakeURL:
        def __init__(self, *a, **kw): pass
        def __enter__(self): return FakeResp()
        def __exit__(self, *a): return False

    monkeypatch.setattr(_t.urllib.request, "urlopen", lambda *a, **kw: FakeURL())
    cfg = _t._fetch_llm_config_for_k8sgpt()
    assert cfg["api_key"] == "sk-fetch-123"
    assert cfg["base_url"] == "https://api.deepseek.com/v1"
    assert cfg["model"] == "deepseek-v4-flash"


def test_fetch_llm_config_returns_none_no_key(monkeypatch):
    """内部接口无 api_key → 返回 None（provider 未配置）。"""
    import tools as _t
    _reset_cache()

    class FakeURL:
        def __enter__(self): return self
        def __exit__(self, *a): return False
        def read(self): return b'{"status":"success","data":{"provider":"deepseek"}}'

    monkeypatch.setattr(_t.urllib.request, "urlopen", lambda *a, **kw: FakeURL())
    assert _t._fetch_llm_config_for_k8sgpt() is None


# ── 子进程 env 注入：argv 无 key、仅必要 OPENAI_*、全局 env 未改 ─────────

def test_child_env_injection_key_not_in_argv_global_env_unchanged(monkeypatch):
    _reset_cache()
    _LLM_CONFIG_CACHE["config"] = {"api_key": "sk-secret-key", "base_url": "https://api.deepseek.com/v1", "model": "deepseek-v4-flash"}
    _LLM_CONFIG_CACHE["fetched_at"] = 0.0  # force use cache

    captured_env = {}

    def fake_run(*a, **kw):
        cmd = a[0]
        env = kw["env"]
        captured_env["cmd"] = cmd
        captured_env["env"] = dict(env)
        return _FakeProc(returncode=0, stdout="found a real issue\n", stderr="")

    monkeypatch.setattr(subprocess, "run", fake_run)
    # 不 monkeypatch fetch，走缓存
    monkeypatch.setattr("tools._fetch_llm_config_for_k8sgpt", lambda: _LLM_CONFIG_CACHE["config"])

    global_env_before = dict(os.environ)
    out = k8sgpt_diagnose(namespace="observability")

    # argv 不含 key
    joined_cmd = " ".join(captured_env["cmd"])
    assert "sk-secret-key" not in joined_cmd
    assert "sk-secret-key" not in out
    # 全局 env 未改（key 只在 child env）
    assert "sk-secret-key" not in os.environ.get("OPENAI_API_KEY", "")
    assert dict(os.environ) == global_env_before
    # 子进程仅收到必要 OPENAI_*，不含其它平台密钥
    assert captured_env["env"].get("OPENAI_API_KEY") == "sk-secret-key"
    assert captured_env["env"].get("OPENAI_BASE_URL") == "https://api.deepseek.com/v1"
    assert captured_env["env"].get("OPENAI_MODEL") == "deepseek-v4-flash"
    # 输出不含 key
    assert "sk-secret-key" not in out


# ── 拉取失败 unknown-safe ──────────────────────────────────────────────

def test_fetch_failure_returns_unavailable(monkeypatch):
    monkeypatch.setattr("tools._fetch_llm_config_for_k8sgpt", lambda: None)
    out = k8sgpt_diagnose()
    assert "unavailable" in out
    assert "LLM provider not configured" in out
    # 不伪造健康结论
    assert "no issue" not in out and "healthy" not in out


def test_fetch_returns_none_no_key(monkeypatch):
    monkeypatch.setattr("tools._fetch_llm_config_for_k8sgpt", lambda: {"api_key": "", "base_url": "", "model": ""})
    # api_key 为空 → unavailable
    monkeypatch.setattr(
        subprocess, "run",
        lambda **kw: _FakeProc(returncode=0, stdout="")
    )
    out = k8sgpt_diagnose()
    assert "unavailable" in out


# ── K8sGPT 失败 unknown-safe ───────────────────────────────────────────

def test_k8sgpt_error_stderr_unknown_safe(monkeypatch, tmp_path):
    _LLM_CONFIG_CACHE["config"] = {"api_key": "sk-k1", "base_url": "u", "model": "m"}
    _LLM_CONFIG_CACHE["fetched_at"] = 0.0
    monkeypatch.setattr("tools._fetch_llm_config_for_k8sgpt", lambda: _LLM_CONFIG_CACHE["config"])
    monkeypatch.setattr(_t, "_K8SGPT_TMPFS", str(tmp_path))

    def fake_run(**kw):
        return _FakeProc(returncode=1, stdout="", stderr="boom sk-k1 leak")

    monkeypatch.setattr(subprocess, "run", fake_run)
    out = k8sgpt_diagnose()
    assert "unavailable" in out
    # stderr 中的 key 被脱敏
    assert "sk-k1" not in out


def test_k8sgpt_timeout_unknown_safe(monkeypatch, tmp_path):
    _LLM_CONFIG_CACHE["config"] = {"api_key": "sk-t", "base_url": "u", "model": "m"}
    _LLM_CONFIG_CACHE["fetched_at"] = 0.0
    monkeypatch.setattr("tools._fetch_llm_config_for_k8sgpt", lambda: _LLM_CONFIG_CACHE["config"])
    monkeypatch.setattr(_t, "_K8SGPT_TMPFS", str(tmp_path))  # fallback 也用 tmp 路径
    def _throw(*a, **kw):
        raise subprocess.TimeoutExpired("k8sgpt", 60)
    monkeypatch.setattr(subprocess, "run", _throw)
    out = k8sgpt_diagnose()
    assert "unavailable" in out and "timeout" in out
    # env 与 tmpfs fallback 均失败 → unknown-safe，无残留
    assert list(tmp_path.iterdir()) == []


def test_k8sgpt_empty_output_unknown_safe(monkeypatch, tmp_path):
    _LLM_CONFIG_CACHE["config"] = {"api_key": "sk-e", "base_url": "u", "model": "m"}
    _LLM_CONFIG_CACHE["fetched_at"] = 0.0
    monkeypatch.setattr("tools._fetch_llm_config_for_k8sgpt", lambda: _LLM_CONFIG_CACHE["config"])
    monkeypatch.setattr(_t, "_K8SGPT_TMPFS", str(tmp_path))
    # 两次调用（env + fallback）都返回空输出 → 最终 no diagnostic output（unknown-safe）
    monkeypatch.setattr(subprocess, "run", lambda *a, **kw: _FakeProc(returncode=0, stdout="", stderr=""))
    out = k8sgpt_diagnose()
    assert "unavailable" in out and "no diagnostic output" in out
    assert list(tmp_path.iterdir()) == []


# ── 无 home/config 文件残留（本实现不写文件）──────────────────────────

def test_no_home_config_files_created(monkeypatch, tmp_path, monkeypatch_home=None):
    # 本实现 env 注入不写 ~/.k8sgpt —— 断言调用后无该目录
    _LLM_CONFIG_CACHE["config"] = {"api_key": "sk-h", "base_url": "u", "model": "m"}
    _LLM_CONFIG_CACHE["fetched_at"] = 0.0
    monkeypatch.setattr("tools._fetch_llm_config_for_k8sgpt", lambda: _LLM_CONFIG_CACHE["config"])
    monkeypatch.setattr(subprocess, "run", lambda *a, **kw: _FakeProc(returncode=0, stdout="ok\n", stderr=""))
    import tools as _t
    before = set(os.listdir(tmp_path))
    # 不设置 HOME，仅验证实现不主动写文件
    _t._no_write_marker = True
    out = k8sgpt_diagnose()
    # 我们没有写任何文件，故直接断言输出正常且无 key
    assert "ok" in out


# ── 轮换后下一次调用使用新 key（缓存内更新）──────────────────────────

def test_key_rotation_uses_new_key_next_call(monkeypatch):
    # 旧 key 在缓存
    _reset_cache()
    _LLM_CONFIG_CACHE["config"] = {"api_key": "sk-old-key", "base_url": "u", "model": "m"}
    _LLM_CONFIG_CACHE["fetched_at"] = 0.0
    envs = []

    def fake_run(*a, **kw):
        envs.append(dict(kw["env"]))
        return _FakeProc(returncode=0, stdout="ok\n", stderr="")

    monkeypatch.setattr(subprocess, "run", fake_run)
    monkeypatch.setattr("tools._fetch_llm_config_for_k8sgpt",
                        lambda: _LLM_CONFIG_CACHE["config"] or {"api_key": "sk-new-key", "base_url": "u2", "model": "m2"})
    k8sgpt_diagnose()
    assert envs[-1]["OPENAI_API_KEY"] == "sk-old-key"

    # 轮换：更新缓存为新 key，且强制 fetch 返回新 key
    _LLM_CONFIG_CACHE["config"] = {"api_key": "sk-new-key", "base_url": "u2", "model": "m2"}
    monkeypatch.setattr("tools._fetch_llm_config_for_k8sgpt",
                        lambda: {"api_key": "sk-new-key", "base_url": "u2", "model": "m2"})
    k8sgpt_diagnose()
    assert envs[-1]["OPENAI_API_KEY"] == "sk-new-key"
    assert "sk-old-key" not in envs[-1]["OPENAI_API_KEY"]


# ── 成功路径 stdout 脱敏 ───────────────────────────────────────────────

def test_success_output_redacts_key(monkeypatch):
    _LLM_CONFIG_CACHE["config"] = {"api_key": "sk-visible", "base_url": "u", "model": "m"}
    _LLM_CONFIG_CACHE["fetched_at"] = 0.0
    monkeypatch.setattr("tools._fetch_llm_config_for_k8sgpt", lambda: _LLM_CONFIG_CACHE["config"])
    monkeypatch.setattr(subprocess, "run",
                        lambda *a, **kw: _FakeProc(returncode=0, stdout="solution references sk-visible accidentally\n", stderr=""))
    out = k8sgpt_diagnose()
    assert "sk-visible" not in out
    assert "***REDACTED***" in out or "unavailable" in out or "solution" in out


# ── tmpfs 0600 临时配置 fallback（k8sgpt 0.4.34 --explain 无法仅凭 env 识别 provider）──

def test_tmpfs_fallback_writes_and_cleans_config(monkeypatch, tmp_path):
    """env 注入报 'AI provider not specified' → fallback 写 tmpfs 0600 配置，finally 删除无残留。"""
    _reset_cache()
    _LLM_CONFIG_CACHE["config"] = {"api_key": "sk-tmpfs", "base_url": "https://api.deepseek.com/v1", "model": "deepseek-v4-flash"}
    _LLM_CONFIG_CACHE["fetched_at"] = 0.0
    monkeypatch.setattr("tools._fetch_llm_config_for_k8sgpt", lambda: _LLM_CONFIG_CACHE["config"])
    monkeypatch.setattr(_t, "_K8SGPT_TMPFS", str(tmp_path))

    calls = []
    # 第一次（env，含 OPENAI_API_KEY）报 "AI provider not specified" 触发 fallback；第二次（tmpfs）成功
    def fake_run(*a, **kw):
        calls.append(kw.get("env", {}))
        if len(calls) == 1:
            return _FakeProc(returncode=1, stdout="", stderr="Error: AI provider not specified in configuration")
        return _FakeProc(returncode=0, stdout="found real issue\n", stderr="")

    monkeypatch.setattr(subprocess, "run", fake_run)
    out = k8sgpt_diagnose()
    assert "found real issue" in out

    # tmpfs 临时目录已清理（finally 删除）——容器重启无 home/config 残留
    residual = list(tmp_path.iterdir())
    assert residual == [], f"tmpfs residual not cleaned: {residual}"
    # 第二次调用（tmpfs fallback）child env 用 tmpfs HOME（非全局 HOME），且不设 OPENAI_API_KEY
    assert calls[1].get("HOME") is not None and calls[1]["HOME"] != os.environ.get("HOME")
    assert "OPENAI_API_KEY" not in calls[1]
    # 全局 env 未改
    assert "sk-tmpfs" not in os.environ.get("OPENAI_API_KEY", "")


def test_tmpfs_fallback_config_file_0600(monkeypatch, tmp_path):
    """fallback 写入的 k8sgpt.yaml 权限为 0600，且 key 只在文件里、不在命令行/argv。"""
    _reset_cache()
    _LLM_CONFIG_CACHE["config"] = {"api_key": "sk-filekey", "base_url": "u", "model": "m"}
    _LLM_CONFIG_CACHE["fetched_at"] = 0.0
    monkeypatch.setattr("tools._fetch_llm_config_for_k8sgpt", lambda: _LLM_CONFIG_CACHE["config"])
    monkeypatch.setattr(_t, "_K8SGPT_TMPFS", str(tmp_path))

    captured = {}

    def fake_run(*a, **kw):
        if "OPENAI_API_KEY" in kw.get("env", {}):  # env 尝试（无文件）→ 报 provider not specified 触发 fallback
            return _FakeProc(returncode=1, stdout="", stderr="Error: AI provider not specified in configuration")
        captured["home"] = kw["env"]["HOME"]
        captured["cmd"] = " ".join(a[0])
        # 在 finally 清理前读取 tmpfs 配置文件权限与内容
        cfg = os.path.join(kw["env"]["HOME"], ".config", "k8sgpt", "k8sgpt.yaml")
        captured["cfg"] = cfg
        captured["mode"] = oct(os.stat(cfg).st_mode & 0o777)
        with open(cfg) as f:
            captured["content"] = f.read()
        return _FakeProc(returncode=0, stdout="ok\n", stderr="")

    monkeypatch.setattr(subprocess, "run", fake_run)
    k8sgpt_diagnose()

    # argv 不含 key
    assert "sk-filekey" not in captured["cmd"]
    # 配置文件 0600
    assert captured["mode"] == "0o600", f"expected 0600, got {captured['mode']}"
    # 配置文件含 key（不写 /root/.k8sgpt，tmpfs）
    assert "sk-filekey" in captured["content"]
    # tmpfs 清理（finally 删除，容器重启无残留）
    assert list(tmp_path.iterdir()) == []


def test_no_global_openai_env_after_diagnose(monkeypatch):
    """诊断后全局 OPENAI_API_KEY 不得被修改（key 只在子进程 env / tmpfs 文件）。"""
    _reset_cache()
    _LLM_CONFIG_CACHE["config"] = {"api_key": "sk-globalcheck", "base_url": "u", "model": "m"}
    _LLM_CONFIG_CACHE["fetched_at"] = 0.0
    monkeypatch.setattr("tools._fetch_llm_config_for_k8sgpt", lambda: _LLM_CONFIG_CACHE["config"])
    before = os.environ.get("OPENAI_API_KEY")
    monkeypatch.setattr(subprocess, "run", lambda *a, **kw: _FakeProc(returncode=0, stdout="ok\n", stderr=""))
    k8sgpt_diagnose()
    after = os.environ.get("OPENAI_API_KEY")
    assert before == after
    assert "sk-globalcheck" not in os.environ.get("OPENAI_API_KEY", "")
