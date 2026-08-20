# --- Build Stage ---
FROM oven/bun:1 AS builder

WORKDIR /app
COPY package.json bun.lock ./
RUN bun install --frozen-lockfile

COPY . .
# Default API URL; override at build time with: docker build --build-arg VITE_API_URL=...
ARG VITE_API_URL=/api/v1
ENV VITE_API_URL=${VITE_API_URL}
RUN bun run build

# --- Runtime Stage ---
FROM nginx:stable-alpine

COPY --from=builder /app/dist /usr/share/nginx/html
COPY nginx.conf /etc/nginx/conf.d/default.conf

EXPOSE 80
CMD ["nginx", "-g", "daemon off;"]
