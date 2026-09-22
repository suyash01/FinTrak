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

// OfflineBanner is where the offline layer speaks to the user: that the app is
// showing saved data rather than live data, how many manual entries are still
// waiting to be sent, and what to do about the ones the server rejected. It
// renders nothing while the app is online with an empty outbox.
export default function OfflineBanner() {
  const { online, servedFromCache, pending, syncing, sync, discardFailed } =
    useOffline();

  const failed = pending.filter((entry) => entry.error !== undefined).length;
  const waiting = pending.length - failed;
  const showingSaved = !online || servedFromCache;

  if (!showingSaved && pending.length === 0) return null;

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
                    Discard {failed} unsent transaction{failed === 1 ? "" : "s"}?
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
      </span>
    </div>
  );
}
