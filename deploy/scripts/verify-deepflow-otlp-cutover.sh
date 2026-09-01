#!/usr/bin/env bash
set -euo pipefail

# Read-only evidence harness for the staged DeepFlow -> Ingest OTLP cutover.
# Missing live evidence is a blocking result, never a synthetic pass.
namespace="${CUTOVER_NAMESPACE:-observability}"
deepflow_namespace="${CUTOVER_DEEPFLOW_NAMESPACE:-deepflow}"
tenant_id="${CUTOVER_TENANT_ID:-7ed01afc-cc79-4ecd-8767-a2befa6168ad}"
output="${CUTOVER_OUTPUT:-/tmp/aiops-deepflow-otlp-cutover.json}"
baseline="${CUTOVER_BASELINE_FILE:-}"
since_seconds="${CUTOVER_SINCE_SECONDS:-900}"
observe_seconds="${CUTOVER_OBSERVE_SECONDS:-20}"

usage() {
  cat <<'EOF'
Usage: verify-deepflow-otlp-cutover.sh [--output PATH] [--baseline PATH]

DeepFlow-owned ClickHouse evidence is opt-in:
  CUTOVER_DEEPFLOW_CH_POD       clickhouse pod in the deepflow namespace
  CUTOVER_DEEPFLOW_CH_SECRET    Secret containing CLICKHOUSE_PASSWORD

The platform ClickHouse defaults to pod clickhouse-0 and Secret aiops-secrets
in the observability namespace. Passwords are never written to the report.
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --output) output="${2:?--output requires a value}"; shift 2 ;;
    --baseline) baseline="${2:?--baseline requires a value}"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) usage >&2; exit 2 ;;
  esac
done

mkdir -p "$(dirname "${output}")"
gates_file="$(mktemp "${TMPDIR:-/tmp}/aiops-deepflow-gates.XXXXXX")"
tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/aiops-deepflow-cutover.XXXXXX")"
pf_pid=""
cleanup() {
  if [[ -n "${pf_pid}" ]]; then
    kill "${pf_pid}" >/dev/null 2>&1 || true
    wait "${pf_pid}" >/dev/null 2>&1 || true
  fi
  rm -f "${gates_file}"
  rm -rf "${tmp_dir}"
}
trap cleanup EXIT

record_gate() {
  python3 - "$gates_file" "$1" "$2" "$3" <<'PY'
import json, sys
path, name, status, reason = sys.argv[1:]
with open(path, "a", encoding="utf-8") as handle:
    handle.write(json.dumps({"name": name, "status": status, "reason": reason}, ensure_ascii=False) + "\n")
PY
}

if ! command -v kubectl >/dev/null 2>&1; then
  record_gate kubectl BLOCKED_BY_ENV "kubectl is unavailable"
else
  record_gate kubectl PASS "kubectl is available"
fi
if ! command -v curl >/dev/null 2>&1; then
  record_gate curl BLOCKED_BY_ENV "curl is unavailable"
else
  record_gate curl PASS "curl is available"
fi

ingest_pod=""
if command -v kubectl >/dev/null 2>&1; then
  ingest_pod="$(kubectl -n "${namespace}" get pods -l app=ingest -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{range .status.containerStatuses[*]}{.ready}{"\n"}{end}{end}' 2>/dev/null | awk '$2 == "true" {print $1; exit}' || true)"
  if [[ -n "${ingest_pod}" ]]; then
    record_gate ingest_ready PASS "ready ingest pod is present"
  else
    record_gate ingest_ready BLOCKED_BY_ENV "no ready ingest pod found"
  fi

  if kubectl -n "${namespace}" get endpoints ingest -o json >"${tmp_dir}/ingest-endpoints.json" 2>/dev/null && \
     python3 - "${tmp_dir}/ingest-endpoints.json" <<'PY'
import json, sys
doc = json.load(open(sys.argv[1], encoding="utf-8"))
ports = {p.get("port") for s in doc.get("subsets", []) for p in s.get("ports", [])}
addresses = [a for s in doc.get("subsets", []) for a in s.get("addresses", [])]
raise SystemExit(0 if 4317 in ports and addresses else 1)
PY
  then
    record_gate ingest_otlp_endpoint PASS "ingest Service has ready TCP 4317 endpoints"
  else
    record_gate ingest_otlp_endpoint BLOCKED_BY_ENV "ingest Service has no ready TCP 4317 endpoint"
  fi

  if kubectl -n "${namespace}" get deploy ingest -o json >"${tmp_dir}/ingest-deployment.json" 2>/dev/null && \
     python3 - "${tmp_dir}/ingest-deployment.json" <<'PY'
