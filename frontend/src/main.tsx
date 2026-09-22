import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import App from "./App";
import { prefetchOfflineRoutes } from "./lib/prefetchRoutes";
import { registerServiceWorker } from "./lib/serviceWorker";

registerServiceWorker();
prefetchOfflineRoutes();

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
