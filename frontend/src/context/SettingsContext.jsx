import { createContext, useContext, useState, useEffect } from 'react';

const SettingsContext = createContext();

export function SettingsProvider({ children }) {
  const [compactLayout, setCompactLayout] = useState(() => {
    const saved = localStorage.getItem('compactLayout');
    return saved !== null ? JSON.parse(saved) : true; // Default to true
  });

  const [pageSize, setPageSize] = useState(() => {
    const saved = localStorage.getItem('pageSize');
    return saved !== null ? Number(saved) : 50; // Default to 50, 0 = no pagination
  });

  useEffect(() => {
    localStorage.setItem('compactLayout', JSON.stringify(compactLayout));
  }, [compactLayout]);

  useEffect(() => {
    localStorage.setItem('pageSize', String(pageSize));
  }, [pageSize]);

  const toggleCompactLayout = () => setCompactLayout(prev => !prev);

  return (
    <SettingsContext.Provider value={{ compactLayout, setCompactLayout, toggleCompactLayout, pageSize, setPageSize }}>
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
