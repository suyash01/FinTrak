import { useState, useEffect, type FormEvent } from "react";
import { toast } from "sonner";
import api from "../../api/client";
import { useDomainData } from "../../context/DomainDataContext";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
import { Spinner } from "@/components/ui/spinner";

export default function PaperlessSettingsManager() {
  const { settings, refreshSettings, loading, errors } = useDomainData();
  const [url, setUrl] = useState("");
  const [token, setToken] = useState("");
  const [tokenSet, setTokenSet] = useState(false);
  const [tag, setTag] = useState("");
  const [saving, setSaving] = useState(false);
  const [saved, setSaved] = useState(false);

  useEffect(() => {
    if (!settings) return;
    setUrl(settings.paperlessUrl || "");
    setToken("");
    setTokenSet(Boolean(settings.hasToken));
    setTag(settings.paperlessTag || "");
  }, [settings]);

  const handleSave = async (e: FormEvent) => {
    e.preventDefault();
    setSaving(true);
    setSaved(false);
    try {
      const payload: {
        paperlessUrl: string;
        paperlessTag: string;
        paperlessToken?: string;
      } = {
        paperlessUrl: url,
        paperlessTag: tag,
      };
      if (token.trim() !== "") {
        payload.paperlessToken = token;
      }
      await api.updatePaperlessSettings(payload);
      setToken("");
      setTokenSet(Boolean(token.trim() !== "" || tokenSet));
      setSaved(true);
      toast.success("Paperless settings saved");
      refreshSettings();
    } catch (err) {
      toast.error((err as Error).message);
    } finally {
      setSaving(false);
    }
  };

  if (loading)
    return (
      <div className="flex items-center gap-2 text-sm text-muted-foreground">
        <Spinner className="size-4" /> Loading...
      </div>
    );

  // A failed settings load must not look like "Paperless is not configured":
  // show a retryable error until a request actually succeeds.
  if (errors.settings) {
    return (
      <div className="space-y-3">
        <div className="px-4 py-3 bg-destructive/10 border border-destructive/30 rounded-lg text-sm text-destructive">
          Could not load Paperless settings: {errors.settings}
        </div>
        <Button
          type="button"
          size="sm"
          variant="outline"
          onClick={() => refreshSettings()}
        >
          Retry
        </Button>
      </div>
    );
  }

  return (
    <form onSubmit={handleSave} className="space-y-3">
      <div className="space-y-1.5">
        <Label
          htmlFor="paperless-settings-url"
          className="text-xs text-muted-foreground"
        >
          Paperless URL
        </Label>
        <Input
          id="paperless-settings-url"
          type="text"
          placeholder="http://localhost:8000"
          value={url}
          onChange={(e) => setUrl(e.target.value)}
        />
      </div>
      <div className="space-y-1.5">
        <Label
          htmlFor="paperless-settings-token"
          className="text-xs text-muted-foreground"
        >
          API Token
        </Label>
        <Input
          id="paperless-settings-token"
          type="password"
          placeholder={
            tokenSet
              ? "Leave blank to keep the saved token"
              : "Paperless-ngx API token"
          }
          value={token}
          onChange={(e) => setToken(e.target.value)}
        />
        <p className="text-[11px] text-muted-foreground">
          {tokenSet
            ? "An API token is saved. Enter a new one only to replace it."
            : "The token is stored encrypted and is never shown again."}
        </p>
      </div>
      <div className="space-y-1.5">
        <Label
          htmlFor="paperless-settings-tag"
          className="text-xs text-muted-foreground"
        >
          Import Tag Label
        </Label>
        <Input
          id="paperless-settings-tag"
          type="text"
          placeholder="e.g. fintrak"
          value={tag}
          onChange={(e) => setTag(e.target.value)}
        />
        <p className="text-[11px] text-muted-foreground">
          When enabled during import, successfully imported documents get tagged
          with this label in Paperless-ngx.
        </p>
      </div>
      <div className="flex items-center gap-3 pt-1">
        <Button type="submit" size="sm" disabled={saving}>
          {saving ? "Saving..." : "Save"}
        </Button>
        {saved && (
          <Badge variant="secondary" className="text-emerald-500">
            Saved
          </Badge>
        )}
      </div>
    </form>
  );
}
