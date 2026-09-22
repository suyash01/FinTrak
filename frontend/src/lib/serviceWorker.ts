// Registers the offline shell worker (public/sw.js).
//
// Production only: the dev server serves modules from memory, has no hashed
// asset graph, and an installed worker there would outlive HMR and keep serving
// stale code. The shell is therefore verified against `bun run preview`, which
// serves the real build.

export function shouldRegisterServiceWorker(): boolean {
  return (
    import.meta.env.PROD &&
    typeof navigator !== "undefined" &&
    "serviceWorker" in navigator
  );
}

export function registerServiceWorker(): void {
  if (!shouldRegisterServiceWorker()) return;

  const register = () => {
    // Best effort: without the worker the app is still the SPA it was.
    navigator.serviceWorker.register("/sw.js").catch(() => {});
  };

  // Registering after load keeps worker installation off the critical path, but
  // a bundle evaluated after load (a late chunk) must not wait for an event that
  // already fired.
  if (document.readyState === "complete") register();
  else window.addEventListener("load", register);
}
