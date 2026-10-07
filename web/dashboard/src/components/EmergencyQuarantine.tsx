import React, { useState } from 'react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import {
  AlertTriangle,
  ShieldAlert,
  KeyRound,
  UserX,
  ListFilter,
  CheckCircle2,
  Trash2,
  Lock,
} from 'lucide-react';
import { apiClient } from '../api/client';
import type { QuarantineRecord, SessionInfo } from '../api/types';

interface EmergencyQuarantineProps {
  session: SessionInfo | null;
}

export const EmergencyQuarantine: React.FC<EmergencyQuarantineProps> = ({ session }) => {
  const queryClient = useQueryClient();
  const [activeTab, setActiveTab] = useState<'principal' | 'token' | 'list'>('principal');

  // Principal Quarantine Form State
  const [principalId, setPrincipalId] = useState('');
  const [quarantineReason, setQuarantineReason] = useState('');
  const [quarantineTtlHours, setQuarantineTtlHours] = useState('24');
  const [quarantineConfirmText, setQuarantineConfirmText] = useState('');

  // Token Revocation Form State
  const [jti, setJti] = useState('');
  const [revokeReason, setRevokeReason] = useState('');
  const [revokeConfirmText, setRevokeConfirmText] = useState('');

  // Feedback notifications
  const [successBanner, setSuccessBanner] = useState<string | null>(null);
  const [errorBanner, setErrorBanner] = useState<string | null>(null);

  const isSecOps = session?.role === 'sec-ops';

  // Query active quarantines from Redis
  const {
    data: activeQuarantinesData,
    isLoading: isLoadingQuarantines,
    refetch: refetchQuarantines,
  } = useQuery<{ quarantines: QuarantineRecord[] }>({
    queryKey: ['quarantines'],
    queryFn: async () => {
      try {
        const resp = await apiClient.get<{ quarantines: QuarantineRecord[] }>('/control/v1/principals/quarantine');
        return resp;
      } catch {
        return { quarantines: [] };
      }
    },
    refetchInterval: 10000,
  });

  const activeQuarantines = activeQuarantinesData?.quarantines || [];

  // Mutation: Quarantine Principal
  const quarantineMutation = useMutation({
    mutationFn: async () => {
      setErrorBanner(null);
      setSuccessBanner(null);
      const expiresAt = new Date(Date.now() + parseInt(quarantineTtlHours, 10) * 3600 * 1000).toISOString();
      return apiClient.post<QuarantineRecord>(
        `/control/v1/principals/${encodeURIComponent(principalId.trim())}/quarantine`,
        {
          reason: quarantineReason.trim(),
          expires_at: expiresAt,
        }
      );
    },
    onSuccess: (data) => {
      setSuccessBanner(`Principal "${data.principal_id}" successfully quarantined cluster-wide (<5s SLA).`);
      setPrincipalId('');
      setQuarantineReason('');
      setQuarantineConfirmText('');
      queryClient.invalidateQueries({ queryKey: ['quarantines'] });
    },
    onError: (err: Error) => {
      setErrorBanner(`Quarantine Propagation Failed: ${err.message}`);
    },
  });

  // Mutation: Revoke Token JTI
  const revokeMutation = useMutation({
    mutationFn: async () => {
      setErrorBanner(null);
      setSuccessBanner(null);
      return apiClient.post<{ status: string; jti: string }>('/control/v1/revocations', {
        jti: jti.trim(),
        reason: revokeReason.trim() || 'ADMINISTRATIVE_TOKEN_REVOCATION',
      });
    },
    onSuccess: (data) => {
      setSuccessBanner(`Token JTI "${data.jti}" successfully revoked cluster-wide.`);
      setJti('');
      setRevokeReason('');
      setRevokeConfirmText('');
    },
    onError: (err: Error) => {
      setErrorBanner(`Token Revocation Failed: ${err.message}`);
    },
  });

  // Mutation: Remove Quarantine Block
  const unquarantineMutation = useMutation({
    mutationFn: async (id: string) => {
      setErrorBanner(null);
      setSuccessBanner(null);
      return apiClient.delete(`/control/v1/principals/${encodeURIComponent(id)}/quarantine`);
    },
    onSuccess: (_, id) => {
      setSuccessBanner(`Principal "${id}" unquarantined successfully.`);
      queryClient.invalidateQueries({ queryKey: ['quarantines'] });
    },
    onError: (err: Error) => {
      setErrorBanner(`Failed to remove quarantine block: ${err.message}`);
    },
  });

  const isQuarantineValid =
    isSecOps &&
    principalId.trim().length > 0 &&
    quarantineReason.trim().length > 0 &&
    quarantineConfirmText.trim() === 'QUARANTINE';

  const isRevokeValid =
    isSecOps &&
    jti.trim().length > 0 &&
    revokeConfirmText.trim() === 'REVOKE';

  return (
    <div className="space-y-6">
      {/* High-Visibility Emergency Warning Banner */}
      <div className="bg-rose-950/60 border-2 border-rose-500/80 rounded-xl p-5 shadow-xl text-rose-100 flex flex-col md:flex-row md:items-center justify-between gap-4">
        <div className="flex items-start space-x-3.5">
          <div className="p-2.5 bg-rose-500/20 border border-rose-500/40 rounded-lg text-rose-400 flex-shrink-0 mt-0.5">
            <ShieldAlert className="w-6 h-6 animate-pulse" />
          </div>
          <div>
            <div className="flex items-center space-x-3">
              <span className="text-xs font-bold uppercase tracking-wider px-2 py-0.5 rounded bg-rose-500 text-white font-mono">
                EMERGENCY INTERVENTION
              </span>
              <h2 className="text-lg font-bold text-white tracking-tight">
                IMMEDIATE CLUSTER-WIDE BLOCK IN &lt;5s
              </h2>
            </div>
            <p className="text-xs text-rose-200/90 mt-1 leading-relaxed">
              This action writes directly to Redis with a cluster-wide propagation SLA of &lt;5 seconds. All gateway replicas will immediately return HTTP 403 Forbidden (<code className="font-mono bg-rose-900/60 px-1 py-0.5 rounded text-rose-200">PRINCIPAL_QUARANTINED</code>) for blocked subjects.
            </p>
          </div>
        </div>

        <div className="flex items-center space-x-2 text-xs font-mono bg-rose-900/50 border border-rose-500/30 px-3 py-2 rounded-lg self-start md:self-auto flex-shrink-0">
          <Lock className="w-3.5 h-3.5 text-rose-400" />
          <span>Role Gate: <strong className="text-white">{session?.role || 'none'}</strong></span>
        </div>
      </div>

      {/* Role Notice if not sec-ops */}
      {!isSecOps && (
        <div className="bg-amber-500/10 border border-amber-500/30 rounded-xl p-4 text-xs text-amber-300 flex items-center space-x-3">
          <AlertTriangle className="w-4 h-4 flex-shrink-0 text-amber-400" />
          <span>
            You are authenticated with role <strong className="font-semibold text-amber-200">{session?.role || 'viewer'}</strong>. Emergency quarantine and token revocation require <strong className="font-semibold text-amber-200">sec-ops</strong> privileges.
          </span>
        </div>
      )}

      {/* Success Notification Banner */}
      {successBanner && (
        <div className="bg-emerald-500/10 border border-emerald-500/30 rounded-xl p-4 text-xs text-emerald-300 flex items-center justify-between">
          <div className="flex items-center space-x-2">
            <CheckCircle2 className="w-4 h-4 text-emerald-400 flex-shrink-0" />
            <span>{successBanner}</span>
          </div>
          <button
            onClick={() => setSuccessBanner(null)}
            className="text-emerald-400 hover:text-emerald-300 font-bold ml-4"
          >
            ×
          </button>
        </div>
      )}

      {/* Error Notification Banner */}
      {errorBanner && (
        <div className="bg-rose-500/10 border border-rose-500/30 rounded-xl p-4 text-xs text-rose-300 flex items-center justify-between">
          <div className="flex items-center space-x-2">
            <AlertTriangle className="w-4 h-4 text-rose-400 flex-shrink-0" />
            <span>{errorBanner}</span>
          </div>
          <button
            onClick={() => setErrorBanner(null)}
            className="text-rose-400 hover:text-rose-300 font-bold ml-4"
          >
            ×
          </button>
        </div>
      )}

      {/* Navigation Sub-Tabs */}
      <div className="flex items-center space-x-2 border-b border-slate-800 pb-3">
        <button
          onClick={() => setActiveTab('principal')}
          className={`flex items-center space-x-2 px-3 py-1.5 rounded-lg text-xs font-medium transition ${
            activeTab === 'principal'
              ? 'bg-rose-600 text-white'
              : 'bg-slate-900 text-slate-400 hover:text-slate-200 hover:bg-slate-800'
          }`}
        >
          <UserX className="w-3.5 h-3.5" />
          <span>Quarantine Principal</span>
        </button>

        <button
          onClick={() => setActiveTab('token')}
          className={`flex items-center space-x-2 px-3 py-1.5 rounded-lg text-xs font-medium transition ${
            activeTab === 'token'
              ? 'bg-rose-600 text-white'
              : 'bg-slate-900 text-slate-400 hover:text-slate-200 hover:bg-slate-800'
          }`}
        >
          <KeyRound className="w-3.5 h-3.5" />
          <span>Revoke Token JTI</span>
        </button>

        <button
          onClick={() => setActiveTab('list')}
          className={`flex items-center space-x-2 px-3 py-1.5 rounded-lg text-xs font-medium transition ${
            activeTab === 'list'
              ? 'bg-rose-600 text-white'
              : 'bg-slate-900 text-slate-400 hover:text-slate-200 hover:bg-slate-800'
          }`}
        >
          <ListFilter className="w-3.5 h-3.5" />
          <span>Active Quarantines ({activeQuarantines.length})</span>
        </button>
      </div>

      {/* Tab 1: Quarantine Principal ID */}
      {activeTab === 'principal' && (
        <div className="bg-slate-900 border border-slate-800 rounded-xl p-6 max-w-2xl">
          <h3 className="text-base font-semibold text-slate-100 flex items-center space-x-2 mb-1">
            <UserX className="w-5 h-5 text-rose-400" />
            <span>Emergency Principal Quarantine</span>
          </h3>
          <p className="text-xs text-slate-400 mb-6 leading-relaxed">
            Immediately cuts off all ingress access for an individual user ID or workload principal ID across all gateway replicas.
          </p>

          <form
            onSubmit={(e) => {
              e.preventDefault();
              if (isQuarantineValid) quarantineMutation.mutate();
            }}
            className="space-y-4"
          >
            <div>
              <label className="block text-xs font-medium text-slate-300 mb-1">
                Principal ID <span className="text-rose-400">*</span>
              </label>
              <input
                type="text"
                placeholder="e.g. user_bad_actor_99 or workload-orders-svc"
                value={principalId}
                onChange={(e) => setPrincipalId(e.target.value)}
                disabled={!isSecOps || quarantineMutation.isPending}
                className="w-full bg-slate-950 border border-slate-800 rounded-lg px-3 py-2 text-sm text-slate-100 font-mono placeholder:text-slate-600 focus:outline-none focus:border-rose-500 disabled:opacity-50"
              />
            </div>

            <div>
              <label className="block text-xs font-medium text-slate-300 mb-1">
                Mandatory Reason (Audit Justification) <span className="text-rose-400">*</span>
              </label>
              <input
                type="text"
                placeholder="e.g. Compromised credentials detected in security incident SEC-1042"
                value={quarantineReason}
                onChange={(e) => setQuarantineReason(e.target.value)}
                disabled={!isSecOps || quarantineMutation.isPending}
                className="w-full bg-slate-950 border border-slate-800 rounded-lg px-3 py-2 text-sm text-slate-100 placeholder:text-slate-600 focus:outline-none focus:border-rose-500 disabled:opacity-50"
              />
            </div>

            <div>
              <label className="block text-xs font-medium text-slate-300 mb-1">
                Quarantine Duration (TTL)
              </label>
              <select
                value={quarantineTtlHours}
                onChange={(e) => setQuarantineTtlHours(e.target.value)}
                disabled={!isSecOps || quarantineMutation.isPending}
                className="w-full bg-slate-950 border border-slate-800 rounded-lg px-3 py-2 text-sm text-slate-100 focus:outline-none focus:border-rose-500 disabled:opacity-50"
              >
                <option value="1">1 hour (Investigation window)</option>
                <option value="24">24 hours (Standard containment)</option>
                <option value="168">7 days (Extended containment)</option>
                <option value="720">30 days (Permanent block)</option>
              </select>
            </div>

            {/* Safety Confirmation Input */}
            <div className="p-4 bg-slate-950/80 border border-rose-900/50 rounded-lg space-y-2">
              <label className="block text-xs font-medium text-rose-300">
                Destructive Safety Confirmation: Type <span className="font-mono font-bold text-white uppercase bg-rose-900/80 px-1 py-0.5 rounded">QUARANTINE</span> to confirm
              </label>
              <input
                type="text"
                placeholder="Type QUARANTINE here"
                value={quarantineConfirmText}
                onChange={(e) => setQuarantineConfirmText(e.target.value)}
                disabled={!isSecOps || quarantineMutation.isPending}
                className="w-full bg-slate-900 border border-slate-700 rounded-lg px-3 py-2 text-sm text-white font-mono placeholder:text-slate-600 focus:outline-none focus:border-rose-500 uppercase disabled:opacity-50"
              />
              <p className="text-[11px] text-slate-400">
                Requires typing &quot;QUARANTINE&quot; in capital letters before block execution.
              </p>
            </div>

            <div className="pt-2 flex items-center justify-end space-x-3">
              <button
                type="button"
                onClick={() => {
                  setPrincipalId('');
                  setQuarantineReason('');
                  setQuarantineConfirmText('');
                }}
                className="px-4 py-2 bg-slate-800 hover:bg-slate-700 text-slate-300 rounded-lg text-xs font-medium transition"
              >
                Dismiss Emergency Action
              </button>
              <button
                type="submit"
                disabled={!isQuarantineValid || quarantineMutation.isPending}
                className="px-5 py-2 bg-rose-600 hover:bg-rose-500 disabled:bg-slate-800 disabled:text-slate-600 disabled:cursor-not-allowed text-white rounded-lg text-xs font-semibold transition flex items-center space-x-2 shadow-sm"
              >
                {quarantineMutation.isPending ? (
                  <>
                    <span className="w-3.5 h-3.5 border-2 border-white/30 border-t-white rounded-full animate-spin" />
                    <span>Propagating to Redis...</span>
                  </>
                ) : (
                  <>
                    <ShieldAlert className="w-4 h-4" />
                    <span>Confirm Quarantine Block</span>
                  </>
                )}
              </button>
            </div>
          </form>
        </div>
      )}

      {/* Tab 2: Revoke Token JTI */}
      {activeTab === 'token' && (
        <div className="bg-slate-900 border border-slate-800 rounded-xl p-6 max-w-2xl">
          <h3 className="text-base font-semibold text-slate-100 flex items-center space-x-2 mb-1">
            <KeyRound className="w-5 h-5 text-rose-400" />
            <span>Emergency Token JTI Revocation</span>
          </h3>
          <p className="text-xs text-slate-400 mb-6 leading-relaxed">
            Instantly revokes a specific JWT by its unique ID (<code className="font-mono text-cyan-400">jti</code> claim). Future requests bearing this token fail immediately.
          </p>

          <form
            onSubmit={(e) => {
              e.preventDefault();
              if (isRevokeValid) revokeMutation.mutate();
            }}
            className="space-y-4"
          >
            <div>
              <label className="block text-xs font-medium text-slate-300 mb-1">
                Token JTI (JWT ID UUID) <span className="text-rose-400">*</span>
              </label>
              <input
                type="text"
                placeholder="e.g. c37bb0aa-0ff7-4c45-9ec1-f2f9c5bc79f1"
                value={jti}
                onChange={(e) => setJti(e.target.value)}
                disabled={!isSecOps || revokeMutation.isPending}
                className="w-full bg-slate-950 border border-slate-800 rounded-lg px-3 py-2 text-sm text-slate-100 font-mono placeholder:text-slate-600 focus:outline-none focus:border-rose-500 disabled:opacity-50"
              />
            </div>

            <div>
              <label className="block text-xs font-medium text-slate-300 mb-1">
                Revocation Reason (Optional)
              </label>
              <input
                type="text"
                placeholder="e.g. Stolen bearer token intercepted in exfiltration attempt"
                value={revokeReason}
                onChange={(e) => setRevokeReason(e.target.value)}
                disabled={!isSecOps || revokeMutation.isPending}
                className="w-full bg-slate-950 border border-slate-800 rounded-lg px-3 py-2 text-sm text-slate-100 placeholder:text-slate-600 focus:outline-none focus:border-rose-500 disabled:opacity-50"
              />
            </div>

            {/* Safety Confirmation Input */}
            <div className="p-4 bg-slate-950/80 border border-rose-900/50 rounded-lg space-y-2">
              <label className="block text-xs font-medium text-rose-300">
                Destructive Safety Confirmation: Type <span className="font-mono font-bold text-white uppercase bg-rose-900/80 px-1 py-0.5 rounded">REVOKE</span> to confirm
              </label>
              <input
                type="text"
                placeholder="Type REVOKE here"
                value={revokeConfirmText}
                onChange={(e) => setRevokeConfirmText(e.target.value)}
                disabled={!isSecOps || revokeMutation.isPending}
                className="w-full bg-slate-900 border border-slate-700 rounded-lg px-3 py-2 text-sm text-white font-mono placeholder:text-slate-600 focus:outline-none focus:border-rose-500 uppercase disabled:opacity-50"
              />
            </div>

            <div className="pt-2 flex items-center justify-end space-x-3">
              <button
                type="button"
                onClick={() => {
                  setJti('');
                  setRevokeReason('');
                  setRevokeConfirmText('');
                }}
                className="px-4 py-2 bg-slate-800 hover:bg-slate-700 text-slate-300 rounded-lg text-xs font-medium transition"
              >
                Dismiss Emergency Action
              </button>
              <button
                type="submit"
                disabled={!isRevokeValid || revokeMutation.isPending}
                className="px-5 py-2 bg-rose-600 hover:bg-rose-500 disabled:bg-slate-800 disabled:text-slate-600 disabled:cursor-not-allowed text-white rounded-lg text-xs font-semibold transition flex items-center space-x-2 shadow-sm"
              >
                {revokeMutation.isPending ? (
                  <>
                    <span className="w-3.5 h-3.5 border-2 border-white/30 border-t-white rounded-full animate-spin" />
                    <span>Revoking in Redis...</span>
                  </>
                ) : (
                  <>
                    <KeyRound className="w-4 h-4" />
                    <span>Execute Token Revocation</span>
                  </>
                )}
              </button>
            </div>
          </form>
        </div>
      )}

      {/* Tab 3: Active Quarantines Table */}
      {activeTab === 'list' && (
        <div className="bg-slate-900 border border-slate-800 rounded-xl overflow-hidden shadow-sm">
          <div className="p-4 border-b border-slate-800 flex items-center justify-between">
            <div>
              <h3 className="text-sm font-semibold text-slate-100">
                Active Cluster-Wide Quarantines
              </h3>
              <p className="text-xs text-slate-400">
                Principals currently rejected with HTTP 403 Forbidden across all gateway nodes
              </p>
            </div>
            <button
              onClick={() => refetchQuarantines()}
              className="px-3 py-1.5 bg-slate-800 hover:bg-slate-700 text-slate-300 text-xs rounded-lg transition"
            >
              Refresh List
            </button>
          </div>

          {isLoadingQuarantines && (
            <div className="p-8 text-center text-xs text-slate-400 animate-pulse">
              Loading active quarantines from Redis...
            </div>
          )}

          {!isLoadingQuarantines && activeQuarantines.length === 0 && (
            <div className="p-12 text-center max-w-md mx-auto">
              <div className="w-10 h-10 bg-slate-800 text-emerald-400 rounded-full flex items-center justify-center mx-auto mb-3">
                <CheckCircle2 className="w-5 h-5" />
              </div>
              <h4 className="text-sm font-semibold text-slate-200">
                No Active Principal Quarantines
              </h4>
              <p className="text-xs text-slate-400 mt-1 mb-4 leading-relaxed">
                No users or workloads are currently quarantined in Redis. All non-revoked credentials evaluate through standard Rego policies.
              </p>
              <button
                onClick={() => setActiveTab('principal')}
                className="px-3 py-1.5 bg-rose-600 hover:bg-rose-500 text-white rounded-lg text-xs font-medium transition"
              >
                Quarantine Principal ID
              </button>
            </div>
          )}

          {!isLoadingQuarantines && activeQuarantines.length > 0 && (
            <div className="overflow-x-auto">
              <table className="w-full text-left border-collapse text-xs">
                <thead>
                  <tr className="border-b border-slate-800 bg-slate-950/60 text-slate-400">
                    <th className="py-2.5 px-4 font-medium">Principal ID</th>
                    <th className="py-2.5 px-4 font-medium">Reason</th>
                    <th className="py-2.5 px-4 font-medium">Quarantined At</th>
                    <th className="py-2.5 px-4 font-medium">Expires At</th>
                    <th className="py-2.5 px-4 font-medium text-right">Action</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-slate-800/60 font-mono">
                  {activeQuarantines.map((rec) => (
                    <tr key={rec.principal_id} className="hover:bg-slate-800/30">
                      <td className="py-3 px-4 font-semibold text-rose-300">
                        {rec.principal_id}
                      </td>
                      <td className="py-3 px-4 font-sans text-slate-300 max-w-xs truncate" title={rec.reason}>
                        {rec.reason}
                      </td>
                      <td className="py-3 px-4 text-slate-400">
                        {new Date(rec.quarantined_at).toLocaleTimeString()}
                      </td>
                      <td className="py-3 px-4 text-slate-400">
                        {rec.expires_at ? new Date(rec.expires_at).toLocaleTimeString() : 'Permanent'}
                      </td>
                      <td className="py-3 px-4 text-right">
                        <button
                          onClick={() => unquarantineMutation.mutate(rec.principal_id)}
                          disabled={!isSecOps || unquarantineMutation.isPending}
                          className="inline-flex items-center space-x-1 px-2.5 py-1 bg-slate-800 hover:bg-rose-900/40 text-slate-300 hover:text-rose-200 border border-slate-700 hover:border-rose-500/40 rounded transition text-xs font-sans disabled:opacity-50"
                        >
                          <Trash2 className="w-3.5 h-3.5" />
                          <span>Remove Block</span>
                        </button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>
      )}
    </div>
  );
};
