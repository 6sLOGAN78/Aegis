import React from 'react';
import { Shield, LogOut } from 'lucide-react';
import type { SessionInfo } from '../api/types';

export type TabId = 'studio' | 'simulator' | 'topology' | 'audit' | 'quarantine';

interface ShellProps {
  children: React.ReactNode;
  activeTab: TabId;
  onTabChange: (tab: TabId) => void;
  session: SessionInfo | null;
  onLogout: () => void;
}

export const Shell: React.FC<ShellProps> = ({
  children,
  activeTab,
  onTabChange,
  session,
  onLogout,
}) => {
  const tabs: { id: TabId; label: string }[] = [
    { id: 'studio', label: 'Policy' },
    { id: 'simulator', label: 'Simulator' },
    { id: 'topology', label: 'Gateways' },
    { id: 'audit', label: 'Audit log' },
    { id: 'quarantine', label: 'Quarantine' },
  ];

  return (
    <div className="min-h-screen bg-bone text-slate-100 flex flex-col">
      <a
        href="#main"
        className="sr-only focus:not-sr-only focus:fixed focus:top-3 focus:left-3 focus:z-50 btn btn-secondary"
      >
        Skip to content
      </a>

      <header className="sticky top-0 z-30 bg-linen border-b border-mist">
        <div className="h-14 max-w-[1200px] mx-auto px-4 sm:px-6 flex items-center gap-8">
          <div className="flex items-center gap-2.5 shrink-0">
            <span className="grid place-items-center w-7 h-7 rounded-md bg-ink">
              <Shield className="w-4 h-4 text-white" />
            </span>
            <span className="text-[15px] font-medium text-slate-100">Aegis</span>
          </div>

          <nav aria-label="Sections" className="hidden md:flex items-stretch h-full gap-1">
            {tabs.map((tab) => {
              const isActive = activeTab === tab.id;
              return (
                <button
                  key={tab.id}
                  onClick={() => onTabChange(tab.id)}
                  aria-current={isActive ? 'page' : undefined}
                  className={`flex items-center gap-2 px-3 text-sm border-b-2 -mb-px transition-colors ${
                    isActive
                      ? 'border-rose-500 text-slate-100 font-medium'
                      : 'border-transparent text-slate-400 hover:text-slate-100'
                  }`}
                >
                  {tab.label}
                </button>
              );
            })}
          </nav>

          <div className="ml-auto flex items-center gap-3">
            {session ? (
              <>
                <span className="hidden sm:flex items-center gap-2 text-sm text-slate-300">
                  <span className="w-1.5 h-1.5 rounded-full bg-emerald-500" aria-hidden="true" />
                  <span className="sr-only">Connected as</span>
                  {session.username}
                  <span className="text-slate-500">· {session.role || 'viewer'}</span>
                </span>
                <button onClick={onLogout} className="btn btn-ghost btn-sm" aria-label="Sign out" title="Sign out">
                  <LogOut />
                </button>
              </>
            ) : (
              <span className="text-sm text-slate-500">Signed out</span>
            )}
          </div>
        </div>

        <nav aria-label="Sections" className="md:hidden flex overflow-x-auto [scrollbar-width:none] px-4 pb-2 gap-1">
          {tabs.map((tab) => {
            const isActive = activeTab === tab.id;
            return (
              <button
                key={tab.id}
                onClick={() => onTabChange(tab.id)}
                aria-current={isActive ? 'page' : undefined}
                className={`btn btn-sm shrink-0 ${isActive ? 'btn-secondary border-ink' : 'btn-ghost'}`}
              >
                {tab.label}
              </button>
            );
          })}
        </nav>
      </header>

      <main id="main" className="flex-1 w-full max-w-[1200px] mx-auto px-4 sm:px-6 py-8 lg:py-10">
        {children}
      </main>
    </div>
  );
};
