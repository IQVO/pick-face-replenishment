#!/usr/bin/env python3
"""Assert this chart's Service selectors isolate each component, and that every
Deployment carries the real runtime's environment.

Why the selector check exists (fleet-wide): `app.kubernetes.io/name` +
`instance` are identical on every pod a release creates. A Service that
selects on those two alone selects ALL components -- verified live in other
fleet charts, where a Kong request for an api /healthz was answered by the
wrong pod. Every Deployment here carries `app.kubernetes.io/component` in
selector.matchLabels and its pod labels, and every Service selects on it, so
each Service selects EXACTLY ONE Deployment. This test fails if that ever
stops being true.

Components: api (always), mcp (ADR 0007, default off), a2a (ADR 0010, default off), the analytics read side
of ADR 0008 -- analytics-projector (no Service) and analytics-reports -- and the
frontend (default off; the nginx pod serving the replenishment_mfe remote: its
own workload, component=frontend, a ClusterIP Service, never routed by this
chart). The enabled render runs with every component TOGETHER.

It also asserts the ADR 0006 runtime: the mandatory env is rendered, the
*_MODE variables never are, and the api HPA owns replicas when enabled.

Run: python3 charts/pick-face-replenishment/tests/test_service_selectors.py
Needs: helm, PyYAML.
"""

from __future__ import annotations

import subprocess
import sys
from pathlib import Path

CHART_DIR = Path(__file__).resolve().parents[1]
RELEASE = "pick-face-replenishment"

# Dummy values, no password: the chart refuses to render without a database
# source and without Kafka brokers.
BASE = [
    "--set", "database.url=postgres://u@example.invalid:5432/db",
    "--set", "kafka.brokers=kafka.example.invalid:9092",
]
ENABLE_EVERYTHING = BASE + [
    "--set", "autoscaling.api.enabled=true",
    "--set", "config.forwardZoneCodes=FWD\\,FWD2",
    "--set", "config.reserveZoneCodes=RSV\\,BULK",
    "--set", "config.outboxRelayInterval=500ms",
    "--set", "config.shutdownDrainDelay=5s",
    "--set", "gatewayApi.enabled=true",
    "--set", "ingress.enabled=true",
    "--set", "mcp.enabled=true",
    "--set", "a2a.enabled=true",
    "--set", "frontend.enabled=true",
    # ADR 0008: the analytics read side ON together with everything else.
    "--set", "analytics.enabled=true",
    "--set", "analytics.url=postgres://u@example.invalid:5432/analytics",
]

# Every env var cmd/api reads that has a dedicated chart value.
API_ENV = (
    "HTTP_ADDR", "LOG_LEVEL", "DATABASE_URL", "MIGRATIONS_DATABASE_URL", "KAFKA_BROKERS",
    "INVENTORY_CONSUMER_GROUP", "SLOTTING_CONSUMER_GROUP", "LAYOUT_CONSUMER_GROUP", "PACK_LEVEL_CONSUMER_GROUP",
    "OUTBOX_RELAY_INTERVAL", "OUTBOX_MAX_ATTEMPTS", "SHUTDOWN_DRAIN_DELAY",
    "FORWARD_ZONE_CODES", "RESERVE_ZONE_CODES",
)
# Never rendered: they default to kafka and the binary rejects permissive.
FORBIDDEN_ENV = ("INVENTORY_MODE", "SLOTTING_MODE", "LAYOUT_MODE", "PACK_LEVEL_MODE")


def helm_template(extra_args: list[str]) -> subprocess.CompletedProcess:
    return subprocess.run(
        ["helm", "template", RELEASE, str(CHART_DIR), *extra_args],
        capture_output=True, text=True,
    )


