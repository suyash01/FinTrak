import { useCallback, useMemo, useRef, useState } from "react";
import { HelpCircle, Search } from "lucide-react";
import { Command, CommandEmpty, CommandGroup, CommandItem, CommandList } from "@/components/ui/command";
import { Input } from "@/components/ui/input";
import { Button } from "@/components/ui/button";
import { Sheet, SheetContent, SheetHeader, SheetTitle, SheetTrigger } from "@/components/ui/sheet";
import { cn } from "@/lib/utils";
import { DATE_PERIODS, FIELD_TABLE, USER_FIELDS, type FieldDef } from "./fields";
import type { QueryDiagnostic } from "./parse";
import type { ResolveSource } from "./resolve";
import { QueryDiagnosticsBanner } from "./QueryDiagnostics";

/**
 * quoteIfNeeded mirrors the serializer's rule, so an inserted value always parses
 * back as the single term the user meant. Without it, accepting "Whole Foods"
 * would insert `payee:Whole Foods`, which parses as a payee term and a bare-word
 * search for "Foods".
 */
function quoteIfNeeded(value: string): string {
  return /[\s,"]/.test(value) ? `"${value.replace(/(["\\])/g, "\\$1")}"` : value;
}

/** CaretToken is the token the caret sits in, which decides what to suggest. */
interface CaretToken {
  /** Offset where the token starts in the input. */
  start: number;
  /** The token text, up to the caret. */
  text: string;
  /** The field name when the token is `field:` or `field<op>`, else null. */
  field: string | null;
}

function caretToken(text: string, caret: number): CaretToken {
  let start = caret;
  while (start > 0 && text[start - 1] !== " " && text[start - 1] !== "\t") start--;
  const raw = text.slice(start, caret);
  const colon = raw.indexOf(":");
  const opMatch = /^([a-z]+)(>=|<=|!=|~|>|<|=)/.exec(raw);
  return {
    start,
    text: raw,
    field: colon > 0 ? raw.slice(0, colon).toLowerCase() : opMatch ? opMatch[1] : null,
  };
}

interface Suggestion {
  group: string;
  label: string;
  /** Exactly what is written into the input. */
  insert: string;
}

/**
 * suggestionsFor is driven off the same field table the parser reads, so it can
 * never offer a term the parser will reject.
 */
function suggestionsFor(text: string, token: CaretToken, source: ResolveSource): Suggestion[] {
  const out: Suggestion[] = [];

  if (!token.field) {
    const partial = token.text.toLowerCase();
    for (const f of USER_FIELDS) {
      if (f.startsWith(partial)) out.push({ group: "Fields", label: f, insert: `${f}:` });
    }
    return out;
  }

  const def: FieldDef | undefined = FIELD_TABLE[token.field];
  if (!def) return out;
  // Whatever the user has typed after the field name and its separator.
  const after = token.text.slice(token.field.length);
  const partial = after.replace(/^[:=<>!~]+/, "").toLowerCase();
  const insert = (value: string) => `${token.field}:${quoteIfNeeded(value)}`;

  if (def.enum) {
    for (const v of def.enum) {
      if (v.startsWith(partial)) out.push({ group: token.field, label: v, insert: `${token.field}:${v}` });
    }
    return out;
  }

  if (token.field === "acct") {
    for (const a of source.accounts) {
      if (a.name.toLowerCase().includes(partial)) out.push({ group: "Accounts", label: a.name, insert: insert(a.name) });
    }
  }
  if (token.field === "payee") {
    for (const p of source.payees) {
      if (p.name.toLowerCase().includes(partial)) out.push({ group: "Payees", label: p.name, insert: insert(p.name) });
    }
  }
  if (token.field === "tag") {
    for (const t of source.tags) {
      if (t.toLowerCase().includes(partial)) out.push({ group: "Tags", label: t, insert: insert(t) });
    }
  }
  if (token.field === "cat") {
    // Only cat takes a category. `group:` resolves a group name or id, so
    // offering a category there produced a `group:Food/Groceries` the resolver
    // then reported as an unresolvable group.
    const groupName = (id: string) => source.groups.find((g) => g.id === id)?.name ?? "";
    for (const c of source.categories) {
      if (!c.name.toLowerCase().includes(partial)) continue;
      // Two categories can share a name, so one is offered qualified. The bare
      // spelling still resolves to both, and the resolver reports the ambiguity
      // rather than picking one.
      const qualified = `${groupName(c.groupId)}/${c.name}`;
      out.push({ group: "Categories", label: qualified, insert: insert(qualified) });
    }
  }
  if (token.field === "cat" || token.field === "group") {
    for (const g of source.groups) {
      if (g.name.toLowerCase().includes(partial)) {
        out.push({ group: "Groups", label: g.name, insert: `${token.field}:${quoteIfNeeded(g.name)}` });
      }
    }
  }
  if (token.field === "date") {
    for (const p of DATE_PERIODS) {
      if (p.includes(partial)) out.push({ group: "Periods", label: p, insert: insert(p) });
    }
  }
  return out;
}

function groupSuggestions(items: Suggestion[]): Array<[string, Suggestion[]]> {
  const map = new Map<string, Suggestion[]>();
  for (const item of items) map.set(item.group, [...(map.get(item.group) ?? []), item]);
  return [...map.entries()];
}

export interface QueryInputProps {
  source: ResolveSource;
  /** The text as the user typed it. Controlled: the page owns it (the URL does). */
  value: string;
  /** Called with the whole text on every keystroke. */
  onValueChange: (text: string) => void;
  /**
   * Called on submit. There is no value to pass: the resolved query is derived
   * from the text continuously, so submitting only has to finalize a half-typed
   * term. Writing a resolved value back here would replace what the user typed in
   * the box (and in the URL) with a uuid, which is the opposite of helpful.
   */
  onSubmit?: () => void;
  diagnostics: QueryDiagnostic[];
  onInProgressChange?: (token: string | null) => void;
  onQueryCommit?: () => void;
  compactLayout?: boolean;
  placeholder?: string;
}

export function QueryInput({
  source,
  value: text,
  onValueChange,
  onSubmit,
  diagnostics,
  onInProgressChange,
  compactLayout,
  placeholder = "Try: cat:Groceries amt>50",
}: QueryInputProps) {
  const [open, setOpen] = useState(false);
  const [sheetOpen, setSheetOpen] = useState(false);
  const inputRef = useRef<HTMLInputElement>(null);

  // Where the caret is decides both the suggestions and which token is still
  // being typed. A trailing token is reported to the parent so it can be left out
  // of the diagnostics until the user finishes it.
  const caret = inputRef.current?.selectionStart ?? text.length;
  const token = useMemo(() => caretToken(text, caret), [text, caret]);
  const trailing = caret >= text.length ? token : null;

  const suggestions = useMemo(() => suggestionsFor(text, token, source), [text, token, source]);
  const showing = open && suggestions.length > 0;

  const setValue = useCallback(
    (next: string) => {
      onValueChange(next);
      // Whatever the caret now sits in is still being typed, so it must not be
      // diagnosed until the user finishes it.
      onInProgressChange?.(caretToken(next, next.length).text);
    },
    [onValueChange, onInProgressChange],
  );

  const accept = useCallback(
    (insert: string) => {
      const t = caretToken(text, inputRef.current?.selectionStart ?? text.length);
      const next = text.slice(0, t.start) + insert + text.slice(t.start + t.text.length);
      setValue(next);
      setOpen(false);
      inputRef.current?.focus();
    },
    [text, setValue],
  );

  const onKeyDown = useCallback(
    (e: React.KeyboardEvent<HTMLInputElement>) => {
      if (e.key === "Enter") {
        e.preventDefault();
        setOpen(false);
        setValue(text);
        onInProgressChange?.(null);
        onSubmit?.();
        return;
      }
      if (e.key === "Escape") {
        setOpen(false);
        return;
      }
      if ((e.key === "Tab" || e.key === "ArrowRight") && showing && suggestions.length > 0) {
        e.preventDefault();
        accept(suggestions[0].insert);
      }
    },
    [text, showing, suggestions, accept, setValue, onInProgressChange, onSubmit],
  );

  return (
    <div className="space-y-2">
      <div className="relative">
        <Search className="pointer-events-none absolute left-2 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" aria-hidden />
        <Input
          ref={inputRef}
          value={text}
          onChange={(e) => setValue(e.target.value)}
          onKeyDown={onKeyDown}
          onFocus={() => setOpen(true)}
          onBlur={() => window.setTimeout(() => setOpen(false), 150)}
          onSelect={(e) => {
            const at = (e.target as HTMLInputElement).selectionStart ?? 0;
            onInProgressChange?.(at >= text.length ? caretToken(text, at).text : null);
          }}
          placeholder={placeholder}
          aria-label="Transaction query"
          role="combobox"
          aria-expanded={showing}
          className={cn("pl-8 pr-9", compactLayout ? "h-8" : "h-9")}
        />
        <Sheet open={sheetOpen} onOpenChange={setSheetOpen}>
          <SheetTrigger asChild>
            <Button
              variant="ghost"
              size="icon"
              aria-label="Query syntax"
              className="absolute right-1 top-1/2 h-7 w-7 -translate-y-1/2"
            >
              <HelpCircle className="h-4 w-4" />
            </Button>
          </SheetTrigger>
          <SheetContent side="bottom">
            <SheetHeader>
              <SheetTitle>Query syntax</SheetTitle>
            </SheetHeader>
            <GrammarSheet />
          </SheetContent>
        </Sheet>
        {showing && (
          <div className="absolute inset-x-0 top-full z-50 mt-1 overflow-hidden rounded-md border bg-popover text-popover-foreground shadow-md">
            <Command>
              <CommandList>
                <CommandEmpty>No match.</CommandEmpty>
                {groupSuggestions(suggestions).map(([group, items]) => (
                  <CommandGroup key={group} heading={group}>
                    {items.map((s) => (
                      <CommandItem key={s.insert} value={s.insert} onSelect={() => accept(s.insert)}>
                        {s.label}
                      </CommandItem>
                    ))}
                  </CommandGroup>
                ))}
              </CommandList>
            </Command>
          </div>
        )}
      </div>
      <QueryDiagnosticsBanner diagnostics={diagnostics} />
    </div>
  );
}

/** describe renders a field's accepted values, for the grammar sheet. */
function describeField(def: FieldDef): string {
  if (def.enum) return def.enum.join(" | ");
  switch (def.kind) {
    case "uuid":
      // Only a nullable column takes a sentinel. Promising `none` on acct or
      // group would send the user to a spelling the server answers with a 500.
      return def.sentinels?.length
        ? `an id, or ${def.sentinels.join(" / ")}`
        : "an id (a uuid)";
    case "amount":
      return "a decimal in major units, e.g. 50 or 50.75; supports > >= < <= = !=";
    case "date":
      return `YYYY-MM-DD or ${DATE_PERIODS.join(" / ")}; supports > >= < <= = !=`;
    case "tags":
      return "a tag name, comma-separated for any of";
    case "currency":
      return "a three-letter currency code, e.g. USD; any case";
    default:
      return "text; ~ is contains";
  }
}

/**
 * GrammarSheet renders the same field table the suggestions come from, so the
 * documented syntax cannot drift from the accepted syntax.
 */
function GrammarSheet() {
  return (
    <div className="space-y-4 px-4 pb-6 text-sm">
      <p className="text-muted-foreground">
        Terms are combined with AND. A comma-separated value matches any of them. Prefix a term with{" "}
        <code>not</code> to exclude it, and use <code>!= </code> for the same thing. A bare word is a free-text
        search over descriptions, notes, payee names and tags.
      </p>
      <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1">
        {USER_FIELDS.map((f) => (
          <div key={f} className="contents">
            <dt>
              <code>{f}:</code>
            </dt>
            <dd className="text-muted-foreground">{describeField(FIELD_TABLE[f])}</dd>
          </div>
        ))}
      </dl>
      <p className="text-muted-foreground">
        Values are ids, not names, so the app resolves what you type. A term it cannot resolve is dropped and
        reported above the table, never silently ignored.
      </p>
    </div>
  );
}
