{{- define "apexload.namespace" -}}
{{- default .Release.Namespace .Values.namespace.name -}}
{{- end -}}

{{- define "apexload.labels" -}}
app.kubernetes.io/part-of: apexload
app.kubernetes.io/managed-by: Helm
helm.sh/chart: {{ .Chart.Name }}-{{ .Chart.Version | replace "+" "_" }}
{{- end -}}

{{- define "apexload.requiredCleanValue" -}}
{{- $name := index . 0 -}}
{{- $value := toString (index . 1) -}}
{{- if or (eq (trim $value) "") (regexMatch "(?i)(changeme|change-me|example\\.com|CHANGE-ME|\\$\\{[A-Z0-9_]+\\})" $value) -}}
{{- fail (printf "%s must be set to a non-placeholder value" $name) -}}
{{- end -}}
{{- $value -}}
{{- end -}}

{{- define "apexload.controllerImage" -}}
{{- $registry := include "apexload.requiredCleanValue" (list "image.registry" .Values.image.registry) -}}
{{- printf "%s/%s:%s" $registry .Values.image.controllerRepository .Values.image.tag -}}
{{- end -}}

{{- define "apexload.generatorImage" -}}
{{- $registry := include "apexload.requiredCleanValue" (list "image.registry" .Values.image.registry) -}}
{{- printf "%s/%s:%s" $registry .Values.image.generatorRepository .Values.image.tag -}}
{{- end -}}

{{- define "apexload.brunoRunnerImage" -}}
{{- $registry := include "apexload.requiredCleanValue" (list "image.registry" .Values.image.registry) -}}
{{- $tag := default .Values.image.tag .Values.bruno.runner.image.tag -}}
{{- printf "%s/%s:%s" $registry .Values.bruno.runner.image.repository $tag -}}
{{- end -}}

{{- define "apexload.wrappingKeyVaultKeyId" -}}
{{- coalesce .Values.projectSecrets.wrappingKeyVaultKeyId .Values.bruno.keyVaultKeyId -}}
{{- end -}}

{{- define "apexload.wrappingMasterKey" -}}
{{- coalesce .Values.projectSecrets.wrappingMasterKey .Values.bruno.artifactMasterKey -}}
{{- end -}}