def render(extra_args: list[str]) -> list[dict]:
    result = helm_template(extra_args)
    if result.returncode != 0:
        raise SystemExit(f"FAIL: helm template {' '.join(extra_args)}:\n{result.stderr}")
    try:
        import yaml  # type: ignore
    except ModuleNotFoundError:  # pragma: no cover - environment guard
        print("SKIP: PyYAML not available; cannot assert selectors", file=sys.stderr)
        raise SystemExit(0)
    return [d for d in yaml.safe_load_all(result.stdout) if d]


def selector_of(doc: dict) -> dict:
    return doc.get("spec", {}).get("selector") or {}


def pod_labels_of(doc: dict) -> dict:
    return doc.get("spec", {}).get("template", {}).get("metadata", {}).get("labels") or {}


def matches(selector: dict, labels: dict) -> bool:
    return bool(selector) and all(labels.get(k) == v for k, v in selector.items())


def env_of(dep: dict) -> list[dict]:
    return dep["spec"]["template"]["spec"]["containers"][0].get("env", [])


def env_names(dep: dict) -> list[str]:
    return [e["name"] for e in env_of(dep)]


def env_value(dep: dict, name: str) -> str | None:
    return next((e.get("value") for e in env_of(dep) if e["name"] == name), None)


def check_selectors(docs: list[dict], failures: list[str]) -> None:
    services = {d["metadata"]["name"]: d for d in docs if d.get("kind") == "Service"}
    deployments = {d["metadata"]["name"]: d for d in docs if d.get("kind") == "Deployment"}

    expected = {RELEASE, f"{RELEASE}-mcp", f"{RELEASE}-a2a", f"{RELEASE}-projector", f"{RELEASE}-reports", f"{RELEASE}-frontend"}
    if set(deployments) != expected:
        failures.append(f"expected Deployments {sorted(expected)}, got {sorted(deployments)}")
    if set(services) != expected - {f"{RELEASE}-projector"}:
        failures.append(f"expected a Service per Deployment but the projector, got {sorted(services)}")

    for name, component in (
        (RELEASE, "api"), (f"{RELEASE}-mcp", "mcp"), (f"{RELEASE}-a2a", "a2a"),
        (f"{RELEASE}-reports", "analytics-reports"), (f"{RELEASE}-frontend", "frontend"),
    ):
        if name in services and selector_of(services[name]).get("app.kubernetes.io/component") != component:
            failures.append(f"Service {name} selector must pin component={component}")
        if name in deployments:
            labels = deployments[name]["metadata"].get("labels", {})
            if labels.get("app.kubernetes.io/component") != component:
                failures.append(f"Deployment {name} must be labelled component={component}")

    # Every Deployment's own selector must pin a component too, and be
    # satisfied by its pod labels.
    for name, dep in deployments.items():
        match_labels = dep["spec"]["selector"].get("matchLabels") or {}
        if "app.kubernetes.io/component" not in match_labels:
            failures.append(f"Deployment {name} selector.matchLabels lacks app.kubernetes.io/component")
        if not matches(match_labels, pod_labels_of(dep)):
            failures.append(f"Deployment {name} pod labels do not satisfy its own selector")

    # The real invariant: each Service selects exactly one Deployment.
    for svc_name, svc in services.items():
        sel = selector_of(svc)
        hit = [d for d, dep in deployments.items() if matches(sel, pod_labels_of(dep))]
        if len(hit) != 1:
            failures.append(f"Service {svc_name} selects {len(hit)} Deployments {sorted(hit)}; expected exactly 1")

    # Every Service's named target port exists on its container.
    for name, svc in services.items():
        dep = deployments.get(name)
        if dep is None:
            continue
        ports = {p["name"] for p in dep["spec"]["template"]["spec"]["containers"][0].get("ports", [])}
        if "http" not in ports:
            failures.append(f"the {name} container does not expose the named port 'http' the Service targets")

    api = deployments.get(RELEASE)
    if api is None:
        failures.append("the api Deployment was not rendered")
        return

    # The api HPA owns replicas when enabled.
    hpa = next((d for d in docs if d.get("kind") == "HorizontalPodAutoscaler"), None)
    if hpa is None or hpa["spec"]["scaleTargetRef"]["name"] != RELEASE:
        failures.append("the api HPA was not rendered (or does not scale the api Deployment)")
    elif "replicas" in api.get("spec", {}):
        failures.append("the api Deployment must omit replicas when its HPA owns them")

    check_env(api, failures)
    check_mcp(deployments.get(f"{RELEASE}-mcp"), failures)
    check_a2a(deployments.get(f"{RELEASE}-a2a"), failures)
    check_analytics(deployments.get(f"{RELEASE}-projector"), deployments.get(f"{RELEASE}-reports"), failures)
    check_frontend(docs, deployments.get(f"{RELEASE}-frontend"), failures)


