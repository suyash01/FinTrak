import { CloudOff, RefreshCw, TriangleAlert } from "lucide-react";
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
import { useOffline } from "@/context/OfflineContext";

export interface OfflineBannerProps {
  // onOpenConflicts opens the dialog the app mounts for the held entries. It is
  // a prop rather than state this component owns because the repo's rule for an
  // overlay is that the parent holds its open state — and it is required rather
  // than optional precisely so that a caller who forgets it is a compile error
  // instead of a button that reaches nothing, which is how this surface stood
  // while the banner was rendered with no props at all.
  onOpenConflicts: () => void;
}

// OfflineBanner is where the offline layer speaks to the user: that the app is
// showing saved data rather than live data, and what is in the queue — waiting
// to be sent, refused by the server, or waiting on the user to decide. It
// renders nothing while the app is online with an empty outbox.
export default function OfflineBanner({ onOpenConflicts }: OfflineBannerProps) {
  const {
    online,
    servedFromCache,
    pending,
    conflicts,
    syncing,
    sync,
    discardFailed,
  } = useOffline();

  const failed = pending.filter((entry) => entry.error !== undefined).length;
  const held = conflicts.length;
  // The three partition the queue rather than each reading all of it. A held
  // entry is still queued — that is what lets a conflict outlive a reload — but
  // nothing will send it until the user answers: a flush that tried re-reads the
  // server's row, finds the same conflict, and holds the same question again.
  // Counting it as waiting would offer a "Sync now" that cannot do what it says.
  // Recording a hold clears the entry's rejection (outbox's `hold`), so no entry
  // is in two of these buckets.
  const waiting = pending.length - failed - held;
  const showingSaved = !online || servedFromCache;

  // The sum rather than `pending.length`, so the guard asks the question the
  // banner actually answers — is there anything here to say — instead of relying
  // on a held entry also being queued.
  if (!showingSaved && waiting + failed + held === 0) return null;

  return (
    <div
      role="status"
      className="flex flex-wrap items-center gap-x-3 gap-y-1.5 border-b border-border bg-muted px-4 py-1.5 text-xs text-muted-foreground"
    >
      {showingSaved && (
        <span className="inline-flex items-center gap-1.5">
          <CloudOff className="size-3.5 shrink-0" aria-hidden="true" />
          {online
            ? "Showing saved data — the server could not be reached"
            : "Offline — showing the data you last loaded"}
        </span>
      )}

      {waiting > 0 && (
        <span className="inline-flex items-center gap-1.5">
          <RefreshCw
            className={syncing ? "size-3.5 shrink-0 animate-spin" : "size-3.5 shrink-0"}
            aria-hidden="true"
          />
          {waiting} waiting to sync
        </span>
      )}

      {failed > 0 && (
        <span
          className="inline-flex items-center gap-1.5 text-destructive"
          title={pending.find((entry) => entry.error !== undefined)?.error}
        >
          <TriangleAlert className="size-3.5 shrink-0" aria-hidden="true" />
          {failed} rejected by the server
        </span>
      )}

      {held > 0 && (
        <span className="inline-flex items-center gap-1.5 text-destructive">
          <TriangleAlert className="size-3.5 shrink-0" aria-hidden="true" />
          {held} need{held === 1 ? "s" : ""} your attention
        </span>
      )}

      {/* The actions follow the counters they answer, so the row reads left to
          right. */}
      <span className="ml-auto inline-flex items-center gap-2">
        {waiting > 0 && (
          <Button
            variant="outline"
            size="xs"
            disabled={!online || syncing}
            onClick={() => void sync()}
          >
            {syncing ? "Syncing…" : "Sync now"}
          </Button>
        )}

        {failed > 0 && (
          <>
            <Button
              variant="outline"
              size="xs"
              disabled={!online || syncing}
              onClick={() => void sync({ retryFailed: true })}
            >
              Retry
            </Button>
            <AlertDialog>
              <AlertDialogTrigger asChild>
                <Button variant="destructive" size="xs">
                  Discard
                </Button>
              </AlertDialogTrigger>
              <AlertDialogContent>
                <AlertDialogHeader>
                  <AlertDialogTitle>
                    Discard {failed} unsent write{failed === 1 ? "" : "s"}?
                  </AlertDialogTitle>
                  <AlertDialogDescription>
                    The server rejected {failed === 1 ? "it" : "them"} and
                    retrying has not helped. Discarding removes{" "}
                    {failed === 1 ? "it" : "them"} from this device for good.
                  </AlertDialogDescription>
                </AlertDialogHeader>
                <AlertDialogFooter>
                  <AlertDialogCancel>Cancel</AlertDialogCancel>
                  <AlertDialogAction onClick={discardFailed}>
                    Discard
                  </AlertDialogAction>
                </AlertDialogFooter>
              </AlertDialogContent>
            </AlertDialog>
          </>
        )}

        {held > 0 && (
          // Not disabled offline, unlike the two beside it: recording the
          // answer is a write to this device's own queue, and the values the
          // user is choosing between are already in it. A decision they can
          // only make once they are back online is a decision the offline
          // layer is not actually offering them.
          <Button variant="outline" size="xs" onClick={onOpenConflicts}>
            Resolve
          </Button>
        )}
      </span>
    </div>
  );
}
