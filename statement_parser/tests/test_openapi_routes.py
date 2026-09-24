import re
import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "src"))

from statement_parser.app import app


OPENAPI = Path(__file__).resolve().parents[1] / "openapi.yaml"
DOCUMENTED_PATH = re.compile(r"^  (/[^:]*):\s*$", re.MULTILINE)
DOCUMENTED_METHOD = re.compile(r"^    (get|post|put|patch|delete):\s*$", re.MULTILINE)


def documented_routes() -> dict[str, set[str]]:
    text = OPENAPI.read_text(encoding="utf-8")
    matches = list(DOCUMENTED_PATH.finditer(text))
    routes: dict[str, set[str]] = {}
    for index, match in enumerate(matches):
        end = matches[index + 1].start() if index + 1 < len(matches) else len(text)
        routes[match.group(1)] = {
            method.upper() for method in DOCUMENTED_METHOD.findall(text[match.end():end])
        }
    return routes


class OpenAPIRouteTests(unittest.TestCase):
    def test_openapi_documents_every_service_route(self):
        documented = documented_routes()
        registered = {
            rule.rule: rule.methods - {"HEAD", "OPTIONS"}
            for rule in app.url_map.iter_rules()
            if rule.endpoint != "static"
        }
        self.assertEqual(set(documented), set(registered))

    def test_openapi_documents_every_service_method(self):
        documented = documented_routes()
        registered = {
            rule.rule: rule.methods - {"HEAD", "OPTIONS"}
            for rule in app.url_map.iter_rules()
            if rule.endpoint != "static"
        }
        self.assertEqual(registered, documented)


if __name__ == "__main__":
    unittest.main()
