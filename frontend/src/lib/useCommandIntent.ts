import { useEffect, useRef } from "react";
import { useLocation, useNavigate } from "react-router-dom";

// Intents the command palette can hand off to a page it navigates to. The
// palette dispatches one via react-router location state; the target page
// consumes it on mount (or while already mounted) with useCommandIntent.
export type CommandIntent =
  | "new-transaction"
  | "new-account"
  | "new-category"
  | "new-group"
  | "new-rule"
  | "new-payee"
  | "new-recurring";

interface CommandIntentState {
  command?: CommandIntent;
}

// Runs `onIntent` exactly once whenever this page is the target of a command
// intent, then clears the intent from history so a reload or back/forward does
// not re-open the dialog.
export function useCommandIntent(intent: CommandIntent, onIntent: () => void) {
  const location = useLocation();
  const navigate = useNavigate();
  const callbackRef = useRef(onIntent);
  callbackRef.current = onIntent;

  const state = location.state as CommandIntentState | null;
  const active = state?.command === intent;

  useEffect(() => {
    if (!active) return;
    callbackRef.current();
    navigate(location.pathname + location.search, {
      replace: true,
      state: null,
    });
  }, [active, location.pathname, location.search, navigate]);

  return active;
}