import json, sys
doc = json.load(open(sys.argv[1], encoding="utf-8"))
envs = {e.get("name", "") for c in doc.get("spec", {}).get("template", {}).get("spec", {}).get("containers", []) for e in c.get("env", [])}
forbidden = {"DEEPFLOW_" + "CH_HOST", "DEEPFLOW_" + "CH_ENDPOINTS", "DEEPFLOW_" + "CH_PORT"}
raise SystemExit(0 if not (envs & forbidden) else 1)
PY
  then
    record_gate ingest_no_legacy_clickhouse PASS "Ingest deployment has no legacy DeepFlow ClickHouse environment"
  else
    record_gate ingest_no_legacy_clickhouse FAIL "legacy DeepFlow ClickHouse environment is still rendered"
  fi
fi

metrics_text=""
if [[ -n "${ingest_pod}" ]] && command -v kubectl >/dev/null 2>&1 && command -v curl >/dev/null 2>&1; then
  local_port="${CUTOVER_METRICS_PORT:-18090}"
  metrics_url="http://127.0.0.1:${local_port}/metrics"
  metrics_curl_args=()
  if [[ "${CUTOVER_METRICS_SCHEME:-https}" == "https" ]]; then
    tls_secret="${CUTOVER_TLS_SECRET:-aiops-internal-tls}"
    if kubectl -n "${namespace}" get secret "${tls_secret}" -o jsonpath='{.data.tls\.crt}' 2>/dev/null | base64 --decode >"${tmp_dir}/client.crt" && \
       kubectl -n "${namespace}" get secret "${tls_secret}" -o jsonpath='{.data.tls\.key}' 2>/dev/null | base64 --decode >"${tmp_dir}/client.key"; then
      metrics_url="https://127.0.0.1:${local_port}/metrics"
      # The local port-forward endpoint has a pod/DNS certificate; keep server
      # verification explicit but use the mounted client identity for mTLS.
      metrics_curl_args+=(--insecure --cert "${tmp_dir}/client.crt" --key "${tmp_dir}/client.key")
    else
      record_gate ingest_metrics BLOCKED_BY_ENV "mTLS client certificate Secret is unavailable"
    fi
  fi
  kubectl -n "${namespace}" port-forward "pod/${ingest_pod}" "${local_port}:8080" >"${tmp_dir}/port-forward.log" 2>&1 &
  pf_pid=$!
  for _ in $(seq 1 30); do
    metrics_text="$(curl -fsS --max-time 2 "${metrics_curl_args[@]}" "${metrics_url}" 2>/dev/null || true)"
    [[ -n "${metrics_text}" ]] && break
    sleep 0.2
  done
  if [[ -n "${metrics_text}" ]]; then
    printf '%s\n' "${metrics_text}" >"${tmp_dir}/metrics.txt"
    record_gate ingest_metrics PASS "Ingest metrics endpoint is readable"
  else
    record_gate ingest_metrics BLOCKED_BY_ENV "Ingest metrics endpoint could not be read"
  fi
else
  record_gate ingest_metrics BLOCKED_BY_ENV "ready ingest pod or kubectl/curl is unavailable"
fi

metric_value() {
  awk -v metric="$1" '$1 == metric {print $2; exit}' "${tmp_dir}/metrics.txt" 2>/dev/null || true
}
received="$(metric_value ai_ingest_otlp_grpc_batches_received_total)"
accepted="$(metric_value ai_ingest_otlp_grpc_spans_accepted_total)"
if [[ "${received}" =~ ^[0-9]+$ && "${accepted}" =~ ^[0-9]+$ && "${received}" -gt 0 && "${accepted}" -gt 0 ]]; then
  record_gate otlp_counter_nonzero PASS "received=${received}, accepted=${accepted}"
else
  record_gate otlp_counter_nonzero BLOCKED_BY_ENV "OTLP counters are zero or unavailable"
fi
if [[ -n "${baseline}" && -f "${baseline}" && "${received}" =~ ^[0-9]+$ && "${accepted}" =~ ^[0-9]+$ ]] && \
   python3 - "${baseline}" "${received}" "${accepted}" <<'PY'
import json, sys
old = json.load(open(sys.argv[1], encoding="utf-8"))
raise SystemExit(0 if int(sys.argv[2]) > int(old.get("received", -1)) and int(sys.argv[3]) > int(old.get("accepted", -1)) else 1)
PY
then
  record_gate otlp_counter_increased PASS "counters increased from supplied baseline"
else
  record_gate otlp_counter_increased BLOCKED_BY_ENV "a prior real counter baseline is required"
fi

if command -v kubectl >/dev/null 2>&1 && kubectl -n "${deepflow_namespace}" get configmap -o json >"${tmp_dir}/deepflow-configmaps.json" 2>/dev/null && \
   python3 - "${tmp_dir}/deepflow-configmaps.json" <<'PY'
import json, sys
doc = json.load(open(sys.argv[1], encoding="utf-8"))
text = "\n".join(str(v) for item in doc.get("items", []) for v in (item.get("data", {}) or {}).values())
required = ("protocol: opentelemetry", "ingest.observability.svc.cluster.local:4317", "flow_log.l7_flow_log", "x-tenant-id")
raise SystemExit(0 if all(value in text for value in required) else 1)
PY
then
  record_gate deepflow_exporter_rendered PASS "live DeepFlow ConfigMap contains canonical OTLP exporter"
