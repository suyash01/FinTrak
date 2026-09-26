import { useMemo } from "react";
import { parseQuery, type QueryDiagnostic } from "./parse";
import { resolveQuery, serializeQuery, type ResolveSource } from "./resolve";

export interface ServerDiagnostics {
  /** The q the response was for. */
  q: string;
  list: QueryDiagnostic[];
}

export interface QueryLanguage {
  /**
   * The value to send as q=, resolved from the text: names to ids, a named period
   * to concrete dates, an amount to minor units. Empty when there is nothing to
   * send.
   */
  q: string;
  /** Local (pre-request) plus server diagnostics, deduped. */
  diagnostics: QueryDiagnostic[];
}

export interface QueryLanguageOptions {
  source: ResolveSource;
  /**
   * The text as the user typed it. It is a prop, not state, because on this page
   * the URL is the state: `?q=` holds what was typed, so a link is shareable and
   * the browser's back button works. The box is controlled by it.
   */
  text: string;
  /**
   * The token the caret is in at the end of the text, or null when the caret is
   * not in a trailing token. The box knows where the caret is; this does not.
   */
  inProgress: string | null;
  /**
   * Set when the user submitted. It finalizes a half-typed term, so `cat:` plus
   * Enter becomes a reported error instead of staying quiet.
   */
  finalized: boolean;
  /** The diagnostics the last response carried, and the q it was for. */
  server: ServerDiagnostics;
}

/**
 * useQueryLanguage derives the q= to send, and the diagnostics to show, from the
 * text in the box.
 *
 * The in-progress rule is the important part. Because matching is lenient, every
 * keystroke is a parse: without excluding the trailing token, typing `cat` would
 * report `ca` as an unknown field, then `cat` as valid, then `cat:` as needing a
 * value, and the warning banner would strobe while the user types. So the token
 * under a trailing caret is neither reported nor sent, and submitting finalizes
 * it — which is when `cat:` legitimately becomes an error.
 */
export function useQueryLanguage(opts: QueryLanguageOptions): QueryLanguage {
  const { source, text, inProgress, finalized, server } = opts;

  const local = useMemo(() => {
    // Drop the trailing in-progress token before parsing, so it is neither
    // diagnosed nor sent. `finalized` stops dropping it.
    const toParse = inProgress && !finalized ? text.slice(0, text.length - inProgress.length) : text;
    const resolved = resolveQuery(parseQuery(toParse), source);
    return { q: serializeQuery(resolved.terms), diagnostics: resolved.diagnostics };
  }, [text, inProgress, finalized, source]);

  const diagnostics = useMemo(() => {
    const all = [...local.diagnostics];
    // A response may only annotate the query it actually answered, and only while
    // the box still holds that query. Without the second condition a slow response
    // for a query the user has already replaced reports the old query's problems
    // against the new one.
    if (server.q !== "" && server.q === local.q) {
      all.push(...server.list);
    }
    const seen = new Set<string>();
    return all.filter((d) => {
      const key = `${d.code}:${d.term}`;
      if (seen.has(key)) return false;
      seen.add(key);
      return true;
    });
  }, [local.diagnostics, local.q, server]);

  return { q: local.q, diagnostics };
}
