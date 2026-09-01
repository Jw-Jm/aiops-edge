#!/usr/bin/env node

// Dependency-free browser resource probe used by graph-resource-snapshot.sh.
// It speaks the Chrome DevTools Protocol directly so the production gate does
// not need Playwright or a package install in the validation environment.

const { spawn } = require("node:child_process");
const { request } = require("node:http");
const { request: httpsRequest } = require("node:https");
const { mkdtemp, rm } = require("node:fs/promises");
const { tmpdir } = require("node:os");
const { join } = require("node:path");

const targetUrl = process.argv[2];
const configuredBrowser = process.argv[3] || "";

function httpJson(url) {
  return new Promise((resolve, reject) => {
    const client = url.startsWith("https:") ? httpsRequest : request;
    const req = client(url, { method: "GET", rejectUnauthorized: false }, (res) => {
      let body = "";
      res.setEncoding("utf8");
      res.on("data", (chunk) => { body += chunk; });
      res.on("end", () => {
        if (res.statusCode < 200 || res.statusCode >= 300) {
          reject(new Error(`CDP HTTP ${res.statusCode}`));
          return;
        }
        try { resolve(JSON.parse(body)); } catch (error) { reject(error); }
      });
    });
    req.on("error", reject);
    req.end();
  });
}

function sleep(milliseconds) {
  return new Promise((resolve) => setTimeout(resolve, milliseconds));
}

async function waitForJson(url, timeoutMs) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    try { return await httpJson(url); } catch (_) { await sleep(100); }
  }
  throw new Error("CDP endpoint did not become ready");
}

async function main() {
  if (!targetUrl) throw new Error("browser URL is required");
  const browser = configuredBrowser || "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome";
  const profile = await mkdtemp(join(tmpdir(), "aiops-cdp-profile-"));
  const port = 9222 + Math.floor(Math.random() * 500);
  const child = spawn(browser, [
    "--headless=new", "--disable-gpu", "--no-sandbox", "--disable-dev-shm-usage",
    "--no-first-run", "--no-default-browser-check", "--remote-allow-origins=*",
    `--remote-debugging-port=${port}`, `--user-data-dir=${profile}`, "about:blank",
  ], { stdio: "ignore" });
  try {
    const targets = await waitForJson(`http://127.0.0.1:${port}/json/list`, 10000);
    const page = targets.find((item) => item.type === "page" && item.webSocketDebuggerUrl);
    if (!page) throw new Error("CDP page target unavailable");
    const WebSocketImpl = globalThis.WebSocket;
    if (!WebSocketImpl) throw new Error("Node WebSocket API unavailable");
    const socket = new WebSocketImpl(page.webSocketDebuggerUrl);
    const pending = new Map();
    let sequence = 0;
    const events = [];
    socket.addEventListener("message", (event) => {
      const message = JSON.parse(event.data);
      if (message.id && pending.has(message.id)) {
        const settle = pending.get(message.id);
        pending.delete(message.id);
        if (message.error) settle.reject(new Error(`${settle.method}: ${message.error.message || "CDP command failed"}`));
        else settle.resolve(message.result || {});
      } else {
        events.push(message);
      }
    });
    await new Promise((resolve, reject) => {
      socket.addEventListener("open", resolve, { once: true });
      socket.addEventListener("error", reject, { once: true });
    });
    const send = (method, params = {}) => new Promise((resolve, reject) => {
      const id = ++sequence;
      pending.set(id, { resolve, reject, method });
      socket.send(JSON.stringify({ id, method, params }));
    });
    await send("Page.enable");
    await send("Runtime.enable");
    await send("Page.addScriptToEvaluateOnNewDocument", { source: `
      (() => {
        window.__aiopsLongTasks = {count: 0, max: 0};
        if (window.PerformanceObserver) {
          try {
            const observer = new PerformanceObserver((list) => {
              for (const entry of list.getEntries()) {
                window.__aiopsLongTasks.count += 1;
                window.__aiopsLongTasks.max = Math.max(window.__aiopsLongTasks.max, entry.duration);
              }
            });
            observer.observe({type: 'longtask', buffered: true});
          } catch (_) {}
        }
      })();
    ` });
    await send("Page.navigate", { url: targetUrl });
    await sleep(1500);
    const evaluated = await send("Runtime.evaluate", {
      expression: "JSON.stringify(window.__aiopsLongTasks || {count: 0, max: 0})",
      returnByValue: true,
    });
    const value = evaluated?.result?.value?.value || evaluated?.result?.value || "{}";
    process.stdout.write(`${value}\n`);
    socket.close();
  } finally {
    child.kill("SIGTERM");
    await rm(profile, { recursive: true, force: true });
  }
}

main().catch((error) => {
  process.stderr.write(`${error.message}\n`);
  process.exitCode = 1;
});
