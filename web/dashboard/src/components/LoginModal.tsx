import React, { useState } from 'react';
import { Shield, AlertCircle, Loader2 } from 'lucide-react';
import { apiClient, ApiError } from '../api/client';
import type { SessionInfo } from '../api/types';

interface LoginModalProps {
  isOpen: boolean;
  onLoginSuccess: (session: SessionInfo) => void;
}

// The seeded demo operator is only offered by the Vite dev server; production
// builds never ship credentials in the bundle.
const DEV_USER = import.meta.env.DEV ? 'admin' : '';
const DEV_PASSWORD = import.meta.env.DEV ? 'admin-secret' : '';

export const LoginModal: React.FC<LoginModalProps> = ({ isOpen, onLoginSuccess }) => {
  const [username, setUsername] = useState(DEV_USER);
  const [password, setPassword] = useState(DEV_PASSWORD);
  const [isLoading, setIsLoading] = useState(false);
  const [errorMessage, setErrorMessage] = useState<string | null>(null);

  if (!isOpen) return null;

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setIsLoading(true);
    setErrorMessage(null);

    try {
      const response = await apiClient.post<SessionInfo>('/control/v1/auth/login', {
        username,
        password,
      });

      apiClient.setCSRFToken(response.csrf_token);
      onLoginSuccess(response);
    } catch (err) {
      if (err instanceof ApiError) {
        setErrorMessage(err.message || 'Sign-in failed. Check the username and password.');
      } else {
        setErrorMessage('Cannot reach the control plane. Check it is running, then try again.');
      }
    } finally {
      setIsLoading(false);
    }
  };

  return (
    <div className="fixed inset-0 z-50 overflow-y-auto bg-bone">
      <div className="min-h-full flex flex-col items-center justify-center px-4 py-12">
        <div className="flex items-center gap-2.5 mb-8">
          <span className="grid place-items-center w-8 h-8 rounded-lg bg-ink">
            <Shield className="w-4.5 h-4.5 text-white" />
          </span>
          <span className="text-base font-medium text-slate-100">Aegis</span>
        </div>

        <main className="w-full max-w-sm panel p-8">
          <h1 className="text-2xl font-display text-slate-100">Sign in</h1>
          <p className="mt-1 text-sm text-slate-500">Operator access to the Aegis control plane.</p>

          <form onSubmit={handleSubmit} className="mt-6 space-y-4" noValidate={false}>
            <div>
              <label htmlFor="login-user" className="label">Username</label>
              <input
                id="login-user"
                type="text"
                autoComplete="username"
                autoFocus
                value={username}
                onChange={(e) => setUsername(e.target.value)}
                required
                className="field"
              />
            </div>

            <div>
              <label htmlFor="login-password" className="label">Password</label>
              <input
                id="login-password"
                type="password"
                autoComplete="current-password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                required
                aria-invalid={errorMessage ? true : undefined}
                aria-describedby={errorMessage ? 'login-error' : undefined}
                className="field"
              />
            </div>

            {errorMessage && (
              <p id="login-error" role="alert" className="flex items-start gap-2 text-sm text-rose-600">
                <AlertCircle className="w-4 h-4 mt-0.5 shrink-0" />
                <span>{errorMessage}</span>
              </p>
            )}

            <button type="submit" disabled={isLoading} className="btn btn-primary w-full h-10">
              {isLoading && <Loader2 className="animate-spin" />}
              {isLoading ? 'Signing in…' : 'Sign in'}
            </button>
          </form>
        </main>

        {import.meta.env.DEV && (
          <p className="mt-6 text-xs text-slate-500">
            Dev server: seeded operator <code>admin</code> / <code>admin-secret</code>
          </p>
        )}
      </div>
    </div>
  );
};
