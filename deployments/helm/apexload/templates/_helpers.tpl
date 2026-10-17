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
{{- $values := include "apexload.upgradeValues" . | fromJson -}}
{{- default "apexload-controller-archive-staging" $values.archiveStaging.persistence.existingClaim -}}
{{- end -}}

{{/* Defaults for sections introduced after the first enterprise chart. Reused
release values bypass values.yaml; deep overwrite preserves explicit false,
empty lists and operator overrides while filling missing nested keys. */}}
{{- define "apexload.upgradeValues" -}}
{{- $defaults := .Files.Get "defaults/upgrade.yaml" | fromYaml -}}
{{- $configured := pick .Values "http3" "archiveStaging" "networkPolicy" -}}
{{- mustMergeOverwrite $defaults $configured | toJson -}}
{{- end -}}

{{/*
HTTP/3 settings shared by the controller and every generator: the flag, the
operator QUIC limits that are set, and the UDP counter scope. Chart pods do
not use hostNetwork, so each has its own network namespace and its UDP
drop counters are attributable to it.
*/}}
{{- define "apexload.http3Env" -}}
{{- $values := include "apexload.upgradeValues" . | fromJson -}}
- name: APEXLOAD_HTTP3
  value: {{ $values.http3.enabled | quote }}
- name: APEXLOAD_UDP_COUNTER_SCOPE
  value: pod
{{- range $key, $env := dict "initialStreamReceiveWindow" "APEXLOAD_QUIC_INITIAL_STREAM_RECEIVE_WINDOW" "maxStreamReceiveWindow" "APEXLOAD_QUIC_MAX_STREAM_RECEIVE_WINDOW" "initialConnectionReceiveWindow" "APEXLOAD_QUIC_INITIAL_CONNECTION_RECEIVE_WINDOW" "maxConnectionReceiveWindow" "APEXLOAD_QUIC_MAX_CONNECTION_RECEIVE_WINDOW" "maxResponseHeaderBytes" "APEXLOAD_QUIC_MAX_RESPONSE_HEADER_BYTES" "initialPacketSize" "APEXLOAD_QUIC_INITIAL_PACKET_SIZE" }}
{{- $value := int64 (index $values.http3.quic $key) }}
{{- if gt $value 0 }}
- name: {{ $env }}
  value: {{ $value | toString | quote }}
{{- end }}
{{- end }}
{{- if $values.http3.quic.disablePathMTUDiscovery }}
- name: APEXLOAD_QUIC_DISABLE_PATH_MTU_DISCOVERY
  value: "true"
{{- end }}
{{- end -}}

{{/* Egress rules every profile shares: DNS, then the load-test targets. */}}
{{- define "apexload.egressTargetRules" -}}
{{- $values := include "apexload.upgradeValues" . | fromJson -}}
{{- $egress := $values.networkPolicy.egress -}}
- to:
    - namespaceSelector:
        matchLabels:
          kubernetes.io/metadata.name: {{ $egress.dns.namespace }}
      podSelector:
        matchLabels:
{{ toYaml $egress.dns.podLabels | indent 10 }}
  ports:
    - protocol: UDP
      port: 53
    - protocol: TCP
      port: 53
{{- if and $egress.targets.cidrs (or $egress.targets.tcpPorts $egress.targets.udpPorts) }}
- to:
{{- range $egress.targets.cidrs }}
    - ipBlock:
        cidr: {{ . }}
{{- $cidr := . }}
{{- $except := list }}
{{- range $egress.targets.exceptCidrs }}
{{- if eq (contains ":" .) (contains ":" $cidr) }}
{{- $except = append $except . }}
{{- end }}
{{- end }}
{{- if $except }}
        except:
{{ toYaml $except | indent 10 }}
{{- end }}
{{- end }}
  ports:
{{- range $egress.targets.tcpPorts }}
    - protocol: TCP
      port: {{ . }}
{{- end }}
{{- range $egress.targets.udpPorts }}
    - protocol: UDP
      port: {{ . }}
{{- end }}
{{- end }}
{{- end -}}
