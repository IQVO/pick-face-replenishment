#!/usr/bin/env bash
# Chart render assertions: the chart deploys the REAL runtime of this context
# (ADR 0006: Postgres and Kafka are the only runtime, no in-memory mode), so
# the mandatory configuration has no silent default, no *_MODE variable is
# ever rendered, and the env the binary reads is the env the chart renders.
# Run: bash charts/pick-face-replenishment/tests/render_test.sh   (needs helm)
set -euo pipefail

chart_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
repo_dir="$(cd "$chart_dir/../.." && pwd)"
release=pick-face-replenishment
# Dummy values, no password: the chart refuses to render without a database
# source and without Kafka brokers.
base=(--set database.url=postgres://u@example.invalid:5432/db --set kafka.brokers=kafka.example.invalid:9092)
# Every optional value set (ct lint's values file), so every env var the
# binary can read is rendered.
everything=(-f "$chart_dir/ci/all-components-values.yaml")

fail() { echo "FAIL: $*" >&2; exit 1; }

render() { helm template "$release" "$chart_dir" "$@"; }
# render_only <template> <helm args...>
render_only() { local t="$1"; shift; render "$@" --show-only "templates/$t"; }

configmap="$(render_only configmap.yaml "${base[@]}")"
deployment="$(render_only deployment.yaml "${base[@]}")"
configmap_name="$(awk '/^  name:/ {print $2; exit}' <<<"$configmap")"
[[ -n "$configmap_name" ]] || fail "could not read the ConfigMap name"

# --- the ConfigMap is mounted (gap fix D14) ---------------------------------
grep -A3 'envFrom:' <<<"$deployment" | grep -q 'configMapRef:' \
  || fail "Deployment does not mount the ConfigMap via envFrom"
grep -A3 'envFrom:' <<<"$deployment" | grep -q "name: $configmap_name" \
  || fail "Deployment envFrom does not reference ConfigMap $configmap_name"

# --- mandatory configuration is rendered from values -------------------------
env_value() { # env_value <rendered> <NAME>
  grep -A1 "name: $2\$" <<<"$1" | grep -m1 'value:' | sed -E 's/.*value: "?//; s/"$//'
}
[[ "$(env_value "$deployment" KAFKA_BROKERS)" == "kafka.example.invalid:9092" ]] || fail "KAFKA_BROKERS is not rendered from kafka.brokers"
[[ "$(env_value "$deployment" INVENTORY_CONSUMER_GROUP)" == "pick-face-replenishment-inventory" ]] || fail "INVENTORY_CONSUMER_GROUP is not rendered from config.inventoryConsumerGroup"
[[ "$(env_value "$deployment" SLOTTING_CONSUMER_GROUP)" == "pick-face-replenishment-slotting" ]] || fail "SLOTTING_CONSUMER_GROUP is not rendered from config.slottingConsumerGroup"
[[ "$(env_value "$deployment" LAYOUT_CONSUMER_GROUP)" == "pick-face-replenishment-layout" ]] || fail "LAYOUT_CONSUMER_GROUP is not rendered from config.layoutConsumerGroup"
grep -A4 'name: DATABASE_URL$' <<<"$deployment" | grep -q 'secretKeyRef:' || fail "DATABASE_URL must come from a Secret (secretKeyRef)"
grep -A6 'name: MIGRATIONS_DATABASE_URL$' <<<"$deployment" | grep -q 'optional: true' || fail "MIGRATIONS_DATABASE_URL must be an optional secretKeyRef"
for var in KAFKA_BROKERS INVENTORY_CONSUMER_GROUP SLOTTING_CONSUMER_GROUP LAYOUT_CONSUMER_GROUP; do
  grep -q "^  $var: " <<<"$configmap" || fail "the ConfigMap does not carry $var"
done
if grep -qE '^  (MIGRATIONS_)?DATABASE_URL:' <<<"$configmap"; then fail "a DSN must never land in the ConfigMap"; fi

# The consumer group ids come from values: overriding them changes the env.
renamed="$(render_only deployment.yaml "${base[@]}" --set config.inventoryConsumerGroup=g-inv --set config.slottingConsumerGroup=g-slot --set config.layoutConsumerGroup=g-lay)"
[[ "$(env_value "$renamed" INVENTORY_CONSUMER_GROUP)" == "g-inv" ]] || fail "config.inventoryConsumerGroup does not reach INVENTORY_CONSUMER_GROUP"
[[ "$(env_value "$renamed" SLOTTING_CONSUMER_GROUP)" == "g-slot" ]] || fail "config.slottingConsumerGroup does not reach SLOTTING_CONSUMER_GROUP"
[[ "$(env_value "$renamed" LAYOUT_CONSUMER_GROUP)" == "g-lay" ]] || fail "config.layoutConsumerGroup does not reach LAYOUT_CONSUMER_GROUP"

# --- no in-memory / permissive mode, ever (ADR 0006) -------------------------
no_mode() { # no_mode <label> <rendered>
  if grep -qE '(name: |^  )[A-Z_]+_MODE:?$|^  [A-Z_]+_MODE: ' <<<"$2"; then fail "$1 renders a *_MODE variable; they default to kafka and must never be set"; fi
  if grep -qiE 'value: "?permissive' <<<"$2"; then fail "$1 renders permissive, which the binary rejects at boot"; fi
}
no_mode "ConfigMap" "$configmap"
no_mode "Deployment" "$deployment"
all_docs="$(render "${base[@]}" "${everything[@]}")"
no_mode "the full render" "$all_docs"
if grep -q 'DATABASE_URL: ""' <<<"$all_docs"; then fail "an empty DATABASE_URL must never be rendered"; fi

# --- probes: /healthz liveness (and startup), /readyz readiness --------------
probe_block() { # probe_block <probeName> <rendered deployment>
  awk -v p="$1:" '$1 == p {f=1; next} f && /^          [a-zA-Z]/ {exit} f {print}' <<<"$2"
}
grep -q 'path: /healthz' <<<"$(probe_block livenessProbe "$deployment")" || fail "livenessProbe must hit /healthz"
grep -q 'path: /healthz' <<<"$(probe_block startupProbe "$deployment")" || fail "startupProbe must hit /healthz"
readiness="$(probe_block readinessProbe "$deployment")"
grep -q 'path: /readyz' <<<"$readiness" || fail "readinessProbe must hit /readyz"
# /readyz checks Postgres AND that a Kafka broker answers (cached 3 s, bounded 2 s): give it a sane
# delay, period, timeout and failure budget so a broker blip does not flap.
grep -q 'initialDelaySeconds: [1-9]' <<<"$readiness" || fail "readinessProbe needs an initialDelaySeconds"
grep -q 'periodSeconds: [1-9]' <<<"$readiness" || fail "readinessProbe needs a periodSeconds"
grep -q 'timeoutSeconds: [3-9]' <<<"$readiness" || fail "readinessProbe timeoutSeconds must exceed the 2 s dependency check bound"
grep -q 'failureThreshold: [3-9]' <<<"$readiness" || fail "readinessProbe needs a failureThreshold of at least 3"
probe_block livenessProbe "$deployment" | grep -q 'failureThreshold: [3-9]' || fail "livenessProbe needs a failureThreshold of at least 3"

# --- container hardening ------------------------------------------------------
grep -q 'runAsNonRoot: true' <<<"$deployment" || fail "the pod must run as non-root"
grep -q 'readOnlyRootFilesystem: true' <<<"$deployment" || fail "the root filesystem must be read-only"
grep -q 'allowPrivilegeEscalation: false' <<<"$deployment" || fail "privilege escalation must be disabled"
grep -A2 'capabilities:' <<<"$deployment" | grep -q 'ALL' || fail "capabilities must drop ALL"

# --- graceful shutdown room ----------------------------------------------------
# SHUTDOWN_DRAIN_DELAY (5s) + HTTP drain 10s + consumers 10s + relay 10s = 35s.
grace="$(awk '/terminationGracePeriodSeconds:/ {print $2; exit}' <<<"$deployment")"
(( grace > 35 )) || fail "terminationGracePeriodSeconds is $grace; want more than 35 (drain delay + 3 x 10 s worker/HTTP bounds)"

# --- optional values reach the ConfigMap and the Deployment -------------------
tuned_args=("${base[@]}" --set config.outboxRelayInterval=7s --set config.outboxMaxAttempts=5 \
  --set config.shutdownDrainDelay=3s --set config.forwardZoneCodes=FWD\\,F2 --set config.reserveZoneCodes=RSV\\,BULK \
  --set config.httpAddr=:9090 --set config.logLevel=debug)
tuned_cm="$(render_only configmap.yaml "${tuned_args[@]}")"
tuned_dep="$(render_only deployment.yaml "${tuned_args[@]}")"
for kv in 'OUTBOX_RELAY_INTERVAL: "7s"' 'OUTBOX_MAX_ATTEMPTS: "5"' 'SHUTDOWN_DRAIN_DELAY: "3s"' \
  'FORWARD_ZONE_CODES: "FWD,F2"' 'RESERVE_ZONE_CODES: "RSV,BULK"' 'HTTP_ADDR: ":9090"' 'LOG_LEVEL: "debug"'; do
  grep -q "$kv" <<<"$tuned_cm" || fail "the ConfigMap does not render $kv"
done
for pair in OUTBOX_RELAY_INTERVAL=7s OUTBOX_MAX_ATTEMPTS=5 SHUTDOWN_DRAIN_DELAY=3s FORWARD_ZONE_CODES=FWD,F2 \
  RESERVE_ZONE_CODES=RSV,BULK HTTP_ADDR=:9090 LOG_LEVEL=debug; do
  [[ "$(env_value "$tuned_dep" "${pair%%=*}")" == "${pair#*=}" ]] || fail "the Deployment does not render ${pair%%=*}=${pair#*=}"
done
# Unset optional values are not rendered at all, so the binary's defaults apply.
for var in OUTBOX_RELAY_INTERVAL SHUTDOWN_DRAIN_DELAY FORWARD_ZONE_CODES RESERVE_ZONE_CODES; do
  if grep -q "$var" <<<"$deployment"; then fail "$var must not be rendered while its value is empty"; fi
done
# The relay attempt cap defaults to 20 and 0 (unlimited) is rendered as is.
grep -q 'OUTBOX_MAX_ATTEMPTS: "20"' <<<"$configmap" || fail "OUTBOX_MAX_ATTEMPTS must default to 20"
unlimited="$(render_only configmap.yaml "${base[@]}" --set config.outboxMaxAttempts=0)"
grep -q 'OUTBOX_MAX_ATTEMPTS: "0"' <<<"$unlimited" || fail "config.outboxMaxAttempts=0 is not rendered"

# --- chart vs binary ------------------------------------------------------------
# Every env var cmd/api reads must be rendered into the Deployment when every
# optional value is set. The names come from the binary's own os.Getenv /
# getenv("...") literals and its *_CONSUMER_GROUP specs, so a new variable in
# cmd/api fails this test until the chart renders it. MIGRATIONS_DATABASE_URL
# is read by internal/adapters/outbound/postgres/migrate.go.
binary_env() {
  local f
  for f in "$repo_dir"/cmd/api/*.go; do
    case "$f" in *_test.go) continue ;; esac
    grep -hoE '[Gg]etenv\("[A-Z][A-Z0-9_]*"' "$f" || true
    grep -hoE '"[A-Z]+_CONSUMER_GROUP"' "$f" || true
  done | sed -E 's/.*"([A-Z0-9_]+)".*/\1/' | sort -u
}
# Word splitting is safe: the names are [A-Z0-9_] only (bash 3.2 has no mapfile).
# shellcheck disable=SC2207
api_vars=($(binary_env))
(( ${#api_vars[@]} >= 10 )) || fail "found only ${#api_vars[@]} env literals in cmd/api; the scan is looking in the wrong place"
full_deployment="$(render_only deployment.yaml "${everything[@]}")"
for var in "${api_vars[@]}" MIGRATIONS_DATABASE_URL; do
  grep -q "name: $var\$" <<<"$full_deployment" || fail "the Deployment does not render $var, which the binary reads"
done
# Every key the ConfigMap renders is a variable cmd/api reads (no dead keys).
full_configmap="$(render_only configmap.yaml "${everything[@]}")"
known=" ${api_vars[*]} "
while read -r key; do
  [[ "$known" == *" $key "* ]] || fail "ConfigMap key $key is read by no binary"
done < <(awk '/^data:/ {d=1; next} d && /^  [A-Z]/ {sub(/:.*/, ""); sub(/^  /, ""); print}' <<<"$full_configmap")

# --- the Secret ----------------------------------------------------------------
secret="$(render_only secret.yaml "${base[@]}")"
grep -q 'DATABASE_URL: "postgres://u@example.invalid:5432/db"' <<<"$secret" || fail "database.url does not land in the chart Secret"
existing="$(render --set database.existingSecret=my-db --set kafka.brokers=k:9092)"
if grep -q 'kind: Secret' <<<"$existing"; then fail "an existingSecret must suppress the chart Secret"; fi
grep -A4 'name: DATABASE_URL$' <<<"$existing" | grep -q 'name: my-db' || fail "an existingSecret is not referenced by DATABASE_URL"

# --- refusals: the mandatory values name themselves -------------------------------
expect_refusal() { # expect_refusal <why> <needle> <helm args...>
  local why="$1" needle="$2" err; shift 2
  if err="$(helm template "$release" "$chart_dir" "$@" 2>&1)"; then
    fail "the chart must refuse to render: $why"
  fi
  grep -qF -- "$needle" <<<"$err" || fail "the refusal for '$why' does not name '$needle':
$err"
}
expect_refusal "no database source and no brokers" "database.url or database.existingSecret"
expect_refusal "no database source" "database.url or database.existingSecret" --set kafka.brokers=k:9092
expect_refusal "no brokers" "kafka.brokers" --set database.url=postgres://u@h/db
expect_refusal "an empty inventory group" "config.inventoryConsumerGroup" "${base[@]}" --set config.inventoryConsumerGroup=
expect_refusal "an empty slotting group" "config.slottingConsumerGroup" "${base[@]}" --set config.slottingConsumerGroup=
expect_refusal "an empty layout group" "config.layoutConsumerGroup" "${base[@]}" --set config.layoutConsumerGroup=
expect_refusal "a short grace period" "terminationGracePeriodSeconds" "${base[@]}" --set terminationGracePeriodSeconds=20
expect_refusal "INVENTORY_MODE through extraEnv" "*_MODE" "${base[@]}" --set 'extraEnv[0].name=INVENTORY_MODE' --set 'extraEnv[0].value=permissive'
expect_refusal "LAYOUT_MODE through extraEnv" "ADR 0006" "${base[@]}" --set 'extraEnv[0].name=LAYOUT_MODE' --set 'extraEnv[0].value=kafka'

# --- the optional components (mcp, analytics, frontend) -------------------------
# Off by default: the default render is the api alone.
default_render="$(render "${base[@]}")"
for component in mcp analytics-projector analytics-reports frontend; do
  if grep -q "app.kubernetes.io/component: $component\$" <<<"$default_render"; then
    fail "the $component component must not render by default"
  fi
done

# A binary's env literals (os.Getenv / getenv("...")) -- for cmd/<dir> -- must all be
# rendered by its Deployment, so a new variable fails until the chart carries it.
cmd_env() { # cmd_env <dir>...
  local d f
  for d in "$@"; do
    for f in "$repo_dir/cmd/$d"/*.go; do
      case "$f" in *_test.go) continue ;; esac
      grep -hoE '(os\.Getenv|getenv)\("[A-Z][A-Z0-9_]*"' "$f" || true
    done
  done | sed -E 's/.*"([A-Z0-9_]+)".*/\1/' | sort -u
}
assert_renders_env() { # assert_renders_env <label> <rendered deployment> <dir>...
  local label="$1" rendered="$2" var; shift 2
  local vars; vars="$(cmd_env "$@")"
  [[ -n "$vars" ]] || fail "found no env literals in cmd/$* ($label); the scan is looking in the wrong place"
  for var in $vars; do
    grep -q "name: $var\$" <<<"$rendered" || fail "the $label Deployment does not render $var, which cmd/$* reads"
  done
}

# MCP (ADR 0007): read-only, Postgres only, its own Service.
mcp_dep="$(render_only mcp-deployment.yaml "${base[@]}" --set mcp.enabled=true)"
mcp_svc="$(render_only mcp-service.yaml "${base[@]}" --set mcp.enabled=true)"
grep -q 'app.kubernetes.io/component: mcp' <<<"$mcp_dep" || fail "the mcp Deployment is not labelled component=mcp"
grep -q 'command: \["/app/mcp"\]' <<<"$mcp_dep" || fail "the mcp Deployment must run /app/mcp"
grep -A1 'selector:' <<<"$mcp_svc" >/dev/null && grep -q 'app.kubernetes.io/component: mcp' <<<"$mcp_svc" || fail "the mcp Service must select component=mcp"
grep -A4 'name: DATABASE_URL$' <<<"$mcp_dep" | grep -q 'secretKeyRef:' || fail "the mcp DATABASE_URL must come from a Secret"
if grep -Eq 'name: (KAFKA_BROKERS|[A-Z]+_CONSUMER_GROUP|OUTBOX_[A-Z_]+)$' <<<"$mcp_dep"; then
  fail "the mcp Deployment must not get Kafka/relay/consumer env (read-only, never dials Kafka)"
fi
no_mode "mcp Deployment" "$mcp_dep"
mcp_full="$(render_only mcp-deployment.yaml "${everything[@]}")"
assert_renders_env "mcp" "$mcp_full" mcp
[[ "$(env_value "$(render_only mcp-deployment.yaml "${base[@]}" --set mcp.enabled=true --set mcp.httpAddr=:9191)" MCP_ADDR)" == ":9191" ]] || fail "mcp.httpAddr does not reach MCP_ADDR"
if render_only mcp-route.yaml "${base[@]}" --set mcp.enabled=true 2>&1 | grep -q 'kind: HTTPRoute'; then fail "the mcp route must stay off until mcp.route.enabled"; fi

# Analytics (ADR 0008): projector (writer) + reports (reader) on a separate DSN.
analytics=("${base[@]}" --set analytics.enabled=true --set analytics.url=postgres://u@example.invalid:5432/analytics)
proj="$(render_only projector-deployment.yaml "${analytics[@]}")"
rep="$(render_only reports-deployment.yaml "${analytics[@]}")"
proj_full="$(render_only projector-deployment.yaml "${everything[@]}")"
rep_full="$(render_only reports-deployment.yaml "${everything[@]}")"
grep -q 'command: \["/app/pfr-projector"\]' <<<"$proj" || fail "the projector must run /app/pfr-projector"
grep -q 'command: \["/app/pfr-reports"\]' <<<"$rep" || fail "the reports Deployment must run /app/pfr-reports"
[[ "$(env_value "$proj" ANALYTICS_CONSUMER_GROUP)" == "pick-face-replenishment-analytics" ]] || fail "ANALYTICS_CONSUMER_GROUP is not rendered from analytics.consumerGroup"
[[ "$(env_value "$proj" KAFKA_BROKERS)" == "kafka.example.invalid:9092" ]] || fail "the projector KAFKA_BROKERS is not rendered from kafka.brokers"
grep -A4 'name: ANALYTICS_DATABASE_URL$' <<<"$proj" | grep -q 'secretKeyRef:' || fail "the projector DSN must come from a Secret"
grep -A4 'name: ANALYTICS_DATABASE_URL$' <<<"$rep" | grep -q 'secretKeyRef:' || fail "the reports DSN must come from a Secret"
if grep -Eq 'name: (DATABASE_URL|MIGRATIONS_DATABASE_URL)$' <<<"$proj$rep"; then fail "analytics must never get the OLTP DSN"; fi
if grep -q 'name: KAFKA_BROKERS$' <<<"$rep"; then fail "the reports Deployment never dials Kafka"; fi
grep -q 'kind: Service' <<<"$(render "${analytics[@]}" --show-only templates/reports-deployment.yaml)" || fail "the reports Service was not rendered"
if grep -q 'kind: Service' <<<"$proj"; then fail "the projector must not render a Service (admin port is probes-only)"; fi
grep -q 'path: /readyz' <<<"$proj" || fail "the projector readiness must hit /readyz"
assert_renders_env "projector" "$proj_full" pfr-projector
assert_renders_env "reports" "$rep_full" pfr-reports
analytics_secret="$(render_only analytics-secret.yaml "${analytics[@]}")"
grep -q 'ANALYTICS_DATABASE_URL: "postgres://u@example.invalid:5432/analytics"' <<<"$analytics_secret" || fail "analytics.url does not land in the analytics Secret"
existing_analytics="$(render "${base[@]}" --set analytics.enabled=true --set analytics.existingSecret=my-analytics)"
if grep -q 'ANALYTICS_DATABASE_URL: "' <<<"$existing_analytics"; then fail "an analytics.existingSecret must suppress the analytics Secret"; fi
grep -A4 'name: ANALYTICS_DATABASE_URL$' <<<"$existing_analytics" | grep -q 'name: my-analytics' || fail "analytics.existingSecret is not referenced"
renamed_group="$(render_only projector-deployment.yaml "${analytics[@]}" --set analytics.consumerGroup=g-analytics)"
[[ "$(env_value "$renamed_group" ANALYTICS_CONSUMER_GROUP)" == "g-analytics" ]] || fail "analytics.consumerGroup does not reach ANALYTICS_CONSUMER_GROUP"
expect_refusal "analytics without a DSN source" "analytics.url nor analytics.existingSecret" "${base[@]}" --set analytics.enabled=true
expect_refusal "analytics without a consumer group" "analytics.consumerGroup" "${analytics[@]}" --set analytics.consumerGroup=

# The web remote (replenishment_mfe): its own Deployment + ClusterIP Service,
# no database/Kafka env, no route in this chart.
frontend_dep="$(render_only frontend-deployment.yaml "${base[@]}" --set frontend.enabled=true)"
frontend_svc="$(render_only frontend-service.yaml "${base[@]}" --set frontend.enabled=true)"
grep -q 'app.kubernetes.io/component: frontend' <<<"$frontend_dep" || fail "the frontend Deployment was not rendered with frontend.enabled=true"
grep -q 'app.kubernetes.io/component: frontend' <<<"$frontend_svc" || fail "the frontend Service was not rendered with frontend.enabled=true"
grep -q 'type: ClusterIP' <<<"$frontend_svc" || fail "the frontend Service must stay ClusterIP"
if grep -Eq 'name: (DATABASE_URL|KAFKA_BROKERS)$' <<<"$frontend_dep"; then fail "the frontend Deployment must not get database/Kafka env (it serves static bytes)"; fi
together="$(render "${base[@]}" --set mcp.enabled=true --set analytics.enabled=true --set analytics.url=postgres://u@h/a --set frontend.enabled=true)"
if grep -E 'kind: (Ingress|HTTPRoute)' <<<"$together" | grep -q frontend; then fail "frontend routing must not live in this chart (the Nginx web gateway owns it)"; fi
for component in api mcp analytics-projector analytics-reports frontend; do
  grep -q "app.kubernetes.io/component: $component\$" <<<"$together" || fail "$component was not rendered when every component is enabled"
done

echo "PASS: pick-face-replenishment chart render assertions"
