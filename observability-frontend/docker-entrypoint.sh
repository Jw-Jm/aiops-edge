#!/bin/sh
set -eu

# DeepFlow is an optional dependency. When it is disabled, remove only the
# Grafana proxy block before nginx parses the configuration; the SPA and API
# routes remain available for a standalone local deployment.
if [ "${DEEPFLOW_ENABLED:-false}" != "true" ]; then
    sed -i '/^[[:space:]]*# AIOPS_DEEPFLOW_PROXY_BEGIN[[:space:]]*$/,/^[[:space:]]*# AIOPS_DEEPFLOW_PROXY_END[[:space:]]*$/d' \
        /etc/nginx/conf.d/default.conf
fi

# Query API, like the other platform services, can expose its listener through
# the local/prod internalTLS profile.  Nginx itself remains the browser-facing
# HTTP endpoint, but its upstream transport must follow that profile.  Public
# Query routes do not require a client certificate; server verification via the
# mounted CA is still mandatory in TLS mode.
if [ "${AIOPS_INTERNAL_TLS_ENABLED:-false}" = "true" ]; then
    sed -i 's#proxy_pass http://query-api\.observability\.svc\.cluster\.local:8080#proxy_pass https://query-api.observability.svc.cluster.local:8080#g' \
        /etc/nginx/conf.d/default.conf
    tmp_conf="$(mktemp)"
    awk '
      /# AIOPS_QUERY_TLS_BEGIN/ { print; enabled=1; next }
      /# AIOPS_QUERY_TLS_END/   { print; enabled=0; next }
      enabled { sub(/^# /, ""); print; next }
      { print }
    ' /etc/nginx/conf.d/default.conf >"${tmp_conf}"
    mv "${tmp_conf}" /etc/nginx/conf.d/default.conf
else
    tmp_conf="$(mktemp)"
    awk '
      /# AIOPS_QUERY_TLS_BEGIN/ { disabled=1; next }
      /# AIOPS_QUERY_TLS_END/   { disabled=0; next }
      !disabled { print }
    ' /etc/nginx/conf.d/default.conf >"${tmp_conf}"
    mv "${tmp_conf}" /etc/nginx/conf.d/default.conf
fi

exec "$@"
