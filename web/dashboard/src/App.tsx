import React, { useState, useEffect } from 'react';
import { Shell, TabId } from './components/Shell';
import { LoginModal } from './components/LoginModal';
import { PolicyStudio } from './components/PolicyStudio';
import { PolicySimulator } from './components/PolicySimulator';
import { AuditStream } from './components/AuditStream';
import { ClusterTopology } from './components/ClusterTopology';
import { EmergencyQuarantine } from './components/EmergencyQuarantine';
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

        {activeTab === 'simulator' && (
          <PolicySimulator candidateRego={candidateRego} />
        )}

        {activeTab === 'topology' && (
          <ClusterTopology />
        )}

        {activeTab === 'audit' && (
          <AuditStream />
        )}

        {activeTab === 'quarantine' && (
          <EmergencyQuarantine session={session} />
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
