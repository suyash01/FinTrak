# Statement Transaction Extractor

Extracts the transaction table from SBI Card style credit card statement PDFs
(tested against the "PhonePe SBI Card SELECT BLACK" monthly statement layout).
Supports password-protected PDFs, a small web UI, a CLI, a REST API, and an
extensible extractor registry so additional statement parsers can be registered
later without changing the app flow.

## Setup

```bash
pip install -r requirements.txt
```

## Run the web app

For local development (Flask dev server):

```bash
python -m statement_parser
```

For production, serve with gunicorn (the WSGI server used in the Docker image):

```bash
gunicorn -b 0.0.0.0:5000 statement_parser.app:app
```

Then open http://localhost:5000 — upload a PDF, enter a password if the file
is protected, and view/download the extracted transactions.

## REST API

**POST** `/api/extract`

Form fields:

- `file` (required) — the statement PDF
- `password` (optional) — the PDF's password

Query params:

- `format` — `json` (default) or `csv`

Example:

```bash
curl -F "file=@statement.pdf" -F "password=1234" http://localhost:5000/api/extract
curl -F "file=@statement.pdf" "http://localhost:5000/api/extract?format=csv" -o transactions.csv
```

Response (JSON):

```json
{
  "transactions": [
    {
      "date": "18 May 26",
      "description": "UPI-SUYASH MITTAL",
      "amount": 310.0,
      "type": "Credit"
    }
  ],
  "summary": {
    "total_amount_due": "40,991.00",
    "credit_limit": "2,29,000.00",
    "available_credit_limit": "1,88,009.02"
  },
  "page_count": 7,
  "transaction_count": 15
}
```

If the PDF is encrypted and no/wrong password is given, the API responds
`401` with `{"error": "...", "password_required": true}`.

## Command line

```bash
python -m statement_parser.sbi_cc_extractor statement.pdf --password 1234 --out transactions.csv
# or omit --out to print JSON to stdout
```

## How it works

The core logic now lives in `sbi_cc_extractor.py` and is registered through
`extractor.py`, which provides a small registry for future extractors. The
SBI implementation uses `pdfplumber` to pull text per page (handling
decryption via `pypdf`/`pdfplumber`'s built-in password support), then
matches each line against a regex tuned to the statement's
`DD Mon YY  Description  Amount  C|D` transaction format. A handful of headline
figures (total due, minimum due, credit limit, etc.) are pulled out separately
as a best-effort summary.

If you add a differently formatted statement parser later, register it with
`register_extractor(...)` in `extractor.py` and select it via the `extractor`
query parameter in the web API.

## Files

- `sbi_cc_extractor.py` — the SBI-specific parsing logic, usable as a library or CLI
- `extractor.py` — extractor registry and public entry points for multiple parsers
- `app.py` — Flask app (web UI + REST API)
- `index.html` — upload form and results view
- `requirements.txt` — dependencies
