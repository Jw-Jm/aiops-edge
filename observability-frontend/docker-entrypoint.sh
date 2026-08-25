#!/bin/sh
set -eu

# DeepFlow is an optional dependency. When it is disabled, remove only the
# Grafana proxy block before nginx parses the configuration; the SPA and API
# routes remain available for a standalone local deployment.
if [ "${DEEPFLOW_ENABLED:-false}" != "true" ]; then
    sed -i '/^[[:space:]]*# AIOPS_DEEPFLOW_PROXY_BEGIN[[:space:]]*$/,/^[[:space:]]*# AIOPS_DEEPFLOW_PROXY_END[[:space:]]*$/d' \
        /etc/nginx/conf.d/default.conf
fi

exec "$@"
