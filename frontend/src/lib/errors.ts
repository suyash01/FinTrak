import { toast } from "sonner";

// toastApiError surfaces a failed request to the user instead of silently
// swallowing it. The API client attaches the backend message to Error.message,
// so prefer that and fall back to a generic label for non-Error throwables.
export function toastApiError(
  err: unknown,
  fallback = "Something went wrong. Please try again.",
): void {
  const message =
    err instanceof Error && err.message ? err.message : fallback;
  toast.error(message);
}
