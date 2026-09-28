import { describe, it, expect } from "vitest";
import { act, renderHook } from "@testing-library/react";
import {
  formatScoped,
  formatScopedMulti,
  formatOne,
  formatAxis,
  signOf,
  signClass,
  sumPerCurrency,
  useAccountCurrency,
  useCurrencyScope,
} from "./currency";
import type { Account, CurrencyScope, ScopedAccount } from "@/types";

function scopedAccount(overrides: Partial<ScopedAccount> = {}): ScopedAccount {
  return {
    id: "a1",
    name: "Checking",
    currency: "INR",
    income: { INR: 5000 },
    expense: { INR: 2000 },
    ...overrides,
  };
}

function scope(overrides: Partial<CurrencyScope> = {}): CurrencyScope {
  return {
    currencies: ["INR"],
    accounts: [scopedAccount()],
    ...overrides,
  };
}

describe("formatScoped", () => {
  it("renders the selected currency with its own code", () => {
    expect(formatScoped({ INR: 5000 }, "INR")).toContain("5,000");
    expect(formatScoped({ INR: 5000 }, "INR")).toContain("INR");
  });

  it("reads a missing key as zero rather than as an error", () => {
    // A foreign account that only ever spends: its currency has no income key.
    expect(formatScoped({ USD: -80 }, "USD")).not.toBe("");
    expect(formatScoped({}, "USD", "no transactions")).toBe("no transactions");
  });

  it("never adds two currencies together", () => {
    const out = formatScopedMulti({ INR: 5000, USD: 120 });
    expect(out).toContain("INR");
    expect(out).toContain("USD");
    expect(out.replace(/[^0-9]/g, "")).not.toContain("5120");
  });

  it("degrades instead of throwing on a code Intl does not know", () => {
    // A user can set an account's currency to anything three letters long, and
    // Intl.NumberFormat throws RangeError on a code it cannot resolve. That
    // would take the whole dashboard down over a display detail.
    expect(() => formatScoped({ XYZ: 1234 }, "XYZ")).not.toThrow();
    expect(formatScoped({ XYZ: 1234 }, "XYZ")).toContain("1,234");
  });

  it("degrades instead of throwing on an empty code", () => {
    // accounts.currency is nullable, so a "" key is representable. Intl
    // rejects it too.
    expect(() => formatScoped({ "": 1234 }, "")).not.toThrow();
  });

  it("renders the selected currency's own share of a mixed map", () => {
    const amounts = { INR: 5000, USD: 120 };
    expect(formatScoped(amounts, "INR")).toContain("5,000.00");
    expect(formatScoped(amounts, "USD")).toContain("120.00");
    // The other currency must not leak into the rendered figure.
    expect(formatScoped(amounts, "INR")).not.toContain("120.00");
  });
  it("falls back to the given text when the map itself is absent", () => {
    expect(formatScoped(undefined, "INR", "no transactions")).toBe(
      "no transactions",
    );
    // A real zero is the caller's to claim, so the seam stays: pass "0.00" where
    // nothing in the response at all really does mean zero.
    expect(formatScoped(undefined, "INR", "0.00")).toBe("0.00");
  });

  it("refuses rather than printing a bare zero for a currency it lacks", () => {
    // A bare "0.00" in a column of "INR 2,000.00" cannot be told apart from a
    // genuine zero, which is how money that was left out reads as money that was
    // not there.
    expect(formatScoped({ USD: 120 }, "INR")).toBe("no INR in this report");
    expect(formatScoped({}, "USD")).toBe("no USD in this report");
    // With no currency named there is nothing to miss.
    expect(formatScoped({ USD: 120 }, "")).toBe("no transactions");
    // A key that is present and zero is a figure, and is rendered as one.
    expect(formatScoped({ INR: 0 }, "INR")).toBe(formatOne(0, "INR"));
  });

  it("reads only its own keys, never the prototype chain's", () => {
    // `"toString" in {}` is true, so the `in` operator would hand back a
    // function here and the three readers of one map would disagree.
    expect(formatScoped({}, "toString")).toBe("no toString in this report");
    expect(signOf({}, "toString")).toBe("none");
  });
});

