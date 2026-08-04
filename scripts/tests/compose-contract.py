#!/usr/bin/env python3
"""Contract tests for the docker compose topology of the walking skeleton.

These assert the invariants the slice promises: every service is health
checked, the control plane starts in dependency order, and the Cerbos PDP is
reachable only from inside the compose network.
"""
from __future__ import annotations

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

    if failures:
        print(f"\n{len(failures)} contract failure(s)")
        return 1
    print("\ncompose contract satisfied")
    return 0


if __name__ == "__main__":
    sys.exit(main())
