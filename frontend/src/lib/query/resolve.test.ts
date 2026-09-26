import { describe, expect, it } from "vitest";
import { parseQuery } from "./parse";
import { resolveQuery, serializeQuery, toMinorUnits, type ResolveSource } from "./resolve";

// Two categories deliberately share the name "Groceries" in different groups,
// because that is the case the resolver has to handle without either silently
// picking one or silently widening to every payee.
const src: ResolveSource = {
  accounts: [{ id: "11111111-1111-4111-8111-111111111111", name: "Checking" }] as ResolveSource["accounts"],
  categories: [
    { id: "22222222-2222-4222-8222-222222222222", name: "Groceries", groupId: "33333333-3333-4333-8333-333333333333" },
    { id: "44444444-4444-4444-8444-444444444444", name: "Groceries", groupId: "55555555-5555-4555-8555-555555555555" },
    { id: "66666666-6666-4666-8666-666666666666", name: "Rent", groupId: "55555555-5555-4555-8555-555555555555" },
  ] as ResolveSource["categories"],
  groups: [
    { id: "33333333-3333-4333-8333-333333333333", name: "Food" },
    { id: "55555555-5555-4555-8555-555555555555", name: "Home" },
  ] as ResolveSource["groups"],
  payees: [{ id: "77777777-7777-4777-8777-777777777777", name: "Whole Foods" }] as ResolveSource["payees"],
  tags: ["vacation", "food"],
};

const run = (q: string) => resolveQuery(parseQuery(q), src);

describe("resolveQuery", () => {
  it("resolves a group-qualified category name to one id", () => {
    const { terms, diagnostics } = run("cat:Food/Groceries");
    expect(diagnostics).toEqual([]);
    expect(terms[0].values).toEqual(["22222222-2222-4222-8222-222222222222"]);
  });

  it("resolves an account and a quoted payee", () => {
    const { terms, diagnostics } = run('acct:Checking payee:"Whole Foods"');
    expect(diagnostics).toEqual([]);
    expect(serializeQuery(terms)).toBe(
      "acct:11111111-1111-4111-8111-111111111111 payee:77777777-7777-4777-8777-777777777777",
    );
  });

  it("leaves an id alone rather than trying to resolve it", () => {
    const { terms, diagnostics } = run("cat:22222222-2222-4222-8222-222222222222");
    expect(diagnostics).toEqual([]);
    expect(terms[0].values).toEqual(["22222222-2222-4222-8222-222222222222"]);
  });

  // Review Focus #4: an ambiguous name must still filter (as an OR) AND say so.
  // Not silently pick one, and not silently widen to everything.
  it("keeps both matches for an ambiguous name and reports the ambiguity", () => {
    const { terms, diagnostics } = run("cat:Groceries");
    expect(terms[0].values).toEqual([
      "22222222-2222-4222-8222-222222222222",
      "44444444-4444-4444-8444-444444444444",
    ]);
    expect(diagnostics).toHaveLength(1);
    expect(diagnostics[0].code).toBe("ambiguous_value");
    // The message names the value the user typed and how to disambiguate it.
    expect(diagnostics[0].message).toContain('"Groceries"');
    expect(diagnostics[0].message).toContain("cat:Group/Name");
  });

  it("drops a name that matches nothing and reports it, keeping the other terms", () => {
    const { terms, diagnostics } = run("payee:Costco amt>50");
    expect(terms.map((t) => t.field)).toEqual(["amt"]);
    expect(diagnostics).toHaveLength(1);
    expect(diagnostics[0].code).toBe("unresolved_value");
    expect(diagnostics[0].message).toContain("Costco");
  });

  it("leaves a bare word and a tag name alone", () => {
    const { terms, diagnostics } = run("coffee tag:vacation");
    expect(diagnostics).toEqual([]);
    expect(terms[0].field).toBe("plain");
    expect(terms[1].values).toEqual(["vacation"]);
  });

  it("converts a major-unit amount to minor units", () => {
    const { terms } = run("amt>50");
    expect(terms[0].values).toEqual(["5000"]);
  });

  it("resolves a named period into concrete date bounds", () => {
    const { terms, diagnostics } = run("date:last_12_months");
    expect(diagnostics).toEqual([]);
    expect(terms).toHaveLength(2);
    expect(terms[0].op).toBe(">=");
    expect(terms[1].op).toBe("<=");
    expect(serializeQuery(terms)).toMatch(/^date>=\d{4}-\d{2}-\d{2} date<=\d{4}-\d{2}-\d{2}$/);
  });

  it("treats an unknown period as a plain date, not as a name to resolve", () => {
    const { terms, diagnostics } = run("date:2026-01-01");
    expect(diagnostics).toEqual([]);
    expect(terms).toHaveLength(1);
    expect(terms[0].values).toEqual(["2026-01-01"]);
  });

  it("keeps the none sentinel rather than looking it up as a name", () => {
    const { terms, diagnostics } = run("cat:none payee:none");
    expect(diagnostics).toEqual([]);
    expect(terms.map((t) => t.values[0])).toEqual(["none", "none"]);
  });
});

describe("toMinorUnits", () => {
  // Money is never computed in a float here: 50.75 must be 5075 exactly.
  it("converts whole and fractional amounts exactly", () => {
    expect(toMinorUnits("50")).toBe("5000");
    expect(toMinorUnits("50.7")).toBe("5070");
    expect(toMinorUnits("50.75")).toBe("5075");
    expect(toMinorUnits("0.05")).toBe("5");
  });

  it("keeps a leading minus", () => {
    expect(toMinorUnits("-12.34")).toBe("-1234");
  });
});

describe("serializeQuery", () => {
  it("quotes a value containing a space and escapes the quote character", () => {
    expect(
      serializeQuery([{ field: "tag", op: "=", values: ['He said "hi"'], negated: false, position: 0, raw: "" }]),
    ).toBe('tag:"He said \\"hi\\""');
  });

  it("leaves a uuid unquoted", () => {
    expect(
      serializeQuery([{ field: "cat", op: "=", values: ["11111111-1111-4111-8111-111111111111"], negated: false, position: 0, raw: "" }]),
    ).toBe("cat:11111111-1111-4111-8111-111111111111");
  });

  it("writes an operator form as field<op>value and a not prefix", () => {
    expect(
      serializeQuery([{ field: "amt", op: ">", values: ["5000"], negated: true, position: 0, raw: "" }]),
    ).toBe("not amt>5000");
  });

  it("keeps a CSV as a CSV", () => {
    expect(
      serializeQuery([{ field: "cat", op: "=", values: ["a", "b"], negated: false, position: 0, raw: "" }]),
    ).toBe("cat:a,b");
  });

  it("renders a bare word as itself, so plain search round-trips", () => {
    expect(
      serializeQuery([{ field: "plain", op: "~", values: ["coffee", "shop"], negated: false, position: 0, raw: "" }]),
    ).toBe("coffee shop");
  });

  it("round-trips: serialize then parse gives the same terms back", () => {
    for (const q of ["coffee", "cat:none", "amt>5000", "tag:vacation", 'tag:"Whole Foods"']) {
      const { terms, diagnostics } = resolveQuery(parseQuery(q), src);
      expect(diagnostics, q).toEqual([]);
      const again = parseQuery(serializeQuery(terms));
      expect(again.diagnostics, q).toEqual([]);
      expect(again.terms.map((t) => [t.field, t.op, t.values, t.negated]), q).toEqual(
        terms.map((t) => [t.field, t.op, t.values, t.negated]),
      );
    }
  });
});
