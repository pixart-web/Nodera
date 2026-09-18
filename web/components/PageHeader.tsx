export function PageHeader({ title, description }: { title: string; description?: string }) {
  return (
    <div className="mb-6">
      <h1 className="text-lg font-medium text-base-100">{title}</h1>
      {description && <p className="mt-1 text-sm text-base-400">{description}</p>}
    </div>
  );
}
