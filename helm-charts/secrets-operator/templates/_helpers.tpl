{{/*
Expand the name of the chart.
*/}}
{{- define "secrets-operator.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
If release name contains chart name it will be used as a full name.
*/}}
{{- define "secrets-operator.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 15 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 15 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 15 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "secrets-operator.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "secrets-operator.labels" -}}
helm.sh/chart: {{ include "secrets-operator.chart" . }}
{{ include "secrets-operator.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "secrets-operator.selectorLabels" -}}
app.kubernetes.io/name: {{ include "secrets-operator.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "secrets-operator.serviceAccountName" -}}
{{- if .Values.controllerManager.serviceAccount.name }}
{{- .Values.controllerManager.serviceAccount.name }}
{{- else }}
{{- printf "%s-controller-manager" (include "secrets-operator.fullname" .) }}
{{- end }}
{{- end }}

{{/*
Compute the list of scoped namespaces.
scopedNamespaces takes precedence over the deprecated scopedNamespace.
Handles both array input (--set "scopedNamespaces={ns1,ns2}") and
comma-separated string input (--set scopedNamespaces="ns1,ns2").
Returns a JSON object with a "list" key that should be parsed with fromJson.
Usage: $namespaces := (include "secrets-operator.scopedNamespaces" . | fromJson).list
*/}}
{{- define "secrets-operator.scopedNamespaces" -}}
{{- if .Values.scopedNamespaces -}}
  {{- if kindIs "string" .Values.scopedNamespaces -}}
    {{- /* Handle comma-separated string input */ -}}
    {"list": {{ splitList "," .Values.scopedNamespaces | toJson }}}
  {{- else -}}
    {{- /* Handle array input */ -}}
    {"list": {{ .Values.scopedNamespaces | toJson }}}
  {{- end -}}
{{- else if .Values.scopedNamespace -}}
{"list": {{ list .Values.scopedNamespace | toJson }}}
{{- else -}}
{"list": []}
{{- end -}}
{{- end }}

{{/*
Check if we're using the deprecated scopedNamespace field.
Returns "true" or "false" as a string.
*/}}
{{- define "secrets-operator.usingDeprecatedScopedNamespace" -}}
{{- if and (not .Values.scopedNamespaces) .Values.scopedNamespace -}}
true
{{- else -}}
false
{{- end -}}
{{- end }}
{{/*
Fail if a default CR (InfisicalConnection/InfisicalAuth) would be installed in a
namespace the operator does not watch. In scoped mode the operator only sees the
namespaces passed via --namespaces, so the default must live in one of them.
Usage: {{ include "secrets-operator.validateDefaultNamespace" (dict "ctx" . "value" "defaultInfisicalAuth") }}
*/}}
{{- define "secrets-operator.validateDefaultNamespace" -}}
{{- $ctx := .ctx -}}
{{- $namespaces := (include "secrets-operator.scopedNamespaces" $ctx | fromJson).list -}}
{{- if and $namespaces $ctx.Values.scopedRBAC -}}
  {{- $trimmed := list -}}
  {{- range $namespaces -}}
    {{- $trimmed = append $trimmed (trim .) -}}
  {{- end -}}
  {{- if not (has $ctx.Release.Namespace $trimmed) -}}
    {{- fail (printf "%s.enabled is true, but the release namespace %q is not in scopedNamespaces (%s). The operator would not be able to read the default resource. Add %q to scopedNamespaces." .value $ctx.Release.Namespace (join ", " $trimmed) $ctx.Release.Namespace) -}}
  {{- end -}}
{{- end -}}
{{- end }}
