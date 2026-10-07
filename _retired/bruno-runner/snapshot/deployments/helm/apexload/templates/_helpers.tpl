{{- define "apexload.namespace" -}}
{{- default .Release.Namespace .Values.namespace.name -}}
{{- end -}}

{{- define "apexload.labels" -}}
app.kubernetes.io/part-of: apexload
app.kubernetes.io/managed-by: Helm
helm.sh/chart: {{ .Chart.Name }}-{{ .Chart.Version | replace "+" "_" }}
{{- end -}}

{{- /*
Normalize restart-control values in the templates rather than relying on
values.yaml being merged. Helm upgrades performed with --reuse-values can
carry forward a legacy release that has no restartControls map (or only the
keys supplied with --set), so every missing key must inherit the current
chart default while explicit false values remain false.
*/ -}}
{{- define "apexload.restartControls" -}}
{{- $effective := dict
      "enabled" true
      "allowForce" true
      "controllerDeployment" "apexload-controller"
      "generatorStatefulSet" "apexload-generator"
      "generatorSelector" "app.kubernetes.io/name=apexload-generator" -}}
{{- $configured := .Values.restartControls | default dict -}}
{{- range $key := keys $effective -}}
{{- if hasKey $configured $key -}}
{{- $_ := set $effective $key (get $configured $key) -}}
{{- end -}}
{{- end -}}
{{- toJson $effective -}}
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

{{- define "apexload.javaVuserRunnerImage" -}}
{{- $registry := include "apexload.requiredCleanValue" (list "image.registry" .Values.image.registry) -}}
{{- /* The runner has only a standard release coordinate. Strip any global
      controller/generator profile and derive the published standard tag. */ -}}
{{- $releaseTag := regexReplaceAll "(-standard|-enterprise)$" .Values.image.tag "" -}}
{{- $defaultTag := printf "%s-standard" $releaseTag -}}
{{- $tag := default $defaultTag .Values.javaVuser.runner.image.tag -}}
{{- printf "%s/%s:%s" $registry .Values.javaVuser.runner.image.repository $tag -}}
{{- end -}}

{{- define "apexload.wrappingKeyVaultKeyId" -}}
{{- coalesce .Values.projectSecrets.wrappingKeyVaultKeyId .Values.bruno.keyVaultKeyId -}}
{{- end -}}

{{- define "apexload.wrappingMasterKey" -}}
{{- coalesce .Values.projectSecrets.wrappingMasterKey .Values.bruno.artifactMasterKey -}}
{{- end -}}

{{- define "apexload.importAssetsClaimName" -}}
{{- default "apexload-controller-import-assets" .Values.importAssets.persistence.existingClaim -}}
{{- end -}}

{{/* One fleet identity shared by the controller and every KEDA query. */}}
{{- define "apexload.fleetID" -}}
{{- $id := default (printf "%s/%s" (include "apexload.namespace" .) .Release.Name) .Values.keda.fleetID -}}
{{- if or (not (regexMatch "^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,127}$" $id)) (contains "change-me" (lower $id)) -}}
{{- fail "keda.fleetID must be 1-128 letters, digits, underscores, dots, colons, slashes, or hyphens" -}}
{{- end -}}
{{- $id -}}
{{- end -}}

{{- define "apexload.archiveStagingClaimName" -}}
{{- default "apexload-controller-archive-staging" .Values.archiveStaging.persistence.existingClaim -}}
{{- end -}}
