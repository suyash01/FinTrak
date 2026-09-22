import { toast } from "sonner";
import { isNetworkError } from "../api/errors";

// toastApiError surfaces a failed request to the user instead of silently
// swallowing it. The API client attaches the backend message to Error.message,
// so prefer that and fall back to a generic label for non-Error throwables.
//
// A request that never reached the server while the browser reports no
// connection is not worth reporting: the offline banner already says the app is
// offline, and a toast on every page load would drown the one message that
// matters. A transport failure while online (a server that is down) still
// surfaces.
export function toastApiError(
  err: unknown,
  fallback = "Something went wrong. Please try again.",
): void {
  if (isNetworkError(err) && navigator.onLine === false) return;

  const message =
    err instanceof Error && err.message ? err.message : fallback;
  toast.error(message);
}
