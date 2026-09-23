import os

from .app import app


def main() -> None:
    # Local development only: the debugger must be an explicit opt-in and the
    # dev server binds loopback so a reachable interface never exposes Werkzeug's
    # interactive debugger (an RCE vector) to the network. Production runs
    # gunicorn and never reaches this entry point.
    debug = os.environ.get("FLASK_DEBUG") == "1"
    host = os.environ.get("STATEMENT_PARSER_HOST", "127.0.0.1")
    app.run(debug=debug, host=host, port=5000)
