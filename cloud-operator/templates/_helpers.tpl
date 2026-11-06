# Copyright 2024 Illumio, Inc. All Rights Reserved.

{{- define "cloud-operator.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "cloud-operator.fullname" -}}
{{- $name := default .Chart.Name .Values.nameOverride -}}
{{- $fullname := printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" -}}
{{- $trimmed := trunc 63 $fullname | trimSuffix "-" -}}
{{- if eq $trimmed $fullname -}}
{{- $fullname -}}
{{- else -}}
{{- trunc 63 $fullname -}}
{{- end -}}
{{- end -}}

{{- define "cloud-operator.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" -}}
{{- end -}}

{{- define "cloud-operator.labels" -}}
app: {{ include "cloud-operator.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/name: {{ include "cloud-operator.name" . }}
chart: {{ include "cloud-operator.chart" . }}
helm.sh/chart: {{ include "cloud-operator.chart" . }}
heritage: {{ .Release.Service }}
release: {{ .Release.Name }}
{{- if .Values.additionalLabels }}
{{ toYaml .Values.additionalLabels }}
{{- end }}
{{- end -}}

{{- define "cloud-operator.serviceAccountName" -}}
{{- if .Values.serviceAccount.name -}}
{{- .Values.serviceAccount.name -}}
{{- else -}}
{{- include "cloud-operator.fullname" . }}
{{- end -}}
{{- end -}}
{{/*
Whether OpenShift's service-ca creates the syslog collector's TLS certificate:
on OpenShift, unless disabled or a certificate Secret is set explicitly.
*/}}
{{- define "cloud-operator.syslogTLSFromOpenShiftServiceCA" -}}
{{- if and .Values.calico.syslogTLS.openshiftServiceCA (not .Values.calico.syslogTLS.secretName) (.Capabilities.APIVersions.Has "security.openshift.io/v1") -}}
true
{{- end -}}
{{- end -}}

{{/*
The Secret with the syslog collector's TLS certificate: calico.syslogTLS.secretName
if set, otherwise the Secret that OpenShift's service-ca creates for the
operator Service, otherwise empty (plain-text syslog only).
*/}}
{{- define "cloud-operator.syslogTLSSecretName" -}}
{{- if .Values.calico.syslogTLS.secretName -}}
{{- .Values.calico.syslogTLS.secretName -}}
{{- else if include "cloud-operator.syslogTLSFromOpenShiftServiceCA" . -}}
{{- printf "%s-syslog-tls" (include "cloud-operator.fullname" .) -}}
{{- end -}}
{{- end -}}
