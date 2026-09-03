{{/*
AIOps Helm Chart helpers
*/}}

{{/*
aiops.name: chart 名
*/}}
{{- define "aiops.name" -}}
{{- .Chart.Name -}}
{{- end -}}

{{- define "aiops.mtlsEnv" -}}
{{- if .Values.internalTLS.enabled }}
- name: AIOPS_MTLS_REQUIRED
  value: {{ .Values.internalTLS.required | quote }}
- name: AIOPS_TLS_CERT_FILE
  value: {{ .Values.internalTLS.certFile | quote }}
- name: AIOPS_TLS_KEY_FILE
  value: {{ .Values.internalTLS.keyFile | quote }}
- name: AIOPS_TLS_CLIENT_CA_FILE
  value: {{ .Values.internalTLS.clientCAFile | quote }}
- name: AIOPS_TLS_CLIENT_SAN
  value: {{ required "internalTLS.clientSAN must be injected when mTLS is enabled" .Values.internalTLS.clientSAN | quote }}
{{- end }}
{{- end -}}

{{- define "aiops.mtlsVolumeMount" -}}
{{- if .Values.internalTLS.enabled }}
- name: internal-tls
  mountPath: {{ .Values.internalTLS.mountPath }}
  readOnly: true
{{- end }}
{{- end -}}

{{- define "aiops.mtlsVolume" -}}
{{- if .Values.internalTLS.enabled }}
- name: internal-tls
  secret:
    secretName: {{ .Values.internalTLS.secretName }}
    defaultMode: 0440
{{- end }}
{{- end -}}

{{- define "aiops.mtlsPodSecurityContext" -}}
{{- if .Values.internalTLS.enabled }}
securityContext:
  fsGroup: 65532
  fsGroupChangePolicy: OnRootMismatch
{{- end }}
{{- end -}}

{{- define "aiops.internalScheme" -}}
{{- if .Values.internalTLS.enabled -}}https{{- else -}}http{{- end -}}
{{- end -}}

{{/*
aiops.imageWithGlobalTag: render a self-owned image reference.

Args: image (repository, may carry a stale :tag), tag (global.imageTag),
digest (global.imageDigests.<component>, may be empty), env (global.environment).

Rules (P1-SUP2):
- production (env == "production"): digest is REQUIRED and must match
  sha256:<64 hex>. The rendered reference is <repository>@sha256:<64hex> and a
  tag-only release is refused (immutable identity cannot be a mutable tag).
- non-production: tag-based rendering is allowed; if a digest is provided it
  is honoured instead (still validated).

Component-level image values are kept for backwards-compatible registry
overrides, but their historical tag must never win over global.imageTag or
global.imageDigests. This is important during `helm upgrade --reuse-values`:
Helm retains old component values and would otherwise silently run a mixed
release.
*/}}
{{- define "aiops.imageWithGlobalTag" -}}
{{- $image := .image | toString | trim -}}
{{- $tag := .tag | toString | trim -}}
{{- $digest := .digest | default "" | toString | trim -}}
{{- $env := .env | default "" | toString | trim -}}
{{- if eq $image "" -}}
{{- fail "self-owned image repository must not be empty" -}}
{{- end -}}
{{- if $digest -}}
{{- if not (regexMatch "^sha256:[0-9a-f]{64}$" $digest) -}}
{{- fail (printf "invalid digest format %q (want sha256:<64 hex>) for image %s" $digest $image) -}}
{{- end -}}
{{- $repo := $image -}}
{{- if regexMatch ":[^/:]+$" $repo -}}
{{- $repo = regexReplaceAll ":[^/:]+$" $repo "" -}}
{{- end -}}
{{- printf "%s@%s" $repo $digest -}}
{{- else if eq $env "production" -}}
{{- fail (printf "production requires an immutable digest for self-owned image %s: set global.imageDigests.<component> (tag-only releases are not permitted; got tag %q)" $image $tag) -}}
{{- else -}}
{{- if eq $tag "" -}}
{{- fail "global.imageTag must not be empty" -}}
{{- end -}}
{{- if contains "@" $image -}}
{{- fail (printf "self-owned image %s must use global.imageTag instead of a digest" $image) -}}
{{- else if regexMatch ":[^/:]+$" $image -}}
{{- regexReplaceAll ":[^/:]+$" $image (printf ":%s" $tag) -}}
{{- else -}}
{{- printf "%s:%s" $image $tag -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/*
aiops.queryApiCommonEnv: shared query runtime env for http, run-dispatch, and alert-eval.
*/}}
{{- define "aiops.queryApiCommonEnv" -}}
- name: AIOPS_ENV
  value: {{ .Values.global.environment | default "production" | quote }}
- name: AIOPS_SYSTEM_TENANT_ID
  value: {{ .Values.queryApi.systemTenantId | quote }}
- name: AIOPS_SYSTEM_TENANT_NAME
  value: {{ .Values.queryApi.systemTenantName | quote }}
