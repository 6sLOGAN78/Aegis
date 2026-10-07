import React, { useState, useEffect } from 'react';
import { useQuery } from '@tanstack/react-query';
import {
  Radio,
  RefreshCw,
  Server,
  AlertTriangle,
  CheckCircle2,
  AlertCircle,
  Clock,
  Layers,
} from 'lucide-react';
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

  // Determine active control plane snapshot version (highest seen among healthy or any)
  const activeVersion = gateways.reduce((max, g) => (g.active_version > max ? g.active_version : max), 1);
  const convergedCount = gateways.filter((g) => g.status === 'healthy' && g.active_version === activeVersion).length;
  const totalCount = gateways.length;
  const convergencePct = totalCount > 0 ? Math.round((convergedCount / totalCount) * 100) : 0;

  const formatRelativeTime = (timestamp?: string) => {
    if (!timestamp) return 'Never';
    const diffMs = now - new Date(timestamp).getTime();
    const diffSec = Math.max(0, Math.floor(diffMs / 1000));
    if (diffSec < 5) return 'Just now';
    if (diffSec < 60) return `${diffSec}s ago`;
    const diffMin = Math.floor(diffSec / 60);
    if (diffMin < 60) return `${diffMin}m ago`;
    return new Date(timestamp).toLocaleTimeString();
  };

  const calculateLeaseRemaining = (leaseExpiresAt?: string, lastHb?: string) => {
    if (leaseExpiresAt) {
      const expMs = new Date(leaseExpiresAt).getTime();
      const remainingSec = (expMs - now) / 1000;
      return Math.max(0, Math.min(10, remainingSec));
    }
    if (lastHb) {
      const hbMs = new Date(lastHb).getTime();
      const ageSec = (now - hbMs) / 1000;
      return Math.max(0, Math.min(10, 10 - ageSec));
    }
    return 0;
  };

  const getStatusBadge = (status: GatewayStatus['status']) => {
    switch (status) {
      case 'healthy':
        return (
          <span className="inline-flex items-center space-x-1 px-2 py-0.5 rounded-full text-xs font-semibold bg-emerald-500/10 text-emerald-400 border border-emerald-500/30">
            <span className="w-1.5 h-1.5 rounded-full bg-emerald-400 animate-pulse" />
            <span>HEALTHY</span>
          </span>
        );
      case 'degraded':
        return (
          <span className="inline-flex items-center space-x-1 px-2 py-0.5 rounded-full text-xs font-semibold bg-amber-500/10 text-amber-400 border border-amber-500/30">
            <span className="w-1.5 h-1.5 rounded-full bg-amber-400" />
            <span>DEGRADED</span>
          </span>
        );
      case 'partitioned':
      default:
        return (
          <span className="inline-flex items-center space-x-1 px-2 py-0.5 rounded-full text-xs font-semibold bg-rose-500/10 text-rose-400 border border-rose-500/30">
            <span className="w-1.5 h-1.5 rounded-full bg-rose-400" />
            <span>PARTITIONED</span>
          </span>
        );
    }
  };

  return (
    <div className="space-y-6">
      {/* Fleet Convergence Summary Hero Bar */}
      <div className="bg-slate-900 border border-slate-800 rounded-xl p-5 shadow-lg">
        <div className="flex flex-col md:flex-row md:items-center justify-between gap-4">
          <div className="flex items-center space-x-4">
            <div className="p-2.5 bg-cyan-500/10 border border-cyan-500/20 rounded-lg text-cyan-400">
              <Layers className="w-6 h-6" />
            </div>
            <div>
              <div className="flex items-center space-x-3">
                <h2 className="text-xl font-semibold text-slate-100">Fleet Replica Topology</h2>
                <span className="px-2.5 py-0.5 rounded-full text-xs font-mono font-semibold bg-cyan-500/15 text-cyan-400 border border-cyan-500/30">
                  v{activeVersion} [ACTIVE]
                </span>
              </div>
              <p className="text-xs text-slate-400 mt-0.5">
                Real-time gRPC snapshot stream synchronization and 10s freshness lease tracking
              </p>
            </div>
          </div>

          <div className="flex items-center space-x-4">
            <div className="flex flex-col items-end">
              <span className="text-xs font-medium text-slate-400">Convergence Rate</span>
              <span className="text-sm font-semibold font-mono text-slate-200">
                {convergedCount} / {totalCount} Replicas ({convergencePct}%)
              </span>
            </div>

            <button
              onClick={() => refetch()}
              disabled={isFetching}
              className="inline-flex items-center space-x-2 px-3 py-2 bg-slate-800 hover:bg-slate-700 text-slate-200 hover:text-white rounded-lg text-xs font-medium border border-slate-700 transition disabled:opacity-50"
            >
              <RefreshCw className={`w-3.5 h-3.5 ${isFetching ? 'animate-spin text-cyan-400' : ''}`} />
              <span>Refresh Fleet Status</span>
            </button>
          </div>
        </div>

        {/* Global Convergence Progress Bar */}
        <div className="mt-4 pt-4 border-t border-slate-800/80">
          <div className="flex items-center justify-between text-xs text-slate-400 mb-1.5">
            <span>Snapshot Convergence Status</span>
            <span className="font-mono">
              {totalCount === 0
                ? '0 Connected'
                : convergencePct === 100
                ? 'Fully Synchronized'
                : 'Reconciliation In Progress'}
            </span>
          </div>
          <div className="w-full h-2 bg-slate-950 rounded-full overflow-hidden border border-slate-800">
            <div
              className={`h-full transition-all duration-500 ${
                convergencePct === 100
                  ? 'bg-emerald-500'
                  : convergencePct >= 50
                  ? 'bg-amber-500'
                  : 'bg-rose-500'
              }`}
              style={{ width: `${totalCount === 0 ? 0 : convergencePct}%` }}
            />
          </div>
        </div>
      </div>

      {/* Error Callout */}
      {isError && (
        <div className="bg-rose-500/10 border border-rose-500/30 rounded-xl p-4 flex items-start space-x-3 text-rose-300">
          <AlertCircle className="w-5 h-5 flex-shrink-0 mt-0.5 text-rose-400" />
          <div className="flex-1">
            <h4 className="text-sm font-semibold text-rose-200">
              Control plane gRPC distribution telemetry unavailable
            </h4>
            <p className="text-xs text-rose-300/80 mt-1">
              {error instanceof Error ? error.message : 'Unable to query connected gateway replicas'}
            </p>
          </div>
          <button
            onClick={() => refetch()}
            className="px-3 py-1 bg-rose-500/20 hover:bg-rose-500/30 text-rose-200 rounded text-xs font-medium border border-rose-500/40"
          >
            Retry Connection
          </button>
        </div>
      )}

      {/* Loading Skeleton */}
      {isLoading && (
        <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
          {[1, 2, 3].map((n) => (
            <div
              key={n}
              className="bg-slate-900/60 border border-slate-800 rounded-xl p-5 animate-pulse space-y-4"
            >
              <div className="flex justify-between items-center">
                <div className="h-5 w-32 bg-slate-800 rounded" />
                <div className="h-5 w-16 bg-slate-800 rounded-full" />
              </div>
              <div className="space-y-2">
                <div className="h-4 w-24 bg-slate-800 rounded" />
                <div className="h-2 w-full bg-slate-800 rounded-full" />
              </div>
              <div className="h-4 w-40 bg-slate-800 rounded" />
            </div>
          ))}
        </div>
      )}

      {/* Empty State */}
      {!isLoading && !isError && gateways.length === 0 && (
        <div className="bg-slate-900 border border-slate-800 rounded-xl p-12 text-center max-w-xl mx-auto shadow-sm">
          <div className="w-12 h-12 bg-slate-800 border border-slate-700 text-cyan-400 rounded-full flex items-center justify-center mx-auto mb-4">
            <Radio className="w-6 h-6" />
          </div>
          <h3 className="text-lg font-semibold text-slate-100">
            No Active Gateway Replicas Connected
          </h3>
          <p className="text-sm text-slate-400 mt-2 mb-6 leading-relaxed">
            The control plane has not detected any bidirectional gRPC snapshot streams from gateway replicas. Verify gateway container health and network connectivity.
          </p>
          <button
            onClick={() => refetch()}
            className="inline-flex items-center space-x-2 px-4 py-2 bg-cyan-600 hover:bg-cyan-500 text-white rounded-lg text-sm font-medium transition shadow-sm"
          >
            <RefreshCw className="w-4 h-4" />
            <span>Refresh Fleet Status</span>
          </button>
        </div>
      )}

      {/* Gateway Replica Cards Grid */}
      {!isLoading && gateways.length > 0 && (
        <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
          {gateways.map((gw) => {
            const isMismatched = gw.active_version !== activeVersion;
            const leaseRemaining = calculateLeaseRemaining(gw.lease_expires_at, gw.last_heartbeat_at);
            const leasePct = Math.round((leaseRemaining / 10) * 100);
            const isPartitioned = gw.status === 'partitioned';

            return (
              <div
                key={gw.gateway_id}
                className={`bg-slate-900 border rounded-xl p-5 shadow-sm transition hover:border-slate-700 flex flex-col justify-between ${
                  isPartitioned
                    ? 'border-rose-500/40'
                    : isMismatched
                    ? 'border-amber-500/40'
                    : 'border-slate-800'
                }`}
              >
                <div>
                  {/* Card Header: ID and Status */}
                  <div className="flex items-center justify-between mb-4">
                    <div className="flex items-center space-x-2.5 truncate">
                      <div className="p-1.5 bg-slate-800 border border-slate-700 rounded-md text-slate-300 flex-shrink-0">
                        <Server className="w-4 h-4" />
                      </div>
                      <span className="font-mono text-sm font-semibold text-slate-100 truncate" title={gw.gateway_id}>
                        {gw.gateway_id}
                      </span>
                    </div>
                    {getStatusBadge(gw.status)}
                  </div>

                  {/* Active Snapshot Version Badge */}
                  <div className="bg-slate-950 border border-slate-800/80 rounded-lg p-3 mb-4 flex items-center justify-between">
                    <div>
                      <div className="text-xs text-slate-400 font-medium">Active Snapshot</div>
                      <div className="font-mono text-base font-semibold text-slate-100">
                        v{gw.active_version}
                      </div>
                    </div>
                    {isMismatched ? (
                      <div
                        className="flex items-center space-x-1.5 text-xs text-amber-400 bg-amber-500/10 border border-amber-500/20 px-2 py-1 rounded"
                        title={`Lags behind active control plane snapshot v${activeVersion}`}
                      >
                        <AlertTriangle className="w-3.5 h-3.5" />
                        <span>Lagging (v{activeVersion})</span>
                      </div>
                    ) : (
                      <div className="flex items-center space-x-1 text-xs text-emerald-400">
                        <CheckCircle2 className="w-4 h-4" />
                        <span>Synchronized</span>
                      </div>
                    )}
                  </div>

                  {/* Freshness Lease Countdown Bar */}
                  <div className="space-y-1.5 mb-4">
                    <div className="flex items-center justify-between text-xs">
                      <span className="text-slate-400 flex items-center space-x-1">
                        <Clock className="w-3 h-3 text-slate-500" />
                        <span>10s Freshness Lease</span>
                      </span>
                      <span className={`font-mono font-medium ${leaseRemaining < 3 ? 'text-rose-400' : 'text-slate-300'}`}>
                        {leaseRemaining.toFixed(1)}s remaining
                      </span>
                    </div>
                    <div className="w-full h-1.5 bg-slate-950 rounded-full overflow-hidden border border-slate-800">
                      <div
                        className={`h-full transition-all duration-300 ${
                          leaseRemaining <= 2
                            ? 'bg-rose-500 animate-pulse'
                            : leaseRemaining <= 5
                            ? 'bg-amber-400'
                            : 'bg-emerald-400'
                        }`}
                        style={{ width: `${leasePct}%` }}
                      />
                    </div>
                    {isPartitioned && (
                      <p className="text-[11px] text-rose-400 flex items-center space-x-1 mt-1">
                        <AlertTriangle className="w-3 h-3 flex-shrink-0" />
                        <span>Fail-closed boundary reached (&gt;60s lease expiry)</span>
                      </p>
                    )}
                  </div>
                </div>

                {/* Footer Timestamps */}
                <div className="pt-3 border-t border-slate-800/80 flex items-center justify-between text-xs text-slate-400">
                  <span>Last Heartbeat:</span>
                  <span className="font-mono text-slate-300">
                    {formatRelativeTime(gw.last_heartbeat_at)}
                  </span>
                </div>
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
};
