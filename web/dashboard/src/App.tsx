import React, { useState, useEffect } from 'react';
import { Shell, TabId } from './components/Shell';
import { LoginModal } from './components/LoginModal';
import { PolicyStudio } from './components/PolicyStudio';
import { apiClient } from './api/client';
import type { SessionInfo } from './api/types';

export const App: React.FC = () => {
  const [session, setSession] = useState<SessionInfo | null>(null);
  const [isLoginOpen, setIsLoginOpen] = useState(false);
  const [activeTab, setActiveTab] = useState<TabId>('studio');
  const [candidateRego, setCandidateRego] = useState<string | undefined>(undefined);

  // Verify active session on mount
  useEffect(() => {
    let mounted = true;

    // Register 401 interceptor
    apiClient.onUnauthorized(() => {
      setSession(null);
      setIsLoginOpen(true);
    });

    apiClient
      .get<SessionInfo>('/control/v1/auth/me')
      .then((data) => {
        if (mounted && data?.username) {
          apiClient.setCSRFToken(data.csrf_token);
          setSession(data);
          setIsLoginOpen(false);
        }
      })
      .catch(() => {
        if (mounted) {
          setSession(null);
          setIsLoginOpen(true);
        }
      });

    return () => {
      mounted = false;
    };
  }, []);

  const handleLoginSuccess = (newSession: SessionInfo) => {
    setSession(newSession);
    setIsLoginOpen(false);
  };

  const handleLogout = async () => {
    try {
      await apiClient.post('/control/v1/auth/logout');
    } catch {
      // Ignore network errors during logout
    } finally {
      apiClient.setCSRFToken(null);
      setSession(null);
      setIsLoginOpen(true);
    }
  };

  return (
    <>
      <Shell
        activeTab={activeTab}
        onTabChange={setActiveTab}
        session={session}
        onLogout={handleLogout}
      >
        {activeTab === 'studio' && (
          <PolicyStudio
            session={session}
            onCandidateRegoChange={setCandidateRego}
          />
        )}

        {activeTab !== 'studio' && (
          <div className="space-y-6">
            <div className="bg-slate-900 border border-slate-800 rounded-xl p-6">
              <h2 className="text-xl font-semibold text-slate-100 mb-2">
                {activeTab === 'simulator' && 'Policy Simulator'}
                {activeTab === 'topology' && 'Cluster Topology & Convergence'}
                {activeTab === 'audit' && 'Filterable Audit Stream'}
                {activeTab === 'quarantine' && 'Emergency Principal Quarantine'}
              </h2>
              <p className="text-sm text-slate-400">
                Active management session role: <code className="text-cyan-400">{session?.role || 'unauthenticated'}</code>
                {candidateRego ? ' (Draft Rego in memory)' : ''}
              </p>
            </div>
          </div>
        )}
      </Shell>

      <LoginModal
        isOpen={isLoginOpen}
        onLoginSuccess={handleLoginSuccess}
      />
    </>
  );
};

export default App;
