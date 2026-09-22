import {
  createContext,
  useContext,
  useState,
  useEffect,
  type ReactNode,
} from "react";

interface SettingsContextValue {
  compactLayout: boolean;
  setCompactLayout: (value: boolean) => void;
  toggleCompactLayout: () => void;
}

const SettingsContext = createContext<SettingsContextValue | undefined>(
  undefined,
);

const COMPACT_LAYOUT_KEY = "compactLayout";

// readStoredCompactLayout tolerates a missing or corrupt localStorage value and
// always yields a boolean, so a bad value can't throw during render (which would
// blank the whole app). Mirrors ThemeContext.readStored.
function readStoredCompactLayout(): boolean {
  try {
    const saved = localStorage.getItem(COMPACT_LAYOUT_KEY);
    if (saved === null) return true; // Default to true
    const parsed = JSON.parse(saved);
    return typeof parsed === "boolean" ? parsed : true;
  } catch {
    return true;
  }
}

export function SettingsProvider({ children }: { children: ReactNode }) {
  const [compactLayout, setCompactLayout] = useState<boolean>(
    readStoredCompactLayout,
  );

  useEffect(() => {
    try {
      localStorage.setItem(COMPACT_LAYOUT_KEY, JSON.stringify(compactLayout));
    } catch {
      // Mirrors readStoredCompactLayout: a full or blocked quota keeps the
      // in-memory value instead of throwing during commit, which would unmount
      // the app to its error boundary.
    }
  }, [compactLayout]);

  const toggleCompactLayout = () => setCompactLayout((prev) => !prev);

  return (
    <SettingsContext.Provider
      value={{ compactLayout, setCompactLayout, toggleCompactLayout }}
    >
      {children}
    </SettingsContext.Provider>
  );
}

export function useSettings(): SettingsContextValue {
  const context = useContext(SettingsContext);
  if (context === undefined) {
    throw new Error("useSettings must be used within a SettingsProvider");
  }
  return context;
}
