# syntax=docker/dockerfile:1
# Build context is the repository root.
FROM docker.io/library/golang:1.25-alpine AS build

WORKDIR /src

# The ADS depends on sibling modules in libs/, resolved by the replace
# directives in its go.mod. Their manifests are copied before the sources so a
# change to ADS code alone still reuses the cached download layer.
COPY apps/ads/go.mod apps/ads/go.sum ./apps/ads/
COPY libs/assignmentstore/go.mod libs/assignmentstore/go.sum ./libs/assignmentstore/
COPY libs/cerbosclient/go.mod libs/cerbosclient/go.sum ./libs/cerbosclient/
COPY libs/permissioncontext/go.mod ./libs/permissioncontext/
COPY libs/canonicalid/go.mod ./libs/canonicalid/
COPY libs/tokenverifier/go.mod ./libs/tokenverifier/
COPY libs/idpdirectory/go.mod ./libs/idpdirectory/
COPY libs/authzcache/go.mod ./libs/authzcache/
COPY libs/capabilitycatalog/go.mod libs/capabilitycatalog/go.sum ./libs/capabilitycatalog/
COPY libs/capabilityeval/go.mod libs/capabilityeval/go.sum ./libs/capabilityeval/
COPY libs/cataloggen/go.mod libs/cataloggen/go.sum ./libs/cataloggen/
WORKDIR /src/apps/ads
RUN go mod download

COPY libs/ /src/libs/
COPY apps/ads/ /src/apps/ads/
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/ads ./cmd/ads

FROM docker.io/library/alpine:3.20 AS runtime

RUN apk add --no-cache ca-certificates wget \
  && adduser -D -u 10001 ads
USER ads

COPY --from=build /out/ads /usr/local/bin/ads

EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/ads"]
