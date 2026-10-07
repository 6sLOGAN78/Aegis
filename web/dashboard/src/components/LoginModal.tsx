import React, { useState } from 'react';
import { Shield, KeyRound, AlertCircle, Loader2 } from 'lucide-react';
import { apiClient, ApiError } from '../api/client';
import type { SessionInfo } from '../api/types';

interface LoginModalProps {
  isOpen: boolean;
  onLoginSuccess: (session: SessionInfo) => void;
}

export const LoginModal: React.FC<LoginModalProps> = ({ isOpen, onLoginSuccess }) => {
  const [username, setUsername] = useState('admin');
  const [password, setPassword] = useState('admin-secret');
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
        setErrorMessage(err.message || 'Authentication failed. Please verify credentials.');
      } else {
        setErrorMessage('Failed to connect to control plane management server.');
      }
    } finally {
      setIsLoading(false);
    }
  };

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/80 backdrop-blur-md p-4">
      <div className="w-full max-w-md bg-slate-900 border border-slate-800 rounded-xl p-8 shadow-2xl space-y-6">
        <div className="flex flex-col items-center text-center space-y-3">
          <div className="p-3 bg-cyan-500/10 border border-cyan-500/20 rounded-xl">
            <Shield className="w-10 h-10 text-cyan-400" />
          </div>
          <div>
            <h2 className="text-2xl font-semibold text-slate-100">Aegis Security Console</h2>
            <p className="text-sm text-slate-400 mt-1">Authenticate to access control plane management APIs</p>
          </div>
        </div>

        {errorMessage && (
          <div className="flex items-start space-x-3 bg-rose-500/10 border border-rose-500/30 p-3 rounded-lg text-rose-300 text-xs">
            <AlertCircle className="w-4 h-4 mt-0.5 shrink-0" />
            <span>{errorMessage}</span>
          </div>
        )}

        <form onSubmit={handleSubmit} className="space-y-4">
          <div>
            <label className="block text-xs font-semibold text-slate-300 uppercase tracking-wider mb-1.5">
              Operator Username
            </label>
            <input
              type="text"
              value={username}
              onChange={(e) => setUsername(e.target.value)}
              required
              className="w-full bg-slate-950 border border-slate-800 rounded-lg px-3.5 py-2.5 text-sm text-slate-100 font-mono focus:ring-2 focus:ring-cyan-500 focus:outline-none transition"
              placeholder="e.g. admin"
            />
          </div>

          <div>
            <label className="block text-xs font-semibold text-slate-300 uppercase tracking-wider mb-1.5">
              Secret Password
            </label>
            <div className="relative">
              <input
                type="password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                required
                className="w-full bg-slate-950 border border-slate-800 rounded-lg px-3.5 py-2.5 text-sm text-slate-100 font-mono focus:ring-2 focus:ring-cyan-500 focus:outline-none transition pr-10"
                placeholder="••••••••••••"
              />
              <KeyRound className="w-4 h-4 text-slate-500 absolute right-3 top-3" />
            </div>
          </div>

          <div className="pt-2">
            <button
              type="submit"
              disabled={isLoading}
              className="w-full flex items-center justify-center space-x-2 bg-cyan-600 hover:bg-cyan-500 text-white font-semibold py-2.5 px-4 rounded-lg transition disabled:opacity-50"
            >
              {isLoading ? (
                <>
                  <Loader2 className="w-4 h-4 animate-spin" />
                  <span>Authenticating...</span>
                </>
              ) : (
                <span>Authenticate Operator Session</span>
              )}
            </button>
          </div>
        </form>

        <div className="border-t border-slate-800/80 pt-4 text-center">
          <p className="text-xs text-slate-500">
            Default credentials: <code className="text-cyan-400">admin</code> / <code className="text-cyan-400">admin-secret</code>
          </p>
        </div>
      </div>
    </div>
  );
};
