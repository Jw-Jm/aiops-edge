#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
chart_dir="${repo_root}/deploy/helm/aiops"
generator="${repo_root}/deploy/scripts/generate-local-secrets.sh"
tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/aiops-secret-format.XXXXXX")"
go_tmp_dir="$(mktemp -d "${repo_root}/ai-apm-query-go/.secret-format-check.XXXXXX")"
trap 'rm -rf "${tmp_dir}" "${go_tmp_dir}"' EXIT

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "missing required command: $1" >&2
    exit 2
  }
}

require_cmd bash
require_cmd go
require_cmd helm
require_cmd python3
require_cmd rg

output_env="${tmp_dir}/local-secrets.env"

if [[ ! -x "${generator}" ]]; then
  echo "secret format contract failed: generator script is missing or not executable: ${generator}" >&2
  exit 1
fi

generated_path="$(bash "${generator}" --output "${output_env}")"
if [[ "${generated_path}" != "${output_env}" ]]; then
  echo "secret format contract failed: generator must print only the requested output path" >&2
  exit 1
fi
if [[ ! -f "${output_env}" ]]; then
  echo "secret format contract failed: generator did not create ${output_env}" >&2
  exit 1
fi

# shellcheck disable=SC1090
source "${output_env}"

require_var() {
  local name="$1"
  if [[ -z "${!name:-}" ]]; then
    echo "secret format contract failed: ${name} is empty" >&2
    exit 1
  fi
}

for name in \
  JWT_SECRET \
  LLM_ENCRYPTION_KEY \
  INTERNAL_TOKEN \
  INGEST_API_KEY \
  CLICKHOUSE_PASSWORD \
  MYSQL_ROOT_PASSWORD \
  MYSQL_APP_PASSWORD \
  MYSQL_MIGRATOR_PASSWORD \
  LLM_PROXY_TOKEN \
  LLM_PROVIDER_KEYS \
  ORCHESTRATOR_TO_QUERY_TOKEN \
  ORCHESTRATOR_TO_QUERY_SIGNING_KEY \
  ORCHESTRATOR_TO_QUERY_VERIFY_KEYS \
  QUERY_TO_ORCHESTRATOR_TOKEN \
  QUERY_TO_ORCHESTRATOR_SIGNING_KEY \
  QUERY_TO_ORCHESTRATOR_VERIFY_KEYS \
  EXECUTOR_TOKEN \
  AI_ACTION_EXECUTOR_SIGNING_KEY \
  AI_ACTION_EXECUTOR_VERIFY_KEYS
do
  require_var "${name}"
done

if [[ "${MYSQL_ROOT_PASSWORD}" == "${MYSQL_APP_PASSWORD}" || "${MYSQL_ROOT_PASSWORD}" == "${MYSQL_MIGRATOR_PASSWORD}" || "${MYSQL_APP_PASSWORD}" == "${MYSQL_MIGRATOR_PASSWORD}" ]]; then
  echo "secret format contract failed: MySQL root/app/migrator passwords must all be distinct" >&2
  exit 1
fi

cat >"${go_tmp_dir}/main.go" <<'EOF'
package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"os"

	trustedauth "github.com/observability-platform/ai-apm-query-go/internal/auth"
)

type keypair struct {
	name       string
	privateEnv string
	publicEnv  string
}

func mustEnv(name string) string {
	value := os.Getenv(name)
	if value == "" {
		fmt.Fprintf(os.Stderr, "missing env: %s\n", name)
		os.Exit(1)
	}
	return value
}

func main() {
	message := []byte("aiops-secret-format-contract")
	pairs := []keypair{
		{name: "orchestrator_to_query", privateEnv: "ORCHESTRATOR_TO_QUERY_SIGNING_KEY", publicEnv: "ORCHESTRATOR_TO_QUERY_VERIFY_KEYS"},
		{name: "query_to_orchestrator", privateEnv: "QUERY_TO_ORCHESTRATOR_SIGNING_KEY", publicEnv: "QUERY_TO_ORCHESTRATOR_VERIFY_KEYS"},
		{name: "query_to_executor", privateEnv: "AI_ACTION_EXECUTOR_SIGNING_KEY", publicEnv: "AI_ACTION_EXECUTOR_VERIFY_KEYS"},
	}

	seenPriv := map[string]struct{}{}
	seenPub := map[string]struct{}{}
	for _, pair := range pairs {
		privEncoded := mustEnv(pair.privateEnv)
		pubEncoded := mustEnv(pair.publicEnv)

		priv, err := trustedauth.DecodePrivateKey(privEncoded)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s decode failed: %v\n", pair.privateEnv, err)
			os.Exit(1)
		}
		if len(priv) != ed25519.PrivateKeySize {
			fmt.Fprintf(os.Stderr, "%s length = %d, want %d\n", pair.privateEnv, len(priv), ed25519.PrivateKeySize)
			os.Exit(1)
		}

		pubRaw, err := base64.RawURLEncoding.DecodeString(pubEncoded)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s decode failed: %v\n", pair.publicEnv, err)
			os.Exit(1)
		}
		if len(pubRaw) != ed25519.PublicKeySize {
			fmt.Fprintf(os.Stderr, "%s length = %d, want %d\n", pair.publicEnv, len(pubRaw), ed25519.PublicKeySize)
			os.Exit(1)
		}

		pub := ed25519.PublicKey(pubRaw)
		if string(priv[32:]) != string(pub) {
			fmt.Fprintf(os.Stderr, "%s does not match %s\n", pair.privateEnv, pair.publicEnv)
			os.Exit(1)
		}
		if _, exists := seenPriv[privEncoded]; exists {
			fmt.Fprintf(os.Stderr, "duplicate private key: %s\n", pair.privateEnv)
			os.Exit(1)
		}
		if _, exists := seenPub[pubEncoded]; exists {
			fmt.Fprintf(os.Stderr, "duplicate public key: %s\n", pair.publicEnv)
			os.Exit(1)
		}
		seenPriv[privEncoded] = struct{}{}
		seenPub[pubEncoded] = struct{}{}

		signature := ed25519.Sign(priv, message)
		fmt.Printf("%s %s %s\n", pair.name, base64.RawURLEncoding.EncodeToString(pub), base64.RawURLEncoding.EncodeToString(signature))
	}
}
EOF

