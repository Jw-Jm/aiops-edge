#!/usr/bin/env bash
set -euo pipefail

# Collects the runtime resource evidence required by the graph performance
# gate. This script deliberately reports unavailable measurements instead of
# converting missing telemetry into zeroes.
namespace="${GRAPH_RESOURCE_NAMESPACE:-${GRAPH_NAMESPACE:-observability}}"
output="${GRAPH_RESOURCE_OUTPUT:-/tmp/aiops-graph-resource-report.json}"
frontend_dist="${GRAPH_FRONTEND_DIST:-}"
browser_url="${GRAPH_BROWSER_URL:-http://127.0.0.1:30253}"
fixture=""

usage() {
  cat <<'EOF'
Usage: graph-resource-snapshot.sh [--namespace NS] [--output PATH]
       [--frontend-dist PATH] [--browser-url URL] [--fixture PATH]

The live collector uses kubectl metrics/cgroup and pod filesystem data. The
fixture option is only for deterministic contract tests and does not inspect a
runtime.
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --namespace) namespace="${2:?--namespace requires a value}"; shift 2 ;;
    --output) output="${2:?--output requires a path}"; shift 2 ;;
    --frontend-dist) frontend_dist="${2:?--frontend-dist requires a path}"; shift 2 ;;
    --browser-url) browser_url="${2:?--browser-url requires a URL}"; shift 2 ;;
    --fixture) fixture="${2:?--fixture requires a path}"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) usage >&2; exit 2 ;;
  esac
done

mkdir -p "$(dirname "${output}")"
if [[ -n "${fixture}" ]]; then
  [[ -f "${fixture}" ]] || { echo "fixture not found: ${fixture}" >&2; exit 2; }
  python3 - "${fixture}" "${output}" <<'PY'
import json, sys
source, target = sys.argv[1:]
data = json.load(open(source, encoding="utf-8"))
if not isinstance(data, dict):
    raise SystemExit("resource fixture must be a JSON object")
json.dump(data, open(target, "w", encoding="utf-8"), ensure_ascii=False, indent=2)
PY
  cat "${output}"
  exit 0
fi

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${script_dir}/../.." && pwd)"
frontend_dist="${frontend_dist:-${repo_root}/observability-frontend/dist}"

status_hg="not_collected"; reason_hg="kubectl metrics unavailable"
hg_rss=""; heap_used=""; heap_max=""
status_rocks="not_collected"; reason_rocks="HugeGraph pod filesystem unavailable"
rocks_data=""; rocks_wal=""
status_query="not_collected"; reason_query="query-api pod metrics unavailable"
query_cpu=""; query_rss=""
status_worker="not_collected"; reason_worker="ai-investigation-worker pod metrics unavailable"
worker_cpu=""; worker_rss=""
status_bundle="not_collected"; reason_bundle="frontend dist directory not found"
bundle_bytes=""
status_browser="not_collected"; reason_browser="Playwright runtime unavailable"
long_task_count=""; long_task_max=""

to_bytes() {
  python3 - "$1" <<'PY'
import re, sys
raw = sys.argv[1].strip()
if not raw:
    raise SystemExit(1)
match = re.fullmatch(r"([0-9]+(?:\.[0-9]+)?)([KMGTP]i?|)", raw, re.I)
if not match:
    raise SystemExit(1)
value = float(match.group(1))
unit = match.group(2).lower()
scale = {"": 1, "k": 1000, "ki": 1024, "m": 1000**2, "mi": 1024**2,
         "g": 1000**3, "gi": 1024**3, "t": 1000**4, "ti": 1024**4,
         "p": 1000**5, "pi": 1024**5}[unit]
print(int(value * scale))
PY
}

to_millicores() {
  python3 - "$1" <<'PY'
import sys
raw = sys.argv[1].strip()
if raw.endswith("m"):
    print(int(float(raw[:-1])))
elif raw:
    print(int(float(raw) * 1000))
else:
    raise SystemExit(1)
PY
}

pod_for_app() {
  kubectl -n "${namespace}" get pods -l "app=$1" -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true
}

pod_top() {
  kubectl -n "${namespace}" top pod "$1" --no-headers 2>/dev/null || true
}