describe("formatAxis", () => {
  it("keeps the code and abbreviates the magnitude", () => {
    expect(formatAxis(50000, "INR")).toBe("INR 50k");
    expect(formatAxis(1500, "USD")).toBe("USD 1.5k");
    expect(formatAxis(-2500, "INR")).toBe("INR -2.5k");
    expect(formatAxis(500, "INR")).toBe("INR 500");
    expect(formatAxis(0, "INR")).toBe("INR 0");
  });

  it("stays short enough for a chart column and never names a symbol", () => {
    const tick = formatAxis(1234567, "INR");
    expect(tick).toBe("INR 1234.6k");
    expect(tick).not.toContain("₹");
    expect(tick.length).toBeLessThanOrEqual(12);
  });

  it("prints no code when the report has none on screen", () => {
    expect(formatAxis(50000, "")).toBe("50k");
  });
});

describe("formatScopedMulti", () => {
  it("says the value is not representable rather than picking one", () => {
    const out = formatScopedMulti({ INR: 5000, USD: 120 });
    expect(out).toContain("USD");
    expect(out).toContain("not combined");
  });

  it("renders a single currency without the refusal", () => {
    const out = formatScopedMulti({ INR: 5000 });
    expect(out).toContain("5,000.00");
    expect(out).not.toContain("not combined");
  });

  it("names an empty map rather than showing a zero of some currency", () => {
    expect(formatScopedMulti({})).toBe("no transactions");
    expect(formatScopedMulti(undefined)).toBe("no transactions");
  });
});

describe("formatOne", () => {
  // The two cases a user-typed code can be that are not "a currency Intl knows".
  // They were conflated here until a dashboard test showed "XYZ XYZ 7.00" on
  // screen, and the conflation hid the fact that the throw branch below was
  // never reached by any test in this file.
  it("does not name a code twice when Intl already prints it", () => {
    // Intl does not reject an unknown but well-formed code — it renders the code
    // itself, having neither a symbol nor an error. Prefixing on top of that is
    // how the code came to appear twice.
    //
    // The assertion counts occurrences rather than matching the whole string,
    // because Intl separates the code from the figure with a NON-BREAKING space
    // (U+00A0) that reads as an ordinary one in a diff and makes an exact
    // toBe() assertion a test of an invisible character. Counting the code is
    // also the more direct statement of the defect: once, not twice.
    const unknown = formatOne(1234, "XYZ");
    expect(unknown.match(/XYZ/g)).toHaveLength(1);
    expect(unknown).toMatch(/1,234\.00/);
  });

  it("does not double a code Intl upper-cases", () => {
    // Kept separate from the case above on purpose: two assertions in one `it`
    // means the first failure aborts the second, and a fix for one is then free
    // to regress the other unobserved.
    //
    // "Xyz" and not "usd": Intl upper-cases a code it renders literally, so
    // "Xyz" formats as "XYZ 1,234.00" and a case-sensitive "does the format
    // already name it" test reads that as un-named and prefixes it a second
    // time. "usd" cannot expose this at all — a code Intl *knows* is rendered as
    // its symbol, so "usd" formats as "$1,234.00" with the letters gone, and
    // there is nothing to double. That was the first example tried here, and it
    // passed against the unfixed code.
    const mixed = formatOne(1234, "Xyz");
    expect(mixed.match(/xyz/gi)).toHaveLength(1);
    expect(mixed).toMatch(/1,234\.00/);
  });

  it("still prefixes a code Intl resolved to a symbol", () => {
    // The other side of the same coin, and the one a "just return the format"
    // fix would break: a known code loses its letters to the symbol, so the
    // prefix is what names it.
    expect(formatOne(1234, "usd")).toBe("usd $1,234.00");
  });

  it("falls back to a grouped number when Intl rejects the code outright", () => {
    // Exported so MultiCurrencyNotice can render an account's contribution
    // through the same safe path, rather than calling formatCurrency directly
    // and throwing on a code the user typed into an account.
    //
    // "US" is the example that actually reaches the catch. The test this
    // replaces used "XYZ" and claimed Intl rejected it; it did not, and because
    // it only asserted toContain("1,234") it passed either way — so the branch
    // below was uncovered.
    expect(formatOne(1234, "US")).toBe("US 1,234.00");
    expect(formatOne(1234, "12")).toBe("12 1,234.00");
    // The empty code is a different path — an early return, not the catch — and
    // names nothing, because there is no code to name.
    expect(formatOne(1234, "")).toBe("1,234.00");
  });

  it("uses the currency's own symbol and code when Intl can resolve it", () => {
    expect(formatOne(1234, "INR")).toBe("INR ₹1,234.00");
    expect(formatOne(1234, "USD")).toContain("1,234.00");
    expect(formatOne(1234, "USD")).not.toContain("₹");
  });

  it("keeps the prefix for a symbol that only partly spells the code", () => {
    // JPY formats as "JP¥", which contains "JP" but not "JPY". The figure is
    // therefore still not named by its own format, and the prefix stays — the
    // conservative outcome, since "¥" alone is shared with CNY. Pinned because
    // it is the one case where "does the format contain the code" is a slightly
    // surprising test, and the natural next "simplification" would break it.
    expect(formatOne(1234, "JPY")).toBe("JPY JP¥1,234.00");
  });
});