def command_of(dep: dict) -> list[str] | None:
    return dep["spec"]["template"]["spec"]["containers"][0].get("command")


def check_mcp(dep: dict | None, failures: list[str]) -> None:
    if dep is None:
        return
    if command_of(dep) != ["/app/mcp"]:
        failures.append("the MCP Deployment must run /app/mcp")
    env = set(env_names(dep))
    # Read-only: it never relays, never consumes, never dials Kafka.
    stray = {
        "KAFKA_BROKERS", "OUTBOX_RELAY_INTERVAL", "OUTBOX_MAX_ATTEMPTS", "SHUTDOWN_DRAIN_DELAY",
        "INVENTORY_CONSUMER_GROUP", "SLOTTING_CONSUMER_GROUP", "LAYOUT_CONSUMER_GROUP", "PACK_LEVEL_CONSUMER_GROUP", *FORBIDDEN_ENV,
    } & env
    if stray:
        failures.append(f"the MCP Deployment must not get Kafka/relay/consumer env (it never dials Kafka): {sorted(stray)}")
    for want in ("DATABASE_URL", "MIGRATIONS_DATABASE_URL", "MCP_ADDR", "LOG_LEVEL", "FORWARD_ZONE_CODES", "RESERVE_ZONE_CODES"):
        if want not in env:
            failures.append(f"the MCP Deployment does not render {want}")


def check_a2a(dep: dict | None, failures: list[str]) -> None:
    if dep is None:
        return
    if command_of(dep) != ["/app/a2a"]:
        failures.append("the A2A Deployment must run /app/a2a")
    env = set(env_names(dep))
    # Read-only like mcp: it never relays, never consumes, never dials Kafka,
    # and builds no event publisher.
    stray = {
        "KAFKA_BROKERS", "EVENT_PUBLISHER", "OUTBOX_RELAY_INTERVAL", "OUTBOX_MAX_ATTEMPTS", "SHUTDOWN_DRAIN_DELAY",
        "INVENTORY_CONSUMER_GROUP", "SLOTTING_CONSUMER_GROUP", "LAYOUT_CONSUMER_GROUP", "PACK_LEVEL_CONSUMER_GROUP", *FORBIDDEN_ENV,
    } & env
    if stray:
        failures.append(f"the A2A Deployment must not get Kafka/publisher/relay/consumer env (it never dials Kafka): {sorted(stray)}")
    for want in ("DATABASE_URL", "MIGRATIONS_DATABASE_URL", "A2A_ADDR", "A2A_AGENT_URL", "LOG_LEVEL", "FORWARD_ZONE_CODES", "RESERVE_ZONE_CODES"):
        if want not in env:
            failures.append(f"the A2A Deployment does not render {want}")
    if env_value(dep, "A2A_ADDR") != ":8093":
        failures.append("the A2A Deployment must listen on :8093")


