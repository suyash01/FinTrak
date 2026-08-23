# --- Build Stage ---
FROM oven/bun:1.4-alpine AS builder

WORKDIR /app
COPY package.json bun.lock ./
RUN bun install --frozen-lockfile

COPY . .
# Default API URL; override at build time with: docker build --build-arg VITE_API_URL=...
ARG VITE_API_URL=/api/v1
ENV VITE_API_URL=${VITE_API_URL}
RUN bun run build

# --- Runtime Stage ---
FROM nginxinc/nginx-unprivileged:1.31-alpine-perl

COPY --from=builder --chown=nginx:nginx /app/dist /usr/share/nginx/html
COPY --chown=nginx:nginx nginx.conf /etc/nginx/conf.d/default.conf

EXPOSE 8080
# Use 127.0.0.1 (not localhost): busybox wget may resolve localhost to ::1,
# while nginx binds IPv4 only -> false "connection refused" / unhealthy status
HEALTHCHECK --interval=30s --timeout=3s CMD wget -qO- http://127.0.0.1:8080/ || exit 1
CMD ["nginx", "-g", "daemon off;"]
