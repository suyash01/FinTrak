# FinTrak Statement Parser

The parser is a standalone, deliberately unauthenticated Flask service that
extracts transactions and statement metadata from supported bank and
credit-card PDFs. It is consumed by the FinTrak backend over HTTP.

> [!WARNING]
> This service parses attacker-controllable PDFs and must never be exposed to
> the public internet. Run it only on a private/internal network reachable by
> the FinTrak backend. The production Compose stack keeps it internal-only. A
> local process should bind to loopback; only a deliberately isolated container
> may bind to `0.0.0.0` on its private network.

## Supported extractors

The registry currently exposes five extractors:

| Name | Display name | Source format |
|---|---|---|
| `sbi_cc` | SBI Credit Card | SBI credit-card monthly statements |
| `icici_cc` | ICICI Credit Card | ICICI credit-card statements |
| `icici_bank` | ICICI Bank Statement | ICICI savings/current-account statements |
| `slice_bank` | Slice Small Finance Bank | Slice savings-account statements |
| `indusind_bank` | IndusInd Bank Statement | IndusInd savings/3-in-1 statements |

`sbi_cc` is the default only when the request omits `extractor`. The supported
list is returned by `GET /api/extractors`; clients should not hard-code it.

Credit-card extractors return a small envelope with `transactions`, `summary`,
`page_count`, and `transaction_count`. Bank extractors additionally return
statement metadata such as account details, balances, period, and validation
warnings. The normalized transaction fields used by the FinTrak import flow
are `date`, `description`, `amount`, and `type` (`Credit` or `Debit`).

## Setup

Requires [uv](https://docs.astral.sh/uv/):

```bash
uv sync --frozen
```

## Run the service

For local development, the Flask development server binds to loopback by
default:

```bash
uv run python -m statement_parser
```

For production, use gunicorn. The image runs two workers with a 120-second
worker timeout:

```bash
uv run gunicorn -b 127.0.0.1:5000 statement_parser.app:app
```

The production image runs as the non-root `appuser` and exposes the health
check at `GET /health`.

Uploads are capped at 20 MB. Each extractor also enforces a page limit; the
default is 500 pages and can be changed with `MAX_PAGES`. Invalid or
non-positive values fall back to the default. The page limit is checked before
the full PDF page tree is materialized, which bounds decompression-bomb work.

The backend has its own four-slot parse semaphore. It fails fast with `429`
when saturated rather than queueing unbounded PDF work. Manual and Paperless
statement parsing share this backend limit.

## REST API

All responses that fail use a JSON object containing an `error` string. The
service has no authentication of its own; the network boundary is the security
control.

### `GET /health`

Liveness response used by the Docker health check:

```json
{"status": "ok"}
```

### `GET /api/extractors`

Lists registered extractors sorted by name:

```bash
curl http://localhost:5000/api/extractors
```

```json
{
  "extractors": [
    {"name": "icici_bank", "display_name": "ICICI Bank Statement"},
    {"name": "sbi_cc", "display_name": "SBI Credit Card"}
  ]
}
```

### `POST /api/extract`

Upload a statement PDF as multipart form field `file`.

Optional form fields:

- `password` — password for an encrypted PDF
- `date_format` — compatibility hint currently ignored by the service

Query parameters:

- `extractor` — registered extractor name; defaults to `sbi_cc`
- `format` — `json` (default) or `csv`

Example:

```bash
curl -F "file=@statement.pdf" -F "password=1234" \
  "http://localhost:5000/api/extract?extractor=sbi_cc"
```

JSON response example:

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
    "credit_limit": "2,29,000.00"
  },
  "page_count": 7,
  "transaction_count": 1
}
```

Bank extractors may additionally return fields such as `bank`,
`statement_type`, `account_holder`, `accounts`, `opening_balance`,
`closing_balance`, and `validation_errors`.

CSV columns are extractor-specific:

- `sbi_cc`, `slice_bank`, and `indusind_bank`: `date, description, amount, type`
- `icici_cc`: `date, ser_no, description, reward_points, amount, type, card_number`
- `icici_bank`: `date, mode, particulars, deposit, withdrawal, balance, account_number`

Text cells are neutralized before CSV output so spreadsheet applications treat
statement values as literal text instead of formulas.

### Error behavior

- `400` — missing file, empty filename, non-PDF filename, or unknown extractor
- `401` — encrypted PDF without the correct password
- `413` — upload exceeds 20 MB
- `422` — too many pages, processing failure, or no recognizable statement
- `200` with an empty transaction list — a recognized statement with no
  importable rows; the reason is reported in `validation_errors`

The parser does not validate PDF magic bytes itself. The backend validates the
filename extension and the parser handles content parsing.

## CLI

The package entry point starts the Flask development service:

```bash
uv run python -m statement_parser
```

Individual extractor modules may also expose module-specific CLIs. For example,
the SBI extractor can be run directly:

```bash
uv run python -m statement_parser.sbi_cc_extractor statement.pdf \
  --password 1234 --out transactions.csv
```

Without `--out`, that CLI prints JSON to stdout. New issuer support should be
implemented as a registered extractor rather than by changing the backend flow.

## Development and tests

Run the parser tests:

```bash
cd statement_parser
uv run python -m unittest discover -s tests -v
```

Run the enforced coverage profile:

```bash
uv run --frozen coverage run --source=statement_parser \
  -m unittest discover -s tests
uv run --frozen coverage report --fail-under=90
```

The parser OpenAPI contract is `openapi.yaml` in this directory.

## Files

- `app.py` — Flask web UI and REST API
- `extractor.py` — registry and public extraction entry points
- `sbi_cc_extractor.py` — SBI credit-card extractor
- `icici_cc_extractor.py` — ICICI credit-card extractor
- `icici_bank_extractor.py` — ICICI bank-account extractor
- `slice_bank_extractor.py` — Slice bank-account extractor
- `indusind_bank_extractor.py` — IndusInd bank-account extractor
- `limits.py` — page and PDF resource limits
- `money.py` — cent-accurate validation comparisons
- `text.py` — CSV text safety helpers
- `openapi.yaml` — standalone service contract
- `pyproject.toml` / `uv.lock` — dependencies managed with uv
