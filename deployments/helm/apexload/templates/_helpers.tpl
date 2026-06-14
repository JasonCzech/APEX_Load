{{- define "apexload.namespace" -}}
{{- default .Release.Namespace .Values.namespace.name -}}
{{- end -}}

{{- define "apexload.labels" -}}
app.kubernetes.io/part-of: apexload
app.kubernetes.io/managed-by: Helm
helm.sh/chart: {{ .Chart.Name }}-{{ .Chart.Version | replace "+" "_" }}
{{- end -}}

{{- define "apexload.controllerImage" -}}
{{- printf "%s/%s:%s" .Values.image.registry .Values.image.controllerRepository .Values.image.tag -}}
{{- end -}}

{{- define "apexload.generatorImage" -}}
{{- printf "%s/%s:%s" .Values.image.registry .Values.image.generatorRepository .Values.image.tag -}}
{{- end -}}
