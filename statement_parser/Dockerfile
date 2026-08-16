# Statement Parser service (Python/Flask) - exposes a REST API used by the
# Go backend. Kept as a separate container so the Go backend and the parser
# only communicate over HTTP (decoupled).
FROM python:3.12-slim

WORKDIR /app

COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

COPY src/ ./src/

ENV FLASK_ENV=production
ENV PYTHONPATH=/app/src
ENV GUNICORN_CMD_ARGS="--workers 2 --timeout 120"
EXPOSE 5000

# Serve the Flask app with gunicorn (production WSGI server).
CMD ["gunicorn", "-b", "0.0.0.0:5000", "statement_parser.app:app"]