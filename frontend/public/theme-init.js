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
    // Density, same reason and same file: the primitives size themselves from
    // --density-*, so the attribute has to be on <html> before first paint or
    // every control renders one size and then jumps. Mirrors
    // readStoredCompactLayout — anything but a boolean is compact.
    var compact = true;
    try {
      var parsed = JSON.parse(localStorage.getItem("compactLayout"));
      if (typeof parsed === "boolean") compact = parsed;
    } catch (e) {}
    root.dataset.density = compact ? "compact" : "comfortable";
  } catch (e) {}
})();