go_signatures="$(
  export ORCHESTRATOR_TO_QUERY_SIGNING_KEY ORCHESTRATOR_TO_QUERY_VERIFY_KEYS
  export QUERY_TO_ORCHESTRATOR_SIGNING_KEY QUERY_TO_ORCHESTRATOR_VERIFY_KEYS
  export AI_ACTION_EXECUTOR_SIGNING_KEY AI_ACTION_EXECUTOR_VERIFY_KEYS
  (
    cd "${repo_root}/ai-apm-query-go"
    go run "${go_tmp_dir}"
  )
)"

export GO_SIGNATURES="${go_signatures}"
python3 <<'PY'
import base64
import os
import sys

from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PublicKey

message = b"aiops-secret-format-contract"
lines = [line.strip() for line in os.environ["GO_SIGNATURES"].splitlines() if line.strip()]
if len(lines) != 3:
    raise SystemExit(f"expected 3 Go signatures, got {len(lines)}")
for line in lines:
    name, pub_b64, sig_b64 = line.split(" ")
    pub = Ed25519PublicKey.from_public_bytes(base64.urlsafe_b64decode(pub_b64 + "=" * (-len(pub_b64) % 4)))
    sig = base64.urlsafe_b64decode(sig_b64 + "=" * (-len(sig_b64) % 4))
    pub.verify(sig, message)
    print(name)
PY

render_ok() {
  local output="$1"
  shift
  helm template aiops "${chart_dir}" \
    --namespace observability \
    --set secrets.jwtSecret="${JWT_SECRET}" \
    --set secrets.llmEncryptionKey="${LLM_ENCRYPTION_KEY}" \
    --set secrets.internalToken="${INTERNAL_TOKEN}" \
    --set secrets.ingestApiKey="${INGEST_API_KEY}" \
    --set secrets.clickhousePassword="${CLICKHOUSE_PASSWORD}" \
    --set secrets.mysqlRootPassword="${MYSQL_ROOT_PASSWORD}" \
    --set secrets.mysqlAppPassword="${MYSQL_APP_PASSWORD}" \
    --set secrets.mysqlMigratorPassword="${MYSQL_MIGRATOR_PASSWORD}" \
    --set secrets.llmProxyToken="${LLM_PROXY_TOKEN}" \
    --set secrets.llmProviderKeys="${LLM_PROVIDER_KEYS}" \
    --set secrets.orchestratorToQueryToken="${ORCHESTRATOR_TO_QUERY_TOKEN}" \
    --set secrets.orchestratorToQuerySigningKey="${ORCHESTRATOR_TO_QUERY_SIGNING_KEY}" \
    --set secrets.orchestratorToQueryVerifyKeys="${ORCHESTRATOR_TO_QUERY_VERIFY_KEYS}" \
    --set secrets.queryToOrchestratorToken="${QUERY_TO_ORCHESTRATOR_TOKEN}" \
    --set secrets.queryToOrchestratorSigningKey="${QUERY_TO_ORCHESTRATOR_SIGNING_KEY}" \
    --set secrets.queryToOrchestratorVerifyKeys="${QUERY_TO_ORCHESTRATOR_VERIFY_KEYS}" \
    --set secrets.executorToken="${EXECUTOR_TOKEN}" \
    --set secrets.aiActionExecutorSigningKey="${AI_ACTION_EXECUTOR_SIGNING_KEY}" \
    --set secrets.aiActionExecutorVerifyKeys="${AI_ACTION_EXECUTOR_VERIFY_KEYS}" \
    "$@" >"${output}"
}

expect_render_fail() {
  local name="$1"
  shift
  if render_ok "${tmp_dir}/${name}.yaml" "$@" >/dev/null 2>"${tmp_dir}/${name}.err"; then
    echo "secret format contract failed: ${name} should have failed Helm rendering" >&2
    exit 1
  fi
}

render_ok "${tmp_dir}/rendered.yaml"
for key in MYSQL_APP_PASSWORD MYSQL_MIGRATOR_PASSWORD; do
  if ! rg -n --fixed-strings "${key}:" "${tmp_dir}/rendered.yaml" >/dev/null; then
    echo "secret format contract failed: ${key} is not rendered into aiops-secrets" >&2
    exit 1
  fi
done

expect_render_fail missing_mysql_app --set secrets.mysqlAppPassword=
expect_render_fail placeholder_mysql_migrator --set secrets.mysqlMigratorPassword=CHANGE_ME
expect_render_fail missing_query_signing --set aiOrchestrator.enabled=true --set secrets.queryToOrchestratorVerifyKeys=
expect_render_fail missing_orchestrator_verify --set queryApi.enabled=true --set secrets.orchestratorToQueryVerifyKeys=
expect_render_fail missing_llm_provider_keys --set llmEgressProxy.enabled=true --set secrets.llmProviderKeys=
expect_render_fail missing_executor_verify_keys --set aiActionExecutor.enabled=true --set aiActionExecutor.realMutation=true --set secrets.aiActionExecutorVerifyKeys=

echo "secret format contract tests passed"