else
  record_gate deepflow_exporter_rendered BLOCKED_BY_ENV "DeepFlow namespace or exporter ConfigMap is unavailable"
fi

get_secret_password() {
  kubectl -n "$1" get secret "$2" -o jsonpath='{.data.CLICKHOUSE_PASSWORD}' 2>/dev/null | base64 --decode 2>/dev/null || true
}
query_count() {
  local ns="$1" pod="$2" password="$3" sql="$4"
  [[ -n "${pod}" && -n "${password}" ]] || return 1
  kubectl -n "${ns}" exec "${pod}" -- env CH_PASSWORD="${password}" sh -c \
    'clickhouse-client --password="$CH_PASSWORD" --query "$1"' sh "${sql}" 2>/dev/null | tr -d '[:space:]'
}

platform_pod="${CUTOVER_PLATFORM_CH_POD:-clickhouse-0}"
platform_secret="${CUTOVER_PLATFORM_CH_SECRET:-aiops-secrets}"
platform_password=""
if command -v kubectl >/dev/null 2>&1; then
  platform_password="$(get_secret_password "${namespace}" "${platform_secret}")"
fi
platform_count=""
if [[ -n "${platform_password}" ]]; then
  platform_count="$(query_count "${namespace}" "${platform_pod}" "${platform_password}" \
    "SELECT count() FROM observability.trace_spans WHERE tenant_id='${tenant_id}' AND start_time >= now() - INTERVAL ${since_seconds} SECOND" || true)"
fi
unset platform_password
if [[ "${platform_count}" =~ ^[1-9][0-9]*$ ]]; then
  record_gate platform_trace_rows PASS "fresh platform trace_spans rows=${platform_count}"
else
  record_gate platform_trace_rows BLOCKED_BY_ENV "fresh platform trace_spans rows could not be read"
fi

deepflow_pod="${CUTOVER_DEEPFLOW_CH_POD:-}"
deepflow_secret="${CUTOVER_DEEPFLOW_CH_SECRET:-}"
deepflow_password=""
deepflow_count=""
if [[ -n "${deepflow_pod}" && -n "${deepflow_secret}" ]] && command -v kubectl >/dev/null 2>&1; then
  deepflow_password="$(get_secret_password "${deepflow_namespace}" "${deepflow_secret}")"
  if [[ -n "${deepflow_password}" ]]; then
    deepflow_count="$(query_count "${deepflow_namespace}" "${deepflow_pod}" "${deepflow_password}" \
      "SELECT count() FROM flow_log.l7_flow_log WHERE time >= now() - INTERVAL ${since_seconds} SECOND" || true)"
  fi
fi
unset deepflow_password
if [[ "${deepflow_count}" =~ ^[1-9][0-9]*$ ]]; then
  record_gate deepflow_raw_rows PASS "fresh DeepFlow raw rows=${deepflow_count}"
else
  record_gate deepflow_raw_rows BLOCKED_BY_ENV "DeepFlow raw ClickHouse evidence requires explicit pod and Secret"
fi

if [[ "${observe_seconds}" =~ ^[0-9]+$ && "${observe_seconds}" -gt 0 && "${observe_seconds}" -le 60 && -n "${ingest_pod}" && -n "${metrics_text}" ]]; then
  sleep "${observe_seconds}"
  record_gate observation_window PASS "observed ${observe_seconds}s without changing runtime state"
else
  record_gate observation_window BLOCKED_BY_ENV "two-flush observation window was not available"
fi

python3 - "${gates_file}" "${output}" "${tenant_id}" "${received}" "${accepted}" "${platform_count}" "${deepflow_count}" <<'PY'
import datetime as dt
import json
import sys
gates_path, output, tenant, received, accepted, platform_count, deepflow_count = sys.argv[1:]
gates = [json.loads(line) for line in open(gates_path, encoding="utf-8") if line.strip()]
statuses = {item["status"] for item in gates}
if "FAIL" in statuses:
    status = "FAIL"
elif "BLOCKED_BY_ENV" in statuses:
    status = "BLOCKED_BY_ENV"
else:
    status = "PASS"
doc = {
    "schema_version": 1,
    "generated_at": dt.datetime.now(dt.timezone.utc).isoformat(),
    "tenant_id": tenant,
    "metrics": {
        "otlp_batches_received": int(received) if received.isdigit() else None,
        "otlp_spans_accepted": int(accepted) if accepted.isdigit() else None,
    },
    "rows": {
        "platform_trace_spans": int(platform_count) if platform_count.isdigit() else None,
        "deepflow_raw": int(deepflow_count) if deepflow_count.isdigit() else None,
    },
    "gates": gates,
    "gate_status": status,
}
json.dump(doc, open(output, "w", encoding="utf-8"), ensure_ascii=False, indent=2)
print(json.dumps({"output": output, "gate_status": status}, ensure_ascii=False))
raise SystemExit(0 if status == "PASS" else (1 if status == "FAIL" else 2))
PY
