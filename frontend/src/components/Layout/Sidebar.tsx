import { NavLink, useNavigate, useLocation } from "react-router-dom";
import {
  LayoutDashboard,
  Upload,
  List,
  Tags,
  ArrowLeftRight,
  Settings,
  Wallet,
  Sparkles,
  Users,
  LogOut,
  FileText,
  PanelLeftClose,
  PanelLeftOpen,
} from "lucide-react";
import packageJson from "../../../package.json";
import { useAuth } from "../../context/AuthContext";
import api from "../../api/client";
import { useState, useEffect } from "react";
import { Button } from "@/components/ui/button";

const SMALL_SCREEN_QUERY = "(max-width: 767px)";

export default function Sidebar() {
  const { user, logout } = useAuth();
  const navigate = useNavigate();
  const location = useLocation();
  const [paperlessEnabled, setPaperlessEnabled] = useState(false);
  // Collapsed by default on small screens, expanded on larger screens.
  const [collapsed, setCollapsed] = useState(
    () => window.matchMedia(SMALL_SCREEN_QUERY).matches,
  );

  // Sync with the breakpoint so a small screen stays collapsed and a large
  // screen stays expanded unless the user manually overrides with the toggle.
  useEffect(() => {
    const mq = window.matchMedia(SMALL_SCREEN_QUERY);
    const onChange = (e: MediaQueryListEvent) => setCollapsed(e.matches);
    mq.addEventListener("change", onChange);
    return () => mq.removeEventListener("change", onChange);
  }, []);

  // Re-check Paperless config on mount and whenever the route changes so a
  // newly-saved setting is picked up without a full page reload.
  useEffect(() => {
    api
      .getPaperlessSettings()
      .then((s) =>
        setPaperlessEnabled(Boolean(s.paperlessUrl && s.hasToken)),
      )
      .catch(() => setPaperlessEnabled(false));
  }, [location.pathname]);

  const handleLogout = () => {
    logout();
    navigate("/login");
  };

  const navLinkClass = ({ isActive }: { isActive: boolean }) =>
    `flex items-center gap-3 py-2.5 rounded-lg no-underline text-sm font-medium cursor-pointer transition-all border ${
      collapsed ? "justify-center px-2" : "px-3.5"
    } ${
      isActive
        ? "bg-primary/10 text-primary border-primary/20"
        : "text-muted-foreground border-transparent hover:bg-accent hover:text-foreground"
    }`;

  const sectionClass = `text-[11px] font-semibold uppercase tracking-widest text-muted-foreground px-3 pt-4 pb-2 ${
    collapsed ? "hidden" : ""
  }`;

  return (
    <aside
      className={`${
        collapsed ? "w-16" : "w-65"
      } bg-sidebar border-r border-sidebar-border flex flex-col shrink-0 relative z-50 transition-all duration-300`}
    >
      <div
        className={`p-5 border-b border-sidebar-border flex items-center ${
          collapsed ? "justify-center" : "gap-3"
        }`}
      >
        <div className="w-9 h-9 bg-linear-to-br from-primary to-violet-500 rounded-lg flex items-center justify-center text-white font-extrabold text-base shrink-0">
          F
        </div>
        {!collapsed && (
          <span className="text-xl font-bold bg-linear-to-br from-primary to-violet-500 bg-clip-text text-transparent">
            FinTrak
          </span>
        )}
        {!collapsed ? (
          <Button
            variant="ghost"
            size="icon-sm"
            onClick={() => setCollapsed((c) => !c)}
            title={collapsed ? "Expand sidebar" : "Collapse sidebar"}
            className="text-muted-foreground hover:text-foreground ml-auto -mr-1.5"
          >
            <PanelLeftClose />
          </Button>
        ) : (
          <Button
            variant="ghost"
            size="icon-sm"
            onClick={() => setCollapsed((c) => !c)}
            title="Expand sidebar"
            className="text-muted-foreground hover:text-foreground"
          >
            <PanelLeftOpen />
          </Button>
        )}
      </div>
      <nav className="flex-1 p-3 overflow-y-auto flex flex-col gap-0.5">
        <div className={sectionClass}>Overview</div>
        <NavLink to="/" end className={navLinkClass} title="Dashboard">
          <LayoutDashboard className="w-4.5 h-4.5 shrink-0" />
          {!collapsed && <span>Dashboard</span>}
        </NavLink>

        <div className={sectionClass}>Manage</div>
        <NavLink to="/import" className={navLinkClass} title="Import">
          <Upload className="w-4.5 h-4.5 shrink-0" />
          {!collapsed && <span>Import</span>}
        </NavLink>
        {paperlessEnabled && (
          <NavLink to="/paperless" className={navLinkClass} title="Paperless">
            <FileText className="w-4.5 h-4.5 shrink-0" />
            {!collapsed && <span>Paperless</span>}
          </NavLink>
        )}
        <NavLink
          to="/transactions"
          className={navLinkClass}
          title="Transactions"
        >
          <List className="w-4.5 h-4.5 shrink-0" />
          {!collapsed && <span>Transactions</span>}
        </NavLink>
        <NavLink to="/accounts" className={navLinkClass} title="Accounts">
          <Wallet className="w-4.5 h-4.5 shrink-0" />
          {!collapsed && <span>Accounts</span>}
        </NavLink>

        <div className={sectionClass}>Organize</div>
        <NavLink to="/categories" className={navLinkClass} title="Categories & Rules">
          <Tags className="w-4.5 h-4.5 shrink-0" />
          {!collapsed && <span>Categories & Rules</span>}
        </NavLink>
        <NavLink to="/payees" className={navLinkClass} title="Payees">
          <Users className="w-4.5 h-4.5 shrink-0" />
          {!collapsed && <span>Payees</span>}
        </NavLink>
        <NavLink
          to="/linking"
          className={navLinkClass}
          title="Transfers & Cashbacks"
        >
          <ArrowLeftRight className="w-4.5 h-4.5 shrink-0" />
          {!collapsed && <span>Transfers & Cashbacks</span>}
        </NavLink>

        <div className={sectionClass}>System</div>
        <NavLink to="/settings" className={navLinkClass} title="Settings">
          <Settings className="w-4.5 h-4.5 shrink-0" />
          {!collapsed && <span>Settings</span>}
        </NavLink>
      </nav>
      <div
        className={`p-4 border-t border-sidebar-border space-y-3 ${
          collapsed ? "flex flex-col items-center" : ""
        }`}
      >
        {!collapsed && (
          <div className="text-xs text-muted-foreground truncate">
            {user?.email}
          </div>
        )}
        <div
          className={`flex items-center ${
            collapsed ? "flex-col gap-3" : "justify-between"
          }`}
        >
          {!collapsed && (
            <div className="flex items-center gap-2">
              <Sparkles size={14} className="text-primary" />
              <span className="text-xs text-muted-foreground">
                FinTrak v{packageJson.version}
              </span>
            </div>
          )}
          <Button
            variant="ghost"
            size="icon-sm"
            onClick={handleLogout}
            title="Log out"
            className="text-muted-foreground hover:text-destructive"
          >
            <LogOut />
          </Button>
        </div>
      </div>
    </aside>
  );
}