def check_analytics(projector: dict | None, reports: dict | None, failures: list[str]) -> None:
    if projector is not None:
        if command_of(projector) != ["/app/pfr-projector"]:
            failures.append("the projector Deployment must run /app/pfr-projector")
        env = set(env_names(projector))
        # It never opens the OLTP database and runs none of the api's consumers.
        stray = {
            "DATABASE_URL", "MIGRATIONS_DATABASE_URL", "OUTBOX_RELAY_INTERVAL", "OUTBOX_MAX_ATTEMPTS",
            "INVENTORY_CONSUMER_GROUP", "SLOTTING_CONSUMER_GROUP", "LAYOUT_CONSUMER_GROUP", "PACK_LEVEL_CONSUMER_GROUP",
            "FORWARD_ZONE_CODES", "RESERVE_ZONE_CODES", *FORBIDDEN_ENV,
        } & env
        if stray:
            failures.append(f"the projector Deployment must not get the api's OLTP env: {sorted(stray)}")
        for want in ("ANALYTICS_DATABASE_URL", "KAFKA_BROKERS", "ANALYTICS_CONSUMER_GROUP", "ADMIN_ADDR", "LOG_LEVEL"):
            if want not in env:
                failures.append(f"the projector Deployment does not render {want}")
        if env_value(projector, "ANALYTICS_CONSUMER_GROUP") != "pick-face-replenishment-analytics":
            failures.append("the projector's ANALYTICS_CONSUMER_GROUP must come from analytics.consumerGroup")
    if reports is not None:
        if command_of(reports) != ["/app/pfr-reports"]:
            failures.append("the reports Deployment must run /app/pfr-reports")
        env = set(env_names(reports))
        stray = {"KAFKA_BROKERS", "DATABASE_URL", "MIGRATIONS_DATABASE_URL", *FORBIDDEN_ENV} & env
        if stray:
            failures.append(f"the reports Deployment must not get Kafka/OLTP env (it reads the analytical database only): {sorted(stray)}")
        for want in ("ANALYTICS_DATABASE_URL", "HTTP_ADDR", "LOG_LEVEL"):
            if want not in env:
                failures.append(f"the reports Deployment does not render {want}")


def check_frontend(docs: list[dict], dep: dict | None, failures: list[str]) -> None:
    # The web remote (replenishment_mfe): its own Deployment + ClusterIP
    # Service, none of the api's OLTP/Kafka env, and no route from this chart
    # (warehouse-infra's Nginx web gateway owns /mfes/pick-face-replenishment/).
    frontend = f"{RELEASE}-frontend"
    svc = next((d for d in docs if d.get("kind") == "Service" and d["metadata"]["name"] == frontend), None)
    if svc is not None and svc["spec"].get("type") != "ClusterIP":
        failures.append("the frontend Service must be ClusterIP")
    if dep is not None and {"DATABASE_URL", "KAFKA_BROKERS"} & set(env_names(dep)):
        failures.append("the frontend Deployment must not get database/Kafka env (it serves static bytes)")
    for d in docs:
        if d.get("kind") in {"Ingress", "HTTPRoute"} and "frontend" in d["metadata"]["name"]:
            failures.append(f"{d['kind']} {d['metadata']['name']}: frontend routing must not live in this chart")


def check_env(api: dict, failures: list[str]) -> None:
    api_env = env_names(api)
    for want in API_ENV:
        if want not in api_env:
            failures.append(f"the api Deployment does not render {want} from its dedicated value")
    for banned in FORBIDDEN_ENV:
        if banned in api_env:
            failures.append(f"the api Deployment renders {banned}: the *_MODE variables are never set (ADR 0006)")
    duplicates = sorted({n for n in api_env if api_env.count(n) > 1})
    if duplicates:
        failures.append(f"the api Deployment renders env vars twice: {duplicates}")
    for name, want in (
        ("KAFKA_BROKERS", "kafka.example.invalid:9092"),
        ("FORWARD_ZONE_CODES", "FWD,FWD2"),
        ("RESERVE_ZONE_CODES", "RSV,BULK"),
        ("OUTBOX_RELAY_INTERVAL", "500ms"),
        ("SHUTDOWN_DRAIN_DELAY", "5s"),
    ):
        if env_value(api, name) != want:
            failures.append(f"the api Deployment renders {name}={env_value(api, name)!r}, want {want!r}")


