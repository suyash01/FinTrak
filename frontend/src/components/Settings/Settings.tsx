import { useAuth } from "../../context/AuthContext";
import { useSettings } from "../../context/SettingsContext";
import {
  useTheme,
  THEME_MODES,
  ACCENT_THEMES,
  ACCENT_COLORS,
} from "../../context/ThemeContext";
import { Button } from "@/components/ui/button";
import { Switch } from "@/components/ui/switch";
import packageJson from "../../../package.json";
import AccountTypesManager from "./AccountTypesManager";
import PaperlessSettingsManager from "./PaperlessSettingsManager";

export default function Settings() {
  const { compactLayout, toggleCompactLayout } = useSettings();
  const { user } = useAuth();
  const { mode, accent, setMode, setAccent } = useTheme();

  return (
    <>
      <div className="shrink-0 px-8 pt-6">
        <h1 className="text-2xl font-bold mb-1">Settings</h1>
        <p className="text-muted-foreground text-sm">Application preferences</p>
      </div>
      <div className="flex-1 px-8 pb-8 pt-6 overflow-y-auto space-y-6">
        <div className="bg-card border border-border rounded-xl p-6 max-w-125">
          <h3 className="text-base font-semibold mb-1">Theme</h3>
          <p className="text-[13px] text-muted-foreground mb-4">
            Choose between light and dark mode, and pick an accent color.
          </p>
          <div className="space-y-5">
            <div>
              <div className="text-sm font-medium mb-2">Mode</div>
              <div className="flex gap-2">
                {THEME_MODES.map((m) => (
                  <Button
                    key={m}
                    variant={mode === m ? "default" : "secondary"}
                    size="sm"
                    onClick={() => setMode(m)}
                    className="capitalize"
                  >
                    {m}
                  </Button>
                ))}
              </div>
            </div>
            <div>
              <div className="text-sm font-medium mb-2">Accent Color</div>
              <div className="flex gap-2.5">
                {ACCENT_THEMES.map((a) => (
                  <button
                    key={a}
                    onClick={() => setAccent(a)}
                    aria-label={`${a} accent`}
                    title={a}
                    className={`h-7 w-7 rounded-full transition-transform hover:scale-110 ${
                      accent === a
                        ? "ring-2 ring-ring ring-offset-2 ring-offset-background"
                        : ""
                    }`}
                    style={{ backgroundColor: ACCENT_COLORS[a] }}
                  />
                ))}
              </div>
            </div>
          </div>
        </div>

        <div className="bg-card border border-border rounded-xl p-6 max-w-125">
          <h3 className="text-base font-semibold mb-4">Display Preferences</h3>
          <div className="flex items-center justify-between">
            <div>
              <div className="text-sm font-medium text-foreground">
                Compact Layout
              </div>
              <div className="text-[13px] text-muted-foreground">
                Reduce padding and spacing to show more data
              </div>
            </div>
            <Switch
              checked={compactLayout}
              onCheckedChange={toggleCompactLayout}
            />
          </div>
        </div>

        {/* Account Types Management (admin-only) */}
        {user?.role === "admin" && (
          <div className="bg-card border border-border rounded-xl p-6 max-w-125">
            <h3 className="text-base font-semibold mb-4">Account Types</h3>
            <AccountTypesManager />
          </div>
        )}

        {/* Paperless-ngx integration */}
        <div className="bg-card border border-border rounded-xl p-6 max-w-125">
          <h3 className="text-base font-semibold mb-1">Paperless-ngx</h3>
          <p className="text-[13px] text-muted-foreground mb-4">
            Connect a Paperless-ngx instance to pull statement PDFs. The import
            UI appears once both a URL and API token are set.
          </p>
          <PaperlessSettingsManager />
        </div>

        <div className="bg-card border border-border rounded-xl p-6 max-w-125">
          <h3 className="text-base font-semibold mb-4">About FinTrak</h3>
          <p className="text-muted-foreground text-sm leading-relaxed">
            FinTrak helps you consolidate bank and credit card statements,
            categorize transactions, and track transfers, cashbacks, refunds,
            and bill payments — all in one place.
          </p>
          <div className="mt-4 text-[13px] text-muted-foreground">
            Version {packageJson.version} · Built with Go + React
          </div>
        </div>
      </div>
    </>
  );
}
