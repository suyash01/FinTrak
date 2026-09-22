import io
import sys
import unittest
from pathlib import Path
from unittest import mock

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "src"))

from statement_parser.app import app
from statement_parser.extractor import PageLimitExceeded, PdfPasswordRequired

DEFAULT_MAX_CONTENT_LENGTH = 20 * 1024 * 1024  # 20 MB


def _pdf_upload(filename="stmt.pdf", content=b"%PDF-1.4 fake"):
    return {"file": (io.BytesIO(content), filename)}


class AppTests(unittest.TestCase):
    def setUp(self):
        app.config["TESTING"] = True
        self.client = app.test_client()

    def tearDown(self):
        app.config["MAX_CONTENT_LENGTH"] = DEFAULT_MAX_CONTENT_LENGTH

    def test_index_renders_html(self):
        resp = self.client.get("/")
        self.assertEqual(resp.status_code, 200)
        self.assertIn("text/html", resp.content_type)
        self.assertIn(b"<html", resp.data.lower())

    def test_health_returns_ok(self):
        resp = self.client.get("/health")
        self.assertEqual(resp.status_code, 200)
        self.assertEqual(resp.get_json(), {"status": "ok"})

    def test_api_extractors_lists_registered(self):
        resp = self.client.get("/api/extractors")
        self.assertEqual(resp.status_code, 200)
        data = resp.get_json()
        names = [e["name"] for e in data["extractors"]]
        self.assertIn("sbi_cc", names)
        self.assertIn("icici_cc", names)

    def test_api_extract_missing_file(self):
        resp = self.client.post("/api/extract", data={})
        self.assertEqual(resp.status_code, 400)
        self.assertIn("No file provided", resp.get_json()["error"])

    def test_api_extract_empty_filename(self):
        resp = self.client.post(
            "/api/extract",
            data=_pdf_upload(filename=""),
            content_type="multipart/form-data",
        )
        self.assertEqual(resp.status_code, 400)
        self.assertIn("No file selected", resp.get_json()["error"])

    def test_api_extract_non_pdf(self):
        resp = self.client.post(
            "/api/extract",
            data=_pdf_upload(filename="notes.txt"),
            content_type="multipart/form-data",
        )
        self.assertEqual(resp.status_code, 400)
        self.assertIn("Only PDF files", resp.get_json()["error"])

    @mock.patch("statement_parser.app.extract_transactions")
    def test_api_extract_json_success(self, mock_extract):
        mock_extract.return_value = {
            "transactions": [
                {"date": "18 May 26", "description": "UPI", "amount": 310.0, "type": "Credit"}
            ],
            "summary": {},
            "page_count": 1,
            "transaction_count": 1,
        }
        resp = self.client.post(
            "/api/extract",
            data=_pdf_upload(),
            content_type="multipart/form-data",
        )
        self.assertEqual(resp.status_code, 200)
        data = resp.get_json()
        self.assertEqual(data["transaction_count"], 1)
        mock_extract.assert_called_once()
        _, kwargs = mock_extract.call_args
        self.assertIsNone(kwargs.get("password"))
        self.assertEqual(kwargs.get("extractor_name"), "sbi_cc")

    @mock.patch("statement_parser.app.extract_transactions")
    def test_api_extract_csv_success(self, mock_extract):
        mock_extract.return_value = {
            "transactions": [
                {"date": "18 May 26", "description": "UPI", "amount": 310.0, "type": "Credit"}
            ],
            "summary": {},
            "page_count": 1,
            "transaction_count": 1,
        }
        resp = self.client.post(
            "/api/extract?format=csv",
            data=_pdf_upload(),
            content_type="multipart/form-data",
        )
        self.assertEqual(resp.status_code, 200)
        self.assertEqual(resp.mimetype, "text/csv")
        self.assertIn(b"date,description,amount,type", resp.data)

    @mock.patch("statement_parser.app.extract_transactions")
    def test_api_extract_password_required(self, mock_extract):
        mock_extract.side_effect = PdfPasswordRequired(
            "This PDF is password-protected. Please supply the password."
        )
        resp = self.client.post(
            "/api/extract",
            data=_pdf_upload(),
            content_type="multipart/form-data",
        )
        self.assertEqual(resp.status_code, 401)
        data = resp.get_json()
        self.assertTrue(data["password_required"])
        self.assertIn("password", data["error"].lower())

    @mock.patch("statement_parser.app.extract_transactions")
    def test_api_extract_unsupported_extractor(self, mock_extract):
        mock_extract.side_effect = KeyError("Unsupported extractor 'nope'")
        resp = self.client.post(
            "/api/extract?extractor=nope",
            data=_pdf_upload(),
            content_type="multipart/form-data",
        )
        self.assertEqual(resp.status_code, 400)
        self.assertIn("Unsupported extractor", resp.get_json()["error"])

    @mock.patch("statement_parser.app.extract_transactions")
    def test_api_extract_generic_error(self, mock_extract):
        mock_extract.side_effect = RuntimeError("boom")
        resp = self.client.post(
            "/api/extract",
            data=_pdf_upload(),
            content_type="multipart/form-data",
        )
        self.assertEqual(resp.status_code, 422)
        self.assertEqual(resp.get_json()["error"], "Failed to process the statement")
        self.assertNotIn("boom", resp.get_json()["error"])

    @mock.patch("statement_parser.app.extract_transactions")
    def test_api_extract_empty_result_is_an_error(self, mock_extract):
        # Nothing about the file was recognised (no metadata, no summary): an
        # image-only PDF, a mis-selected extractor and a drifted template all
        # land here. Reporting that as a clean empty import hid the failure
        # from the user, so it is a 422.
        mock_extract.return_value = {
            "transactions": [],
            "summary": {},
            "page_count": 1,
            "transaction_count": 0,
        }
        resp = self.client.post(
            "/api/extract",
            data=_pdf_upload(),
            content_type="multipart/form-data",
        )
        self.assertEqual(resp.status_code, 422)
        self.assertIn("No transactions found", resp.get_json()["error"])

    @mock.patch("statement_parser.app.extract_transactions")
    def test_api_extract_empty_csv_result_is_an_error(self, mock_extract):
        mock_extract.return_value = {
            "transactions": [],
            "summary": {},
            "page_count": 1,
            "transaction_count": 0,
        }
        resp = self.client.post(
            "/api/extract?format=csv",
            data=_pdf_upload(),
            content_type="multipart/form-data",
        )
        self.assertEqual(resp.status_code, 422)

    @mock.patch("statement_parser.app.extract_transactions")
    def test_api_extract_recognised_but_empty_statement_is_a_zero_row_success(
        self, mock_extract
    ):
        # A quiet period parses to zero importable rows (the bank extractors
        # drop the zero-amount Brought/Carried Forward rows) even though the
        # metadata and printed summary prove the template WAS matched. That is
        # a 0-row import, not a parse failure: the old guard answered 422 and
        # named a scanned PDF/wrong extractor, both false for this file.
        mock_extract.return_value = {
            "transactions": [],
            "summary": {"opening_balance": "15843.32", "closing_balance": "15843.32"},
            "bank": "IndusInd Bank",
            "account_holder": "SUYASH MITTAL",
            "account_number": "157044793121",
            "statement_period_from": "2023-06-01",
            "statement_period_to": "2023-06-30",
            "opening_balance": 15843.32,
            "closing_balance": 15843.32,
            "validation_errors": [],
            "page_count": 2,
            "transaction_count": 0,
        }
        resp = self.client.post(
            "/api/extract?extractor=indusind_bank",
            data=_pdf_upload(),
            content_type="multipart/form-data",
        )
        self.assertEqual(resp.status_code, 200)
        data = resp.get_json()
        self.assertEqual(data["transactions"], [])
        self.assertEqual(data["transaction_count"], 0)
        self.assertEqual(data["account_holder"], "SUYASH MITTAL")
        self.assertIn("no importable transactions", data["validation_errors"][0])

    @mock.patch("statement_parser.app.extract_transactions")
    def test_api_extract_recognised_empty_statement_csv_is_a_header_only_success(
        self, mock_extract
    ):
        mock_extract.return_value = {
            "transactions": [],
            "summary": {"total_amount_due": "40,991.00"},
            "page_count": 3,
            "transaction_count": 0,
        }
        resp = self.client.post(
            "/api/extract?format=csv",
            data=_pdf_upload(),
            content_type="multipart/form-data",
        )
        self.assertEqual(resp.status_code, 200)
        self.assertEqual(resp.mimetype, "text/csv")
        self.assertEqual(resp.data.decode("utf-8").strip(), "date,description,amount,type")

    @mock.patch("statement_parser.app.extract_transactions")
    def test_api_extract_empty_balances_alone_are_not_recognition(self, mock_extract):
        # Balances are only ever set from parsed rows, but the guard must not
        # read a bare 0.0/None as proof of a recognised template: a result with
        # no metadata, no accounts and no summary stays a 422.
        mock_extract.return_value = {
            "transactions": [],
            "summary": {},
            "accounts": [],
            "account_holder": None,
            "opening_balance": 0.0,
            "closing_balance": 0.0,
            "page_count": 1,
            "transaction_count": 0,
        }
        resp = self.client.post(
            "/api/extract",
            data=_pdf_upload(),
            content_type="multipart/form-data",
        )
        self.assertEqual(resp.status_code, 422)

    @mock.patch("statement_parser.app.extract_transactions")
    def test_api_extract_bank_name_alone_is_not_recognition(self, mock_extract):
        # A bank extractor names itself ("IndusInd Bank") even when it matched
        # nothing, so an image-only PDF must stay an error rather than turning
        # into a plausible-looking 0-row import.
        mock_extract.return_value = {
            "transactions": [],
            "summary": {},
            "bank": "IndusInd Bank",
            "statement_type": "savings_account",
            "accounts": [],
            "account_holder": None,
            "customer_id": None,
            "statement_period_from": None,
            "statement_period_to": None,
            "opening_balance": None,
            "closing_balance": None,
            "validation_errors": [],
            "page_count": 4,
            "transaction_count": 0,
        }
        resp = self.client.post(
            "/api/extract?extractor=indusind_bank",
            data=_pdf_upload(),
            content_type="multipart/form-data",
        )
        self.assertEqual(resp.status_code, 422)

    @mock.patch("statement_parser.app.extract_transactions")
    def test_api_extract_a_recognised_account_block_is_enough(self, mock_extract):
        # icici_bank fills `accounts` from the statement's own account block, so
        # that alone is evidence the template matched even with no rows.
        mock_extract.return_value = {
            "transactions": [],
            "summary": {},
            "bank": "ICICI Bank",
            "statement_type": "savings_account",
            "accounts": [
                {"type": "Savings", "number": "000405001234", "balance": 194497.61}
            ],
            "validation_errors": [],
            "page_count": 1,
            "transaction_count": 0,
        }
        resp = self.client.post(
            "/api/extract?extractor=icici_bank",
            data=_pdf_upload(),
            content_type="multipart/form-data",
        )
        self.assertEqual(resp.status_code, 200)
        data = resp.get_json()
        self.assertEqual(data["transactions"], [])
        self.assertEqual(data["accounts"][0]["number"], "000405001234")

    @mock.patch("statement_parser.app.extract_transactions")
    def test_api_extract_page_limit(self, mock_extract):
        mock_extract.side_effect = PageLimitExceeded(
            "statement has 600 pages, exceeding the 500-page limit"
        )
        resp = self.client.post(
            "/api/extract",
            data=_pdf_upload(),
            content_type="multipart/form-data",
        )
        self.assertEqual(resp.status_code, 422)
        self.assertIn("exceeding", resp.get_json()["error"])

    def test_too_large_returns_413(self):
        app.config["MAX_CONTENT_LENGTH"] = 1024  # shrink limit for the test
        resp = self.client.post(
            "/api/extract",
            data=_pdf_upload(content=b"x" * 2048),
            content_type="multipart/form-data",
        )
        self.assertEqual(resp.status_code, 413)
        self.assertIn("File too large", resp.get_json()["error"])


if __name__ == "__main__":
    unittest.main()