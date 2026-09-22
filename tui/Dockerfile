# --- Build Stage ---
# The build context is the repository root, because the TUI module replaces the
# shared client module with a path (../client): both have to be visible to the
# build. See the root .dockerignore for what is kept out of that context.
FROM golang:1.27.1-alpine3.24 AS builder

WORKDIR /src
COPY client/go.mod client/go.sum ./client/
COPY tui/go.mod tui/go.sum ./tui/
RUN cd tui && GOFLAGS=-mod=readonly go mod download

COPY client/ ./client/
COPY tui/ ./tui/

# Override at build time with: docker build --build-arg VERSION=v1.2.3
ARG VERSION=dev
RUN cd tui && CGO_ENABLED=0 go build -ldflags="-s -w -X main.version=${VERSION}" -o /fintrak-tui .

# --- Runtime Stage ---
FROM alpine:3.24

RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S appgroup && adduser -S appuser -G appgroup \
    && mkdir -p /home/appuser/.config/fintrak-tui \
    && chown -R appuser:appgroup /home/appuser/.config

WORKDIR /app
COPY --from=builder --chown=appuser:appgroup /fintrak-tui .

USER appuser
EXPOSE 2222

# The generated SSH host key lives here. Persist this path: a regenerated host
# key makes every client refuse to connect until its known_hosts entry is
# cleared. The directory is created above and owned by appuser so a fresh named
# volume inherits the ownership — Docker copies the image's directory metadata
# into a new volume, and a root-owned mount point would leave the unprivileged
# process unable to write its host key.
VOLUME ["/home/appuser/.config/fintrak-tui"]

# The container only makes sense as the SSH door; a local terminal has no meaning
# inside it.
ENV FINTRAK_SSH=true \
    FINTRAK_SSH_ADDR=:2222 \
    FINTRAK_API_URL=http://backend:8080/api/v1

ENTRYPOINT ["./fintrak-tui"]
