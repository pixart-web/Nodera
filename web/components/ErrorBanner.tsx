export function ErrorBanner({ message }: { message: string }) {
  return (
    <div className="mb-4 rounded-md border border-danger/30 bg-danger/10 px-3 py-2 text-sm text-danger">
      {message}
    </div>
  );
}
