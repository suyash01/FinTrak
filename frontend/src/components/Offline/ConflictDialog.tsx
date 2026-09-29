// The one surface where a held offline write is put to the user: the two
// competing values per field, the base they were both edited away from, and the
// two answers. Its actions arrive as props rather than from the offline context,
// so the whole of it is testable without a provider; App supplies them.
//
// One note on the signature: the return type is ReactElement rather than the
// JSX.Element the interface in the plan spells, because React 19 dropped the
// global JSX namespace and `JSX.Element` does not resolve in this project.
import { useState, type ReactElement } from "react";
import { TriangleAlert } from "lucide-react";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import type { BulkEntry, ConflictUnit, EditEntry, QueuedWrite } from "@/api/outbox";
import type { FieldValue } from "@/api/merge";
import { formatNumber } from "@/utils/formatters";

type Side = "mine" | "theirs";

export interface ConflictDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  entries: QueuedWrite[];
  onResolve: (key: string, resolution: Record<string, Side>) => void;
  onReCreate: (key: string) => void;
  onDiscard: (key: string) => void;
}

// NOTHING renders where a value is absent. A base the client never held is
// `undefined` for a real reason — the user set a field the base did not carry,
// so there is no shared baseline and the engine could not attribute the field to
// a side — and it is the one row where "keep theirs" throws away an edit the
// user made. The em-dash is that: it says the field had nothing to be edited
// away from, which is what makes the row legible. Rendering `undefined` instead
// would print a JavaScript value at a user who is deciding about money.
const NOTHING = "—";

// fieldLabel turns an API field name into something a person recognises:
// camelCase split into words, a trailing `Id` dropped, and only the first word
// capitalised ("categoryId" -> "Category", "billingDay" -> "Billing day").
// Deliberately generic rather than a per-field lookup table: the names come from
// the projections in api/projections.ts, and a second list of them here is one
// more thing to forget when a field is added.
function fieldLabel(field: string): string {
  const words = field
    .replace(/([a-z0-9])([A-Z])/g, "$1 $2")
    .split(/\s+/)
    .filter((word) => word.length > 0);
  if (words[words.length - 1] === "Id") words.pop();
  return words
    .map((word, index) =>
      index === 0
        ? word.charAt(0).toUpperCase() + word.slice(1)
        : word.toLowerCase(),
    )
    .join(" ");
}

// displayValue renders one side of a conflict. A null and an undefined are both
// rendered as the em-dash rather than told apart: the decision is which value to
// write, and "no value" is the same answer either way. Arrays are joined because
// `tags` is a list to the user, and `String(["a", "b"])` happening to produce
// "a,b" is not a rendering anyone chose.
function displayValue(value: FieldValue): string {
  if (value === undefined || value === null) return NOTHING;
  if (Array.isArray(value)) return value.length === 0 ? NOTHING : value.join(", ");
  return String(value);
}

// shortRow names a row in a conflict unit. A conflict on a bulk write carries
// the row it is about, because that write is one field over many rows and the
// base and the server's value differ row by row.
function shortRow(rowId: string): string {
  return rowId.length > 8 ? `${rowId.slice(0, 8)}…` : rowId;
}

