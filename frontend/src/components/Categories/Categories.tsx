import { useEffect, useRef, useState } from "react";
import { useSearchParams } from "react-router-dom";
import { useSettings } from "../../context/SettingsContext";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import GroupsTab from "./GroupsTab";
import CategoriesTab from "./CategoriesTab";
import RulesTab from "./RulesTab";

type CategoryTab = "groups" | "categories" | "rules";

const TAB_PARAM = "tab";

export default function Categories() {
  const [searchParams, setSearchParams] = useSearchParams();
  const syncedUrlRef = useRef(searchParams.toString());
  const [tab, setTab] = useState<CategoryTab>(() => {
    const t = searchParams.get(TAB_PARAM);
    return t === "categories" || t === "rules" ? t : "groups";
  });
  const { compactLayout } = useSettings();

  // Keep the active tab in sync with the URL so it is shareable and
  // deep-linkable (e.g. ?tab=rules). The default "groups" tab stays implicit.
  useEffect(() => {
    const params: Record<string, string> = {};
    if (tab !== "groups") params[TAB_PARAM] = tab;
    const desiredQs = new URLSearchParams(params).toString();
    if (desiredQs === syncedUrlRef.current) return;
    syncedUrlRef.current = desiredQs;
    setSearchParams(params, { replace: true });
  }, [tab]);

  useEffect(() => {
    const currentQs = searchParams.toString();
    if (currentQs === syncedUrlRef.current) return;
    const t = searchParams.get(TAB_PARAM);
    const next: CategoryTab =
      t === "categories" || t === "rules" ? t : "groups";
    if (next !== tab) setTab(next);
    syncedUrlRef.current = currentQs;
    // React to external URL changes only; the setters/state read above are
    // stable and including them would re-sync on state we just wrote.
  }, [searchParams]);

  return (
    <>
      <div className="shrink-0 px-8 pt-6">
        <h1 className="text-2xl font-bold mb-1 text-foreground">
          Categories & Rules
        </h1>
        <p className="text-muted-foreground text-sm">
          Manage transaction groups, categories and auto-categorization rules
        </p>
      </div>
      <div className="flex-1 px-8 pb-8 pt-6 overflow-y-auto w-full">
        <Tabs
          className="gap-0"
          value={tab}
          onValueChange={(v) => setTab(v as typeof tab)}
        >
          <div
            className={`${compactLayout ? "mb-4" : "mb-6"} border-b border-border`}
          >
            <TabsList
              variant="line"
              className={`${compactLayout ? "gap-1" : "gap-2"}`}
            >
              <TabsTrigger value="groups">Groups</TabsTrigger>
              <TabsTrigger value="categories">Categories</TabsTrigger>
              <TabsTrigger value="rules">Rules</TabsTrigger>
            </TabsList>
          </div>

          <TabsContent value="groups">
            <GroupsTab />
          </TabsContent>

          <TabsContent value="categories">
            <CategoriesTab />
          </TabsContent>

          <TabsContent value="rules">
            <RulesTab />
          </TabsContent>
        </Tabs>
      </div>
    </>
  );
}
