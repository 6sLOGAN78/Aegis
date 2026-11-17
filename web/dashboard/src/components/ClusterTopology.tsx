import React, { useState, useEffect } from 'react';
import { useQuery } from '@tanstack/react-query';
import { RefreshCw, AlertTriangle, AlertCircle } from 'lucide-react';
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

  // Same words as the Overview line diagram: good service, delayed, suspended.
  const statusBadge = (status: GatewayStatus['status']) => {
    switch (status) {
      case 'healthy':
        return <span className="badge badge-ok"><span className="dot" aria-hidden="true" />Good service</span>;
      case 'degraded':
        return <span className="badge badge-warn"><span className="dot" aria-hidden="true" />Delayed</span>;
      case 'partitioned':
      default:
        return <span className="badge badge-bad"><span className="dot" aria-hidden="true" />Suspended</span>;
    }
  };

  // Heartbeat age as an exact-length bar: full width is the 60 s lease, where a gateway fails closed.
  const heartbeatBar = (timestamp?: string) => {
    const ageS = timestamp ? Math.max(0, (now - new Date(timestamp).getTime()) / 1000) : 60;
    const pct = Math.min(100, (ageS / 60) * 100);
    const tone = ageS >= 60 ? 'var(--color-bad)' : ageS >= 15 ? 'var(--color-warn)' : 'var(--color-ink)';
    return (
      <span className="hidden md:inline-block w-24 h-1 bg-mist rounded-[1px] align-middle mr-3" aria-hidden="true">
        <span className="block h-1 rounded-[1px]" style={{ width: `${Math.max(2, pct)}%`, background: tone }} />
      </span>
    );
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
            <span className={`badge ${convergencePct === 100 ? 'badge-ok' : 'badge-warn'} num`}><span className="dot" aria-hidden="true" />
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
              <div key={n} className="flex items-center gap-6 px-5 h-12 animate-pulse">
                <div className="h-3.5 w-40 bg-linen rounded" />
                <div className="h-3.5 w-20 bg-linen rounded" />
                <div className="h-3.5 w-12 bg-linen rounded" />
              </div>
            ))}
          </div>
        ) : gateways.length === 0 ? (
          <div className="px-5 py-16 text-center">
            <p className="text-sm font-semibold text-rose-600">No gateways connected. Every request is refused.</p>
            <p className="mt-1 text-sm text-slate-500 max-w-sm mx-auto">
              Gateways appear here once they open a snapshot stream to the control plane on port 9090.
            </p>
          </div>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="text-left">
                  <th scope="col" className="px-5 h-10">Gateway</th>
                  <th scope="col" className="px-5 h-10">Status</th>
                  <th scope="col" className="px-5 h-10">Snapshot</th>
                  <th scope="col" className="px-5 h-10 text-right">Last heartbeat</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-mist">
                {gateways.map((gw) => {
                  const behind = gw.active_version !== activeVersion;
                  return (
                    <tr key={gw.gateway_id} className="hover:bg-linen">
                      <td className="px-5 h-12 font-mono text-[13px] text-slate-100 max-w-[280px] truncate" title={gw.gateway_id}>
                        {gw.gateway_id}
                      </td>
                      <td className="px-5 h-12">{statusBadge(gw.status)}</td>
                      <td className="px-5 h-12">
                        <span className="num text-slate-100">v{gw.active_version}</span>
                        {behind ? (
                          <span className="ml-2 text-xs font-semibold text-amber-300">behind v{activeVersion}</span>
                        ) : (
                          <span className="ml-2 text-xs text-slate-500">current</span>
                        )}
                      </td>
                      <td className="px-5 h-12 text-right text-slate-400 num">
                        {heartbeatBar(gw.last_heartbeat_at)}
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