if command -v kubectl >/dev/null 2>&1; then
  hugegraph_pod="$(pod_for_app hugegraph)"
  # The HTTP-facing Query deployment is intentionally named query-api-http;
  # the dispatcher/evaluator replicas share the query-api image but do not
  # expose the browser-facing resource budget measured by this gate.
  query_pod="$(pod_for_app query-api-http)"
  worker_pod="$(pod_for_app ai-investigation-worker)"

  if [[ -n "${hugegraph_pod}" ]]; then
    top_line="$(pod_top "${hugegraph_pod}")"
    top_mem_raw="$(awk 'NR == 1 {print $3}' <<<"${top_line}")"
    jvm_pid="$(kubectl -n "${namespace}" exec "${hugegraph_pod}" -- ps -eo pid,comm 2>/dev/null | awk '$2 == "java" {print $1; exit}' || true)"
    hg_rss=""
    if [[ "${jvm_pid}" =~ ^[0-9]+$ ]]; then
      hg_rss="$(kubectl -n "${namespace}" exec "${hugegraph_pod}" -- awk '$1 == "VmRSS:" {printf "%.0f\\n", $2 * 1024; exit}' "/proc/${jvm_pid}/status" 2>/dev/null || true)"
    fi
    [[ "${hg_rss}" =~ ^[0-9]+$ ]] || hg_rss="$(to_bytes "${top_mem_raw}" 2>/dev/null || true)"
    heap_output="$(kubectl -n "${namespace}" exec "${hugegraph_pod}" -- sh -c "jcmd ${jvm_pid:-1} GC.heap_info 2>/dev/null || true" 2>/dev/null || true)"
    heap_values="$(python3 -c 'import re,sys
text = sys.stdin.read()
used = re.search(r"used\s+([0-9]+)([KMG])", text, re.I)
total = re.search(r"(?:total|max)\s+([0-9]+)([KMG])", text, re.I)
def convert(match):
    if not match:
        return ""
    return str(int(match.group(1)) * {"k": 1024, "m": 1024**2, "g": 1024**3}[match.group(2).lower()])
print(convert(used), convert(total))' <<<"${heap_output}" 2>/dev/null || true)"
    read -r heap_used heap_max <<<"${heap_values}"
    # The production HugeGraph image is intentionally JRE-sized and may not
    # contain jcmd/jstat. In that case obtain the configured heap ceiling from
    # the JVM command line and keep RSS as the measured resident footprint.
    # We do not relabel RSS as heap-used; the JSON omits heap_used_bytes and
    # records the fallback reason while still making the capacity ceiling
    # auditable.
    if [[ ! "${heap_max}" =~ ^[0-9]+$ && "${jvm_pid}" =~ ^[0-9]+$ ]]; then
      jvm_cmdline="$(kubectl -n "${namespace}" exec "${hugegraph_pod}" -- cat "/proc/${jvm_pid}/cmdline" 2>/dev/null | tr '\000' ' ' || true)"
      xmx_raw="$(grep -oE -- '-Xmx[0-9]+[KkMmGg]' <<<"${jvm_cmdline}" | head -1 | sed 's/^-Xmx//' || true)"
      heap_max="$(to_bytes "${xmx_raw}" 2>/dev/null || true)"
    fi
    if [[ "${hg_rss}" =~ ^[0-9]+$ && "${heap_max}" =~ ^[0-9]+$ ]]; then
      status_hg="collected"; reason_hg=""
      if [[ ! "${heap_used}" =~ ^[0-9]+$ ]]; then
        reason_hg="heap_used unavailable in image; RSS and JVM -Xmx collected"
      fi
    else
      reason_hg="HugeGraph JVM RSS/heap metrics were incomplete"
    fi
    rocks_output="$(kubectl -n "${namespace}" exec "${hugegraph_pod}" -- sh -c 'du -sk /var/lib/hugegraph/data /var/lib/hugegraph/wal 2>/dev/null' 2>/dev/null || true)"
    rocks_data_k="$(awk 'NR == 1 {print $1}' <<<"${rocks_output}")"
    rocks_wal_k="$(awk 'NR == 2 {print $1}' <<<"${rocks_output}")"
    if [[ "${rocks_data_k}" =~ ^[0-9]+$ && "${rocks_wal_k}" =~ ^[0-9]+$ ]]; then
      rocks_data=$((rocks_data_k * 1024)); rocks_wal=$((rocks_wal_k * 1024))
      status_rocks="collected"; reason_rocks=""
    else
      reason_rocks="HugeGraph RocksDB data/WAL paths were not measurable"
    fi
  fi

  collect_top() {
    local pod="$1"; local which="$2"; local line cpu_raw mem_raw cpu_value mem_value
    [[ -n "${pod}" ]] || return 0
    line="$(pod_top "${pod}")"
    cpu_raw="$(awk 'NR == 1 {print $2}' <<<"${line}")"
    mem_raw="$(awk 'NR == 1 {print $3}' <<<"${line}")"
    cpu_value="$(to_millicores "${cpu_raw}" 2>/dev/null || true)"
    mem_value="$(to_bytes "${mem_raw}" 2>/dev/null || true)"
    if [[ "${cpu_value}" =~ ^[0-9]+$ && "${mem_value}" =~ ^[0-9]+$ ]]; then
      if [[ "${which}" == "query" ]]; then
        query_cpu="${cpu_value}"; query_rss="${mem_value}"; status_query="collected"; reason_query=""
      else
        worker_cpu="${cpu_value}"; worker_rss="${mem_value}"; status_worker="collected"; reason_worker=""
      fi
    fi
  }
  collect_top "${query_pod}" query
  collect_top "${worker_pod}" worker
