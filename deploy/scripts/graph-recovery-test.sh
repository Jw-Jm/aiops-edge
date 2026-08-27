#!/usr/bin/env bash
set -euo pipefail

# Read-only recovery evidence. Failure injection is intentionally not part of
# local validation; an operator may run the documented maintenance procedure.
namespace="${GRAPH_NAMESPACE:-observability}"
usage() { echo "Usage: graph-recovery-test.sh [--namespace NS] [--offline]"; }
offline=0
while [[ $# -gt 0 ]]; do
  case "$1" in
    --namespace) namespace="${2:?--namespace requires a value}"; shift 2 ;;
    --offline) offline=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) usage >&2; exit 2 ;;
  esac
done
if [[ "${offline}" == "1" ]]; then
  echo '{"recovery_test":"planned","checks":["processing reclaim","outbox retry/dead","schema mismatch pause","generation stale grace"],"mutation":"none"}'
  exit 0
fi
command -v kubectl >/dev/null 2>&1 || { echo "kubectl is required" >&2; exit 2; }
kubectl -n "${namespace}" get statefulset/hugegraph -o wide
kubectl -n "${namespace}" get job/graph-schema-migrator -o jsonpath='{.status.succeeded}{"\n"}'
kubectl -n "${namespace}" get pods -l app=hugegraph -o jsonpath='{range .items[*]}{.metadata.name}{" ready="}{.status.containerStatuses[0].ready}{"\n"}{end}'
echo '{"recovery_test":"observed","failure_injection":"not_run","mutation":"none","note":"Use graph-cutover.md maintenance procedure for an explicit pod-failure drill"}'
