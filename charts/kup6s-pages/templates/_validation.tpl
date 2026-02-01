{{/*
Validate webhook configuration
*/}}
{{- define "kup6s-pages.validateWebhook" -}}
{{- if .Values.webhook.enabled -}}
{{- if not (include "kup6s-pages.webhook.hasSecret" .) -}}
{{- fail "webhook.enabled=true requires a webhook secret. Set either webhook.secret or webhook.secretRef.name" -}}
{{- end -}}
{{- end -}}
{{- end -}}