- name: AIOPS_SYSTEM_CLUSTER_ID
  value: {{ .Values.queryApi.systemClusterId | quote }}
- name: AIOPS_SYSTEM_CLUSTER_SLUG
  value: {{ .Values.queryApi.systemClusterSlug | quote }}
- name: AIOPS_SYSTEM_CLUSTER_NAME
  value: {{ .Values.queryApi.systemClusterName | quote }}
- name: AIOPS_SYSTEM_CLUSTER_ENVIRONMENT
  value: {{ .Values.queryApi.systemClusterEnvironment | quote }}
- name: AIOPS_SYSTEM_CLUSTER_REGION
  value: {{ .Values.queryApi.systemClusterRegion | quote }}
- name: AIOPS_SYSTEM_CLUSTER_CREDENTIAL_REF
  value: {{ .Values.queryApi.systemClusterCredentialRef | quote }}
- name: AIOPS_SYSTEM_CLUSTER_IDENTITY_UID
  value: {{ .Values.queryApi.systemClusterIdentityUID | quote }}
- name: AUTH_REQUIRE_FIRST_LOGIN_PASSWORD_CHANGE
  value: {{ .Values.queryApi.authRequireFirstLoginPasswordChange | quote }}
- name: CLICKHOUSE_HOST
  value: {{ .Values.queryApi.clickhouseHost | quote }}
- name: CLICKHOUSE_PORT
  value: {{ .Values.queryApi.clickhousePort | quote }}
- name: CLICKHOUSE_USER
  value: {{ .Values.clickhouse.user | default "default" | quote }}
- name: CLICKHOUSE_PASSWORD
  valueFrom:
    secretKeyRef: { name: aiops-secrets, key: CLICKHOUSE_PASSWORD }
- name: VICTORIA_METRICS_URL
  value: {{ .Values.queryApi.victoriaMetricsUrl | quote }}
- name: VICTORIA_LOGS_URL
  value: {{ .Values.queryApi.victoriaLogsUrl }}/insert/jsonline
- name: QUERY_READER_MODE
  value: {{ .Values.queryApi.queryReaderMode | default "legacy" | quote }}
- name: GRAPH_BACKEND
  value: {{ tpl (.Values.queryApi.graphBackend | default .Values.graph.backend) . | quote }}
- name: HUGEGRAPH_URL
  value: {{ tpl .Values.queryApi.hugeGraphUrl . | quote }}
- name: HUGEGRAPH_GRAPHSPACE
  value: {{ tpl .Values.queryApi.hugeGraphGraphspace . | quote }}
- name: HUGEGRAPH_GRAPH
  value: {{ tpl .Values.queryApi.hugeGraphGraph . | quote }}
- name: HUGEGRAPH_USERNAME
  valueFrom:
    secretKeyRef: { name: aiops-secrets, key: HUGEGRAPH_USERNAME }
- name: HUGEGRAPH_PASSWORD
  valueFrom:
    secretKeyRef: { name: aiops-secrets, key: HUGEGRAPH_PASSWORD }
- name: GRAPH_READ_TIMEOUT_MS
  value: {{ tpl .Values.queryApi.graphReadTimeoutMs . | quote }}
- name: GRAPH_WRITE_TIMEOUT_MS
  value: {{ tpl .Values.queryApi.graphWriteTimeoutMs . | quote }}
- name: K8S_API_URL
  value: {{ .Values.queryApi.k8sApiUrl | quote }}
- name: K8S_INSECURE_SKIP_VERIFY
  value: {{ .Values.queryApi.k8sInsecureSkipVerify | default "false" | quote }}
- name: JWT_SECRET
  valueFrom:
    secretKeyRef: { name: aiops-secrets, key: JWT_SECRET }
- name: LLM_ENCRYPTION_KEY
  valueFrom:
    secretKeyRef: { name: aiops-secrets, key: LLM_ENCRYPTION_KEY }
- name: AI_LLM_EGRESS_PROXY_URL
  value: {{ printf "%s://ai-llm-egress-proxy.%s.svc.cluster.local:8080" (include "aiops.internalScheme" .) .Values.namespace.observability | quote }}
- name: AI_ORCHESTRATOR_URL
  value: {{ printf "%s://ai-orchestrator.%s.svc.cluster.local:8080" (include "aiops.internalScheme" .) .Values.namespace.observability | quote }}
- name: AI_INVESTIGATION_WORKER_URL
  value: {{ printf "%s://ai-investigation-worker.%s.svc.cluster.local:8080" (include "aiops.internalScheme" .) .Values.namespace.observability | quote }}
- name: LLM_PROXY_TOKEN
  valueFrom:
    secretKeyRef: { name: aiops-secrets, key: LLM_PROXY_TOKEN }
- name: INTERNAL_TOKEN
  valueFrom:
    secretKeyRef: { name: aiops-secrets, key: INTERNAL_TOKEN }
