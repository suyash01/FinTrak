import { NavLink } from 'react-router-dom';
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
} from 'lucide-react';
import packageJson from '../../../package.json';

export default function Sidebar() {
  const navLinkClass = ({ isActive }) =>
    `flex items-center gap-3 py-2.5 px-3.5 rounded-lg no-underline text-sm font-medium cursor-pointer transition-all border ${isActive
      ? 'bg-cyan-500/10 text-cyan-400 border-cyan-500/20'
      : 'text-slate-400 border-transparent hover:bg-slate-800 hover:text-slate-200'
    }`;

  return (
    <aside className="w-[260px] bg-slate-900 border-r border-slate-800 flex flex-col shrink-0 relative z-50 transition-all duration-300">
      <div className="p-5 border-b border-slate-800 flex items-center gap-3">
        <div className="w-9 h-9 bg-linear-to-br from-cyan-500 to-violet-500 rounded-lg flex items-center justify-center text-white font-extrabold text-base shrink-0">F</div>
        <span className="text-xl font-bold bg-linear-to-br from-cyan-500 to-violet-500 bg-clip-text text-transparent">FinTrak</span>
      </div>
      <nav className="flex-1 p-3 overflow-y-auto flex flex-col gap-0.5">
        <div className="text-[11px] font-semibold uppercase tracking-widest text-slate-500 px-3 pt-4 pb-2">Overview</div>
        <NavLink to="/" end className={navLinkClass}>
          <LayoutDashboard className="w-[18px] h-[18px] shrink-0" /> Dashboard
        </NavLink>

        <div className="text-[11px] font-semibold uppercase tracking-widest text-slate-500 px-3 pt-4 pb-2">Manage</div>
        <NavLink to="/import" className={navLinkClass}>
          <Upload className="w-[18px] h-[18px] shrink-0" /> Import
        </NavLink>
        <NavLink to="/transactions" className={navLinkClass}>
          <List className="w-[18px] h-[18px] shrink-0" /> Transactions
        </NavLink>
        <NavLink to="/accounts" className={navLinkClass}>
          <Wallet className="w-[18px] h-[18px] shrink-0" /> Accounts
        </NavLink>

        <div className="text-[11px] font-semibold uppercase tracking-widest text-slate-500 px-3 pt-4 pb-2">Organize</div>
        <NavLink to="/categories" className={navLinkClass}>
          <Tags className="w-[18px] h-[18px] shrink-0" /> Categories & Rules
        </NavLink>
        <NavLink to="/payees" className={navLinkClass}>
          <Users className="w-[18px] h-[18px] shrink-0" /> Payees
        </NavLink>
        <NavLink to="/linking" className={navLinkClass}>
          <ArrowLeftRight className="w-[18px] h-[18px] shrink-0" /> Transfers & Cashbacks
        </NavLink>

        <div className="text-[11px] font-semibold uppercase tracking-widest text-slate-500 px-3 pt-4 pb-2">System</div>
        <NavLink to="/settings" className={navLinkClass}>
          <Settings className="w-[18px] h-[18px] shrink-0" /> Settings
        </NavLink>
      </nav>
      <div className="p-4 flex items-center gap-2 border-t border-slate-800">
        <Sparkles size={14} className="text-cyan-500" />
        <span className="text-xs text-slate-500">FinTrak v{packageJson.version}</span>
      </div>
    </aside>
  );
}
