"""AI Orchestrator v3 — LangGraph + SSE streaming + Checkpoint"""
import json
import os
import sys
import uuid
from http.server import HTTPServer, BaseHTTPRequestHandler
from urllib.parse import urlparse
from orchestrator import brain
from shell_policy import ShellPolicy

shell_policy = ShellPolicy()


class Handler(BaseHTTPRequestHandler):
    PROVIDER_BACKEND = {
        "openai": "openai", "deepseek": "deepseek",
        "kimi": "openai", "moonshot": "openai",
        "qwen": "openai", "dashscope": "openai",
        "xiaomi": "openai", "custom": "openai",
    }

    def _map_provider(self, p: str) -> str:
        return self.PROVIDER_BACKEND.get(p.lower(), "openai")

    # ── HTTP boilerplate ──

    def do_OPTIONS(self):
        self._cors(); self.send_response(200); self.end_headers()

    def do_GET(self):
        path = urlparse(self.path).path
        if path in ("/health", "/api/v1/health"):
            self._json(200, {"status": "ok", "version": "3.0", "langgraph": True, "sse": True})
        elif path in ("/api/v1/ai/sessions", "/api/v1/ai/sessions/"):
            self._list_sessions()
        elif path.startswith("/api/v1/ai/session/"):
            sid = path.split("/")[-1]
            self._get_session(sid)
        elif path == "/api/v1/mcp/tools":
            from mcp_server import mcp
            self._json(200, {"tools": mcp.list_tools()})
        else:
            self._json(404, {"error": "not found"})

    def do_DELETE(self):
        path = urlparse(self.path).path
        if path.startswith("/api/v1/ai/session/"):
            sid = path.split("/")[-1]
            self._delete_session(sid)
        else:
            self._json(404, {"error": "not found"})

    def do_POST(self):
        path = urlparse(self.path).path
        content_length = int(self.headers.get("Content-Length", 0))
        body = self.rfile.read(content_length) if content_length else b"{}"
        try: data = json.loads(body)
        except json.JSONDecodeError:
            self._json(400, {"error": "invalid JSON"}); return

        if path == "/api/v1/ai/chat":
            self._handle_chat(data)
        elif path == "/api/v1/ai/shell/check":
            cmd = data.get("command", "")
            reject = shell_policy.check(cmd)
            self._json(200, {"allowed": reject is None, "reason": reject} if reject else {"allowed": True})
        elif path == "/api/v1/mcp/call":
            from mcp_server import mcp
            name = data.get("name", ""); args = data.get("args", {})
            self._json(200, {"result": mcp.call_tool(name, args)})
        else:
            self._json(404, {"error": "not found"})

    # ── Chat handler ──

    def _handle_chat(self, data):
        intent = data.get("intent", "diagnose")
        service = data.get("service", "")
        message = data.get("message", "")
        thread_id = data.get("session_id", str(uuid.uuid4())[:8])
        stream = data.get("stream", False)

        # Parse LLM config from ProxyAI headers
        if self.headers.get("X-LLM-API-Key"):
            brain.set_llm_config({
                "api_key": self.headers["X-LLM-API-Key"],
                "model": self.headers.get("X-LLM-Model", "gpt-4o"),
                "base_url": self.headers.get("X-LLM-Base-URL", "https://api.openai.com/v1"),
                "provider": self.headers.get("X-LLM-Provider", "openai"),
                "backend": self._map_provider(self.headers.get("X-LLM-Provider", "openai")),
            })
        else:
            brain.set_llm_config(None)

        try:
            if stream:
                self._stream_sse(intent, service, message, thread_id)
            else:
                result = brain.execute_sync(intent, service, message, thread_id)
                self._send_raw(200, result[:10000], "text/markdown; charset=utf-8")
        except Exception as e:
            self._json(500, {"error": str(e)})

    # ── SSE streaming ──

    def _stream_sse(self, intent, service, message, thread_id):
        self.send_response(200)
        self._cors()
        self.send_header("Content-Type", "text/event-stream")
        self.send_header("Cache-Control", "no-cache")
        self.send_header("X-Session-Id", thread_id)
        self.end_headers()

        try:
            for event in brain.stream_sync(intent, service, message, thread_id):
                self.wfile.write(f"data: {json.dumps(event, ensure_ascii=False)}\n\n".encode())
                self.wfile.flush()
        except Exception as e:
            self.wfile.write(f"data: {json.dumps({'type':'error','text':str(e)}, ensure_ascii=False)}\n\n".encode())
            self.wfile.flush()

    # ── Session (LangGraph checkpoint) ──

    def _list_sessions(self):
        """List sessions with summary: id, preview, intent, time."""
        try:
            rows = brain._conn.execute(
                "SELECT DISTINCT thread_id FROM checkpoints ORDER BY thread_id DESC LIMIT 50"
            ).fetchall()
        except:
            self._json(200, {"sessions": []})
            return

        sessions = []
        seen = set()
        for (tid,) in rows:
            if tid in seen:
                continue
            seen.add(tid)
            try:
                state = brain.graph.get_state({"configurable": {"thread_id": tid}})
                if state and state.values:
                    vals = state.values
                    user_msg = vals.get("user_message", "")
                    final = vals.get("final_response", "")
                    preview = (user_msg or final or "")[:80]
                    sessions.append({
                        "session_id": tid,
                        "preview": preview,
                        "intent": vals.get("intent", ""),
                        "created_at": vals.get("messages", [None])[0] if vals.get("messages") else "",
                    })
            except:
                sessions.append({"session_id": tid, "preview": tid, "intent": "", "created_at": ""})
        self._json(200, {"sessions": sessions})

    def _get_session(self, sid):
        """Get full session state including message history."""
        state = brain.graph.get_state({"configurable": {"thread_id": sid}})
        if state and state.values:
            vals = state.values
            # Build chat-format messages from state
            msgs = []
            # User message
            if vals.get("user_message"):
                msgs.append({"role": "user", "content": vals["user_message"]})
            # AI response
            if vals.get("final_response"):
                msgs.append({"role": "assistant", "content": vals["final_response"]})
            # Include status messages if any
            for m in vals.get("messages", []):
                if isinstance(m, str) and m.startswith("[系统]"):
                    msgs.append({"role": "system", "content": m})

            self._json(200, {
                "session_id": sid,
                "intent": vals.get("intent", ""),
                "service": vals.get("service", ""),
                "messages": msgs,
                "final_response": vals.get("final_response", ""),
            })
        else:
            self._json(404, {"error": "session not found"})

    def _delete_session(self, sid):
        """Delete all checkpoints for a session thread."""
        try:
            brain._conn.execute("DELETE FROM checkpoints WHERE thread_id = ?", (sid,))
            brain._conn.execute("DELETE FROM writes WHERE thread_id = ?", (sid,))
            brain._conn.commit()
            self._json(200, {"message": "session deleted", "session_id": sid})
        except Exception as e:
            self._json(500, {"error": f"delete failed: {e}"})

    # ── Helpers ──

    def _json(self, code, data):
        self.send_response(code); self._cors()
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        self.wfile.write(json.dumps(data, ensure_ascii=False).encode())

    def _send_raw(self, code, text, content_type="text/plain; charset=utf-8"):
        self.send_response(code); self._cors()
        self.send_header("Content-Type", content_type)
        self.end_headers()
        self.wfile.write(text.encode("utf-8"))

    def _cors(self):
        self.send_header("Access-Control-Allow-Origin", "*")
        self.send_header("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
        self.send_header("Access-Control-Allow-Headers", "Content-Type, X-Tenant-ID")

    def log_message(self, fmt, *args):
        print(f"{self.address_string()} - {args[0]}", file=sys.stderr)


if __name__ == "__main__":
    port = int(os.environ.get("PORT", "8080"))
    server = HTTPServer(("0.0.0.0", port), Handler)
    print(f"AI Orchestrator v3 (LangGraph+SSE+Checkpoint) on :{port}", flush=True)
    server.serve_forever()
