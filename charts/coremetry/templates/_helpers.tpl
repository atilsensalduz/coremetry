{{/* vim: set filetype=mustache: */}}

{{- define "coremetry.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "coremetry.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name (include "coremetry.name" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}

{{- define "coremetry.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "coremetry.labels" -}}
helm.sh/chart: {{ include "coremetry.chart" . }}
{{ include "coremetry.selectorLabels" . }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{- define "coremetry.selectorLabels" -}}
app.kubernetes.io/name: {{ include "coremetry.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "coremetry.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "coremetry.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}

{{/*
Resolve the secret name. If the user supplied an existing secret, use that;
otherwise reference the one this chart creates.
*/}}
{{- define "coremetry.secretName" -}}
{{- if .Values.secrets.existingSecret -}}
{{- .Values.secrets.existingSecret -}}
{{- else -}}
{{- printf "%s-secret" (include "coremetry.fullname" .) -}}
{{- end -}}
{{- end -}}

{{/*
Redis URL: explicit external takes priority, then in-cluster service when
enabled, then empty (single-instance / no cache mode).
*/}}
{{- define "coremetry.redisURL" -}}
{{- if .Values.redis.external.url -}}
{{- .Values.redis.external.url -}}
{{- else if .Values.redis.enabled -}}
{{- printf "redis://%s-redis:6379/0" (include "coremetry.fullname" .) -}}
{{- end -}}
{{- end -}}

{{/*
coremetry.image renders a fully-qualified image reference for any of the
chart's components. Resolution order for the registry portion:

  1. global.imageRegistry (when set) — wins for ALL images, useful for
     air-gapped clusters that mirror everything into one internal registry
  2. <component>.image.registry — per-image override
  3. "" — no prefix, lets the runtime pick the default (docker.io)

Tag falls back to .defaultTag (typically Chart.AppVersion for the coremetry
image, hard-coded for upstream images).

Usage:
  image: {{ include "coremetry.image" (dict "imageRoot" .Values.image
                                          "global"    .Values.global
                                          "defaultTag" .Chart.AppVersion) }}
*/}}
{{- define "coremetry.image" -}}
{{- $registry := .imageRoot.registry | default "" -}}
{{- if and .global .global.imageRegistry -}}
{{- $registry = .global.imageRegistry -}}
{{- end -}}
{{- $repo := .imageRoot.repository -}}
{{- $tag := .imageRoot.tag | toString -}}
{{- if eq $tag "" -}}{{- $tag = (.defaultTag | toString) -}}{{- end -}}
{{- if eq $tag "" -}}{{- $tag = "latest" -}}{{- end -}}
{{- if $registry -}}
{{- printf "%s/%s:%s" $registry $repo $tag -}}
{{- else -}}
{{- printf "%s:%s" $repo $tag -}}
{{- end -}}
{{- end -}}

{{/*
coremetry.imagePullSecrets renders an `imagePullSecrets` list. Merges
global.imagePullSecrets and the per-image .image.pullSecrets if any.
*/}}
{{- define "coremetry.imagePullSecrets" -}}
{{- $secrets := list -}}
{{- if and .global .global.imagePullSecrets -}}
{{- $secrets = concat $secrets .global.imagePullSecrets -}}
{{- end -}}
{{- if .imageRoot.pullSecrets -}}
{{- $secrets = concat $secrets .imageRoot.pullSecrets -}}
{{- end -}}
{{- if $secrets }}
imagePullSecrets:
{{- range $secrets }}
  - name: {{ . }}
{{- end }}
{{- end }}
{{- end -}}

{{/*
ClickHouse address (host:port): explicit external takes priority, then
in-cluster service when enabled, then a sane "clickhouse:9000" placeholder
that will fail loudly so the misconfiguration is obvious.
*/}}
{{- define "coremetry.clickhouseAddr" -}}
{{- if .Values.clickhouse.external.addr -}}
{{- .Values.clickhouse.external.addr -}}
{{- else if .Values.clickhouse.enabled -}}
{{- printf "%s-clickhouse:%d" (include "coremetry.fullname" .) (int .Values.clickhouse.service.nativePort) -}}
{{- else -}}
clickhouse:9000
{{- end -}}
{{- end -}}
