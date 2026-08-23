# --- Build Stage ---
FROM python:3.14-alpine3.24 AS builder

WORKDIR /app

# Build tools needed if any requirement compiles from source (musl has no wheels for many C-extension packages).
RUN apk add --no-cache gcc musl-dev libffi-dev

COPY requirements.txt .
RUN pip install --no-cache-dir --user -r requirements.txt

# --- Runtime Stage ---
FROM python:3.14-alpine3.24

WORKDIR /app

RUN addgroup -S appgroup && adduser -S appuser -G appgroup

# Copy installed packages from the builder (no compiler needed here).
COPY --from=builder /root/.local /home/appuser/.local
COPY --chown=appuser:appgroup src/ ./src/

ENV FLASK_ENV=production \
    PYTHONPATH=/app/src \
    PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    PATH=/home/appuser/.local/bin:$PATH \
    GUNICORN_CMD_ARGS="--workers 2 --timeout 120"

USER appuser
EXPOSE 5000
HEALTHCHECK --interval=30s --timeout=3s CMD python -c "import urllib.request; urllib.request.urlopen('http://localhost:5000/health')" || exit 1

CMD ["gunicorn", "-b", "0.0.0.0:5000", "statement_parser.app:app"]