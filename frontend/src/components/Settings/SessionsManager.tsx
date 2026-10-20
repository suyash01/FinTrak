import { useCallback, useEffect, useState } from "react";
import { toast } from "sonner";
import api from "../../api/client";
import type { Session } from "../../types";
import { describeUserAgent } from "../../lib/userAgent";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";

// Which destructive action a pending AlertDialog is confirming. One dialog for
// all three rather than one per row: the rows are data, the actions are three,
// and a dialog per row would mount N Radix roots for a list that is usually
// one row long.
type Pending =
  | { kind: "session"; session: Session }
  | { kind: "others" }
  | { kind: "all" };

// Intl.RelativeTimeFormat is the platform's own answer to "last seen", so this
// needs no dependency. lastSeen is an RFC 3339 instant (see the Session comment
// in types.ts), so it is a `new Date` and never parseDateOnly — that helper
// slices a calendar day out of the string and would render the wrong day east
// of UTC.
const RELATIVE = new Intl.RelativeTimeFormat("en", { numeric: "auto" });

function timeAgo(iso: string): string {
  const then = new Date(iso).getTime();
  if (Number.isNaN(then)) return "";
  const seconds = Math.round((Date.now() - then) / 1000);
  const scale: [number, Intl.RelativeTimeFormatUnit][] = [
    [60, "second"],
    [60, "minute"],
    [24, "hour"],
    [7, "day"],
    [4.348, "week"],
    [12, "month"],
    [Number.POSITIVE_INFINITY, "year"],
  ];
  let value = seconds;
  for (const [step, unit] of scale) {
    if (Math.abs(value) < step) return RELATIVE.format(Math.round(value), unit);
    value /= step;
  }
  return "";
}

export default function SessionsManager() {
  const [sessions, setSessions] = useState<Session[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [pending, setPending] = useState<Pending | null>(null);
  const [busy, setBusy] = useState(false);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      setSessions(await api.getSessions());
      setError(null);
    } catch (err) {
      // A failed load must not render as "no devices signed in": show it and
      // offer the retry until a request actually succeeds.
      setError((err as Error).message);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  const confirm = async () => {
    const action = pending;
    if (!action) return;
    setPending(null);
    setBusy(true);
    try {
      if (action.kind === "session") {
        await api.revokeSession(action.session.id);
        // Revoking your OWN session ends this tab's credentials exactly as
        // "sign out everywhere" does — RequireAuth refuses the next request and
        // the app signs you out — so the same no-reload rule applies. Ending
        // any OTHER device leaves this one signed in, and the list must change.
        if (!action.session.current) {
          await load();
        } else {
          // No reload means nothing on screen changes to report the outcome by,
          // and Settings polls nothing — without this the user sits on a dead
          // session with no indication until some later request 401s.
          toast.success("Signed out of this device");
        }
        return;
      }
      if (action.kind === "others") {
        await api.revokeOtherSessions();
      } else {
        await api.revokeAllSessions();
        // Deliberately no reload. This one revokes the CALLER'S OWN session, so
        // the next request 401s and the app signs the user out; a getSessions()
        // here would race that logout and report its 401 as a failure of a
        // sign-out that in fact worked. Same reason it announces itself.
        toast.success("Signed out of every device");
        return;
      }
      await load();
    } catch (err) {
      toast.error((err as Error).message);
    } finally {
      setBusy(false);
    }
  };

  if (error)
    // Unconditional, like PaperlessSettingsManager, and deliberately NOT gated
    // on `sessions.length === 0`. A reload that fails after a successful revoke
    // leaves rows that are known-stale, and the one thing this panel must never
    // do is show a device the user just ended under an "Active" badge.
    // Withdrawing the list is the honest answer — "we do not know what is signed
    // in", with a Retry — rather than a confident wrong one.
    return (
      <div className="space-y-3">
        <div className="px-4 py-3 bg-destructive/10 border border-destructive/30 rounded-lg text-sm text-destructive">
          Could not load signed-in devices: {error}
        </div>
        <Button
          type="button"
          size="sm"
          variant="outline"
          disabled={loading}
          onClick={() => void load()}
        >
          Retry
        </Button>
      </div>
    );

  if (loading && sessions.length === 0)
    return (
      <div className="flex items-center gap-2 text-sm text-muted-foreground">
        <Spinner className="size-4" /> Loading...
      </div>
    );

  return (
    <div className="space-y-4">
      {sessions.length === 0 ? (
        // No rows means no rows. A bare <ul> over two bulk buttons offered "Sign
        // out everywhere" as an action with nothing to sign out of, which reads
        // as a broken control rather than as an empty panel.
        <p className="text-sm text-muted-foreground">No signed-in devices.</p>
      ) : (
        <>
          <ul className="space-y-2">
            {sessions.map((s) => {
              // A session past its deadline is dead whatever the backend would
              // say about it: its access tokens are long expired and its refresh
              // family cannot mint another. Calling it Active would be the worst
              // thing this panel could say, so the deadline is compared here
              // rather than assumed from the fact that the row is listed.
              const expired = new Date(s.expiresAt).getTime() <= Date.now();
              // Named once and reused twice: the row and this button's accessible
              // name have to agree on which device is being talked about.
              const device = describeUserAgent(s.userAgent);
              return (
                <li
                  key={s.id}
                  className="flex items-center justify-between gap-3 rounded-lg border border-border px-3 py-2"
                >
                  <div className="min-w-0">
                    <div className="flex items-center gap-2 text-sm font-medium text-foreground">
                      <span className="truncate">{device}</span>
                      {s.current && <Badge variant="secondary">This device</Badge>}
                      <Badge variant={expired ? "outline" : "secondary"}>
                        {expired ? "Expired" : "Active"}
                      </Badge>
                    </div>
                    <div className="text-[13px] text-muted-foreground">
                      Last seen {timeAgo(s.lastSeen)}
                    </div>
                  </div>
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    disabled={busy}
                    // Without this, N rows are N identically-named buttons and a
                    // screen-reader user cannot tell which is which. The visible
                    // label stays "Sign out"; this is the name it answers to.
                    aria-label={`Sign out ${device}`}
                    onClick={() => setPending({ kind: "session", session: s })}
                  >
                    Sign out
                  </Button>
                </li>
              );
            })}
          </ul>

          <div className="flex flex-wrap items-center gap-2">
            <Button
              type="button"
              variant="outline"
              size="sm"
              disabled={busy}
              onClick={() => setPending({ kind: "others" })}
            >
              Sign out other devices
            </Button>
            <Button
              type="button"
              variant="destructive"
              size="sm"
              disabled={busy}
              onClick={() => setPending({ kind: "all" })}
            >
              Sign out everywhere
            </Button>
          </div>
        </>
      )}

      <AlertDialog
        open={pending !== null}
        onOpenChange={(open) => !open && setPending(null)}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              {pending?.kind === "others"
                ? "Sign out other devices?"
                : pending?.kind === "all"
                  ? "Sign out everywhere?"
                  : `Sign out ${
                      pending?.kind === "session"
                        ? describeUserAgent(pending.session.userAgent)
                        : ""
                    }?`}
            </AlertDialogTitle>
            <AlertDialogDescription>
              {pending?.kind === "all"
                ? "Every session ends, including this one, so you will be asked to sign in again. Do this if you think your account has been compromised."
                : pending?.kind === "others"
                  ? "Every other session ends immediately. This device stays signed in."
                  : "That session ends immediately and cannot be used again."}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              variant="destructive"
              onClick={() => void confirm()}
            >
              Sign out
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}
