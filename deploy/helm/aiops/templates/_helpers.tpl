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
