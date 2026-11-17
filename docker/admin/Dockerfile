# Repository-root context. No build arguments or environment-injected credentials.
# Resolve reviewed immutable digests for BOTH base images before release.
FROM node:22.16.0-bookworm-slim AS build
WORKDIR /app
COPY apps/admin/package.json apps/admin/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY apps/admin/ ./
RUN npm run lint && npm run build

FROM nginx:1.28.0-alpine
COPY docker/admin/nginx.conf /etc/nginx/nginx.conf
COPY --from=build /app/dist/ /usr/share/nginx/html/
USER 101:101
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget -q -O /dev/null http://127.0.0.1:8080/healthz || exit 1
# Bypass the upstream entrypoint's configuration rewrites; config is read-only.
ENTRYPOINT ["nginx"]
CMD ["-g", "daemon off;"]