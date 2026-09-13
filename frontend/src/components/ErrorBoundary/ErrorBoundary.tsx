import { Component, type ErrorInfo, type ReactNode } from "react";
import { AlertTriangle } from "lucide-react";
import { Button } from "@/components/ui/button";

interface ErrorBoundaryProps {
  children: ReactNode;
  fallback?: ReactNode;
}

interface ErrorBoundaryState {
  error: Error | null;
}

// ErrorBoundary is the top-level and per-route safety net: without it a single
// render error unmounts the whole tree (a white screen). It logs the error and
// shows a recoverable fallback instead.
export default class ErrorBoundary extends Component<
  ErrorBoundaryProps,
  ErrorBoundaryState
> {
  state: ErrorBoundaryState = { error: null };

  static getDerivedStateFromError(error: Error): ErrorBoundaryState {
    return { error };
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    console.error("Unhandled render error", error, info.componentStack);
  }

  private handleRetry = () => {
    this.setState({ error: null });
  };

  render() {
    if (this.state.error) {
      if (this.props.fallback) return this.props.fallback;
      return <AppErrorFallback onRetry={this.handleRetry} />;
    }
    return this.props.children;
  }
}

// AppErrorFallback is the full-screen variant shown when the app shell fails.
export function AppErrorFallback({ onRetry }: { onRetry?: () => void }) {
  return (
    <div className="min-h-screen flex items-center justify-center bg-background px-4">
      <div className="w-full max-w-sm text-center">
        <div className="mx-auto mb-4 flex h-12 w-12 items-center justify-center rounded-full bg-destructive/10 text-destructive">
          <AlertTriangle />
        </div>
        <h1 className="text-lg font-semibold text-foreground">
          Something went wrong
        </h1>
        <p className="mt-1 text-sm text-muted-foreground">
          An unexpected error occurred while rendering this page.
        </p>
        {onRetry && (
          <Button className="mt-5" onClick={onRetry}>
            Try again
          </Button>
        )}
      </div>
    </div>
  );
}

// PageErrorFallback is the contained variant shown when a single route fails,
// keeping the sidebar and shell mounted.
export function PageErrorFallback({ onRetry }: { onRetry?: () => void }) {
  return (
    <div className="flex-1 flex items-center justify-center px-4">
      <div className="w-full max-w-sm text-center">
        <div className="mx-auto mb-4 flex h-12 w-12 items-center justify-center rounded-full bg-destructive/10 text-destructive">
          <AlertTriangle />
        </div>
        <h1 className="text-lg font-semibold text-foreground">
          This page failed to load
        </h1>
        <p className="mt-1 text-sm text-muted-foreground">
          An unexpected error occurred while rendering this page.
        </p>
        {onRetry && (
          <Button className="mt-5" onClick={onRetry}>
            Try again
          </Button>
        )}
      </div>
    </div>
  );
}
