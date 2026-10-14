import { NoderaMark } from "@/components/brand/NoderaMark";

export function Logo({ className }: { className?: string }) {
  return (
    <span className={`inline-flex items-center gap-2.5 ${className ?? ""}`}>
      <NoderaMark size={30} />
      <span className="text-[19px] font-semibold tracking-[0.18em] text-nd-text">NODERA</span>
    </span>
  );
}