- name: TRUSTED_CONTEXT_ISSUER
  value: "ai-orchestrator"
- name: TRUSTED_CONTEXT_PUBLIC_KEYS
  valueFrom:
    secretKeyRef: { name: aiops-secrets, key: ORCHESTRATOR_TO_QUERY_VERIFY_KEYS }
- name: QUERY_TO_ORCHESTRATOR_SIGNING_KEY
  valueFrom:
    secretKeyRef: { name: aiops-secrets, key: QUERY_TO_ORCHESTRATOR_SIGNING_KEY }
- name: QUERY_TO_ORCHESTRATOR_TOKEN
  valueFrom:
    secretKeyRef: { name: aiops-secrets, key: QUERY_TO_ORCHESTRATOR_TOKEN }
- name: AI_ACTION_EXECUTOR_URL
  value: {{ .Values.queryApi.actionExecutorUrl | default (printf "%s://ai-action-executor.%s.svc.cluster.local:8080" (include "aiops.internalScheme" .) .Values.namespace.observability) | quote }}
- name: AI_ACTION_EXECUTOR_SIGNING_KEY
  valueFrom:
    secretKeyRef: { name: aiops-secrets, key: AI_ACTION_EXECUTOR_SIGNING_KEY }
- name: EXECUTOR_TOKEN
  valueFrom:
    secretKeyRef: { name: aiops-secrets, key: EXECUTOR_TOKEN }
- name: MYSQL_HOST
  value: {{ .Values.queryApi.mysqlHost | default "mysql" | quote }}
- name: MYSQL_PORT
  value: {{ .Values.queryApi.mysqlPort | default "3306" | quote }}
- name: MYSQL_USER
  value: {{ .Values.queryApi.mysqlUser | default "aiops_app" | quote }}
- name: MYSQL_PASSWORD
  valueFrom:
    secretKeyRef: { name: aiops-secrets, key: MYSQL_APP_PASSWORD }
- name: MYSQL_DB
  value: {{ .Values.queryApi.mysqlDb | default "aiops" | quote }}
- name: ADMIN_INITIAL_PASSWORD
  valueFrom:
    secretKeyRef: { name: aiops-secrets, key: ADMIN_INITIAL_PASSWORD }
{{- if .Values.internalTLS.enabled }}
- name: AIOPS_MTLS_REQUIRED
  value: {{ .Values.internalTLS.required | quote }}
- name: AIOPS_TLS_CERT_FILE
  value: {{ .Values.internalTLS.certFile | quote }}
- name: AIOPS_TLS_KEY_FILE
  value: {{ .Values.internalTLS.keyFile | quote }}
- name: AIOPS_TLS_CLIENT_CA_FILE
  value: {{ .Values.internalTLS.clientCAFile | quote }}
- name: AIOPS_TLS_CLIENT_SAN
  value: {{ required "internalTLS.clientSAN must be injected when mTLS is enabled" .Values.internalTLS.clientSAN | quote }}
{{- end }}
{{- end -}}

{{/*
aiops.fullname: release-chart 组合名
*/}}
{{- define "aiops.fullname" -}}
{{- printf "%s-%s" .Release.Name .Chart.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
aiops.ns: observability 命名空间
*/}}
{{- define "aiops.ns" -}}
{{- .Values.namespace.observability -}}
{{- end -}}

{{/*
aiops.requireSecret: 校验密钥值非空且非占位符（CHANGE_ME），否则渲染失败。
G5 安全加固：values-prod.yaml 的 CHANGE_ME 占位符必须被拒绝，不能通过 required 校验直接部署。
用法: {{ include "aiops.requireSecret" (dict "name" "secrets.jwtSecret" "value" .Values.secrets.jwtSecret) | quote }}
*/}}
{{- define "aiops.requireSecret" -}}
{{- $v := .value | default "" | trim -}}
{{- if or (eq $v "") (eq $v "CHANGE_ME") -}}
{{- fail (printf "%s 必须注入真实强随机值（空值与 CHANGE_ME 占位符将被拒绝，禁止占位符部署）" .name) -}}
{{- end -}}
{{- $v -}}
{{- end -}}

{{/*
aiops.requireSecretWhen: enabled=true 时密钥必须非空且非 CHANGE_ME；未启用时保留
调用方显式传入的值（便于 bootstrap -> runtime 升级不覆盖同一 Secret），无值时返回空字符串。
*/}}
{{- define "aiops.requireSecretWhen" -}}
{{- if .enabled -}}
{{- include "aiops.requireSecret" (dict "name" .name "value" .value) -}}
{{- else -}}
{{- $v := .value | default "" | trim -}}
{{- if eq $v "CHANGE_ME" -}}
{{- fail (printf "%s 不能使用 CHANGE_ME 占位符" .name) -}}
{{- end -}}
{{- $v -}}
{{- end -}}
{{- end -}}
