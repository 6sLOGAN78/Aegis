import React, { useState, useEffect } from 'react';
import { Shell, TabId } from './components/Shell';
import { LoginModal } from './components/LoginModal';
import { PolicyStudio } from './components/PolicyStudio';
import { PolicySimulator } from './components/PolicySimulator';
import { AuditStream } from './components/AuditStream';
import { ClusterTopology } from './components/ClusterTopology';
import { EmergencyQuarantine } from './components/EmergencyQuarantine';
import { Overview } from './components/Overview';
import { apiClient } from './api/client';
import type { SessionInfo } from './api/types';

export const App: React.FC = () => {
  const [session, setSession] = useState<SessionInfo | null>(null);
  const [isLoginOpen, setIsLoginOpen] = useState(false);
  const [activeTab, setActiveTab] = useState<TabId>('overview');
  const [candidateRego, setCandidateRego] = useState<string | undefined>(undefined);
  // Snapshot version the candidate edit session is based on (If-Match for publish).
  // Lifted here with candidateRego so it survives PolicyStudio remounts.
  const [candidateBaseVersion, setCandidateBaseVersion] = useState<number | undefined>(undefined);

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
      {/* Mount the console only for a signed-in operator: screens poll the API,
          and polling behind the sign-in page just collects 401s. */}
      {session && (
        <Shell
          activeTab={activeTab}
          onTabChange={setActiveTab}
          session={session}
          onLogout={handleLogout}
        >
          {activeTab === 'overview' && <Overview onNavigate={setActiveTab} />}

          {activeTab === 'studio' && (
            <PolicyStudio
              session={session}
              candidateRego={candidateRego}
              onCandidateRegoChange={setCandidateRego}
              baseVersion={candidateBaseVersion}
              onBaseVersionChange={setCandidateBaseVersion}
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
      )}

      <LoginModal
        isOpen={isLoginOpen}
        onLoginSuccess={handleLoginSuccess}
      />
    </>
  );
};

export default App;
