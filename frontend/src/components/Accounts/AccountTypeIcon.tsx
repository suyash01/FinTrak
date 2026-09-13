import { CreditCard, Building2, Landmark } from "lucide-react";

interface AccountTypeIconProps {
  accountTypeId: string;
  color: string;
  size: number;
}

export default function AccountTypeIcon({
  accountTypeId,
  color,
  size,
}: AccountTypeIconProps) {
  if (accountTypeId === "credit_card")
    return <CreditCard size={size} style={{ color }} />;
  if (accountTypeId === "loan")
    return <Landmark size={size} style={{ color }} />;
  return <Building2 size={size} style={{ color }} />;
}
