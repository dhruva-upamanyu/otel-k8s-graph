{{/*
Effective Redis host. Bundled Redis wins; otherwise the inline host. Fails
the render when external mode has neither a host nor an existingSecret.
*/}}
{{- define "graph.redisHost" -}}
{{- if .Values.redis.internal.enabled -}}
{{ .Release.Name }}-redis
{{- else if .Values.redis.host -}}
{{ .Values.redis.host }}
{{- else -}}
{{ fail "redis: enable the bundled instance (redis.internal.enabled=true) or set redis.host / redis.existingSecret for an external one" }}
{{- end -}}
{{- end -}}

{{/*
Shared Redis env block (REDIS_* + GRAPH_REDIS_KEY_PREFIX), used by all three
components. Call with the root context:
  {{- include "graph.redisEnv" . | nindent 12 }}

Bundled Redis (redis.internal.enabled): HOST is the in-release Service, no
auth. External with redis.existingSecret: HOST/USER/PASSWORD come from that
Secret via secretKeyRef (USER/PASSWORD optional). External inline: values
below, USER/PASSWORD emitted only when non-empty. PORT/DB are always inline.
*/}}
{{- define "graph.redisEnv" -}}
{{- $r := .Values.redis -}}
- name: REDIS_HOST
{{- if and (not $r.internal.enabled) $r.existingSecret }}
  valueFrom:
    secretKeyRef:
      name: {{ $r.existingSecret }}
      key: {{ $r.secretKeys.host }}
{{- else }}
  value: {{ include "graph.redisHost" . | quote }}
{{- end }}
- name: REDIS_PORT
  value: {{ $r.port | quote }}
- name: REDIS_DB
  value: {{ $r.db | quote }}
{{- if not $r.internal.enabled }}
{{- if $r.existingSecret }}
- name: REDIS_USER
  valueFrom:
    secretKeyRef:
      name: {{ $r.existingSecret }}
      key: {{ $r.secretKeys.username }}
      optional: true
- name: REDIS_PASSWORD
  valueFrom:
    secretKeyRef:
      name: {{ $r.existingSecret }}
      key: {{ $r.secretKeys.password }}
      optional: true
{{- else }}
{{- with $r.username }}
- name: REDIS_USER
  value: {{ . | quote }}
{{- end }}
{{- with $r.password }}
- name: REDIS_PASSWORD
  value: {{ . | quote }}
{{- end }}
{{- end }}
{{- end }}
- name: GRAPH_REDIS_KEY_PREFIX
  value: {{ .Values.graph.keyPrefix | quote }}
{{- end -}}

{{/* Fully-qualified image ref for a component, e.g. registry/graph-k8s:tag */}}
{{- define "graph.image" -}}
{{- $registry := required "image.registry is required (e.g. --set image.registry=ghcr.io/<you>)" .root.Values.image.registry -}}
{{- printf "%s/%s:%s" $registry .name (.root.Values.image.tag | toString) -}}
{{- end -}}
