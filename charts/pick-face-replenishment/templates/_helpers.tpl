{{/*
Expand the name of the chart.
*/}}
{{- define "pick-face-replenishment.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "pick-face-replenishment.fullname" -}}
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
Chart name and version as used by the chart label.
*/}}
{{- define "pick-face-replenishment.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "pick-face-replenishment.labels" -}}
helm.sh/chart: {{ include "pick-face-replenishment.chart" . }}
{{ include "pick-face-replenishment.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels. The Deployment and the Service ALSO pin
app.kubernetes.io/component (api), so a Service never selects more than the
pods of one component (tests/test_service_selectors.py).
*/}}
{{- define "pick-face-replenishment.selectorLabels" -}}
app.kubernetes.io/name: {{ include "pick-face-replenishment.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "pick-face-replenishment.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "pick-face-replenishment.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Name of the Secret holding DATABASE_URL: the operator's own
(database.existingSecret) or the one this chart creates from database.url.
*/}}
{{- define "pick-face-replenishment.databaseSecretName" -}}
{{- if .Values.database.existingSecret }}
{{- .Values.database.existingSecret }}
{{- else }}
{{- include "pick-face-replenishment.fullname" . }}-database
{{- end }}
{{- end }}

{{/*
Image reference.
*/}}
{{- define "pick-face-replenishment.image" -}}
{{ .Values.image.repository }}:{{ .Values.image.tag | default .Chart.AppVersion }}
{{- end }}

{{/*
MANDATORY (ADR 0006: Postgres is the only runtime). The binary exits at boot
without DATABASE_URL, so a render without a database source is never a
deployable state: surface it at render time, naming the values.
*/}}
{{- define "pick-face-replenishment.requireDatabase" -}}
{{- if not (or .Values.database.url .Values.database.existingSecret) -}}
{{- fail "pick-face-replenishment requires database.url or database.existingSecret to be set (DATABASE_URL): the service runs on real Postgres only and exits at boot without it (ADR 0006)." -}}
{{- end -}}
{{- end -}}

{{/*
MANDATORY (ADR 0006: Kafka is the only runtime). The binary exits at boot
without KAFKA_BROKERS; there is no default broker.
*/}}
{{- define "pick-face-replenishment.requireKafka" -}}
{{- $_ := required "pick-face-replenishment requires kafka.brokers to be set (KAFKA_BROKERS, comma-separated): the service runs on real Kafka only and exits at boot without it (ADR 0006)." .Values.kafka.brokers -}}
{{- end -}}

{{/*
MANDATORY: the three consumers always run, each with a STABLE group id read
from the environment (cmd/api planConsumers exits at boot without one). The
group ids live in values (config.*ConsumerGroup), never in a template literal.
*/}}
{{- define "pick-face-replenishment.requireConsumerGroups" -}}
{{- $_ := required "pick-face-replenishment requires config.inventoryConsumerGroup to be set (INVENTORY_CONSUMER_GROUP): a stable Kafka group id; the binary exits at boot without it." .Values.config.inventoryConsumerGroup -}}
{{- $_ := required "pick-face-replenishment requires config.slottingConsumerGroup to be set (SLOTTING_CONSUMER_GROUP): a stable Kafka group id; the binary exits at boot without it." .Values.config.slottingConsumerGroup -}}
{{- $_ := required "pick-face-replenishment requires config.layoutConsumerGroup to be set (LAYOUT_CONSUMER_GROUP): a stable Kafka group id; the binary exits at boot without it." .Values.config.layoutConsumerGroup -}}
{{- $_ := required "pick-face-replenishment requires config.packLevelConsumerGroup to be set (PACK_LEVEL_CONSUMER_GROUP): a stable Kafka group id; the binary exits at boot without it." .Values.config.packLevelConsumerGroup -}}
{{- end -}}

{{/*
Shutdown is sequential: SHUTDOWN_DRAIN_DELAY (default 5s) + HTTP drain 10s +
consumers 10s + outbox relay 10s = 35s in the worst case. A shorter grace
period gets the pod SIGKILLed mid-drain.
*/}}
{{- define "pick-face-replenishment.requireGracePeriod" -}}
{{- if lt (int .Values.terminationGracePeriodSeconds) 35 -}}
{{- fail (printf "terminationGracePeriodSeconds is %v — want at least 35: SHUTDOWN_DRAIN_DELAY (5s default) + 10s HTTP drain + 10s consumers + 10s outbox relay." .Values.terminationGracePeriodSeconds) -}}
{{- end -}}
{{- end -}}

{{/*
The *_MODE variables (INVENTORY_MODE, SLOTTING_MODE, LAYOUT_MODE) default to
kafka and the binary rejects "permissive" at boot (ADR 0006): they must never
be injected through extraEnv either.
*/}}
{{- define "pick-face-replenishment.requireNoModeEnv" -}}
{{- range .Values.extraEnv -}}
{{- if hasSuffix "_MODE" (toString .name) -}}
{{- fail (printf "extraEnv sets %s — the *_MODE variables are not configurable: the only mode is kafka and the binary rejects permissive at boot (ADR 0006)." .name) -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/*
Comma-joined names of this chart's enabled KongPlugins, for the
konghq.com/plugins annotation on the HTTPRoute this chart renders.
Empty string (annotation omitted) when none are enabled.
*/}}
{{- define "pick-face-replenishment.kongPluginNames" -}}
{{- $names := list -}}
{{- range $name, $cfg := .Values.kongPlugins -}}
{{- if $cfg.enabled -}}
{{- $names = append $names (printf "%s-%s" (include "pick-face-replenishment.fullname" $) $name) -}}
{{- end -}}
{{- end -}}
{{- join "," $names -}}
{{- end -}}

{{/*
Fully qualified names of the optional components. Each is its own workload
with its own app.kubernetes.io/component label; a Service never selects more
than one of them (tests/test_service_selectors.py).
*/}}
{{- define "pick-face-replenishment.mcpFullname" -}}
{{- include "pick-face-replenishment.fullname" . }}-mcp
{{- end }}

{{- define "pick-face-replenishment.analyticsProjectorFullname" -}}
{{- include "pick-face-replenishment.fullname" . }}-projector
{{- end }}

{{- define "pick-face-replenishment.analyticsReportsFullname" -}}
{{- include "pick-face-replenishment.fullname" . }}-reports
{{- end }}

{{/*
Name of the Secret holding ANALYTICS_DATABASE_URL: the operator's own
(analytics.existingSecret) or the one this chart creates from analytics.url
(analytics-secret.yaml).
*/}}
{{- define "pick-face-replenishment.analyticsSecretName" -}}
{{- if .Values.analytics.existingSecret -}}
{{- .Values.analytics.existingSecret -}}
{{- else -}}
{{- include "pick-face-replenishment.fullname" . }}-analytics
{{- end -}}
{{- end }}

{{/*
Analytics guard (ADR 0008): the projector and reports binaries exit at boot
without ANALYTICS_DATABASE_URL, and the projector without KAFKA_BROKERS (always
required by this chart) and ANALYTICS_CONSUMER_GROUP (no default). Refuse them
at render time, only while analytics is on, naming the value.
*/}}
{{- define "pick-face-replenishment.requireAnalyticsConfig" -}}
{{- if not (or .Values.analytics.url .Values.analytics.existingSecret) -}}
{{- fail "analytics.enabled is true but neither analytics.url nor analytics.existingSecret is set: the projector and reports binaries exit at boot without ANALYTICS_DATABASE_URL (ADR 0008)." -}}
{{- end -}}
{{- $_ := required "analytics.enabled is true but analytics.consumerGroup is empty: the projector exits at boot without ANALYTICS_CONSUMER_GROUP, a stable Kafka group id (ADR 0008)." .Values.analytics.consumerGroup -}}
{{- end -}}
