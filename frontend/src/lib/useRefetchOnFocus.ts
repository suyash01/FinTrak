import { useEffect, useRef } from "react";

// useRefetchOnFocus runs `callback` whenever the tab regains focus or becomes
// visible again. Aggregate pages (money flow, calendar, dashboard) show
// server-computed data with no client cache, so without this they stay stale
// after the user edits transactions in another tab.
export function useRefetchOnFocus(callback: () => void, enabled = true): void {
  const callbackRef = useRef(callback);
  useEffect(() => {
    callbackRef.current = callback;
  }, [callback]);

  useEffect(() => {
    if (!enabled) return;
    const onFocus = () => callbackRef.current();
    const onVisibility = () => {
      if (document.visibilityState === "visible") callbackRef.current();
    };
    window.addEventListener("focus", onFocus);
    document.addEventListener("visibilitychange", onVisibility);
    return () => {
      window.removeEventListener("focus", onFocus);
      document.removeEventListener("visibilitychange", onVisibility);
    };
  }, [enabled]);
}
