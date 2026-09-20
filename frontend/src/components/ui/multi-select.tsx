import * as React from "react";
import { ChevronDownIcon } from "lucide-react";
import {
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuLabel,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { selectTriggerClasses } from "@/components/ui/select";
import { cn } from "@/lib/utils";

export interface MultiSelectOption {
  value: string;
  // Menu item content, so a caller can add icons, counts, or muted styling.
  label: React.ReactNode;
  // Trigger text when this option is the only selection; keep it short.
  triggerLabel: string;
  // Indent the item under its group's own option (a category under its group).
  inset?: boolean;
}

export interface MultiSelectGroup {
  // Optional heading rendered above the group's options.
  label?: string;
  options: MultiSelectOption[];
}

interface MultiSelectProps {
  // Selected option values; the control is fully controlled.
  values: string[];
  onValuesChange: (values: string[]) => void;
  groups: MultiSelectGroup[];
  // Trigger text while nothing is selected, e.g. "All Accounts".
  placeholder: string;
  ariaLabel: string;
  triggerClassName?: string;
  menuClassName?: string;
}

// MultiSelect is the checkbox-list counterpart of Select: the trigger keeps
// Select's chrome, the menu stays open across toggles so several values can be
// picked in a row, and the trigger summarises the selection (placeholder / the
// single option's name / "N selected"). Clearing the last box clears the
// filter, which is what the placeholder advertises.
export default function MultiSelect({
  values,
  onValuesChange,
  groups,
  placeholder,
  ariaLabel,
  triggerClassName,
  menuClassName,
}: MultiSelectProps) {
  const selected = React.useMemo(() => new Set(values), [values]);
  const optionByValue = React.useMemo(() => {
    const byValue = new Map<string, MultiSelectOption>();
    for (const group of groups) {
      for (const option of group.options) byValue.set(option.value, option);
    }
    return byValue;
  }, [groups]);

  const summary =
    values.length === 0
      ? placeholder
      : values.length === 1
        ? optionByValue.get(values[0])?.triggerLabel || values[0]
        : `${values.length} selected`;

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <button
          type="button"
          aria-label={ariaLabel}
          data-size="default"
          data-placeholder={values.length === 0 ? "" : undefined}
          className={cn(selectTriggerClasses, triggerClassName)}
        >
          <span data-slot="select-value">{summary}</span>
          <ChevronDownIcon className="pointer-events-none size-4 text-muted-foreground" />
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className={cn("w-64", menuClassName)}>
        {groups.map((group, index) => (
          <DropdownMenuGroup key={group.label ?? index}>
            {group.label && (
              <DropdownMenuLabel>{group.label}</DropdownMenuLabel>
            )}
            {group.options.map((option) => (
              <DropdownMenuCheckboxItem
                key={option.value}
                checked={selected.has(option.value)}
                inset={option.inset}
                // Keep the menu open: the whole point is toggling several
                // values without reopening it.
                onSelect={(event) => {
                  event.preventDefault();
                  onValuesChange(
                    selected.has(option.value)
                      ? values.filter((value) => value !== option.value)
                      : [...values, option.value],
                  );
                }}
              >
                {option.label}
              </DropdownMenuCheckboxItem>
            ))}
          </DropdownMenuGroup>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
