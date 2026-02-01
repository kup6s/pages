{{/*
Expand the name of the chart.
*/}}
{{- define "kup6s-pages.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "kup6s-pages.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Target namespace
*/}}
{{- define "kup6s-pages.namespace" -}}
{{- default .Release.Namespace .Values.namespace }}
{{- end }}

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "kup6s-pages.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "kup6s-pages.labels" -}}
helm.sh/chart: {{ include "kup6s-pages.chart" . }}
app.kubernetes.io/name: {{ include "kup6s-pages.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Operator labels
*/}}
{{- define "kup6s-pages.operator.labels" -}}
{{ include "kup6s-pages.labels" . }}
app.kubernetes.io/component: operator
{{- end }}

{{/*
Operator selector labels
*/}}
{{- define "kup6s-pages.operator.selectorLabels" -}}
app.kubernetes.io/name: {{ include "kup6s-pages.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/component: operator
{{- end }}

{{/*
Syncer labels
*/}}
{{- define "kup6s-pages.syncer.labels" -}}
{{ include "kup6s-pages.labels" . }}
app.kubernetes.io/component: syncer
{{- end }}

{{/*
Syncer selector labels
*/}}
{{- define "kup6s-pages.syncer.selectorLabels" -}}
app.kubernetes.io/name: {{ include "kup6s-pages.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/component: syncer
{{- end }}

{{/*
nginx labels
*/}}
{{- define "kup6s-pages.nginx.labels" -}}
{{ include "kup6s-pages.labels" . }}
app.kubernetes.io/component: nginx
{{- end }}

{{/*
nginx selector labels
*/}}
{{- define "kup6s-pages.nginx.selectorLabels" -}}
app.kubernetes.io/name: {{ include "kup6s-pages.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/component: nginx
{{- end }}

{{/*
Operator service account name
*/}}
{{- define "kup6s-pages.operator.serviceAccountName" -}}
{{- if .Values.operator.serviceAccount.create }}
{{- default (printf "%s-operator" (include "kup6s-pages.fullname" .)) .Values.operator.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.operator.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Syncer service account name
*/}}
{{- define "kup6s-pages.syncer.serviceAccountName" -}}
{{- if .Values.syncer.serviceAccount.create }}
{{- default (printf "%s-syncer" (include "kup6s-pages.fullname" .)) .Values.syncer.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.syncer.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Build image reference
Usage: {{ include "kup6s-pages.image" (dict "image" .Values.operator.image "defaultTag" .Chart.AppVersion) }}
*/}}
{{- define "kup6s-pages.image" -}}
{{- $registry := .image.registry -}}
{{- $repository := .image.repository -}}
{{- $tag := default .defaultTag .image.tag -}}
{{- if $registry -}}
{{- printf "%s/%s:%s" $registry $repository $tag -}}
{{- else -}}
{{- printf "%s:%s" $repository $tag -}}
{{- end -}}
{{- end }}

{{/*
PVC claim name
*/}}
{{- define "kup6s-pages.pvcName" -}}
{{- if .Values.storage.existingClaim }}
{{- .Values.storage.existingClaim }}
{{- else }}
{{- printf "%s-sites" (include "kup6s-pages.fullname" .) }}
{{- end }}
{{- end }}

{{/*
Webhook ClusterIssuer
*/}}
{{- define "kup6s-pages.webhook.clusterIssuer" -}}
{{- default .Values.operator.clusterIssuer .Values.webhook.clusterIssuer }}
{{- end }}

{{/*
Check if webhook secret is configured
Returns true if either webhook.secret or webhook.secretRef.name is set
*/}}
{{- define "kup6s-pages.webhook.hasSecret" -}}
{{- if or .Values.webhook.secret .Values.webhook.secretRef.name -}}
true
{{- end -}}
{{- end }}

{{/*
Webhook secret name (for secretRef)
*/}}
{{- define "kup6s-pages.webhook.secretName" -}}
{{- if .Values.webhook.secretRef.name -}}
{{- .Values.webhook.secretRef.name -}}
{{- else -}}
{{- printf "%s-webhook-secret" (include "kup6s-pages.fullname" .) -}}
{{- end -}}
{{- end }}
