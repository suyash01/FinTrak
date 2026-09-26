import { existsSync, readFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { describe, expect, it } from "vitest";
import { parseQuery, FIELD_PLAIN } from "./parse";

/**
 * locateCorpus walks up from the working directory to find the shared corpus.
 * A relative path from the test file would need import.meta.url, which under
 * jsdom is an http URL rather than a file URL, and a fixed relative path would
 * break if vitest is invoked from the repo root instead of frontend/.
 */
function locateCorpus(): string {
  const relative = join("backend", "internal", "query", "testdata", "corpus.json");
  let dir = process.cwd();
  for (let i = 0; i < 8; i++) {
    const candidate = resolve(dir, relative);
    if (existsSync(candidate)) return candidate;
    const parent = dirname(dir);
    if (parent === dir) break;
    dir = parent;
  }
  throw new Error(`could not find ${relative} above ${process.cwd()}`);
}

// The same file the Go parser tests read. A field or operator added on one side
// without the other fails both suites, which is the whole point of keeping the
// grammar in one JSON file rather than in two implementations' heads.
const CORPUS = JSON.parse(readFileSync(locateCorpus(), "utf-8")) as { cases: CorpusCase[] };

interface CorpusTerm {
  field: string;
  op: string;
  values: string[];
  negated: boolean;
  position: number;
}

interface CorpusCase {
  name: string;
  surface: string;
  canonical: string;
  terms: CorpusTerm[];
  diagnostics: Array<{ term: string; code: string; message: string; position: number }>;
}

describe("parseQuery corpus contract", () => {
  it("has a non-empty corpus to check against", () => {
    expect(CORPUS.cases.length).toBeGreaterThan(0);
  });

  for (const tc of CORPUS.cases) {
    it(`matches the corpus: ${tc.name}`, () => {
      const got = parseQuery(tc.surface);
      expect(
        got.terms.map((t) => ({
          field: t.field,
          op: t.op,
          values: t.values,
          negated: t.negated,
          position: t.position,
        })),
      ).toEqual(tc.terms);
      // Diagnostics are compared on term, code and position but never on
      // message: the Go and TypeScript validators are free to word a message
      // differently, and comparing prose would couple them on cosmetics.
      expect(got.diagnostics.map((d) => ({ term: d.term, code: d.code, position: d.position }))).toEqual(
        tc.diagnostics.map((d) => ({ term: d.term, code: d.code, position: d.position })),
      );
    });
  }
});

describe("parseQuery in isolation", () => {
  it("synthesises the plain pseudo-field for a bare word", () => {
    const { terms, diagnostics } = parseQuery("coffee");
    expect(diagnostics).toEqual([]);
    expect(terms[0].field).toBe(FIELD_PLAIN);
    expect(terms[0].op).toBe("~");
  });

  it("keeps a not prefix inside the term, so the UI can highlight it", () => {
    const { terms } = parseQuery("not cat:11111111-1111-4111-8111-111111111111");
    expect(terms[0].negated).toBe(true);
    expect(terms[0].position).toBe(0);
  });

  it("rejects the plain pseudo-field as user input", () => {
    const { terms, diagnostics } = parseQuery("plain:coffee");
    expect(terms).toEqual([]);
    expect(diagnostics[0].code).toBe("unknown_field");
  });

  it("refuses a tag containing a quote, which cannot be a SQL literal", () => {
    const { terms, diagnostics } = parseQuery("tag:it's");
    expect(terms).toEqual([]);
    expect(diagnostics[0].code).toBe("unresolved_value");
  });

  it("never throws on punctuation it cannot start a token with", () => {
    expect(() => parseQuery('"""')).not.toThrow();
    expect(() => parseQuery(",,,")).not.toThrow();
    expect(() => parseQuery("<>=")).not.toThrow();
  });

  it("caps the term count and says so", () => {
    const { terms, diagnostics } = parseQuery(Array.from({ length: 40 }, () => "desc:a").join(" "));
    expect(terms).toHaveLength(32);
    expect(diagnostics.some((d) => d.code === "too_long")).toBe(true);
  });

  it("truncates an overlong query and says so", () => {
    const { diagnostics } = parseQuery("a".repeat(2100));
    expect(diagnostics).toHaveLength(1);
    expect(diagnostics[0].code).toBe("too_long");
  });

  it("returns nothing for an empty query, with no diagnostics", () => {
    expect(parseQuery("")).toEqual({ terms: [], diagnostics: [] });
    expect(parseQuery("   ")).toEqual({ terms: [], diagnostics: [] });
  });
});
