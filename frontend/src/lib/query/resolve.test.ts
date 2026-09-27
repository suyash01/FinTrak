import { describe, expect, it } from "vitest";
import { parseQuery } from "./parse";
import { resolveQuery, serializeQuery, type ResolveSource } from "./resolve";

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
  // Two payees deliberately share the name "Whole Foods", so the ambiguity
  // advice can be checked on a field that does NOT understand the Group/Name
  // spelling. "Corner Store" is unique, for the tests that want a clean resolve.
  payees: [
    { id: "77777777-7777-4777-8777-777777777777", name: "Whole Foods" },
    { id: "88888888-8888-4888-8888-888888888888", name: "Whole Foods" },
    { id: "99999999-9999-4999-8999-999999999999", name: "Corner Store" },
  ] as ResolveSource["payees"],
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
    const { terms, diagnostics } = run('acct:Checking payee:"Corner Store"');
    expect(diagnostics).toEqual([]);
    expect(serializeQuery(terms)).toBe(
      "acct:11111111-1111-4111-8111-111111111111 payee:99999999-9999-4999-8999-999999999999",
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

  // The advice must name a spelling the field actually resolves. Only cat
  // understands Group/Name, so telling a payee to use it sent the user to a
  // string that resolves to nothing.
  it("does not offer Group/Name for a field that cannot resolve it", () => {
    const { diagnostics } = run('payee:"Whole Foods"');
    expect(diagnostics).toHaveLength(1);
    expect(diagnostics[0].code).toBe("ambiguous_value");
    expect(diagnostics[0].message).not.toContain("Group/Name");
    expect(diagnostics[0].message).toContain("Use the exact name");
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

  // The wire carries MAJOR units, like the JSON boundary everywhere else in this
  // API, and the server does the single major->minor conversion. Converting here
  // as well made every amount filter 100x too large: `amt>50` filtered above
  // $5,000. The server's own curl path was always right, so only the web app
  // was wrong and no test noticed.
  it("sends an amount in major units, unconverted", () => {
    const { terms, diagnostics } = run("amt>50");
    expect(diagnostics).toEqual([]);
    expect(terms[0].values).toEqual(["50"]);
    expect(serializeQuery(terms)).toBe("amt>50");
  });

  it("sends a fractional amount unconverted too", () => {
    const { terms } = run("amt>=50.75");
    expect(terms[0].values).toEqual(["50.75"]);
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

  // A ccy term is dropped by resolveQuery unless this switch has a case for the
  // currency kind, and the drop is SILENT: no diagnostic, an empty term list,
  // and therefore q="" on the wire. The user asked for one currency, got the
  // whole unfiltered ledger, and was told nothing. So this asserts all three of
  // what "handled" means - the term survives, its values are untouched, and
  // nothing was reported against it.
  it("passes a currency through untouched, with no diagnostic", () => {
    const { terms, diagnostics } = run("ccy:usd");
    expect(diagnostics).toEqual([]);
    expect(terms).toHaveLength(1);
    expect(terms[0].field).toBe("ccy");
    expect(terms[0].values).toEqual(["usd"]);
    // The case is not folded here: the server folds it when it binds, so the
    // wire carries what the user typed.
    expect(serializeQuery(terms)).toBe("ccy:usd");
  });

  it("keeps a currency CSV and a negated currency as resolvable terms", () => {
    const csv = run("ccy:usd,eur");
    expect(csv.diagnostics).toEqual([]);
    expect(csv.terms).toHaveLength(1);
    expect(csv.terms[0].values).toEqual(["usd", "eur"]);
    expect(serializeQuery(csv.terms)).toBe("ccy:usd,eur");

    const negated = run("not ccy:usd");
    expect(negated.diagnostics).toEqual([]);
    expect(negated.terms).toHaveLength(1);
    expect(negated.terms[0].negated).toBe(true);
    expect(serializeQuery(negated.terms)).toBe("not ccy:usd");
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
      serializeQuery([{ field: "amt", op: ">", values: ["50"], negated: true, position: 0, raw: "" }]),
    ).toBe("not amt>50");
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
    for (const q of ["coffee", "cat:none", "amt>50", "tag:vacation", 'tag:"Whole Foods"', "ccy:usd"]) {
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
