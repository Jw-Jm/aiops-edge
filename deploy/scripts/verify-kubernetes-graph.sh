#!/usr/bin/env bash
set -euo pipefail

# Read-only post-deploy proof that the Kubernetes source reached the configured
# HugeGraph named graph. Credentials are used only inside the query-api pod and
# response bodies are intentionally not printed.
namespace="${GRAPH_NAMESPACE:-observability}"
since="${GRAPH_VERIFY_SINCE:-15m}"
query_api_deployment="${QUERY_API_DEPLOYMENT:-query-api-http}"

usage() {
  cat <<'EOF'
Usage: verify-kubernetes-graph.sh [--namespace NS] [--since DURATION]

Read-only verification. It checks the configured HugeGraph list, the named
graph endpoint, one current Kubernetes node vertex, and a successful worker
Kubernetes source reconcile with non-zero mutations and batches.
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --namespace) namespace="${2:?--namespace requires a value}"; shift 2 ;;
    --since) since="${2:?--since requires a value}"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) usage >&2; exit 2 ;;
  esac
done

for command_name in kubectl rg python3 base64; do
  command -v "${command_name}" >/dev/null 2>&1 || {
    echo "missing required command: ${command_name}" >&2
    exit 2
  }
done

die() {
  echo "Kubernetes graph verification failed: $1" >&2
  exit 1
}

query_api_env="$(kubectl -n "${namespace}" get deployment "${query_api_deployment}" -o jsonpath='{range .spec.template.spec.containers[0].env[*]}{.name}={.value}{"\n"}{end}')" \
  || die "query-api deployment is unavailable"

deployment_value() {
  local key="$1"
  awk -F= -v wanted="${key}" '$1 == wanted { sub(/^[^=]*=/, ""); print; exit }' <<<"${query_api_env}"
}

graphspace="$(deployment_value HUGEGRAPH_GRAPHSPACE)"
graph="$(deployment_value HUGEGRAPH_GRAPH)"
hugegraph_url="$(deployment_value HUGEGRAPH_URL)"
cluster_id="$(deployment_value AIOPS_SYSTEM_CLUSTER_ID)"
[[ -n "${graphspace}" && -n "${graph}" && -n "${hugegraph_url}" && -n "${cluster_id}" ]] \
  || die "query-api graph configuration is incomplete"
[[ "${graphspace}" =~ ^[A-Za-z0-9._-]+$ && "${graph}" =~ ^[A-Za-z0-9._-]+$ ]] \
  || die "unexpected graphspace or graph name"

secret_value() {
  local key="$1"
  kubectl -n "${namespace}" get secret aiops-secrets -o "jsonpath={.data.${key}}" | base64 -d
}

hugegraph_user="$(secret_value HUGEGRAPH_USERNAME)"
hugegraph_pass="$(secret_value HUGEGRAPH_PASSWORD)"
[[ -n "${hugegraph_user}" && -n "${hugegraph_pass}" ]] || die "HugeGraph credentials are missing"

node_uid="$(kubectl get nodes -o jsonpath='{.items[0].metadata.uid}')"
[[ -n "${node_uid}" ]] || die "the cluster has no Kubernetes node to verify"
entity_uid="k8s-k8s_node:v1:${cluster_id}:${node_uid}"
encoded_entity_uid="$(python3 -c 'import urllib.parse,sys; print(urllib.parse.quote(sys.argv[1], safe=""))' "${entity_uid}")"
vertex_url="${hugegraph_url%/}/graphspaces/${graphspace}/graphs/${graph}/graph/vertices/%22${encoded_entity_uid}%22"

kubectl -n "${namespace}" exec "deploy/${query_api_deployment}" -- \
  env "HG_USER=${hugegraph_user}" "HG_PASS=${hugegraph_pass}" \
  "HG_URL=${hugegraph_url%/}" "HG_SPACE=${graphspace}" "HG_GRAPH=${graph}" "HG_VERTEX_URL=${vertex_url}" \
  sh -c '
    set -eu
    auth="$(printf "%s:%s" "$HG_USER" "$HG_PASS" | base64 | tr -d "\n")"
    graphs="$(wget -q -O - --header="Authorization: Basic ${auth}" "${HG_URL}/graphs")"
    printf "%s\n" "$graphs" | grep -F "\"${HG_GRAPH}\"" >/dev/null
    wget -q -O /dev/null --header="Authorization: Basic ${auth}" \
      "${HG_URL}/graphspaces/${HG_SPACE}/graphs/${HG_GRAPH}"
    wget -q -O /dev/null --header="Authorization: Basic ${auth}" "${HG_VERTEX_URL}"
  ' >/dev/null \
  || die "named graph or projected Kubernetes node vertex is not readable"

worker_log="$(kubectl -n "${namespace}" logs deploy/ai-investigation-worker --since="${since}" --all-containers=true 2>/dev/null || true)"
rg -q 'source=kubernetes status=success' <<<"${worker_log}" \
  || die "worker has no successful Kubernetes source reconcile in ${since}"
rg -q 'source=kubernetes status=success .*mutations=[1-9][0-9]* .*batches=[1-9][0-9]*' <<<"${worker_log}" \
  || die "Kubernetes source reconcile did not report non-zero mutations and batches"

echo "Kubernetes graph verification passed"
echo "named_graph=${graphspace}/${graph}"
echo "projected_entity=k8s_node"
echo "source_reconcile=success"