describe("useAccountCurrency", () => {
  const account = (overrides: Partial<Account> = {}): Account =>
    ({
      id: "a1",
      name: "Checking",
      accountTypeId: "bank",
      bank: "",
      currency: "INR",
      color: "#000",
      isDefault: true,
      closed: false,
      balance: 0,
      ...overrides,
    }) as Account;

  const lookup = (accounts: Account[]) =>
    renderHook(() => useAccountCurrency(accounts)).result.current;

  it("resolves an account id to that account's own currency", () => {
    const code = lookup([
      account(),
      account({ id: "a2", currency: "USD" }),
      account({ id: "a3", currency: "EUR" }),
    ]);
    expect(code("a1")).toBe("INR");
    expect(code("a2")).toBe("USD");
    expect(code("a3")).toBe("EUR");
  });

  // The one that matters. "" reaches formatOne, which renders a plain grouped
  // number naming no currency. The alternative — a default here — is the defect
  // this helper exists to end, and it would be invisible: every caller would
  // render a confident figure in a currency nobody asked for.
  it("names no currency for an account it does not know", () => {
    const code = lookup([account()]);
    expect(code("a-missing")).toBe("");
    expect(code("")).toBe("");
    // And the consequence, end to end, so the two halves are pinned together.
    expect(formatOne(250, code("a-missing"))).not.toContain("₹");
  });

  // accounts.currency is nullable, so "" is representable for a real account and
  // must not be confused with "unknown". Both render as an unnamed number, and
  // the difference does not change the output — which is why this is a comment
  // worth having rather than a behaviour worth branching on.
  it("passes an account with no currency through as empty", () => {
    const code = lookup([account({ currency: "" })]);
    expect(code("a1")).toBe("");
  });

  it("sees an account added after the first render", () => {
    const first = [account()];
    const { result, rerender } = renderHook(
      ({ list }: { list: Account[] }) => useAccountCurrency(list),
      { initialProps: { list: first } },
    );
    expect(result.current("a2")).toBe("");

    // Reference data loads after the screen does, so a hook that captured the
    // map once would keep answering from the empty set and every figure would
    // silently lose its currency.
    rerender({ list: [...first, account({ id: "a2", currency: "USD" })] });
    expect(result.current("a2")).toBe("USD");
  });
});

describe("signOf", () => {
  it("reports the selected currency's own sign", () => {
    expect(signOf({ INR: -100 }, "INR")).toBe("negative");
    expect(signOf({ INR: 100 }, "INR")).toBe("positive");
  });

  it("claims no sign for a currency the map does not carry", () => {
    // A window that earned in USD and spent in INR: the INR day is a real
    // figure, but there is no INR income to read a surplus from. Reading this
    // as positive is how a mixed day is drawn as a green gain.
    expect(signOf({ USD: 500 }, "INR")).toBe("none");
    expect(signOf({}, "INR")).toBe("none");
  });

  it("claims no sign for a figure that is zero", () => {
    expect(signOf({ INR: 0 }, "INR")).toBe("none");
  });

  it("falls back to the map's own agreement when nothing is selected", () => {
    expect(signOf({ INR: -1, USD: -2 }, "")).toBe("negative");
    expect(signOf({ INR: 1, USD: 2 }, "")).toBe("positive");
    // Mixed, and a present zero: neither a deficit nor a surplus.
    expect(signOf({ INR: -1, USD: 2 }, "")).toBe("none");
    expect(signOf({ INR: 0, USD: 2 }, "")).toBe("none");
    expect(signOf({}, "")).toBe("none");
    expect(signOf(undefined, "")).toBe("none");
  });

  it("colours every sign with a semantic token and never a raw palette class", () => {
    expect(signClass.negative).toBe("text-destructive");
    expect(signClass.positive).toBe("text-chart-3");
    expect(signClass.none).toBe("text-muted-foreground");
  });
});

