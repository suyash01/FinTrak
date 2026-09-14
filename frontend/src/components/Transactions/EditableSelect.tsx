import type { CSSProperties } from "react";

export interface SelectOption {
  value: string;
  label: string;
}

export interface EditableSelectGroup {
  label: string;
  options: SelectOption[];
}

interface EditableSelectProps {
  value?: string | null;
  options?: SelectOption[];
  optionGroups?: EditableSelectGroup[];
  onChange: (value: string) => void;
  placeholder: string;
  displayText?: string;
  style?: CSSProperties;
  ariaLabel?: string;
}

// Dense inline category/payee picker. This is a deliberate native <select>
// exception to the shadcn primitives (see AGENTS.md): it lives inside table
// cells and needs optgroup grouping without the Radix portal overhead.
export default function EditableSelect({
  value,
  options,
  optionGroups,
  onChange,
  placeholder,
  displayText,
  style,
  ariaLabel,
}: EditableSelectProps) {
  const isPlaceholder = !value;
  const allOptions = optionGroups?.flatMap((g) => g.options) ?? options ?? [];
  const isMissing = Boolean(value) && !allOptions.some((o) => o.value === value);

  return (
    <select
      className={`bg-transparent border-none text-[13px] outline-none focus:ring-0 w-full rounded px-1 py-0.5 cursor-pointer appearance-none hover:bg-accent transition-all block truncate ${isPlaceholder ? "text-muted-foreground italic" : ""}`}
      style={{ ...style, backgroundImage: "none" }}
      value={value || ""}
      onChange={(e) => onChange(e.target.value)}
      aria-label={ariaLabel}
      title="Click to edit"
    >
      <option value="" className="bg-popover text-muted-foreground not-italic">
        {placeholder}
      </option>
      {isMissing && (
        <option
          value={value ?? ""}
          className="bg-popover text-foreground not-italic"
          hidden
        >
          {displayText}
        </option>
      )}
      {optionGroups
        ? optionGroups.map((g) => (
            <optgroup
              key={g.label}
              label={g.label}
              className="bg-popover text-muted-foreground"
            >
              {g.options.map((o) => (
                <option
                  key={o.value}
                  value={o.value}
                  className="bg-popover text-foreground not-italic"
                >
                  {o.label}
                </option>
              ))}
            </optgroup>
          ))
        : options?.map((o) => (
            <option
              key={o.value}
              value={o.value}
              className="bg-popover text-foreground not-italic"
            >
              {o.label}
            </option>
          ))}
    </select>
  );
}