fi

if [[ -d "${frontend_dist}" ]]; then
  bundle_bytes="$(python3 - "${frontend_dist}" <<'PY'
import os, sys
root = sys.argv[1]
print(sum(os.path.getsize(os.path.join(directory, name))
          for directory, _, names in os.walk(root) for name in names))
PY
  )"
  if [[ "${bundle_bytes}" =~ ^[0-9]+$ ]]; then
    status_bundle="collected"; reason_bundle=""
  fi
fi

if command -v node >/dev/null 2>&1 && [[ -f "${repo_root}/observability-frontend/package.json" ]]; then
  browser_output="$(cd "${repo_root}/observability-frontend" && node - "${browser_url}" <<'NODE'
const url = process.argv[2];
let playwright;
try { playwright = require("playwright"); } catch (_) { process.exit(3); }
(async () => {
  const browser = await playwright.chromium.launch({headless: true});
  const page = await browser.newPage();
  await page.addInitScript(() => {
    window.__aiopsLongTasks = {count: 0, max: 0};
    if (window.PerformanceObserver) {
      try {
        const observer = new PerformanceObserver((list) => {
          for (const entry of list.getEntries()) {
            window.__aiopsLongTasks.count += 1;
            window.__aiopsLongTasks.max = Math.max(window.__aiopsLongTasks.max, entry.duration);
          }
        });
        observer.observe({type: "longtask", buffered: true});
      } catch (_) {}
    }
  });
  await page.goto(url, {waitUntil: "networkidle", timeout: 30000});
  await page.waitForTimeout(1000);
  console.log(JSON.stringify(await page.evaluate(() => window.__aiopsLongTasks)));
  await browser.close();
})().catch(() => process.exit(4));
NODE
  )" || true
  if [[ -z "${browser_output}" && -x "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" && -f "${repo_root}/deploy/scripts/collect-browser-long-tasks.js" ]]; then
    browser_output="$(node "${repo_root}/deploy/scripts/collect-browser-long-tasks.js" "${browser_url}" "${AIOPS_BROWSER_BIN:-}" 2>/dev/null || true)"
  fi
  if [[ -n "${browser_output}" ]]; then
    read -r long_task_count long_task_max < <(python3 - "${browser_output}" <<'PY'
import json, sys
data = json.loads(sys.argv[1])
print(int(data["count"]), int(float(data["max"])))
PY
    )
    if [[ "${long_task_count}" =~ ^[0-9]+$ && "${long_task_max}" =~ ^[0-9]+$ ]]; then
      status_browser="collected"; reason_browser=""
    fi
  fi
fi

export status_hg reason_hg hg_rss heap_used heap_max
export status_rocks reason_rocks rocks_data rocks_wal
export status_query reason_query query_cpu query_rss
export status_worker reason_worker worker_cpu worker_rss
export status_bundle reason_bundle bundle_bytes
export status_browser reason_browser long_task_count long_task_max
python3 - "${output}" <<'PY'
import json, os, sys

def number(name):
    raw = os.environ.get(name, "")
    return int(raw) if raw.isdigit() else None

def item(status_name, reason_name, fields):
    value = {"status": os.environ[status_name]}
    reason = os.environ.get(reason_name, "")
    if reason:
        value["reason"] = reason
    for output_name, json_name in fields:
        parsed = number(output_name)
        if parsed is not None:
            value[json_name] = parsed
    return value

report = {
    "hugegraph_jvm_rss_heap": item("status_hg", "reason_hg", [
        ("hg_rss", "jvm_rss_bytes"), ("heap_used", "heap_used_bytes"), ("heap_max", "heap_max_bytes")]),
    "rocksdb_disk_wal": item("status_rocks", "reason_rocks", [
        ("rocks_data", "data_bytes"), ("rocks_wal", "wal_bytes")]),
    "query_api_cpu_rss": item("status_query", "reason_query", [
        ("query_cpu", "cpu_millicores"), ("query_rss", "rss_bytes")]),
    "ai_investigation_worker_cpu_rss": item("status_worker", "reason_worker", [
        ("worker_cpu", "cpu_millicores"), ("worker_rss", "rss_bytes")]),
    "frontend_bundle_bytes": item("status_bundle", "reason_bundle", [("bundle_bytes", "value")]),
    "browser_long_tasks": item("status_browser", "reason_browser", [
        ("long_task_count", "count"), ("long_task_max", "max_duration_ms")]),
}
json.dump(report, open(sys.argv[1], "w", encoding="utf-8"), ensure_ascii=False, indent=2)
PY
cat "${output}"
