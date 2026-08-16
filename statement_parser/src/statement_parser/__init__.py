from .app import app


def main() -> None:
    app.run(debug=True, host="0.0.0.0", port=5000)