// ConflictDialog is the only place the user is asked to decide anything about a
// held offline write. It shows the two competing values per field and the base
// they were both edited away from, because without the base there is no way to
// tell which of the two is the user's own change — and it writes nothing until
// they save, since a conflict exists precisely because the engine would not
// guess.
//
// Every held entry is here at once, not one at a time: several can be held
// together and nothing selects between them.
export default function ConflictDialog({
  open,
  onOpenChange,
  entries,
  onResolve,
  onReCreate,
  onDiscard,
}: ConflictDialogProps): ReactElement | null {
  // Per entry, per field. A side is read through `choice` with "mine" as the
  // fallback rather than seeded into state, so a field that starts out unchosen
  // needs no effect to agree with a unit the user has not seen yet.
  const [choices, setChoices] = useState<Record<string, Record<string, Side>>>({});

  const choice = (key: string, field: string): Side => choices[key]?.[field] ?? "mine";

  const choose = (key: string, field: string, side: Side) =>
    setChoices((prev) => ({ ...prev, [key]: { ...prev[key], [field]: side } }));

  // The shortcut is a setter over the units the entry actually holds, not a
  // toggle: it overrides whatever a per-field answer said.
  const chooseAll = (entry: QueuedWrite, side: Side) =>
    setChoices((prev) => ({
      ...prev,
      [entry.key]: Object.fromEntries(
        unitsOf(entry).map((unit) => [unit.field, side]),
      ),
    }));

  // Nothing held is nothing to ask, so the dialog is not mounted at all. The
  // parent still owns `open`; this only means there is nothing to show for it,
  // which is also what happens to a queue whose last conflict was just saved.
  if (entries.length === 0) return null;

  const save = () => {
    for (const entry of entries) {
      const units = unitsOf(entry);
      if (units.length === 0) continue;
      // One record for the entry, carrying every field it holds. The queue takes
      // the record as an override of the queued patch, so a field left out of it
      // is not "undecided" — it is simply not written, which is not what the
      // user was asked.
      onResolve(
        entry.key,
        Object.fromEntries(
          units.map((unit) => [unit.field, choice(entry.key, unit.field)]),
        ),
      );
    }
  };

  // The two holds answer different questions — a field somebody else moved, and
  // a row that is not there any more — so the description names whichever of
  // them is on screen rather than describing the other.
  const fields = entries.reduce(
    (total, entry) => total + unitsOf(entry).length,
    0,
  );
  // Rows rather than entries, for the reason the section under it counts them:
  // a bulk write that lost one row of two hundred is one entry and one lost row,
  // and a count of entries would say the opposite of what the user is reading.
  const gone = entries.reduce(
    (total, entry) =>
      entry.gone === true
        ? total + (entry.kind === "bulk" ? entry.rows.length : 1)
        : total,
    0,
  );

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-3xl sm:max-w-3xl">
        <DialogHeader>
          <div className="flex items-center gap-2.5">
            <TriangleAlert className="size-5 text-destructive" aria-hidden="true" />
            <DialogTitle className="text-lg font-bold text-foreground">
              Offline changes need your decision
            </DialogTitle>
          </div>
          <DialogDescription>
            {fields > 0 && (
              <>
                Someone else changed{" "}
                {fields === 1 ? "a field" : `${fields} fields`} you edited while you
                were offline.{" "}
              </>
            )}
            {gone > 0 && (
              <>
                {gone === 1 ? "A row" : `${gone} rows`} you edited no longer{" "}
                {gone === 1 ? "exists" : "exist"} on the server.{" "}
              </>
            )}
            Nothing has been sent — choose which version to keep for each field, and
            the next sync sends it.
          </DialogDescription>
        </DialogHeader>

        {entries.map((entry) =>
          // `gone` and a row write together: a gone entry is a row the server
          // no longer has, and a create is a row that does not exist yet, so the
          // two can never meet. The second half is a guard rather than a cast
          // because CreateEntry's `kind` is optional, so the type alone cannot
          // make the leap (outbox.ts's isRowWrite says the same).
          entry.gone === true && isRowWrite(entry) ? (
            <GoneSection
              key={entry.key}
              entry={entry}
              onReCreate={onReCreate}
              onDiscard={onDiscard}
            />
          ) : (
            <ConflictSection
              key={entry.key}
              entry={entry}
              choice={choice}
              choose={choose}
              chooseAll={chooseAll}
            />
          ),
        )}

        <DialogFooter showCloseButton>
          {/* Recording the answer is not sending it: the entry stays queued and
              goes out on the next sync, which is why the description above says
              so rather than promising an immediate write. */}
          <Button onClick={save}>Save</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// unitsOf is the entry's held fields, and it is the one place the two kinds of
// hold are told apart. A gone row is held as a fact about a row, not as a
// question about a field, so an entry never carries both (outbox.ts's
// WriteEnvelope invariant) — which is what lets the caller branch on `gone` and
// let this be the only reader of `conflict`.
function unitsOf(entry: QueuedWrite): ConflictUnit[] {
  return entry.conflict?.units ?? [];
}

function isRowWrite(entry: QueuedWrite): entry is EditEntry | BulkEntry {
  return entry.kind !== "create";
}

interface ConflictSectionProps {
  entry: QueuedWrite;
  choice: (key: string, field: string) => Side;
  choose: (key: string, field: string, side: Side) => void;
  chooseAll: (entry: QueuedWrite, side: Side) => void;
}

function ConflictSection({ entry, choice, choose, chooseAll }: ConflictSectionProps) {
  const units = unitsOf(entry);
  // Unreachable through the queue — a hold is only recorded with units in it —
  // and kept because the alternative is a section headed "0 fields need your
  // decision" with two shortcuts that decide nothing.
  if (units.length === 0) return null;
  return (
    <section className="flex flex-col gap-2">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h3 className="text-sm font-medium text-foreground">
          {units.length === 1
            ? "1 field needs your decision"
            : `${units.length} fields need your decision`}
        </h3>
        <div className="flex items-center gap-2">
          <Button
            variant="outline"
            size="sm"
            onClick={() => chooseAll(entry, "mine")}
          >
            Keep all mine
          </Button>
          <Button
            variant="outline"
            size="sm"
            onClick={() => chooseAll(entry, "theirs")}
          >
            Keep all theirs
          </Button>
        </div>
      </div>

      <div className="max-h-60 overflow-y-auto rounded-lg border border-border">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Field</TableHead>
              <TableHead>Your value</TableHead>
              <TableHead>Their value</TableHead>
              {/* "Base" is what makes the other two readable: it is the value
                  both of them were edited away from. */}
              <TableHead>Base</TableHead>
              <TableHead className="text-right">Keep</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {units.map((unit) => (
              <UnitRow
                key={`${unit.rowId}:${unit.field}`}
                unit={unit}
                side={choice(entry.key, unit.field)}
                onChoose={(side) => choose(entry.key, unit.field, side)}
              />
            ))}
          </TableBody>
        </Table>
      </div>
    </section>
  );
}

function UnitRow({
  unit,
  side,
  onChoose,
}: {
  unit: ConflictUnit;
  side: Side;
  onChoose: (side: Side) => void;
}) {
  const label = fieldLabel(unit.field);
  // The row travels in the button names because a resolution is keyed by field
  // and not by row: two rows of a bulk write that conflicted on the same field
  // are one switch, and a name that hid the row would read as two independent
  // answers to one question.
  const where = `on row ${shortRow(unit.rowId)}`;
  return (
    <TableRow>
      <TableCell>
        <div className="font-medium text-foreground">{label}</div>
        <div className="text-xs text-muted-foreground">{where}</div>
      </TableCell>
      <TableCell className="whitespace-normal break-words text-foreground">
        {displayValue(unit.mine)}
      </TableCell>
      <TableCell className="whitespace-normal break-words text-foreground">
        {displayValue(unit.theirs)}
      </TableCell>
      <TableCell className="whitespace-normal break-words text-muted-foreground">
        {displayValue(unit.base)}
      </TableCell>
      <TableCell className="text-right">
        <div className="flex items-center justify-end gap-1.5">
          <Button
            variant={side === "mine" ? "default" : "outline"}
            size="sm"
            aria-pressed={side === "mine"}
            aria-label={`Keep mine for ${label} ${where}`}
            onClick={() => onChoose("mine")}
          >
            Mine
          </Button>
          <Button
            variant={side === "theirs" ? "default" : "outline"}
            size="sm"
            aria-pressed={side === "theirs"}
            aria-label={`Keep theirs for ${label} ${where}`}
            onClick={() => onChoose("theirs")}
          >
            Theirs
          </Button>
        </div>
      </TableCell>
    </TableRow>
  );
}

function GoneSection({
  entry,
  onReCreate,
  onDiscard,
}: {
  entry: EditEntry | BulkEntry;
  onReCreate: (key: string) => void;
  onDiscard: (key: string) => void;
}) {
  // A create against a row the server still has would insert a second money
  // row, so the context refuses it; and a bulk write is not one row to rebuild,
  // so it refuses that too. Neither is offered here, because an action that
  // fails is worse than an action that is absent.
  const canReCreate = entry.kind === "edit";
  return (
    <section className="flex flex-col gap-2 rounded-lg border border-border bg-muted/50 p-3">
      <div>
        <h3 className="text-sm font-medium text-foreground">
          {entry.kind === "bulk"
            ? `${entry.rows.length} rows no longer exist on the server`
            : "A row no longer exists on the server"}
        </h3>
        <p className="text-sm text-muted-foreground">
          {entry.kind === "edit" ? (
            <>
              {goneSummary(entry) ?? shortRow(entry.rowId)} was edited offline and deleted
              elsewhere. It was not applied.
            </>
          ) : (
            <>
              The change set {fieldLabel(entry.field)} to {displayValue(entry.value)} on
              every one of them. It was not applied.
            </>
          )}
        </p>
      </div>

      <div className="flex flex-wrap items-center gap-2">
        {canReCreate && (
          <Button size="sm" onClick={() => onReCreate(entry.key)}>
            Re-create as a new row
          </Button>
        )}
        <AlertDialog>
          <AlertDialogTrigger asChild>
            <Button variant="destructive" size="sm">
              Discard
            </Button>
          </AlertDialogTrigger>
          <AlertDialogContent>
            <AlertDialogHeader>
              <AlertDialogTitle>Discard this change?</AlertDialogTitle>
              <AlertDialogDescription>
                Removing it drops the change from this device for good. Nothing was
                written to the server, so this is not undoing anything there.
              </AlertDialogDescription>
            </AlertDialogHeader>
            <AlertDialogFooter>
              <AlertDialogCancel>Cancel</AlertDialogCancel>
              <AlertDialogAction onClick={() => onDiscard(entry.key)}>
                Discard
              </AlertDialogAction>
            </AlertDialogFooter>
          </AlertDialogContent>
        </AlertDialog>
      </div>
    </section>
  );
}

// goneSummary names what the row held, so the user can recognise it before
// deciding whether to write it again. A queued entry carries no account and so
// no currency, which is why the amount goes through the currency-free
// formatter; an op whose projection has neither field falls back to the row id
// in the copy above rather than inventing a description.
function goneSummary(entry: EditEntry): string | null {
  if (entry.kind !== "edit") return null;
  const description = entry.snapshot.description;
  const amount = entry.snapshot.amount;
  // typeof rather than `!== undefined`: `amount` is a FieldValue, so a base that
  // carries it as null would otherwise reach formatNumber and print NaN.
  const parts = [
    typeof description === "string" ? description : undefined,
    typeof amount === "number" ? formatNumber(amount) : undefined,
  ].filter((part): part is string => part !== undefined);
  return parts.length > 0 ? parts.join(" · ") : null;
}
