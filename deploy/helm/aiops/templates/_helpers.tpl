{{/*
AIOps Helm Chart helpers
*/}}

{{/*
aiops.name: chart 名
*/}}
{{- define "aiops.name" -}}
{{- .Chart.Name -}}
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
{{- $v := .value | default "" -}}
{{- if or (eq $v "") (eq $v "CHANGE_ME") -}}
{{- fail (printf "%s 必须注入真实强随机值（空值与 CHANGE_ME 占位符将被拒绝，禁止占位符部署）" .name) -}}
{{- end -}}
{{- $v -}}
{{- end -}}
