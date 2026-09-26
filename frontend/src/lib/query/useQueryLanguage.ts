import { useCallback, useMemo, useState } from "react";
import { parseQuery, type QueryDiagnostic } from "./parse";
import { resolveQuery, serializeQuery, type ResolveSource } from "./resolve";

export interface QueryLanguage {
  /** What the user has typed, verbatim. */
  text: string;
  setText(next: string): void;
  /** The q= value to send, or "" when there is nothing to send. */
  q: string;
  /** Local (pre-request) plus server diagnostics, deduped. */
  diagnostics: QueryDiagnostic[];
  /**
   * The token the caret is sitting in at the end of the input, or null when the
   * caret is not in a trailing token. The input owns this, because only it knows
   * where the caret is.
   */
  inProgress: string | null;
  setInProgress(token: string | null): void;
  /** Called on submit, so a half-typed term becomes a real diagnostic. */
  finalize(): void;
  clear(): void;
  /** Called with the response's queryDiagnostics. */
  setServerDiagnostics(list: QueryDiagnostic[]): void;
}

/**
 * useQueryLanguage turns the text in the box into the q= value to send, plus the
 * diagnostics to show.
 *
 * The in-progress rule is the important part. Because matching is lenient, every
 * keystroke is a parse: without excluding the trailing token, typing `cat` would
 * report `ca` as an unknown field, then `cat` as valid, then `cat:` as needing a
 * value, and the warning banner would strobe while the user types. So the token
 * under a trailing caret is neither reported nor serialized, and pressing Enter
 * finalizes it — which is when `cat:` legitimately becomes an error.
 */
export function useQueryLanguage(opts: { source: ResolveSource; ownerQ: string }): QueryLanguage {
  const { source, ownerQ } = opts;
  const [text, setText] = useState("");
  const [inProgress, setInProgress] = useState<string | null>(null);
  const [finalized, setFinalized] = useState(false);
  const [server, setServer] = useState<{ q: string; list: QueryDiagnostic[] }>({ q: "", list: [] });

  const local = useMemo(() => {
    // Drop the trailing in-progress token before parsing, so it is neither
    // diagnosed nor sent. `finalize` stops dropping it.
    const toParse = inProgress && !finalized ? text.slice(0, text.length - inProgress.length) : text;
    const resolved = resolveQuery(parseQuery(toParse), source);
    return { q: serializeQuery(resolved.terms), diagnostics: resolved.diagnostics };
  }, [text, inProgress, finalized, source]);

  const diagnostics = useMemo(() => {
    const all = [...local.diagnostics];
    // A response may only annotate the query it actually answered. Two
    // conditions, and both are needed: `server.q === ownerQ` says the
    // diagnostics came with that response, and `ownerQ === local.q` says the box
    // still holds that query. Without the second, a slow response for a query the
    // user has already replaced reports the old query's problems against the new
    // one.
    if (ownerQ !== "" && server.q === ownerQ && ownerQ === local.q) {
      all.push(...server.list);
    }
    const seen = new Set<string>();
    return all.filter((d) => {
      const key = `${d.code}:${d.term}`;
      if (seen.has(key)) return false;
      seen.add(key);
      return true;
    });
  }, [local.diagnostics, local.q, server, ownerQ]);

  return {
    text,
    setText,
    q: local.q,
    diagnostics,
    inProgress,
    setInProgress,
    finalize: useCallback(() => setFinalized(true), []),
    clear: useCallback(() => {
      setText("");
      setInProgress(null);
      setFinalized(false);
      setServer({ q: "", list: [] });
    }, []),
    setServerDiagnostics: useCallback(
      (list: QueryDiagnostic[]) => setServer({ q: ownerQ, list }),
      [ownerQ],
    ),
  };
}