describe("sumPerCurrency", () => {
  it("adds within a currency and never across one", () => {
    const out = sumPerCurrency(
      { INR: 100, USD: 5 },
      { INR: 200 },
      { USD: 6 },
      undefined,
    );
    expect(out).toEqual({ INR: 300, USD: 11 });
  });

  it("invents no key and never returns a map that sums to a mixed total", () => {
    expect(sumPerCurrency({ INR: 100 }, {})).toEqual({ INR: 100 });
    expect(Object.keys(sumPerCurrency({}, { USD: 1 }))).toEqual(["USD"]);
  });
});

describe("useCurrencyScope", () => {
  const twoCurrencies = scope({
    currencies: ["USD", "INR"],
    accounts: [
      scopedAccount(),
      scopedAccount({
        id: "a2",
        name: "Dollars",
        currency: "USD",
        income: { USD: 120 },
        expense: { USD: 80 },
      }),
    ],
  });

  it("defaults to the first code in sorted order, not the server's order", () => {
    const { result } = renderHook(() => useCurrencyScope(twoCurrencies));
    expect(result.current.code).toBe("INR");
    expect(result.current.codes).toEqual(["INR", "USD"]);
  });

  it("reads the selected currency's share and never a neighbour's", () => {
    const { result } = renderHook(() => useCurrencyScope(twoCurrencies));
    act(() => result.current.setCode("USD"));
    expect(result.current.code).toBe("USD");
    expect(result.current.scoped({ INR: 5000, USD: 120 })).toBe(120);
    expect(result.current.scoped({ INR: 5000, USD: 120 })).not.toBe(5120);
  });

  it("reads an absent key as zero and an absent map as zero", () => {
    const { result } = renderHook(() => useCurrencyScope(twoCurrencies));
    expect(result.current.scoped({ USD: 120 })).toBe(0);
    expect(result.current.scoped(undefined)).toBe(0);
  });

  it("lists the accounts behind every currency it is not showing", () => {
    const { result } = renderHook(() => useCurrencyScope(twoCurrencies));
    expect(result.current.others.map((a) => a.name)).toEqual(["Dollars"]);
    act(() => result.current.setCode("USD"));
    expect(result.current.others.map((a) => a.name)).toEqual(["Checking"]);
  });

  it("drops a selection the next response no longer covers", () => {
    const { result, rerender } = renderHook(
      ({ s }: { s: CurrencyScope }) => useCurrencyScope(s),
      { initialProps: { s: twoCurrencies } },
    );
    act(() => result.current.setCode("USD"));
    rerender({ s: scope({ currencies: ["INR"] }) });
    expect(result.current.code).toBe("INR");
  });

  it("has nothing to select before a scope has loaded", () => {
    const { result } = renderHook(() => useCurrencyScope(undefined));
    expect(result.current.code).toBe("");
    expect(result.current.codes).toEqual([]);
    expect(result.current.others).toEqual([]);
    expect(result.current.scoped({ INR: 5000 })).toBe(0);
  });

  it("never offers or defaults to the empty code an account with no currency holds", () => {
    // "" sorts first, so leaving it in would make the whole report render as
    // unnamed numbers with a picker showing no matching item.
    const { result } = renderHook(() =>
      useCurrencyScope(
        scope({
          currencies: ["", "USD", "INR"],
          accounts: [
            scopedAccount({ id: "a0", name: "Unlabelled", currency: "" }),
            scopedAccount(),
            scopedAccount({
              id: "a2",
              name: "Dollars",
              currency: "USD",
              income: { USD: 120 },
              expense: { USD: 80 },
            }),
          ],
        }),
      ),
    );
    expect(result.current.codes).toEqual(["INR", "USD"]);
    expect(result.current.code).toBe("INR");
    // The nameless account is still disclosed, as one of the currencies left out.
    expect(result.current.others.map((a) => a.name)).toEqual([
      "Unlabelled",
      "Dollars",
    ]);
  });

  it("keeps only the nameless account out of the picker", () => {
    const { result } = renderHook(() =>
      useCurrencyScope(
        scope({
          currencies: [""],
          accounts: [scopedAccount({ currency: "" })],
        }),
      ),
    );
    expect(result.current.codes).toEqual([]);
    expect(result.current.code).toBe("");
  });
});