def check_defaults(failures: list[str]) -> None:
    """Default values deploy the api only: no HPA, no route, no ingress."""
    defaults = render(BASE)
    kinds = sorted(d["kind"] for d in defaults)
    stray = [k for k in kinds if k in {"HorizontalPodAutoscaler", "Ingress", "HTTPRoute", "KongPlugin"}]
    stray += [
        d["metadata"]["name"] for d in defaults
        if d["metadata"]["name"].endswith(("-mcp", "-a2a", "-frontend", "-projector", "-reports", "-analytics"))
    ]
    if stray:
        failures.append(f"optional components rendered with default values: {stray}")
    if sorted(d["kind"] for d in defaults if d["kind"] in {"Deployment", "Service"}) != ["Deployment", "Service"]:
        failures.append("default values must deploy the api only (one Deployment, one Service)")
    dep = next(d for d in defaults if d.get("kind") == "Deployment")
    names = env_names(dep)
    for banned in FORBIDDEN_ENV:
        if banned in names:
            failures.append(f"{banned} must not be rendered with default values")
    # Optional values stay absent so the binary's own defaults apply.
    off = {"OUTBOX_RELAY_INTERVAL", "SHUTDOWN_DRAIN_DELAY", "FORWARD_ZONE_CODES", "RESERVE_ZONE_CODES"}
    if off & set(names):
        failures.append(f"these must be off with default values: {sorted(off & set(names))}")


def check_refusals(failures: list[str]) -> None:
    for label, args, needle in (
        ("without a database source", ["--set", "kafka.brokers=k:9092"], "database.url or database.existingSecret"),
        ("without Kafka brokers", ["--set", "database.url=postgres://u@h/db"], "kafka.brokers"),
        ("an empty inventory group", BASE + ["--set", "config.inventoryConsumerGroup="], "config.inventoryConsumerGroup"),
        ("an empty slotting group", BASE + ["--set", "config.slottingConsumerGroup="], "config.slottingConsumerGroup"),
        ("an empty layout group", BASE + ["--set", "config.layoutConsumerGroup="], "config.layoutConsumerGroup"),
        ("an empty pack-level group", BASE + ["--set", "config.packLevelConsumerGroup="], "config.packLevelConsumerGroup"),
        ("analytics without a DSN source", BASE + ["--set", "analytics.enabled=true"],
         "neither analytics.url nor analytics.existingSecret"),
        ("analytics without a consumer group",
         BASE + ["--set", "analytics.enabled=true", "--set", "analytics.url=postgres://u@h/a", "--set", "analytics.consumerGroup="],
         "analytics.consumerGroup"),
        ("a *_MODE variable through extraEnv",
         BASE + ["--set", "extraEnv[0].name=SLOTTING_MODE", "--set", "extraEnv[0].value=permissive"], "SLOTTING_MODE"),
    ):
        result = helm_template(args)
        if result.returncode == 0 or needle not in result.stderr:
            failures.append(f"chart rendered (or failed for another reason) {label}")


def main() -> int:
    failures: list[str] = []

    check_selectors(render(ENABLE_EVERYTHING), failures)
    check_defaults(failures)
    check_refusals(failures)

    if failures:
        for f in failures:
            print(f"FAIL: {f}")
        return 1

    print("PASS: every Service selects exactly one Deployment (api, mcp, a2a, analytics-reports, frontend); mcp, a2a, analytics, "
          "frontend, HPA and routes are off by default; "
          "every cmd/api env var has a dedicated value and no *_MODE is rendered; the chart refuses to "
          "render without a database source, without brokers, with an empty consumer group or with a "
          "*_MODE injected through extraEnv, or with analytics on but no DSN source or no consumer group")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
