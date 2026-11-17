import React, { useState, useEffect } from 'react';
import { useQuery } from '@tanstack/react-query';
import { RefreshCw, Server, AlertTriangle, CheckCircle2, AlertCircle } from 'lucide-react';
import { PageHeader } from './PageHeader';
import { apiClient } from '../api/client';
import type { GatewayListResponse, GatewayStatus } from '../api/types';

export const ClusterTopology: React.FC = () => {
  const [now, setNow] = useState<number>(Date.now());

  // Update clock every second for smooth lease countdowns
  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(timer);
  }, []);

  const {
    data,
    isLoading,
    isError,
    error,
    refetch,
    isFetching,
  } = useQuery<GatewayListResponse>({
    queryKey: ['gateways'],
    queryFn: () => apiClient.get<GatewayListResponse>('/control/v1/gateways'),
    refetchInterval: 5000,
    refetchOnWindowFocus: true,
  });

  const gateways = data?.gateways || [];
  const hiddenStaleCount = data?.hidden_stale_count ?? 0;

  // Determine active control plane snapshot version (highest seen among healthy or any)
  const activeVersion = gateways.reduce((max, g) => (g.active_version > max ? g.active_version : max), 1);
  const convergedCount = gateways.filter((g) => g.status === 'healthy' && g.active_version === activeVersion).length;
  const totalCount = gateways.length;
  const convergencePct = totalCount > 0 ? Math.round((convergedCount / totalCount) * 100) : 0;

  const formatRelativeTime = (timestamp?: string) => {
    if (!timestamp) return 'never';
    const diffMs = now - new Date(timestamp).getTime();
    const diffSec = Math.max(0, Math.floor(diffMs / 1000));
    if (diffSec < 5) return 'just now';
    if (diffSec < 60) return `${diffSec}s ago`;
    const diffMin = Math.floor(diffSec / 60);
    if (diffMin < 60) return `${diffMin}m ago`;
    return new Date(timestamp).toLocaleTimeString();
  };

  const statusBadge = (status: GatewayStatus['status']) => {
    switch (status) {
      case 'healthy':
        return <span className="badge badge-ok"><span className="dot" />Healthy</span>;
      case 'degraded':
        return <span className="badge badge-warn"><span className="dot" />Degraded</span>;
      case 'partitioned':
      default:
        return <span className="badge badge-bad"><span className="dot" />Partitioned</span>;
    }
  };

  const syncSummary =
    totalCount === 0
      ? 'No gateways connected'
      : `${convergedCount} of ${totalCount} gateway${totalCount === 1 ? '' : 's'} on v${activeVersion}`;

  return (
    <div>
      <PageHeader
        title="Gateways"
        description="Gateway replicas streaming policy snapshots from the control plane. A replica that misses heartbeats for 60 seconds fails closed."
        meta={
          totalCount > 0 && (
            <span className={`badge ${convergencePct === 100 ? 'badge-ok' : 'badge-warn'} num`}>
              {syncSummary}
            </span>
          )
        }
        actions={
          <button onClick={() => refetch()} disabled={isFetching} className="btn btn-secondary">
            <RefreshCw className={isFetching ? 'animate-spin' : ''} />
            Refresh
          </button>
        }
      />

      {hiddenStaleCount > 0 && (
        <p className="mb-4 flex items-center gap-2 text-sm text-amber-300">
          <AlertTriangle className="w-4 h-4 shrink-0" />
          {hiddenStaleCount} stale replica{hiddenStaleCount === 1 ? ' is' : 's are'} hidden from this list
          and from the sync count.
        </p>
      )}

      {isError && (
        <div role="alert" className="mb-4 panel p-4 flex items-start gap-3">
          <AlertCircle className="w-5 h-5 shrink-0 mt-0.5 text-rose-500" />
          <div className="flex-1 min-w-0">
            <p className="text-sm font-medium text-slate-100">Gateway status is unavailable</p>
            <p className="text-sm text-slate-500 mt-0.5">
              {error instanceof Error ? error.message : 'The control plane did not answer.'}
            </p>
          </div>
          <button onClick={() => refetch()} className="btn btn-secondary btn-sm">Try again</button>
        </div>
      )}

      <section className="panel overflow-hidden">
        {isLoading ? (
          <div className="divide-y divide-mist" aria-busy="true" aria-label="Loading gateways">
            {[1, 2, 3].map((n) => (
              <div key={n} className="flex items-center gap-6 px-5 h-14 animate-pulse">
                <div className="h-3.5 w-40 bg-linen rounded" />
                <div className="h-3.5 w-20 bg-linen rounded" />
                <div className="h-3.5 w-12 bg-linen rounded" />
              </div>
            ))}
          </div>
        ) : gateways.length === 0 ? (
          <div className="px-5 py-16 text-center">
            <Server className="w-6 h-6 mx-auto text-slate-500" />
            <p className="mt-3 text-sm text-slate-300">No gateways connected</p>
            <p className="mt-1 text-sm text-slate-500 max-w-sm mx-auto">
              Gateways appear here once they open a snapshot stream to the control plane on port 9090.
            </p>
          </div>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="text-left text-xs text-slate-500 border-b border-mist">
                  <th scope="col" className="font-medium px-5 h-10">Gateway</th>
                  <th scope="col" className="font-medium px-5 h-10">Status</th>
                  <th scope="col" className="font-medium px-5 h-10">Snapshot</th>
                  <th scope="col" className="font-medium px-5 h-10 text-right">Last heartbeat</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-mist">
                {gateways.map((gw) => {
                  const behind = gw.active_version !== activeVersion;
                  return (
                    <tr key={gw.gateway_id} className="hover:bg-linen/60">
                      <td className="px-5 h-14 font-mono text-[13px] text-slate-100 max-w-[280px] truncate" title={gw.gateway_id}>
                        {gw.gateway_id}
                      </td>
                      <td className="px-5 h-14">{statusBadge(gw.status)}</td>
                      <td className="px-5 h-14">
                        <span className="num text-slate-100">v{gw.active_version}</span>
                        {behind ? (
                          <span className="ml-2 text-xs text-amber-300">behind v{activeVersion}</span>
                        ) : (
                          <span className="ml-2 inline-flex items-center gap-1 text-xs text-slate-500">
                            <CheckCircle2 className="w-3.5 h-3.5 text-emerald-500" />
                            current
                          </span>
                        )}
                      </td>
                      <td className="px-5 h-14 text-right text-slate-400 num">
                        <time dateTime={gw.last_heartbeat_at}>{formatRelativeTime(gw.last_heartbeat_at)}</time>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}
      </section>
    </div>
  );
};
