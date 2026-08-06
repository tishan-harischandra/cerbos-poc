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
COPY libs/tokenverifier/go.mod ./libs/tokenverifier/
COPY libs/outbox/go.mod libs/outbox/go.sum ./libs/outbox/
COPY libs/permissionevents/go.mod ./libs/permissionevents/
WORKDIR /src/apps/admin-service
RUN go mod download

COPY libs/ /src/libs/
COPY apps/admin-service/ /src/apps/admin-service/
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/admin-service ./cmd/admin-service

FROM docker.io/library/alpine:3.20 AS runtime

RUN apk add --no-cache ca-certificates wget \
  && adduser -D -u 10002 admin-service
USER admin-service

COPY --from=build /out/admin-service /usr/local/bin/admin-service

EXPOSE 8081
ENTRYPOINT ["/usr/local/bin/admin-service"]
