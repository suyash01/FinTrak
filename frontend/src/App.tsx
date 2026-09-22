import { BrowserRouter, Routes, Route, useLocation } from "react-router-dom";
import { lazy, Suspense, useState } from "react";
import Sidebar from "./components/Layout/Sidebar";
import CommandPalette from "./components/CommandPalette/CommandPalette";
import ErrorBoundary, {
  PageErrorFallback,
} from "./components/ErrorBoundary/ErrorBoundary";
import { DomainDataProvider } from "./context/DomainDataContext";
import { OfflineProvider } from "./context/OfflineContext";
import { SettingsProvider } from "./context/SettingsContext";
import { AuthProvider, useAuth } from "./context/AuthContext";
import { ThemeProvider } from "./context/ThemeContext";
import OfflineBanner from "./components/Layout/OfflineBanner";
import { Toaster } from "@/components/ui/sonner";
import { Spinner } from "@/components/ui/spinner";
import "./index.css";

// Route-level code splitting: each page is loaded on demand so the login screen
// doesn't pull in the charting, CSV and virtualization libraries.
const Dashboard = lazy(() => import("./components/Dashboard/Dashboard"));
const MoneyFlow = lazy(() => import("./components/MoneyFlow/MoneyFlow"));
const CashFlowCalendar = lazy(
  () => import("./components/CashFlowCalendar/CashFlowCalendar"),
);
const Import = lazy(() => import("./components/Import/Import"));
const PaperlessImport = lazy(
  () => import("./components/PaperlessImport/PaperlessImport"),
);
const Transactions = lazy(
  () => import("./components/Transactions/Transactions"),
);
const Accounts = lazy(() => import("./components/Accounts/Accounts"));
const Categories = lazy(() => import("./components/Categories/Categories"));
const Payees = lazy(() => import("./components/Payees/Payees"));
const Linking = lazy(() => import("./components/Linking/Linking"));
const Recurring = lazy(() => import("./components/Recurring/Recurring"));
const Settings = lazy(() => import("./components/Settings/Settings"));
const Login = lazy(() => import("./components/Auth/Login"));

export default function App() {
  return (
    <ErrorBoundary>
      <ThemeProvider>
        <BrowserRouter>
          <SettingsProvider>
            <AuthProvider>
              <Root />
            </AuthProvider>
          </SettingsProvider>
        </BrowserRouter>
        <Toaster />
      </ThemeProvider>
    </ErrorBoundary>
  );
}

function RouteFallback() {
  return (
    <div className="min-h-screen flex items-center justify-center bg-background">
      <div
        role="status"
        aria-label="Loading"
        className="h-8 w-8 animate-spin rounded-full border-2 border-border border-t-primary"
      />
    </div>
  );
}

function PageFallback() {
  return (
    <div className="flex-1 flex items-center justify-center">
      <Spinner className="size-8 text-primary" />
    </div>
  );
}

function Root() {
  const { isAuthenticated, initializing } = useAuth();
  const location = useLocation();
  const [commandOpen, setCommandOpen] = useState(false);

  // Wait for the session cookie to be verified before choosing the auth screen,
  // otherwise a locked-down reload would briefly render the login page.
  if (initializing) {
    return <RouteFallback />;
  }

  if (!isAuthenticated) {
    return (
      <Suspense fallback={<RouteFallback />}>
        <Routes>
          <Route path="*" element={<Login />} />
        </Routes>
      </Suspense>
    );
  }

  return (
    <DomainDataProvider>
      <OfflineProvider>
        <div className="flex h-screen w-screen overflow-hidden">
          <Sidebar onOpenCommandPalette={() => setCommandOpen(true)} />
          <main className="flex-1 flex flex-col overflow-hidden min-w-0">
            <OfflineBanner />
            <ErrorBoundary
              key={location.pathname}
              fallback={<PageErrorFallback onRetry={() => window.location.reload()} />}
            >
              <Suspense fallback={<PageFallback />}>
                <Routes>
                  <Route path="/" element={<Dashboard />} />
                  <Route path="/money-flow" element={<MoneyFlow />} />
                  <Route
                    path="/cash-flow-calendar"
                    element={<CashFlowCalendar />}
                  />
                  <Route path="/import" element={<Import />} />
                  <Route path="/paperless" element={<PaperlessImport />} />
                  <Route path="/transactions" element={<Transactions />} />
                  <Route path="/accounts" element={<Accounts />} />
                  <Route path="/categories" element={<Categories />} />
                  <Route path="/payees" element={<Payees />} />
                  <Route path="/linking" element={<Linking />} />
                  <Route path="/recurring" element={<Recurring />} />
                  <Route path="/settings" element={<Settings />} />
                  <Route path="*" element={<Dashboard />} />
                </Routes>
              </Suspense>
            </ErrorBoundary>
          </main>
        </div>
        <CommandPalette open={commandOpen} onOpenChange={setCommandOpen} />
      </OfflineProvider>
    </DomainDataProvider>
  );
}
