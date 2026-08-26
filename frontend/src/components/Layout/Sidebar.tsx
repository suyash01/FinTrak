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
} from "lucide-react";
import packageJson from "../../../package.json";
import { useAuth } from "../../context/AuthContext";
import api from "../../api/client";
import { useState, useEffect } from "react";
import { Button } from "@/components/ui/button";

export default function Sidebar() {
  const { user, logout } = useAuth();
  const navigate = useNavigate();
  const location = useLocation();
  const [paperlessEnabled, setPaperlessEnabled] = useState(false);

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
    `flex items-center gap-3 py-2.5 px-3.5 rounded-lg no-underline text-sm font-medium cursor-pointer transition-all border ${
      isActive
        ? "bg-primary/10 text-primary border-primary/20"
        : "text-muted-foreground border-transparent hover:bg-accent hover:text-foreground"
    }`;

  return (
    <aside className="w-65 bg-sidebar border-r border-sidebar-border flex flex-col shrink-0 relative z-50 transition-all duration-300">
      <div className="p-5 border-b border-sidebar-border flex items-center gap-3">
        <div className="w-9 h-9 bg-linear-to-br from-primary to-violet-500 rounded-lg flex items-center justify-center text-white font-extrabold text-base shrink-0">
          F
        </div>
        <span className="text-xl font-bold bg-linear-to-br from-primary to-violet-500 bg-clip-text text-transparent">
          FinTrak
        </span>
      </div>
      <nav className="flex-1 p-3 overflow-y-auto flex flex-col gap-0.5">
        <div className="text-[11px] font-semibold uppercase tracking-widest text-muted-foreground px-3 pt-4 pb-2">
          Overview
        </div>
        <NavLink to="/" end className={navLinkClass}>
          <LayoutDashboard className="w-4.5 h-4.5 shrink-0" /> Dashboard
        </NavLink>

        <div className="text-[11px] font-semibold uppercase tracking-widest text-muted-foreground px-3 pt-4 pb-2">
          Manage
        </div>
        <NavLink to="/import" className={navLinkClass}>
          <Upload className="w-4.5 h-4.5 shrink-0" /> Import
        </NavLink>
        {paperlessEnabled && (
          <NavLink to="/paperless" className={navLinkClass}>
            <FileText className="w-4.5 h-4.5 shrink-0" /> Paperless
          </NavLink>
        )}
        <NavLink to="/transactions" className={navLinkClass}>
          <List className="w-4.5 h-4.5 shrink-0" /> Transactions
        </NavLink>
        <NavLink to="/accounts" className={navLinkClass}>
          <Wallet className="w-4.5 h-4.5 shrink-0" /> Accounts
        </NavLink>

        <div className="text-[11px] font-semibold uppercase tracking-widest text-muted-foreground px-3 pt-4 pb-2">
          Organize
        </div>
        <NavLink to="/categories" className={navLinkClass}>
          <Tags className="w-4.5 h-4.5 shrink-0" /> Categories & Rules
        </NavLink>
        <NavLink to="/payees" className={navLinkClass}>
          <Users className="w-4.5 h-4.5 shrink-0" /> Payees
        </NavLink>
        <NavLink to="/linking" className={navLinkClass}>
          <ArrowLeftRight className="w-4.5 h-4.5 shrink-0" /> Transfers &
          Cashbacks
        </NavLink>

        <div className="text-[11px] font-semibold uppercase tracking-widest text-muted-foreground px-3 pt-4 pb-2">
          System
        </div>
        <NavLink to="/settings" className={navLinkClass}>
          <Settings className="w-4.5 h-4.5 shrink-0" /> Settings
        </NavLink>
      </nav>
      <div className="p-4 border-t border-sidebar-border space-y-3">
        <div className="text-xs text-muted-foreground truncate">
          {user?.email}
        </div>
        <div className="flex items-center justify-between">
          <div className="flex items-center gap-2">
            <Sparkles size={14} className="text-primary" />
            <span className="text-xs text-muted-foreground">
              FinTrak v{packageJson.version}
            </span>
          </div>
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