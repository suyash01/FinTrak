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

export function SettingsProvider({ children }: { children: ReactNode }) {
  const [compactLayout, setCompactLayout] = useState<boolean>(() => {
    const saved = localStorage.getItem("compactLayout");
    return saved !== null ? JSON.parse(saved) : true; // Default to true
  });

  useEffect(() => {
    localStorage.setItem("compactLayout", JSON.stringify(compactLayout));
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
