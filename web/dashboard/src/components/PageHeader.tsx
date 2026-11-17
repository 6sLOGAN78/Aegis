import React from 'react';

interface PageHeaderProps {
  title: string;
  description?: React.ReactNode;
  meta?: React.ReactNode;
  actions?: React.ReactNode;
}

/** Page title on the canvas, with optional status meta and actions on the right. */
export const PageHeader: React.FC<PageHeaderProps> = ({ title, description, meta, actions }) => (
  <header className="flex flex-col gap-4 sm:flex-row sm:items-end sm:justify-between pb-6 mb-6 border-b border-mist">
    <div className="min-w-0">
      <div className="flex flex-wrap items-center gap-3">
        <h1 className="text-3xl font-display text-slate-100 text-balance">{title}</h1>
        {meta}
      </div>
      {description && (
        <p className="mt-2 text-sm text-slate-400 max-w-[65ch] text-pretty">{description}</p>
      )}
    </div>
    {actions && <div className="flex flex-wrap items-center gap-2 shrink-0">{actions}</div>}
  </header>
);
