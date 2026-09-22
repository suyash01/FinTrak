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
        PageLimitExceeded,
        PdfPasswordRequired,
        extract_transactions,
        list_extractors,
        to_csv_bytes,
    )
except ImportError:  # pragma: no cover - allows direct script execution
    from extractor import (  # type: ignore[import-not-found]
        PageLimitExceeded,
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


#: Reported when the template was recognised but the statement itself carries no
#: importable rows (e.g. a period with no activity). Distinct from the 422 below,
#: which means nothing about the file was recognised at all.
EMPTY_STATEMENT_NOTE = (
    "The statement was recognised but contains no importable transactions for "
    "the selected period."
)

#: Keys that carry the statement's OWN content: an extractor only fills them from
#: text it actually matched (the account block, the period line, the printed
#: summary row, or the credit-card headline figures), so any one of them present
#: proves the template was recognised. `bank` and `statement_type` are
#: deliberately absent: they are constants naming the extractor and are set even
#: when nothing at all was parsed.
_RECOGNITION_KEYS = (
    "summary",  # headline figures, e.g. total_amount_due
    "accounts",  # the statement's own account list
    "account_holder",
    "account_number",
    "customer_id",
    "statement_period_from",
    "statement_period_to",
    "opening_balance",
    "closing_balance",
)


def _statement_was_recognised(result: dict) -> bool:
    """True when the extractor found evidence of the expected template.

    Used to tell "nothing recognised" (a scanned PDF, the wrong extractor) from
    "recognised, but there is nothing to import": the latter renders as a 0-row
    import, the former as an error.
    """
    return any(result.get(key) for key in _RECOGNITION_KEYS)


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
    except PageLimitExceeded as e:
        return jsonify({"error": str(e)}), 422
    except KeyError as e:
        return jsonify({"error": str(e)}), 400
    except Exception:
        # Log the real traceback server-side; never leak library internals or
        # stack fragments to the client.
        app.logger.exception("Failed to process uploaded statement")
        return jsonify({"error": "Failed to process the statement"}), 422
    finally:
        os.remove(tmp_path)

    if not result.get("transactions"):
        if not _statement_was_recognised(result):
            # Nothing about the file was recognised: an image-only (scanned)
            # PDF, the wrong extractor, or a template the extractor no longer
            # matches. Answering 200 with an empty list made those look like a
            # clean, warning-free import in the app, so they stay an error.
            return (
                jsonify(
                    {
                        "error": (
                            "No transactions found. The file may be a scanned PDF "
                            "without a text layer, or the wrong extractor may be "
                            "selected."
                        )
                    }
                ),
                422,
            )
        # Recognised template, zero importable rows (the bank extractors drop the
        # zero-amount Brought/Carried Forward rows, so a quiet period yields an
        # empty list). That is a 0-row result, not a parse failure; the note goes
        # in validation_errors, the list the import UI already surfaces.
        result = {
            **result,
            "validation_errors": [
                *result.get("validation_errors", []),
                EMPTY_STATEMENT_NOTE,
            ],
        }

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
