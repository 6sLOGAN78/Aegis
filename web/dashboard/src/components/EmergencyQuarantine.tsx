import React, { useState } from 'react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { AlertTriangle, ShieldAlert, KeyRound, UserX, CheckCircle2, Trash2, X, RefreshCw } from 'lucide-react';
import { PageHeader } from './PageHeader';
import { apiClient } from '../api/client';
import { newIdempotencyKey } from '../api/idempotency';
import type { QuarantineRecord, SessionInfo } from '../api/types';

interface EmergencyQuarantineProps {
  session: SessionInfo | null;
}

export const EmergencyQuarantine: React.FC<EmergencyQuarantineProps> = ({ session }) => {
  const queryClient = useQueryClient();
  const [activeTab, setActiveTab] = useState<'principal' | 'token'>('principal');

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
    isError: isQuarantineListError,
    error: quarantineListError,
    refetch: refetchQuarantines,
    isFetching: isFetchingQuarantines,
  } = useQuery<{ quarantines: QuarantineRecord[] }>({
    queryKey: ['quarantines'],
    queryFn: () => apiClient.get<{ quarantines: QuarantineRecord[] }>('/control/v1/principals/quarantine'),
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
        },
        { idempotencyKey: newIdempotencyKey() }
      );
    },
    onSuccess: (data) => {
      setSuccessBanner(`${data.principal_id} is quarantined. Gateways reject it within about 5 seconds.`);
      setPrincipalId('');
      setQuarantineReason('');
      setQuarantineConfirmText('');
      queryClient.invalidateQueries({ queryKey: ['quarantines'] });
    },
    onError: (err: Error) => {
      setErrorBanner(`Quarantine failed: ${err.message}`);
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
      }, { idempotencyKey: newIdempotencyKey() });
    },
    onSuccess: (data) => {
      setSuccessBanner(`Token ${data.jti} is revoked.`);
      setJti('');
      setRevokeReason('');
      setRevokeConfirmText('');
    },
    onError: (err: Error) => {
      setErrorBanner(`Revocation failed: ${err.message}`);
    },
  });

  // Mutation: Remove Quarantine Block
  const unquarantineMutation = useMutation({
    mutationFn: async (id: string) => {
      setErrorBanner(null);
      setSuccessBanner(null);
      return apiClient.delete(`/control/v1/principals/${encodeURIComponent(id)}/quarantine`, {
        idempotencyKey: newIdempotencyKey(),
      });
    },
    onSuccess: (_, id) => {
      setSuccessBanner(`${id} is no longer quarantined.`);
      queryClient.invalidateQueries({ queryKey: ['quarantines'] });
    },
    onError: (err: Error) => {
      setErrorBanner(`Could not lift the quarantine: ${err.message}`);
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

  const formatDateTime = (iso?: string) =>
    iso
      ? new Date(iso).toLocaleString(undefined, { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })
      : 'No expiry';

  const busy = quarantineMutation.isPending || revokeMutation.isPending;

  return (
    <div>
      <PageHeader
        title="Quarantine"
        description={
          <>
            Block a principal or revoke a token on every gateway. Blocked requests get 403{' '}
            <code className="font-mono text-[13px] text-slate-300">PRINCIPAL_QUARANTINED</code> within about 5 seconds.
          </>
        }
      />

      {!isSecOps && (
        <p className="mb-4 flex items-start gap-2 text-sm text-amber-300">
          <AlertTriangle className="w-4 h-4 mt-0.5 shrink-0" />
          <span>
            You are signed in as <strong className="font-medium">{session?.role || 'viewer'}</strong>. Quarantine
            and token revocation need the <strong className="font-medium">sec-ops</strong> role.
          </span>
        </p>
      )}

      {successBanner && (
        <div role="status" className="mb-4 panel px-4 py-3 flex items-center justify-between gap-3 text-sm">
          <span className="flex items-center gap-2 text-slate-100">
            <CheckCircle2 className="w-4 h-4 shrink-0 text-emerald-500" />
            {successBanner}
          </span>
          <button onClick={() => setSuccessBanner(null)} aria-label="Dismiss message" className="btn btn-ghost btn-sm">
            <X />
          </button>
        </div>
      )}

      {errorBanner && (
        <div role="alert" className="mb-4 panel px-4 py-3 flex items-center justify-between gap-3 text-sm border-rose-300">
          <span className="flex items-center gap-2 text-rose-600">
            <AlertTriangle className="w-4 h-4 shrink-0" />
            {errorBanner}
          </span>
          <button onClick={() => setErrorBanner(null)} aria-label="Dismiss error" className="btn btn-ghost btn-sm">
            <X />
          </button>
        </div>
      )}

      <div className="grid grid-cols-1 lg:grid-cols-[minmax(0,5fr)_minmax(0,7fr)] gap-6 items-start">
        <section className="panel">
          <div className="panel-head">
            <div role="tablist" aria-label="Action" className="flex gap-1 -ml-1.5">
              {(
                [
                  { id: 'principal', label: 'Quarantine principal', icon: UserX },
                  { id: 'token', label: 'Revoke token', icon: KeyRound },
                ] as const
              ).map(({ id, label, icon: Icon }) => (
                <button
                  key={id}
                  role="tab"
                  aria-selected={activeTab === id}
                  onClick={() => setActiveTab(id)}
                  className={`btn btn-sm ${activeTab === id ? 'btn-secondary border-ink' : 'btn-ghost'}`}
                >
                  <Icon />
                  {label}
                </button>
              ))}
            </div>
          </div>

          {activeTab === 'principal' ? (
            <form
              onSubmit={(e) => {
                e.preventDefault();
                if (isQuarantineValid) quarantineMutation.mutate();
              }}
              className="p-5 space-y-4"
            >
              <div>
                <label htmlFor="q-principal" className="label">Principal ID</label>
                <input
                  id="q-principal"
                  type="text"
                  required
                  placeholder="usr_alice or spiffe://aegis.local/workload/orders"
                  value={principalId}
                  onChange={(e) => setPrincipalId(e.target.value)}
                  disabled={!isSecOps || busy}
                  className="field font-mono text-[13px]"
                />
              </div>
              <div>
                <label htmlFor="q-reason" className="label">Reason</label>
                <input
                  id="q-reason"
                  type="text"
                  required
                  placeholder="Credentials leaked in incident SEC-1042"
                  value={quarantineReason}
                  onChange={(e) => setQuarantineReason(e.target.value)}
                  disabled={!isSecOps || busy}
                  className="field"
                />
                <p className="hint">Stored with the quarantine and written to the audit log.</p>
              </div>
              <div>
                <label htmlFor="q-ttl" className="label">Duration</label>
                <select
                  id="q-ttl"
                  value={quarantineTtlHours}
                  onChange={(e) => setQuarantineTtlHours(e.target.value)}
                  disabled={!isSecOps || busy}
                  className="field"
                >
                  <option value="1">1 hour</option>
                  <option value="24">24 hours</option>
                  <option value="168">7 days</option>
                  <option value="720">30 days</option>
                </select>
              </div>
              <div>
                <label htmlFor="q-confirm" className="label">
                  Type <span className="font-mono text-rose-600">QUARANTINE</span> to confirm
                </label>
                <input
                  id="q-confirm"
                  type="text"
                  autoComplete="off"
                  value={quarantineConfirmText}
                  onChange={(e) => setQuarantineConfirmText(e.target.value)}
                  disabled={!isSecOps || busy}
                  className="field font-mono text-[13px]"
                />
              </div>
              <div className="pt-2 flex justify-end gap-2">
                <button
                  type="button"
                  onClick={() => {
                    setPrincipalId('');
                    setQuarantineReason('');
                    setQuarantineConfirmText('');
                  }}
                  className="btn btn-ghost"
                >
                  Clear
                </button>
                <button type="submit" disabled={!isQuarantineValid || busy} className="btn btn-danger">
                  <ShieldAlert />
                  {quarantineMutation.isPending ? 'Quarantining…' : 'Quarantine principal'}
                </button>
              </div>
            </form>
          ) : (
            <form
              onSubmit={(e) => {
                e.preventDefault();
                if (isRevokeValid) revokeMutation.mutate();
              }}
              className="p-5 space-y-4"
            >
              <div>
                <label htmlFor="r-jti" className="label">Token ID (jti claim)</label>
                <input
                  id="r-jti"
                  type="text"
                  required
                  placeholder="c37bb0aa-0ff7-4c45-9ec1-f2f9c5bc79f1"
                  value={jti}
                  onChange={(e) => setJti(e.target.value)}
                  disabled={!isSecOps || busy}
                  className="field font-mono text-[13px]"
                />
              </div>
              <div>
                <label htmlFor="r-reason" className="label">
                  Reason <span className="font-normal text-slate-500">(optional)</span>
                </label>
                <input
                  id="r-reason"
                  type="text"
                  placeholder="Token exposed in a support ticket"
                  value={revokeReason}
                  onChange={(e) => setRevokeReason(e.target.value)}
                  disabled={!isSecOps || busy}
                  className="field"
                />
              </div>
              <div>
                <label htmlFor="r-confirm" className="label">
                  Type <span className="font-mono text-rose-600">REVOKE</span> to confirm
                </label>
                <input
                  id="r-confirm"
                  type="text"
                  autoComplete="off"
                  value={revokeConfirmText}
                  onChange={(e) => setRevokeConfirmText(e.target.value)}
                  disabled={!isSecOps || busy}
                  className="field font-mono text-[13px]"
                />
              </div>
              <div className="pt-2 flex justify-end gap-2">
                <button
                  type="button"
                  onClick={() => {
                    setJti('');
                    setRevokeReason('');
                    setRevokeConfirmText('');
                  }}
                  className="btn btn-ghost"
                >
                  Clear
                </button>
                <button type="submit" disabled={!isRevokeValid || busy} className="btn btn-danger">
                  <KeyRound />
                  {revokeMutation.isPending ? 'Revoking…' : 'Revoke token'}
                </button>
              </div>
            </form>
          )}
        </section>

        <section className="panel overflow-hidden">
          <div className="panel-head">
            <h2 className="panel-title">
              Active quarantines <span className="ml-1 text-slate-500 num">{activeQuarantines.length}</span>
            </h2>
            <button
              onClick={() => refetchQuarantines()}
              disabled={isFetchingQuarantines}
              aria-label="Refresh active quarantines"
              className="btn btn-ghost btn-sm"
            >
              <RefreshCw className={isFetchingQuarantines ? 'animate-spin' : ''} />
            </button>
          </div>

          {isLoadingQuarantines ? (
            <div className="divide-y divide-mist" aria-busy="true" aria-label="Loading quarantines">
              {[1, 2].map((n) => (
                <div key={n} className="flex items-center gap-6 px-5 h-12 animate-pulse">
                  <div className="h-3.5 w-36 bg-linen rounded" />
                  <div className="h-3.5 w-48 bg-linen rounded" />
                </div>
              ))}
            </div>
          ) : isQuarantineListError ? (
            <div role="alert" className="px-5 py-10 text-center">
              <p className="text-sm text-rose-600">Could not load quarantines</p>
              <p className="mt-1 text-sm text-slate-500">
                {quarantineListError instanceof Error ? quarantineListError.message : 'The control plane did not answer.'}
              </p>
              <button onClick={() => refetchQuarantines()} className="btn btn-secondary btn-sm mt-4">Try again</button>
            </div>
          ) : activeQuarantines.length === 0 ? (
            <div className="px-5 py-14 text-center">
              <p className="text-sm text-slate-300">No principals are quarantined</p>
              <p className="mt-1 text-sm text-slate-500">Every request is evaluated by the active policy.</p>
            </div>
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full text-left text-[13px]">
                <thead>
                  <tr className="text-xs text-slate-500 border-b border-mist">
                    <th scope="col" className="font-medium px-4 h-10">Principal</th>
                    <th scope="col" className="font-medium px-4 h-10">Reason</th>
                    <th scope="col" className="font-medium px-4 h-10">Since</th>
                    <th scope="col" className="font-medium px-4 h-10">Expires</th>
                    <th scope="col" className="px-4 h-10"><span className="sr-only">Actions</span></th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-mist">
                  {activeQuarantines.map((rec) => (
                    <tr key={rec.principal_id}>
                      <td className="px-4 h-12 font-mono text-[12px] text-slate-100 max-w-[200px] truncate" title={rec.principal_id}>
                        {rec.principal_id}
                      </td>
                      <td className="px-4 h-12 text-slate-300 max-w-[220px] truncate" title={rec.reason}>{rec.reason}</td>
                      <td className="px-4 h-12 whitespace-nowrap text-slate-400 num">
                        <time dateTime={rec.quarantined_at}>{formatDateTime(rec.quarantined_at)}</time>
                      </td>
                      <td className="px-4 h-12 whitespace-nowrap text-slate-400 num">
                        <time dateTime={rec.expires_at}>{formatDateTime(rec.expires_at)}</time>
                      </td>
                      <td className="px-4 h-12 text-right">
                        <button
                          onClick={() => unquarantineMutation.mutate(rec.principal_id)}
                          disabled={!isSecOps || unquarantineMutation.isPending}
                          aria-label={`Lift quarantine for ${rec.principal_id}`}
                          className="btn btn-secondary btn-sm"
                        >
                          <Trash2 />
                          Lift
                        </button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </section>
      </div>
    </div>
  );
};
