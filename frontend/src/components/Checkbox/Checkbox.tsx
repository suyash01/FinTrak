import { Check } from "lucide-react";
import type { ButtonHTMLAttributes } from "react";

interface CheckboxProps extends Omit<
  ButtonHTMLAttributes<HTMLButtonElement>,
  "onChange"
> {
  checked: boolean;
  onChange?: (checked: boolean) => void;
}

export default function Checkbox({
  checked,
  onChange,
  className = "",
  ...props
}: CheckboxProps) {
  return (
    <button
      type="button"
      role="checkbox"
      aria-checked={checked}
      onClick={() => onChange?.(!checked)}
      className={`inline-flex items-center justify-center w-4 h-4 rounded border shrink-0 transition-colors ${
        checked
          ? "bg-cyan-500 border-cyan-500 text-slate-950"
          : "bg-slate-950 border-slate-700 text-transparent hover:border-slate-500"
      } ${className}`}
      {...props}
    >
      <Check size={12} strokeWidth={3.5} />
    </button>
  );
}
