#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage: generate-local-secrets.sh --output <path> [--force]
EOF
}

output_path=""
force="false"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --output)
      [[ $# -ge 2 ]] || {
        usage >&2
        exit 2
      }
      output_path="$2"
      shift 2
      ;;
    --force)
      force="true"
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      usage >&2
      exit 2
      ;;
  esac
done

if [[ -z "${output_path}" ]]; then
  usage >&2
  exit 2
fi

if [[ -e "${output_path}" && "${force}" != "true" ]]; then
  echo "refusing to overwrite existing file: ${output_path} (pass --force to replace it)" >&2
  exit 1
fi

if [[ -z "${LLM_PROVIDER_KEYS:-}" ]]; then
  echo "LLM_PROVIDER_KEYS must be supplied explicitly; refusing to generate a fake provider credential" >&2
  exit 1
fi

mkdir -p "$(dirname "${output_path}")"
output_file="${output_path}"
tmp_go_dir="$(mktemp -d "${TMPDIR:-/tmp}/aiops-generate-local-secrets.XXXXXX")"
tmp_output="${output_file}.tmp.$$"
trap 'rm -rf "${tmp_go_dir}" "${tmp_output}"' EXIT

cat >"${tmp_go_dir}/main.go" <<'EOF'
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
)

type keypair struct {
	privateName string
	publicName  string
}

func mustRandomURLBase64(numBytes int) string {
	buf := make([]byte, numBytes)
	if _, err := rand.Read(buf); err != nil {
		fmt.Fprintf(os.Stderr, "generate random bytes: %v\n", err)
		os.Exit(1)
	}
	return base64.RawURLEncoding.EncodeToString(buf)
}

func mustKeypair() (string, string) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		fmt.Fprintf(os.Stderr, "generate Ed25519 keypair: %v\n", err)
		os.Exit(1)
	}
	return base64.RawURLEncoding.EncodeToString(priv), base64.RawURLEncoding.EncodeToString(pub)
}

func main() {
	pairs := []keypair{
		{privateName: "ORCHESTRATOR_TO_QUERY_SIGNING_KEY", publicName: "ORCHESTRATOR_TO_QUERY_VERIFY_KEYS"},
		{privateName: "QUERY_TO_ORCHESTRATOR_SIGNING_KEY", publicName: "QUERY_TO_ORCHESTRATOR_VERIFY_KEYS"},
		{privateName: "AI_ACTION_EXECUTOR_SIGNING_KEY", publicName: "AI_ACTION_EXECUTOR_VERIFY_KEYS"},
	}
	values := map[string]string{
		"JWT_SECRET":                  mustRandomURLBase64(32),
		"LLM_ENCRYPTION_KEY":          mustRandomURLBase64(32),
		"INTERNAL_TOKEN":              mustRandomURLBase64(24),
		"INGEST_API_KEY":              mustRandomURLBase64(24),
		"CLICKHOUSE_PASSWORD":         mustRandomURLBase64(24),
		"MYSQL_ROOT_PASSWORD":         mustRandomURLBase64(24),
		"MYSQL_APP_PASSWORD":          mustRandomURLBase64(24),
		"MYSQL_MIGRATOR_PASSWORD":     mustRandomURLBase64(24),
		// Local bootstrap uses the documented first-login credential. The query-api
		// forces an immediate password change after this one-time seed.
		"ADMIN_INITIAL_PASSWORD":      "admin123",
		"LLM_PROXY_TOKEN":             mustRandomURLBase64(24),
		"EXECUTOR_TOKEN":              mustRandomURLBase64(24),
		"ORCHESTRATOR_TO_QUERY_TOKEN": mustRandomURLBase64(24),
		"QUERY_TO_ORCHESTRATOR_TOKEN": mustRandomURLBase64(24),
	}

	for _, pair := range pairs {
		privateValue, publicValue := mustKeypair()
		values[pair.privateName] = privateValue
		values[pair.publicName] = publicValue
	}

	values["LLM_PROVIDER_KEYS"] = os.Getenv("LLM_PROVIDER_KEYS")

	order := []string{
		"JWT_SECRET",
		"LLM_ENCRYPTION_KEY",
		"INTERNAL_TOKEN",
		"INGEST_API_KEY",
		"CLICKHOUSE_PASSWORD",
		"MYSQL_ROOT_PASSWORD",
		"MYSQL_APP_PASSWORD",
		"MYSQL_MIGRATOR_PASSWORD",
		"ADMIN_INITIAL_PASSWORD",
		"LLM_PROXY_TOKEN",
		"LLM_PROVIDER_KEYS",
		"ORCHESTRATOR_TO_QUERY_TOKEN",
		"ORCHESTRATOR_TO_QUERY_SIGNING_KEY",
		"ORCHESTRATOR_TO_QUERY_VERIFY_KEYS",
		"QUERY_TO_ORCHESTRATOR_TOKEN",
		"QUERY_TO_ORCHESTRATOR_SIGNING_KEY",
		"QUERY_TO_ORCHESTRATOR_VERIFY_KEYS",
		"EXECUTOR_TOKEN",
		"AI_ACTION_EXECUTOR_SIGNING_KEY",
		"AI_ACTION_EXECUTOR_VERIFY_KEYS",
	}

	for _, key := range order {
		fmt.Printf("export %s='%s'\n", key, values[key])
	}
}
EOF

go run "${tmp_go_dir}/main.go" >"${tmp_output}"
chmod 600 "${tmp_output}"
mv "${tmp_output}" "${output_file}"
printf '%s\n' "${output_file}"
