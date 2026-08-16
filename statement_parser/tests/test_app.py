import io
import sys
import unittest
from pathlib import Path
from unittest import mock

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "src"))

from statement_parser.app import app
from statement_parser.extractor import PdfPasswordRequired

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
        self.assertIn("Failed to process PDF", resp.get_json()["error"])

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