import React from 'react';
import { LogOut } from 'lucide-react';
import type { SessionInfo } from '../api/types';

export type TabId = 'overview' | 'studio' | 'simulator' | 'topology' | 'audit' | 'quarantine';

interface ShellProps {
  children: React.ReactNode;
  activeTab: TabId;
  onTabChange: (tab: TabId) => void;
  session: SessionInfo | null;
  onLogout: () => void;
}

export const TABS: { id: TabId; label: string }[] = [
  { id: 'overview', label: 'Overview' },
  { id: 'studio', label: 'Policy' },
  { id: 'simulator', label: 'Simulator' },
  { id: 'topology', label: 'Gateways' },
  { id: 'audit', label: 'Audit log' },
  { id: 'quarantine', label: 'Quarantine' },
];

/** The Aegis mark: a single route line through a station, drawn not borrowed. */
export const AegisMark: React.FC<{ className?: string }> = ({ className = 'w-6 h-6' }) => (
  <svg viewBox="0 0 24 24" className={className} aria-hidden="true">
    <rect x="1" y="10" width="22" height="4" rx="2" fill="currentColor" />
    <circle cx="12" cy="12" r="6" fill="var(--color-ink)" stroke="currentColor" strokeWidth="3" />
  </svg>
);

export const Shell: React.FC<ShellProps> = ({ children, activeTab, onTabChange, session, onLogout }) => (
  <div className="min-h-screen bg-bone text-slate-100 flex flex-col">
    <a
      href="#main"
      className="sr-only focus:not-sr-only focus:fixed focus:top-3 focus:left-3 focus:z-50 btn btn-secondary"
    >
      Skip to content
    </a>

    {/* The sign band: black, full width, white type. Current section carries a white underline. */}
    <header className="sticky top-0 z-30 bg-ink text-white">
      <div className="h-14 max-w-[1320px] mx-auto px-4 sm:px-6 flex items-stretch gap-8">
        <div className="flex items-center gap-2.5 shrink-0">
          <AegisMark className="w-6 h-6 text-white" />
          <span className="text-[18px] font-bold tracking-[-0.01em]">Aegis</span>
        </div>

        <nav aria-label="Sections" className="hidden lg:flex items-stretch gap-1">
          {TABS.map((tab) => {
            const isActive = activeTab === tab.id;
            return (
              <button
                key={tab.id}
                onClick={() => onTabChange(tab.id)}
                aria-current={isActive ? 'page' : undefined}
                className={`relative px-3 text-[14px] font-semibold transition-colors focus-visible:outline-white focus-visible:-outline-offset-4 ${
                  isActive ? 'text-white' : 'text-white/65 hover:text-white'
                }`}
              >
                {tab.label}
                <span
                  aria-hidden="true"
                  className={`absolute left-3 right-3 bottom-0 h-[3px] bg-white transition-opacity duration-150 ${isActive ? 'opacity-100' : 'opacity-0'}`}
                />
              </button>
            );
          })}
        </nav>

        <div className="ml-auto flex items-center gap-3 text-sm">
          {session && (
            <>
              <span className="hidden sm:inline text-white/80">
                <span className="sr-only">Signed in as </span>
                <span className="font-semibold text-white">{session.username}</span>
                <span className="text-white/60"> · {session.role || 'viewer'}</span>
              </span>
              <button
                onClick={onLogout}
                className="inline-flex items-center gap-2 h-8 px-2.5 rounded-[4px] text-white/80 hover:text-white hover:bg-white/10 font-semibold focus-visible:outline-white"
              >
                <LogOut className="w-4 h-4" />
                <span className="hidden sm:inline">Sign out</span>
                <span className="sr-only sm:hidden">Sign out</span>
              </button>
            </>
          )}
        </div>
      </div>

      <nav
        aria-label="Sections"
        className="lg:hidden flex overflow-x-auto [scrollbar-width:none] px-2 sm:px-4 border-t border-white/15"
      >
        {TABS.map((tab) => {
          const isActive = activeTab === tab.id;
          return (
            <button
              key={tab.id}
              onClick={() => onTabChange(tab.id)}
              aria-current={isActive ? 'page' : undefined}
              className={`relative shrink-0 h-11 px-3 text-[14px] font-semibold focus-visible:outline-white focus-visible:-outline-offset-4 ${
                isActive ? 'text-white' : 'text-white/65'
              }`}
            >
              {tab.label}
              {isActive && <span aria-hidden="true" className="absolute left-3 right-3 bottom-0 h-[3px] bg-white" />}
            </button>
          );
        })}
      </nav>
    </header>

    <main id="main" className="flex-1 w-full max-w-[1320px] mx-auto px-4 sm:px-6 py-6 lg:py-8">
      {children}
    </main>
  </div>
);
