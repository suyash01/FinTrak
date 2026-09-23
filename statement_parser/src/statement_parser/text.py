"""Text helpers shared by the statement extractors."""

#: Leading characters that make a spreadsheet treat a cell as a formula or DDE
#: command instead of literal text when the CSV is opened.
_FORMULA_PREFIXES = ("=", "+", "-", "@", "\t", "\r")


def csv_cell(value: str) -> str:
    """Return `value` made safe for a spreadsheet CSV cell.

    A leading `=`, `+`, `-`, `@`, tab or carriage return makes Excel,
    LibreOffice and Google Sheets evaluate the cell as a formula, so a crafted
    transaction description (from an attacker-influenced PDF) could execute or
    exfiltrate data when the downloaded CSV is opened. Prefixing a single quote
    forces literal text; the quote is not shown in the spreadsheet.
    """
    if value and value.startswith(_FORMULA_PREFIXES):
        return "'" + value
    return value
