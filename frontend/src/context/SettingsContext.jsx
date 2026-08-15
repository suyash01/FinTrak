import { createContext, useContext, useState, useEffect } from 'react';

const SettingsContext = createContext();

export function SettingsProvider({ children }) {
  const [compactLayout, setCompactLayout] = useState(() => {
    const saved = localStorage.getItem('compactLayout');
    return saved !== null ? JSON.parse(saved) : true; // Default to true
  });

  useEffect(() => {
    localStorage.setItem('compactLayout', JSON.stringify(compactLayout));
  }, [compactLayout]);

  const toggleCompactLayout = () => setCompactLayout(prev => !prev);

  return (
    <SettingsContext.Provider value={{ compactLayout, setCompactLayout, toggleCompactLayout }}>
      {children}
    </SettingsContext.Provider>
  );
}

export function useSettings() {
  const context = useContext(SettingsContext);
  if (context === undefined) {
    throw new Error('useSettings must be used within a SettingsProvider');
  }
  return context;
}
