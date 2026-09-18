import { useEffect } from "react";
import { useNavigate } from "react-router-dom";
import {
  ArrowLeftRight,
  CalendarDays,
  FileText,
  LayoutDashboard,
  List,
  Moon,
  Play,
  PlusCircle,
  Repeat,
  Settings,
  Sun,
  Tags,
  Upload,
  Users,
  Wallet,
  Waypoints,
} from "lucide-react";
import api from "../../api/client";
import { useDomainData } from "../../context/DomainDataContext";
import { useTheme } from "../../context/ThemeContext";
import type { CommandIntent } from "../../lib/useCommandIntent";
import {
  Command,
  CommandDialog,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
  CommandSeparator,
} from "@/components/ui/command";
import { toast } from "sonner";

interface CommandPaletteProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

export default function CommandPalette({
  open,
  onOpenChange,
}: CommandPaletteProps) {
  const navigate = useNavigate();
  const { isDark, setMode } = useTheme();
  const { settings } = useDomainData();
  const paperlessEnabled = Boolean(settings?.paperlessUrl && settings?.hasToken);

  // Global Cmd/Ctrl-K toggles the palette from anywhere in the authed app.
  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        onOpenChange(!open);
      }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [open, onOpenChange]);

  // Every command closes the palette first so the resulting dialog/navigation
  // is not competing with the palette for focus.
  const run = (action: () => void) => {
    onOpenChange(false);
    action();
  };

  const go = (to: string) => run(() => navigate(to));
  const goIntent = (to: string, command: CommandIntent) =>
    run(() => navigate(to, { state: { command } }));

  const handleApplyRules = () =>
    run(async () => {
      try {
        const result = await api.applyRules();
        const n = result.updated;
        toast.success(
          `${n} transaction${n === 1 ? "" : "s"} updated`,
        );
      } catch (err) {
        toast.error((err as Error).message);
      }
    });

  const navItems = [
    { to: "/", label: "Dashboard", icon: LayoutDashboard, keywords: "home overview" },
    { to: "/money-flow", label: "Money Flow", icon: Waypoints, keywords: "sankey graph" },
    {
      to: "/cash-flow-calendar",
      label: "Cash Flow Calendar",
      icon: CalendarDays,
      keywords: "heatmap daily",
    },
    { to: "/import", label: "Import", icon: Upload, keywords: "csv upload statement" },
    ...(paperlessEnabled
      ? [
          {
            to: "/paperless",
            label: "Paperless",
            icon: FileText,
            keywords: "documents scan",
          },
        ]
      : []),
    { to: "/transactions", label: "Transactions", icon: List, keywords: "list ledger" },
    { to: "/accounts", label: "Accounts", icon: Wallet, keywords: "wallets balances" },
    {
      to: "/categories",
      label: "Categories & Rules",
      icon: Tags,
      keywords: "organize groups auto-categorize",
    },
    { to: "/payees", label: "Payees", icon: Users, keywords: "merchants" },
    {
      to: "/linking",
      label: "Transfers & Cashbacks",
      icon: ArrowLeftRight,
      keywords: "links refunds bill payment",
    },
    {
      to: "/recurring",
      label: "Recurring & Subscriptions",
      icon: Repeat,
      keywords: "subscriptions bills series",
    },
    { to: "/settings", label: "Settings", icon: Settings, keywords: "preferences theme" },
  ];

  const newItems: {
    label: string;
    icon: typeof PlusCircle;
    to: string;
    command: CommandIntent;
  }[] = [
    {
      label: "Add Transaction",
      icon: PlusCircle,
      to: "/transactions",
      command: "new-transaction",
    },
    {
      label: "Add Account",
      icon: PlusCircle,
      to: "/accounts",
      command: "new-account",
    },
    {
      label: "Add Category",
      icon: PlusCircle,
      to: "/categories?tab=categories",
      command: "new-category",
    },
    {
      label: "Add Group",
      icon: PlusCircle,
      to: "/categories?tab=groups",
      command: "new-group",
    },
    {
      label: "Add Rule",
      icon: PlusCircle,
      to: "/categories?tab=rules",
      command: "new-rule",
    },
    {
      label: "Add Payee",
      icon: PlusCircle,
      to: "/payees",
      command: "new-payee",
    },
    {
      label: "Add Recurring Series",
      icon: PlusCircle,
      to: "/recurring",
      command: "new-recurring",
    },
  ];

  return (
    <CommandDialog open={open} onOpenChange={onOpenChange}>
      <Command>
        <CommandInput placeholder="Search pages and commands..." />
        <CommandList>
          <CommandEmpty>No results found.</CommandEmpty>
          <CommandGroup heading="Go to">
            {navItems.map((item) => (
              <CommandItem
                key={item.to}
                value={`${item.label} ${item.keywords}`}
                onSelect={() => go(item.to)}
              >
                <item.icon />
                <span>{item.label}</span>
              </CommandItem>
            ))}
          </CommandGroup>
          <CommandSeparator />
          <CommandGroup heading="Create">
            {newItems.map((item) => (
              <CommandItem
                key={item.command}
                value={`${item.label} new add create ${item.to}`}
                onSelect={() => goIntent(item.to, item.command)}
              >
                <item.icon />
                <span>{item.label}</span>
              </CommandItem>
            ))}
          </CommandGroup>
          <CommandSeparator />
          <CommandGroup heading="Actions">
            <CommandItem
              value="apply rules uncategorized categorize"
              onSelect={handleApplyRules}
            >
              <Play />
              <span>Apply Rules to Uncategorized</span>
            </CommandItem>
            <CommandItem
              value="toggle theme dark light appearance"
              onSelect={() => run(() => setMode(isDark ? "light" : "dark"))}
            >
              {isDark ? <Sun /> : <Moon />}
              <span>Switch to {isDark ? "light" : "dark"} theme</span>
            </CommandItem>
          </CommandGroup>
        </CommandList>
      </Command>
    </CommandDialog>
  );
}
