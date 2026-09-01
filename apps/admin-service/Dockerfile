# syntax=docker/dockerfile:1
# Build context is the repository root.
FROM docker.io/library/golang:1.25-alpine AS build

WORKDIR /src

# admin-service depends on sibling modules in libs/, resolved by the replace
# directives in its go.mod. Their manifests are copied before the sources so a
# change to admin-service code alone still reuses the cached download layer.
COPY apps/admin-service/go.mod apps/admin-service/go.sum ./apps/admin-service/
COPY libs/assignmentstore/go.mod libs/assignmentstore/go.sum ./libs/assignmentstore/
COPY libs/capabilitycatalog/go.mod libs/capabilitycatalog/go.sum ./libs/capabilitycatalog/
COPY libs/cataloggen/go.mod libs/cataloggen/go.sum ./libs/cataloggen/
COPY libs/canonicalid/go.mod ./libs/canonicalid/
COPY libs/idpdirectory/go.mod ./libs/idpdirectory/
COPY libs/leaderlock/go.mod libs/leaderlock/go.sum ./libs/leaderlock/
COPY libs/tokenverifier/go.mod ./libs/tokenverifier/
COPY libs/outbox/go.mod libs/outbox/go.sum ./libs/outbox/
COPY libs/permissionevents/go.mod ./libs/permissionevents/
COPY libs/policyrelease/go.mod libs/policyrelease/go.sum ./libs/policyrelease/
COPY libs/tenantregistry/go.mod libs/tenantregistry/go.sum ./libs/tenantregistry/
WORKDIR /src/apps/admin-service
RUN go mod download

COPY libs/ /src/libs/
COPY apps/admin-service/ /src/apps/admin-service/
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/admin-service ./cmd/admin-service

# The Admin Console's bundle ships inside this image, because this service
# serves it (ADR-008). The stage is copied from apps/admin-console's former
# Dockerfile, which this replaces.
FROM docker.io/library/node:24-alpine AS console

# The Nx Go plugin computes the project graph and needs a Go toolchain on PATH.
# Taking it from the official image keeps the host free of a Go install.
COPY --from=docker.io/library/golang:1.25-alpine /usr/local/go /usr/local/go
ENV PATH="/usr/local/go/bin:${PATH}"

WORKDIR /workspace
COPY package.json package-lock.json ./
RUN npm ci --no-audit --no-fund

COPY nx.json tsconfig.base.json eslint.config.mjs go.work ./
# Every module go.work names must exist or the Nx Go plugin cannot compute the
# project graph, even though this stage only builds the Angular app.
COPY apps/ads/go.mod apps/ads/go.sum ./apps/ads/
COPY libs/cerbosclient/go.mod libs/cerbosclient/go.sum ./libs/cerbosclient/
COPY libs/permissioncontext/go.mod ./libs/permissioncontext/
COPY tests/architecture/go.mod ./tests/architecture/
COPY libs/web/auth/ ./libs/web/auth/
COPY apps/admin-console/ ./apps/admin-console/
RUN npx nx build admin-console --configuration=production

FROM docker.io/library/alpine:3.20 AS runtime

RUN apk add --no-cache ca-certificates wget \
  && adduser -D -u 10002 admin-service
USER admin-service

COPY --from=build /out/admin-service /usr/local/bin/admin-service
# ADMIN_CONSOLE_DIR defaults to this path.
COPY --from=console /workspace/dist/apps/admin-console/browser /admin-console

EXPOSE 8081
ENTRYPOINT ["/usr/local/bin/admin-service"]
