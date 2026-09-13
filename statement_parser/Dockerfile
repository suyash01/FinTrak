# --- Build Stage ---
FROM python:3.14.7-alpine3.24 AS builder

# uv is a fast, lockfile-driven Python package manager. The official distroless
# image ships a statically-linked binary that runs on musl (alpine).
COPY --from=ghcr.io/astral-sh/uv:0.12.13 /uv /uvx /bin/

WORKDIR /app

# Build tools needed if any requirement compiles from source (musl has no wheels for many C-extension packages).
RUN apk add --no-cache gcc musl-dev libffi-dev

ENV UV_COMPILE_BYTECODE=1 \
    UV_LINK_MODE=copy \
    UV_PYTHON_DOWNLOADS=never

# Install only the dependencies (not the project itself) from the lockfile, in a
# cacheable layer that doesn't change when application source does.
COPY pyproject.toml uv.lock ./
RUN uv sync --locked --no-install-project --no-dev

# --- Runtime Stage ---
FROM python:3.14.7-alpine3.24

WORKDIR /app

RUN addgroup -S appgroup && adduser -S appuser -G appgroup

# Copy the virtualenv built from the lockfile and the application source.
COPY --from=builder --chown=appuser:appgroup /app/.venv /app/.venv
COPY --chown=appuser:appgroup src/ ./src/

ENV FLASK_ENV=production \
    PYTHONPATH=/app/src \
    PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    PATH=/app/.venv/bin:$PATH \
    GUNICORN_CMD_ARGS="--workers 2 --timeout 120"

USER appuser
EXPOSE 5000
HEALTHCHECK --interval=30s --timeout=3s CMD python -c "import urllib.request; urllib.request.urlopen('http://localhost:5000/health')" || exit 1

CMD ["gunicorn", "-b", "0.0.0.0:5000", "statement_parser.app:app"]
