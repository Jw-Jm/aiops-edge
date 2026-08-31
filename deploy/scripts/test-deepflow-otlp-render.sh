#!/usr/bin/env bash
set -euo pipefail

# Verify the official DeepFlow chart receives the OTLP exporter values.  This
# is a contract test, not a runtime evidence gate: the cutover harness must
# additionally prove fresh rows and accepted counters in a live cluster.
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
values_file="${DEEPFLOW_VALUES_FILE:-${repo_root}/deploy/helm/aiops/values-deepflow.yaml}"
chart_version="${DEEPFLOW_CHART_VERSION:-7.1.002}"
require_render="${DEEPFLOW_REQUIRE_RENDER:-0}"

[[ -f "${values_file}" ]] || { echo "DeepFlow values file not found: ${values_file}" >&2; exit 2; }

render_file=""
cleanup() {
  [[ -z "${render_file}" ]] || rm -f "${render_file}"
}
trap cleanup EXIT

chart_ref="${DEEPFLOW_CHART_PACKAGE:-}"
if [[ -z "${chart_ref}" ]] && command -v helm >/dev/null 2>&1; then
  # Helm 3.13 and older do not support `helm env --template`; parse the
  # quoted key from the portable env output instead.
  cache_dir="$(helm env 2>/dev/null | sed -n 's/^HELM_REPOSITORY_CACHE="\(.*\)"$/\1/p' | head -n 1)"
  if [[ -n "${cache_dir}" && -f "${cache_dir}/deepflow-${chart_version}.tgz" ]]; then
    chart_ref="${cache_dir}/deepflow-${chart_version}.tgz"
  fi
fi

if [[ -n "${chart_ref}" ]]; then
  command -v helm >/dev/null 2>&1 || { echo "helm is required to render ${chart_ref}" >&2; exit 2; }
  render_file="$(mktemp "${TMPDIR:-/tmp}/aiops-deepflow-render.XXXXXX.yaml")"
  if ! helm template deepflow "${chart_ref}" --namespace deepflow -f "${values_file}" >"${render_file}"; then
    echo "DeepFlow chart rendering failed" >&2
    exit 1
  fi
  input_file="${render_file}"
  input_mode="rendered-chart"
else
  if [[ "${require_render}" == "1" ]]; then
    echo "DeepFlow chart package unavailable; refusing static-only verification" >&2
    exit 1
  fi
  input_file="${values_file}"
  input_mode="values-static"
fi

python3 - "${input_file}" "${input_mode}" <<'PY'
import json
import re
import sys

try:
    import yaml
except Exception as exc:
    raise SystemExit(f"PyYAML is required for DeepFlow render contract: {exc}")

path, mode = sys.argv[1:]
documents = list(yaml.safe_load_all(open(path, encoding="utf-8")))
configs = []
for document in documents:
    if not isinstance(document, dict):
        continue
    if mode == "values-static":
        configs.append(document.get("configmap", {}).get("server.yaml", {}))
        continue
    if document.get("kind") != "ConfigMap":
        continue
    data = document.get("data", {}) or {}
    raw = data.get("server.yaml")
    if raw:
        try:
            configs.append(yaml.safe_load(raw) or {})
        except yaml.YAMLError as exc:
            raise SystemExit(f"rendered DeepFlow server.yaml is invalid YAML: {exc}")

if not configs:
    raise SystemExit(f"{mode}: no DeepFlow server.yaml ConfigMap found")

exporters = []
for config in configs:
    ingester = config.get("ingester", {}) if isinstance(config, dict) else {}
    exporters.extend(ingester.get("exporters", []) or [])

target = next((item for item in exporters
               if isinstance(item, dict) and item.get("protocol") == "opentelemetry"), None)
if target is None:
    raise SystemExit(f"{mode}: enabled opentelemetry exporter is missing")
if target.get("enabled") is not True:
    raise SystemExit(f"{mode}: opentelemetry exporter must set enabled: true")
if target.get("endpoints") != ["ingest.observability.svc.cluster.local:4317"]:
    raise SystemExit(f"{mode}: exporter endpoint is not the canonical Ingest OTLP service")
if "flow_log.l7_flow_log" not in (target.get("data-sources") or []):
    raise SystemExit(f"{mode}: flow_log.l7_flow_log is not exported")
for key, expected in (("queue-count", 4), ("queue-size", 100000),
                      ("batch-size", 32), ("flush-timeout", 10)):
    if target.get(key) != expected:
        raise SystemExit(f"{mode}: exporter {key}={target.get(key)!r}, want {expected!r}")
if set(target.get("export-fields") or []) != {"$tag", "$metrics", "$k8s.label"}:
    raise SystemExit(f"{mode}: exporter fields are incomplete or unexpected")
headers = target.get("extra-headers") or {}
tenant = str(headers.get("x-tenant-id") or "")
if not re.fullmatch(r"[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}", tenant, re.I):
    raise SystemExit(f"{mode}: x-tenant-id must be a canonical UUID")
if re.search(r"password|secret|token|api[_-]?key|provider[_-]?key", json.dumps(target, ensure_ascii=False), re.I):
    raise SystemExit(f"{mode}: exporter block contains a credential-like field")

print(json.dumps({
    "status": "PASS",
    "input_mode": mode,
    "protocol": target["protocol"],
    "endpoint": target["endpoints"][0],
    "data_source": "flow_log.l7_flow_log",
    "tenant_id": tenant,
}, ensure_ascii=False))
PY
