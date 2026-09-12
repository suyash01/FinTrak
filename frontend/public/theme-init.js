// Applies the persisted theme before React mounts to avoid a flash of the
// wrong color scheme. Kept as an external file (rather than an inline script)
// so the production Content-Security-Policy can disallow inline scripts.
(function () {
  try {
    var raw = localStorage.getItem("fintrak_theme");
    var stored = raw ? JSON.parse(raw) : {};
    var modes = ["light", "dark", "system"];
    var mode = modes.indexOf(stored.mode) !== -1 ? stored.mode : "system";
    var dark =
      mode === "dark" ||
      (mode === "system" &&
        window.matchMedia("(prefers-color-scheme: dark)").matches);
    var root = document.documentElement;
    root.classList.toggle("dark", dark);
    if (stored.accent) root.dataset.theme = stored.accent;
  } catch (e) {}
})();
