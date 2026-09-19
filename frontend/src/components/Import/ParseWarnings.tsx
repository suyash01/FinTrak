import { AlertTriangle } from "lucide-react";
import { cn } from "@/lib/utils";

interface ParseWarningsProps {
  // Parser-reported mismatches between a page's rebuilt subtotal and the
  // printed one; non-empty means the extracted rows are suspect.
  warnings: string[];
  className?: string;
}

// Amber warning affordance listing the parser's own validation messages. The
// text uses theme tokens — the softer amber tints are unreadable on the light
// theme's near-white background.
export default function ParseWarnings({
  warnings,
  className,
}: ParseWarningsProps) {
  if (warnings.length === 0) return null;

  return (
    <div
      className={cn(
        "p-4 bg-amber-500/10 border border-amber-500/25 rounded-lg flex gap-3 items-start",
        className,
      )}
    >
      <AlertTriangle size={18} className="text-amber-500 shrink-0 mt-0.5" />
      <div className="text-sm">
        <p className="font-semibold mb-1 text-foreground">
          {warnings.length} parse warning{warnings.length === 1 ? "" : "s"} —
          the parsed rows may be wrong.
        </p>
        <ul className="list-disc pl-5 space-y-1 text-muted-foreground">
          {warnings.map((warning, i) => (
            <li key={i}>{warning}</li>
          ))}
        </ul>
      </div>
    </div>
  );
}
