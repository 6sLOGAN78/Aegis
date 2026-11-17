import React from 'react';

interface PageHeaderProps {
  title: string;
  description?: React.ReactNode;
  meta?: React.ReactNode;
  actions?: React.ReactNode;
}

/** Page title in the sign system's bold voice, with optional status meta and actions on the right. */
export const PageHeader: React.FC<PageHeaderProps> = ({ title, description, meta, actions }) => (
  <header className="flex flex-col gap-4 sm:flex-row sm:items-end sm:justify-between mb-6">
    <div className="min-w-0">
      <div className="flex flex-wrap items-center gap-3">
        <h1 className="text-[28px] font-display text-slate-100 text-balance">{title}</h1>
        {meta}
      </div>
      {description && (
        <p className="mt-1 text-sm text-slate-500 max-w-[72ch] text-pretty">{description}</p>
      )}
    </div>
    {actions && <div className="flex flex-wrap items-center gap-2 shrink-0">{actions}</div>}
  </header>
);
