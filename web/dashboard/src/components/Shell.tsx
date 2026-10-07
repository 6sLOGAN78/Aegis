import React from 'react';
import { Shield, Radio, LogOut, Code, Play, Network, ScrollText, AlertTriangle } from 'lucide-react';
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
  const tabs: { id: TabId; label: string; icon: React.FC<{ className?: string }> }[] = [
    { id: 'studio', label: 'Policy Studio', icon: Code },
    { id: 'simulator', label: 'Policy Simulator', icon: Play },
    { id: 'topology', label: 'Cluster Topology', icon: Network },
    { id: 'audit', label: 'Audit Stream', icon: ScrollText },
    { id: 'quarantine', label: 'Emergency Quarantine', icon: AlertTriangle },
  ];

  const getRoleBadge = (role?: string) => {
    switch (role) {
      case 'sec-ops':
        return (
          <span className="text-xs font-semibold px-2 py-0.5 rounded-full bg-cyan-500/10 text-cyan-400 border border-cyan-500/30">
            sec-ops
          </span>
        );
      case 'auditor':
        return (
          <span className="text-xs font-semibold px-2 py-0.5 rounded-full bg-purple-500/10 text-purple-400 border border-purple-500/30">
            auditor
          </span>
        );
      case 'viewer':
      default:
        return (
          <span className="text-xs font-semibold px-2 py-0.5 rounded-full bg-slate-700/50 text-slate-300 border border-slate-600">
            viewer
          </span>
        );
    }
  };

  return (
    <div className="min-h-screen bg-slate-950 text-slate-100 flex flex-col font-sans">
      {/* Top Application Header */}
      <header className="h-16 bg-slate-900 border-b border-slate-800 flex items-center justify-between px-6 sticky top-0 z-30">
        {/* Brand and Logo */}
        <div className="flex items-center space-x-3">
          <div className="p-1.5 bg-cyan-500/10 border border-cyan-500/20 rounded-lg">
            <Shield className="w-6 h-6 text-cyan-400" />
          </div>
          <div className="flex flex-col">
            <span className="text-base font-semibold text-slate-100 tracking-tight leading-tight">
              Aegis Console
            </span>
            <span className="text-[10px] uppercase font-semibold text-slate-400 tracking-wider">
              Zero-Trust Gateways
            </span>
          </div>
        </div>

        {/* Navigation Tabs */}
        <nav className="hidden md:flex items-center space-x-1 h-full">
          {tabs.map((tab) => {
            const Icon = tab.icon;
            const isActive = activeTab === tab.id;
            return (
              <button
                key={tab.id}
                onClick={() => onTabChange(tab.id)}
                className={`flex items-center space-x-2 h-full px-4 text-sm font-semibold transition border-b-2 ${
                  isActive
                    ? 'border-cyan-400 text-cyan-300'
                    : 'border-transparent text-slate-400 hover:text-slate-200 hover:border-slate-700'
                }`}
              >
                <Icon className="w-4 h-4" />
                <span>{tab.label}</span>
              </button>
            );
          })}
        </nav>

        {/* Right-aligned Operator Session Cluster */}
        <div className="flex items-center space-x-4">
          {session ? (
            <div className="flex items-center space-x-3 bg-slate-950/60 border border-slate-800 px-3 py-1.5 rounded-lg">
              {/* Heartbeat pulse */}
              <div className="flex items-center space-x-1.5 text-xs text-slate-400" title="Active Control Plane Connection">
                <span className="relative flex h-2 w-2">
                  <span className="animate-ping absolute inline-flex h-full w-full rounded-full bg-emerald-400 opacity-75"></span>
                  <span className="relative inline-flex rounded-full h-2 w-2 bg-emerald-500"></span>
                </span>
                <Radio className="w-3.5 h-3.5 text-emerald-400" />
              </div>

              {/* Username & Role */}
              <span className="text-xs font-mono font-medium text-slate-200">
                {session.username}
              </span>
              {getRoleBadge(session.role)}

              {/* Terminate Session Button */}
              <button
                onClick={onLogout}
                aria-label="Terminate operator session"
                title="Terminate Operator Session"
                className="text-slate-400 hover:text-rose-400 transition ml-1 p-1 rounded hover:bg-slate-800"
              >
                <LogOut className="w-4 h-4" />
              </button>
            </div>
          ) : (
            <span className="text-xs text-slate-500 font-mono">Unauthenticated</span>
          )}
        </div>
      </header>

      {/* Mobile Navigation Bar */}
      <div className="md:hidden flex overflow-x-auto bg-slate-900 border-b border-slate-800 px-4 py-2 space-x-2">
        {tabs.map((tab) => {
          const Icon = tab.icon;
          const isActive = activeTab === tab.id;
          return (
            <button
              key={tab.id}
              onClick={() => onTabChange(tab.id)}
              className={`flex items-center space-x-1.5 px-3 py-1.5 rounded-lg text-xs font-semibold whitespace-nowrap ${
                isActive
                  ? 'bg-cyan-500/10 text-cyan-300 border border-cyan-500/30'
                  : 'text-slate-400 hover:bg-slate-800 text-slate-200'
              }`}
            >
              <Icon className="w-3.5 h-3.5" />
              <span>{tab.label}</span>
            </button>
          );
        })}
      </div>

      {/* Main Content Surface */}
      <main className="flex-1 p-6 max-w-7xl w-full mx-auto">
        {children}
      </main>
    </div>
  );
};
