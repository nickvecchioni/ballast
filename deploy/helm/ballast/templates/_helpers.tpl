{{/*
Expand the name of the chart.
*/}}
{{- define "ballast.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Fullname: release-name truncated to 63 chars.
*/}}
{{- define "ballast.fullname" -}}
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
Common labels applied to every resource.
*/}}
{{- define "ballast.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{ include "ballast.selectorLabels" . }}
{{- end }}

{{/*
Selector labels (subset used in matchLabels).
*/}}
{{- define "ballast.selectorLabels" -}}
app.kubernetes.io/name: {{ include "ballast.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
ServiceAccount name.
*/}}
{{- define "ballast.serviceAccountName" -}}
{{- if .Values.serviceAccount.name }}
{{- .Values.serviceAccount.name }}
{{- else }}
{{- include "ballast.fullname" . }}
{{- end }}
{{- end }}

{{/*
Collector component labels.
*/}}
{{- define "ballast.collector.labels" -}}
{{ include "ballast.labels" . }}
app.kubernetes.io/component: collector
{{- end }}

{{- define "ballast.collector.selectorLabels" -}}
{{ include "ballast.selectorLabels" . }}
app.kubernetes.io/component: collector
{{- end }}
