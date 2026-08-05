#!/usr/bin/env python3
"""Contract tests for the docker compose topology of the walking skeleton.

These assert the invariants the slice promises: every service is health
checked, the control plane starts in dependency order, and the Cerbos PDP is
reachable only from inside the compose network.
"""
from __future__ import annotations

import json
import pathlib
import sys

try:
    import yaml
except ModuleNotFoundError:  # pragma: no cover - environment guard
    sys.exit("PyYAML is required: pip install pyyaml")

REPO_ROOT = pathlib.Path(__file__).resolve().parents[2]
COMPOSE_FILE = REPO_ROOT / "docker-compose.yml"

failures: list[str] = []


def check(description: str, condition: bool) -> None:
    status = "ok  " if condition else "FAIL"
    print(f"{status} {description}")
    if not condition:
        failures.append(description)


# Two dependencies the prototype deliberately refuses to carry, both easy to
# reintroduce by accident:
#
#   Oracle Instant Client - the reason the Oracle driver is go-ora, which speaks
#   the wire protocol in pure Go. An ODPI-based driver would put a native client
#   library inside every image that touches the database.
#
#   A JVM - Liquibase needs one, so Liquibase runs as its own container rather
#   than being installed into a service image or onto the host.
FORBIDDEN_IN_IMAGES = {
    "instantclient": "Oracle Instant Client",
    "libaio": "the Instant Client's native dependency",
    "oracle-instantclient": "Oracle Instant Client",
    "openjdk": "a JVM",
    "default-jre": "a JVM",
    "default-jdk": "a JVM",
    "temurin": "a JVM",
    "liquibase": "Liquibase, which belongs in its own container",
}


def check_images_carry_no_native_clients() -> None:
    dockerfiles = sorted(REPO_ROOT.glob("apps/*/Dockerfile"))
    check("there is at least one service Dockerfile to inspect", bool(dockerfiles))

    for dockerfile in dockerfiles:
        relative = dockerfile.relative_to(REPO_ROOT)
        text = dockerfile.read_text().lower()
        offenders = [
            description
            for token, description in FORBIDDEN_IN_IMAGES.items()
            if token in text
        ]
        check(
            f"{relative} installs no native database client and no JVM",
            not offenders,
        )
        if offenders:
            print(f"     found: {', '.join(sorted(set(offenders)))}")


def check_identity_provider(services: dict) -> None:
    """The §7.1 installation selection, as the running stack expresses it."""
    check("service 'keycloak' is defined", "keycloak" in services)
    if "keycloak" not in services:
        return

    keycloak = services["keycloak"]
    # Unlike the PDP and the database, Keycloak has to be reachable from the
    # browser: an OIDC redirect the user cannot follow is not a login.
    check("keycloak is published to the host so a browser can log in",
          bool(keycloak.get("ports")))
    check("keycloak imports a realm rather than being configured by hand",
          "--import-realm" in (keycloak.get("command") or []))

    realm_import = REPO_ROOT / "deploy" / "keycloak" / "realm-cerbos-poc.json"
    check("the realm import exists", realm_import.exists())
    if realm_import.exists():
        realm = json.loads(realm_import.read_text())
        clients = {client["clientId"]: client for client in realm.get("clients", [])}
        check("the realm defines the browser-facing client", "patient-app" in clients)
        check("the realm defines the confidential service account",
              "authorization-admin-service" in clients)
        if "patient-app" in clients:
            # §7.3: the client a browser holds must not be able to read the
            # directory. A public client with a service account would hand
            # every browser session an administrative identity.
            check("the browser-facing client is public and has no service account",
                  clients["patient-app"].get("publicClient") is True
                  and not clients["patient-app"].get("serviceAccountsEnabled"))
        if "authorization-admin-service" in clients:
            service = clients["authorization-admin-service"]
            check("the service account client is confidential and browserless",
                  service.get("publicClient") is False
                  and service.get("serviceAccountsEnabled") is True
                  and not service.get("redirectUris"))

    ads = services.get("ads", {})
    environment = ads.get("environment") or {}
    check("the ads waits for a healthy keycloak",
          (ads.get("depends_on") or {}).get("keycloak", {}).get("condition") == "service_healthy")
    check("the ads is told which identity provider to use", "IDP_TYPE" in environment)

    # §7.1's credentialsSecretRef. A secret passed by value would be readable
    # through `docker inspect` and inherited by every child process.
    check("the ads reads the service account secret by reference, not by value",
          "IDP_CREDENTIALS_SECRET_REF" in environment
          and not any(key.endswith("CLIENT_SECRET") for key in environment))

    console_environment = services.get("admin-console", {}).get("environment") or {}
    check("no identity provider credential reaches the browser-facing service",
          not any("SECRET" in key.upper() or "PASSWORD" in key.upper()
                  for key in console_environment))


def main() -> int:
    if not COMPOSE_FILE.exists():
        print(f"FAIL {COMPOSE_FILE} does not exist")
        return 1

    compose = yaml.safe_load(COMPOSE_FILE.read_text())
    services = compose.get("services", {})

    for name in ("postgres", "cerbos", "ads", "admin-console"):
        check(f"service '{name}' is defined", name in services)
    if failures:
        return 1

    for name, service in services.items():
        check(f"service '{name}' declares a healthcheck", "healthcheck" in service)

    check(
        "the Cerbos PDP is not published to the host",
        not services["cerbos"].get("ports"),
    )
    check(
        "postgres is not published to the host",
        not services["postgres"].get("ports"),
    )

    ads_deps = services["ads"].get("depends_on", {})
    for dependency in ("cerbos", "postgres"):
        check(
            f"ads waits for a healthy '{dependency}'",
            ads_deps.get(dependency, {}).get("condition") == "service_healthy",
        )

    console_deps = services["admin-console"].get("depends_on", {})
    check(
        "admin-console waits for a healthy 'ads'",
        console_deps.get("ads", {}).get("condition") == "service_healthy",
    )

    check(
        "the admin console is published to the host",
        bool(services["admin-console"].get("ports")),
    )
    check(
        "postgres data lives on a named volume so 'make clean' can remove it",
        bool(compose.get("volumes")),
    )

    # Oracle is a portability target, not part of the running stack. The image is
    # large and slow to start, so a default `make up` that pulled it would make
    # the ordinary path unusable.
    check("service 'oracle' is defined", "oracle" in services)
    if "oracle" in services:
        check(
            "oracle sits behind a profile, so it does not start by default",
            "oracle" in services["oracle"].get("profiles", []),
        )
        check(
            "oracle is not published to the host",
            not services["oracle"].get("ports"),
        )
        # Nothing may wait on Oracle: a dependency would drag it into the default
        # start-up path through the back door.
        for name, service in services.items():
            if name == "oracle":
                continue
            check(
                f"service '{name}' does not depend on oracle",
                "oracle" not in (service.get("depends_on") or {}),
            )

    for name in ("ads", "admin-console"):
        build = services[name].get("build")
        check(f"service '{name}' is built from a Dockerfile in this repo", bool(build))

    check_images_carry_no_native_clients()
    check_identity_provider(services)

    if failures:
        print(f"\n{len(failures)} contract failure(s)")
        return 1
    print("\ncompose contract satisfied")
    return 0


if __name__ == "__main__":
    sys.exit(main())
