import {
  createContext,
  useContext,
  useEffect,
  useRef,
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
  const initial = useRef<StoredTheme>(readStored()).current;
  const [mode, setModeState] = useState<ThemeMode>(initial.mode);
  const [accent, setAccentState] = useState<AccentTheme>(initial.accent);
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
    localStorage.setItem(STORAGE_KEY, JSON.stringify({ mode, accent }));
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