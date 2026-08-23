"""
app.py
------
Flask app exposing:
  - A simple web UI at "/" to upload a statement PDF (with optional password)
    and view/download the extracted transactions.
  - A REST API at POST /api/extract that accepts a PDF (+ optional password)
    and returns JSON, or CSV if ?format=csv is passed.

Run:
    python app.py
Then open http://localhost:5000
"""

import os
import tempfile

from flask import Flask, request, jsonify, render_template, Response
from werkzeug.datastructures import FileStorage
from werkzeug.exceptions import RequestEntityTooLarge

try:
    from .extractor import (
        PdfPasswordRequired,
        extract_transactions,
        list_extractors,
        to_csv_bytes,
    )
except ImportError:  # pragma: no cover - allows direct script execution
    from extractor import (  # type: ignore[import-not-found]
        PdfPasswordRequired,
        extract_transactions,
        list_extractors,
        to_csv_bytes,
    )

app = Flask(
    __name__,
    template_folder=os.path.dirname(__file__),
)
app.config["MAX_CONTENT_LENGTH"] = 20 * 1024 * 1024  # 20 MB upload limit


def _save_upload_to_temp(file_storage: FileStorage) -> str:
    fd, path = tempfile.mkstemp(suffix=".pdf")
    os.close(fd)
    file_storage.save(path)
    return path


@app.route("/")
def index():
    return render_template("./index.html")


@app.route("/health")
def health():
    # Liveness probe used by the Docker HEALTHCHECK.
    return jsonify({"status": "ok"})


@app.route("/api/extractors")
def api_extractors():
    return jsonify({"extractors": list_extractors()})


@app.route("/api/extract", methods=["POST"])
def api_extract():
    """
    REST API endpoint.

    Form fields:
      - file: the PDF file (required)
      - password: PDF password (optional)

    Query params:
      - format: "json" (default) or "csv"
    """
    if "file" not in request.files:
        return jsonify({"error": "No file provided. Attach it as 'file'."}), 400

    file = request.files["file"]
    if file.filename == "":
        return jsonify({"error": "No file selected."}), 400

    if not file.filename or not file.filename.lower().endswith(".pdf"):
        return jsonify({"error": "Only PDF files are supported."}), 400

    password = request.form.get("password") or None
    out_format = request.args.get("format", "json").lower()
    extractor_name = request.args.get("extractor", "sbi_cc") or "sbi_cc"

    tmp_path = _save_upload_to_temp(file)
    try:
        result = extract_transactions(
            tmp_path,
            extractor_name=extractor_name,
            password=password,
        )
    except PdfPasswordRequired as e:
        return jsonify({"error": str(e), "password_required": True}), 401
    except KeyError as e:
        return jsonify({"error": str(e)}), 400
    except Exception as e:
        return jsonify({"error": f"Failed to process PDF: {e}"}), 422
    finally:
        os.remove(tmp_path)

    if out_format == "csv":
        csv_bytes = to_csv_bytes(
            result["transactions"],
            extractor_name=extractor_name,
        )
        return Response(
            csv_bytes,
            mimetype="text/csv",
            headers={
                "Content-Disposition": "attachment; filename=transactions.csv"
            },
        )

    return jsonify(result)


@app.errorhandler(413)
def too_large(_e: RequestEntityTooLarge):
    return jsonify({"error": "File too large. Max upload size is 20 MB."}), 413
