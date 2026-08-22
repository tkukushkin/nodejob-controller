{{/*
Names are fixed rather than derived from the release. The ClusterRole is generated from the
+kubebuilder:rbac markers and cannot be templated, so two releases in one cluster would collide
on it anyway — and one controller already watches every node in that cluster.
*/}}
{{- define "nodejob-controller.name" -}}
nodejob-controller
{{- end -}}

{{- define "nodejob-controller.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
app.kubernetes.io/name: {{ include "nodejob-controller.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{/*
Selector labels carry no release name: it is immutable on a Deployment, and pinning it would
break a reinstall under a different release name.
*/}}
{{- define "nodejob-controller.selectorLabels" -}}
app.kubernetes.io/name: {{ include "nodejob-controller.name" . }}
{{- end -}}
