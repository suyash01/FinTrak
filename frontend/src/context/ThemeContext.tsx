import {
  createContext,
  useContext,
  useEffect,
  useState,
  type ReactNode,
} from "react";

export type ThemeMode = "light" | "dark" | "system";
export type AccentTheme =
  | "cyan"
  | "violet"
  | "emerald"
  | "amber"
  | "rose"
  | "zinc";

export const THEME_MODES: ThemeMode[] = ["light", "dark", "system"];
export const ACCENT_THEMES: AccentTheme[] = [
  "cyan",
  "violet",
  "emerald",
  "amber",
  "rose",
  "zinc",
];

export const ACCENT_COLORS: Record<AccentTheme, string> = {
  cyan: "#06b6d4",
  violet: "#8b5cf6",
  emerald: "#10b981",
  amber: "#f59e0b",
  rose: "#f43f5e",
  zinc: "#71717a",
};

interface StoredTheme {
  mode: ThemeMode;
  accent: AccentTheme;
}

interface ThemeContextValue {
  mode: ThemeMode;
  accent: AccentTheme;
  isDark: boolean;
  setMode: (mode: ThemeMode) => void;
  setAccent: (accent: AccentTheme) => void;
}

const STORAGE_KEY = "fintrak_theme";

// The installed app's toolbar follows the resolved mode: a manifest declares one
// theme_color, and a <meta> content value cannot read a CSS variable, so these
// mirror --background in index.css (:root and .dark).
const BACKGROUND_COLOR: Record<"light" | "dark", string> = {
  light: "#f8fafc",
  dark: "#020618",
};

const ThemeContext = createContext<ThemeContextValue | undefined>(undefined);

function readStored(): StoredTheme {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (raw) {
      const parsed = JSON.parse(raw) as Partial<StoredTheme>;
      return {
        mode: THEME_MODES.includes(parsed.mode as ThemeMode)
          ? (parsed.mode as ThemeMode)
          : "system",
        accent: ACCENT_THEMES.includes(parsed.accent as AccentTheme)
          ? (parsed.accent as AccentTheme)
          : "cyan",
      };
    }
  } catch {
    // ignore corrupt storage
  }
  return { mode: "system", accent: "cyan" };
}

export function ThemeProvider({ children }: { children: ReactNode }) {
  // Read persisted values once via lazy initializers; calling readStored() with
  // an eager useRef argument ran it on every render.
  const [mode, setModeState] = useState<ThemeMode>(() => readStored().mode);
  const [accent, setAccentState] = useState<AccentTheme>(
    () => readStored().accent,
  );
  const [systemDark, setSystemDark] = useState<boolean>(
    () => window.matchMedia("(prefers-color-scheme: dark)").matches,
  );

  useEffect(() => {
    const mq = window.matchMedia("(prefers-color-scheme: dark)");
    const onChange = (e: MediaQueryListEvent) => setSystemDark(e.matches);
    mq.addEventListener("change", onChange);
    return () => mq.removeEventListener("change", onChange);
  }, []);

  const isDark =
    mode === "dark" || (mode === "system" && systemDark);

  useEffect(() => {
    const root = document.documentElement;
    root.classList.toggle("dark", isDark);
    root.dataset.theme = accent;
    try {
      localStorage.setItem(STORAGE_KEY, JSON.stringify({ mode, accent }));
    } catch {
      // A full or blocked quota must not take the app down: the write happens
      // during commit, so throwing here unmounts to the ErrorBoundary and
      // replaces the whole UI. The in-memory theme stays, like the reader
      // (readStored) tolerates a missing value.
    }
    document
      .querySelector('meta[name="theme-color"]')
      ?.setAttribute(
        "content",
        isDark ? BACKGROUND_COLOR.dark : BACKGROUND_COLOR.light,
      );
  }, [mode, accent, isDark]);

  return (
    <ThemeContext.Provider
      value={{
        mode,
        accent,
        isDark,
        setMode: setModeState,
        setAccent: setAccentState,
      }}
    >
      {children}
    </ThemeContext.Provider>
  );
}

export function useTheme(): ThemeContextValue {
  const context = useContext(ThemeContext);
  if (context === undefined) {
    throw new Error("useTheme must be used within a ThemeProvider");
  }
  return context;
}