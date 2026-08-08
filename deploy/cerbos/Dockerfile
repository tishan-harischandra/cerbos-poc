# syntax=docker/dockerfile:1
#
# Packages the committed Cerbos policy tree and capability/authorization
# catalogs as a small "assets" image. docker-compose bind-mounts
# deploy/cerbos/policies and deploy/cerbos/catalog straight from the host,
# which has no equivalent in Kubernetes; this image is copied into an
# emptyDir by an initContainer on every pod that needs one of these trees
# instead (see deploy/k8s/base/*/deployment.yaml), because the combined
# tree is well over the ~1MiB ConfigMap size Kubernetes enforces and its
# subdirectory structure (one policy file per resource, ADR-006) doesn't
# survive a flat ConfigMap's key space anyway.
#
# Build context is the repository root, matching every other Dockerfile
# here, so this can sit alongside deploy/cerbos/config.yaml unmodified.
FROM docker.io/library/alpine:3.20

COPY deploy/cerbos/policies /assets/policies
COPY deploy/cerbos/catalog /assets/catalog
COPY deploy/cerbos/config.yaml /assets/config.yaml
COPY deploy/cerbos/config-managed.yaml /assets/config-managed.yaml

ENTRYPOINT ["/bin/sh"]
