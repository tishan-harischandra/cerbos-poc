# syntax=docker/dockerfile:1
# Build context is the repository root.
FROM docker.io/library/node:24-alpine AS build

# The Nx Go plugin computes the project graph and needs a Go toolchain on PATH.
# Taking it from the official image keeps the host free of a Go install.
COPY --from=docker.io/library/golang:1.25-alpine /usr/local/go /usr/local/go
ENV PATH="/usr/local/go/bin:${PATH}"

WORKDIR /workspace
COPY package.json package-lock.json ./
RUN npm ci --no-audit --no-fund

COPY nx.json tsconfig.base.json eslint.config.mjs go.work ./
# Every module go.work names must exist or the Nx Go plugin cannot compute the
# project graph, even though this image only builds the Angular app.
COPY apps/ads/go.mod apps/ads/go.sum ./apps/ads/
COPY libs/cerbosclient/go.mod libs/cerbosclient/go.sum ./libs/cerbosclient/
COPY libs/permissioncontext/go.mod ./libs/permissioncontext/
COPY tests/architecture/go.mod ./tests/architecture/
COPY apps/admin-console/ ./apps/admin-console/
RUN npx nx build admin-console --configuration=production

FROM docker.io/library/nginx:1.27-alpine AS runtime

RUN apk add --no-cache wget
COPY apps/admin-console/nginx.conf.template /etc/nginx/templates/default.conf.template

# Makes the image publish NGINX_LOCAL_RESOLVERS, which the template needs.
ENV NGINX_ENTRYPOINT_LOCAL_RESOLVERS=1
COPY --from=build /workspace/dist/apps/admin-console/browser /usr/share/nginx/html

EXPOSE 80
