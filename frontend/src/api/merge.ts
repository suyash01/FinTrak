// The merge policy for an offline edit, as three pure functions.
//
// An edit made with no network is queued as a field-level patch and merged
// three ways on flush: `base` is what the client believed when the edit was
// made, `mine` is what the user set, `theirs` is what the server holds now.
// The unit of merge is the single field. What comes back is the patch to send
// and the fields the user has to decide; no I/O happens here, so the whole
// policy is provable under a table test and the UI cannot quietly disagree
// with it. Nothing is discarded in either direction: a field the engine cannot
// attribute to a side is held for the user, never resolved.
//
// Three things about representation, rather than policy, are what make that
// correct:
//
//   Absent is not null. A key missing from a patch is untouched; `null` is the
//   user clearing a field. A missing key therefore reads as `undefined` and is
//   *omitted* from the patch these functions return, never returned as
//   `undefined`: `JSON.stringify` drops such a key, so a field the merge meant
//   to send would leave the wire disagreeing with the merge without saying so.
//
//   Collections compare order-insensitively. `tags` is a set to the user, so
//   re-ordering it offline is not a change and must not read as one.
//
//   No arithmetic, ever. Amounts are compared as the numbers the API sent, so
//   the `money.Amount` minor-unit rule is untouched by this work.

export type FieldValue = string | number | boolean | null | string[] | undefined;

export interface FieldPatch {
  [field: string]: FieldValue;
}

// FieldConflict is a field both sides changed, held for the user rather than
// written. It carries the base so a dialog can show what the two competing
// values were edited away from.
export interface FieldConflict {
  field: string;
  base: FieldValue;
  mine: FieldValue;
  theirs: FieldValue;
}

export interface MergeResult {
  patch: FieldPatch;
  conflicts: FieldConflict[];
}

// valuesEqual is the engine's only comparison: strict equality, except that two
// collections compare as sets — equal length, every member present on both
// sides. Multiplicity is not distinguished, so ["a","b","b"] equals
// ["a","a","b"]; a repeated tag is not a distinction this merge makes, because
// the tag list it reads comes from a store that does not hold duplicates.
export function valuesEqual(a: FieldValue, b: FieldValue): boolean {
  if (Array.isArray(a) && Array.isArray(b)) {
    return (
      a.length === b.length &&
      a.every((value) => b.includes(value)) &&
      b.every((value) => a.includes(value))
    );
  }
  return a === b;
}

// diffAgainstBase reduces a whole-row form payload to the fields the user
// actually changed, which is what makes a field-level patch safe to build from
// one: a field the payload does not carry never appears, so a PATCH cannot
// revert a column another writer changed while the form was open.
export function diffAgainstBase(base: FieldPatch, mine: FieldPatch): FieldPatch {
  const diff: FieldPatch = {};
  for (const [field, value] of Object.entries(mine)) {
    if (value === undefined || valuesEqual(value, base[field])) continue;
    diff[field] = value;
  }
  return diff;
}

// mergeFields resolves one field at a time, in this order:
//
//   1. the user did not change it          -> theirs
//   2. nobody else changed it              -> mine
//   3. both sides landed on the same value -> that value
//   4. otherwise                            -> a held conflict, nothing written
//
// A field counts as changed by the user wherever `mine` carries it and its
// value differs from the base's, so a field the user's patch does not carry is
// untouched and takes theirs. A field the user did set that the base never held
// is the other kind of field, and the difference matters: there is no shared
// baseline, so the engine cannot say who moved it — it holds a conflict rather
// than resolving, because resolving to theirs there would silently discard an
// edit the user made. That is the one case where the engine refuses to answer,
// and the queue is what keeps it rare: the base a queued entry records carries
// every field its patch sets.
export function mergeFields(
  base: FieldPatch,
  mine: FieldPatch,
  theirs: FieldPatch,
): MergeResult {
  const patch: FieldPatch = {};
  const conflicts: FieldConflict[] = [];
  const fields = new Set([
    ...Object.keys(base),
    ...Object.keys(mine),
    ...Object.keys(theirs),
  ]);

  for (const field of fields) {
    const baseValue = base[field];
    const mineValue = mine[field];
    const theirsValue = theirs[field];
    const userChanged = mineValue !== undefined && !valuesEqual(mineValue, baseValue);

    let resolved: FieldValue;
    if (!userChanged) {
      resolved = theirsValue;
    } else if (valuesEqual(theirsValue, baseValue)) {
      resolved = mineValue;
    } else if (valuesEqual(mineValue, theirsValue)) {
      resolved = theirsValue;
    } else {
      conflicts.push({
        field,
        base: baseValue,
        mine: mineValue,
        theirs: theirsValue,
      });
      continue;
    }

    // Omitted rather than set: an explicit `undefined` is not a field, and
    // `JSON.stringify` would drop it, leaving the wire quietly narrower than
    // the merge decided.
    if (resolved !== undefined) patch[field] = resolved;
  }

  return { patch, conflicts };
}
