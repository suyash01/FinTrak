import { Check } from "lucide-react";

export interface ImportStep {
  num: number;
  label: string;
}

const STEPS: ImportStep[] = [
  { num: 1, label: "Select Account" },
  { num: 2, label: "Upload CSV" },
  { num: 3, label: "Map Columns" },
  { num: 4, label: "Preview" },
  { num: 5, label: "Done" },
];

interface ImportStepsProps {
  step: number;
  onSelect: (step: number) => void;
}

// Presentational wizard step indicator. Completed steps are clickable (to go
// back); the current and future steps are not.
export default function ImportSteps({ step, onSelect }: ImportStepsProps) {
  return (
    <div className="flex items-center gap-4 mb-8 overflow-x-auto pb-2 scrollbar-none">
      {STEPS.map((s) => (
        <div
          key={s.num}
          role={step > s.num ? "button" : undefined}
          tabIndex={step > s.num ? 0 : undefined}
          aria-current={step === s.num ? "step" : undefined}
          aria-disabled={step > s.num ? undefined : true}
          className={`flex items-center gap-2 text-sm font-medium whitespace-nowrap px-3 py-1.5 rounded-lg transition-colors ${step === s.num ? "bg-primary/10 text-primary" : step > s.num ? "text-emerald-500" : "text-muted-foreground"}`}
          onClick={() => step > s.num && onSelect(s.num)}
          onKeyDown={(e) => {
            if (step > s.num && (e.key === "Enter" || e.key === " ")) {
              e.preventDefault();
              onSelect(s.num);
            }
          }}
          style={{ cursor: step > s.num ? "pointer" : "default" }}
        >
          <span
            className={`w-6 h-6 rounded-full flex items-center justify-center text-xs font-bold shrink-0 ${step === s.num ? "bg-primary text-primary-foreground" : step > s.num ? "bg-emerald-500/20 text-emerald-500" : "bg-muted text-muted-foreground"}`}
          >
            {step > s.num ? <Check size={12} /> : s.num}
          </span>
          {s.label}
        </div>
      ))}
    </div>
  );
}
