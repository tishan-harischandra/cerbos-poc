# syntax=docker/dockerfile:1
# Build context is the repository root.
FROM docker.io/library/golang:1.25-alpine AS build

WORKDIR /src

# The resource service depends on sibling modules in libs/, resolved by the
# replace directives in its go.mod. Their manifests are copied before the
# sources so a change to resource-service code alone still reuses the cached
# download layer.
COPY apps/resource-service/go.mod apps/resource-service/go.sum ./apps/resource-service/
COPY libs/assignmentstore/go.mod libs/assignmentstore/go.sum ./libs/assignmentstore/
COPY libs/tokenverifier/go.mod ./libs/tokenverifier/
COPY libs/canonicalid/go.mod ./libs/canonicalid/
COPY libs/idpdirectory/go.mod ./libs/idpdirectory/
WORKDIR /src/apps/resource-service
RUN go mod download

COPY libs/ /src/libs/
COPY apps/resource-service/ /src/apps/resource-service/
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/resource-service ./cmd/resource-service

FROM docker.io/library/alpine:3.20 AS runtime

RUN apk add --no-cache ca-certificates wget \
  && adduser -D -u 10001 resource-service
USER resource-service

COPY --from=build /out/resource-service /usr/local/bin/resource-service

EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/resource-service"]
