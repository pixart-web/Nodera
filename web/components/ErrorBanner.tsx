import { XCircle } from "lucide-react";

export function ErrorBanner({ message }: { message: string }) {
  return (
    <div role="alert" className="mb-4 flex items-start gap-3 rounded-nd border border-nd-danger/25 bg-nd-danger/10 px-3.5 py-3 text-sm text-nd-danger">
      <XCircle className="mt-0.5 h-4 w-4 shrink-0" aria-hidden />
      <span>{message}</span>
    </div>
  );
}
