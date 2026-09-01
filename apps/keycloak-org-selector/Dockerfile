# syntax=docker/dockerfile:1
# Build context is the repository root.
#
# The custom Keycloak image issue #79 requires: the organization selector
# provider (apps/keycloak-org-selector) is built in this Maven stage and
# baked into the server with `kc.sh build`, so no JVM or Maven is needed on
# the host or in any Go service image - Keycloak is the one component in
# this repository that legitimately carries one.
FROM docker.io/library/maven:3.9-eclipse-temurin-17 AS build

WORKDIR /src
COPY apps/keycloak-org-selector/pom.xml ./
# Dependencies first, cached separately from source changes.
RUN mvn -q -B dependency:go-offline

COPY apps/keycloak-org-selector/src ./src
# Runs the JUnit suite as part of the image build (issue #79's acceptance
# criterion): a case that regresses fails the build rather than shipping.
RUN mvn -q -B package

FROM quay.io/keycloak/keycloak:26.4

COPY --from=build /src/target/org-selector.jar /opt/keycloak/providers/org-selector.jar
# `--features=organization` matches docker-compose.yml/the k8s manifests:
# Keycloak Organizations is a preview feature and every organization import
# 400s without it.
RUN /opt/keycloak/bin/kc.sh build --features=organization
