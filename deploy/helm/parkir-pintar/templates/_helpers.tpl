{{/* Common labels */}}
{{- define "parkir.labels" -}}
app.kubernetes.io/name: {{ .name }}
app.kubernetes.io/part-of: parkir-pintar
app.kubernetes.io/managed-by: {{ .Release.Service | default "Helm" }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
env: {{ .Values.global.env }}
{{- end }}

{{/* Image full name */}}
{{- define "parkir.image" -}}
{{ printf "%s/%s:%s" .Values.global.imageRegistry .name .Values.global.imageTag }}
{{- end }}

{{/* Common pod env vars */}}
{{- define "parkir.commonEnv" -}}
- name: APP_ENV
  value: {{ .Values.global.env | quote }}
- name: LOG_LEVEL
  value: "info"
- name: OTEL_EXPORTER_OTLP_ENDPOINT
  value: {{ .Values.opentelemetry.endpoint | quote }}
- name: NATS_URL
  value: {{ .Values.nats.url | quote }}
- name: NATS_STREAM
  value: {{ .Values.nats.stream | quote }}
{{- end }}
