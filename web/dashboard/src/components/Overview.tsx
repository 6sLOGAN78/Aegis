import React, { useEffect, useMemo, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { ArrowRight, RefreshCw } from 'lucide-react';
import { apiClient } from '../api/client';
import type {
  AuditEventListResponse,
  GatewayListResponse,
  GatewayStatus,
  PolicyVersionListResponse,
  QuarantineRecord,
  RouteListResponse,
} from '../api/types';
import { DEFAULT_POLICY_ID } from './PolicyStudio';
import { ServiceBullet, routeFor } from './ServiceBullet';
import type { TabId } from './Shell';

const HOUR_MS = 60 * 60 * 1000;
const BUCKETS = 12; // 5-minute buckets over the last hour
const FAIL_CLOSED_S = 60; // a gateway without a fresh lease for 60 s fails closed
const LATE_S = 15;

type Service = 'good' | 'delayed' | 'suspended';

interface StationState {
  gw: GatewayStatus;
  service: Service;
  ageS: number | null;
  note: string;
}

const SERVICE_WORD: Record<Service, string> = {
  good: 'Good service',
  delayed: 'Delayed',
  suspended: 'Suspended',
};

function ageSeconds(ts: string | undefined, now: number): number | null {
  if (!ts) return null;
  return Math.max(0, Math.round((now - new Date(ts).getTime()) / 1000));
}

function formatAge(s: number | null): string {
  if (s === null) return 'no heartbeat';
  if (s < 60) return `${s} s`;
  return `${Math.floor(s / 60)} min`;
}

function clock(ts: string): string {
  return new Date(ts).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false });
}

/** The state word with a shape that differs per state, so it never depends on colour. */
const StatusWord: React.FC<{ service: Service; children?: React.ReactNode }> = ({ service, children }) => (
  <span className={`badge ${service === 'good' ? 'badge-ok' : service === 'delayed' ? 'badge-warn' : 'badge-bad'}`}>
    <span className="dot" aria-hidden="true" />
    {children ?? SERVICE_WORD[service]}
  </span>
);

interface OverviewProps {
  onNavigate: (tab: TabId) => void;
}

export const Overview: React.FC<OverviewProps> = ({ onNavigate }) => {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(t);
  }, []);

  const gatewaysQ = useQuery<GatewayListResponse>({
    queryKey: ['gateways'],
    queryFn: () => apiClient.get<GatewayListResponse>('/control/v1/gateways'),
    refetchInterval: 5000,
  });
  const versionsQ = useQuery<PolicyVersionListResponse>({
    queryKey: ['policy-versions', DEFAULT_POLICY_ID],
    queryFn: () => apiClient.get<PolicyVersionListResponse>(`/control/v1/policies/${DEFAULT_POLICY_ID}/versions`),
    refetchInterval: 15000,
  });
  const routesQ = useQuery<RouteListResponse>({
    queryKey: ['routes'],
    queryFn: () => apiClient.get<RouteListResponse>('/control/v1/routes'),
    refetchInterval: 60000,
  });
  const deniesQ = useQuery<AuditEventListResponse>({
    queryKey: ['overview-denials'],
    queryFn: () => {
      const p = new URLSearchParams({
        decision: 'deny',
        from_timestamp: new Date(Date.now() - HOUR_MS).toISOString(),
        limit: '200',
      });
      return apiClient.get<AuditEventListResponse>(`/control/v1/audit-events?${p.toString()}`);
    },
    refetchInterval: 15000,
  });
  const quarantineQ = useQuery<{ quarantines: QuarantineRecord[] }>({
    queryKey: ['quarantines'],
    queryFn: () => apiClient.get<{ quarantines: QuarantineRecord[] }>('/control/v1/principals/quarantine'),
    refetchInterval: 10000,
  });

  const gateways = gatewaysQ.data?.gateways ?? [];
  const hiddenStale = gatewaysQ.data?.hidden_stale_count ?? 0;
  const versions = versionsQ.data?.versions ?? [];
  const live = versions.reduce<(typeof versions)[number] | undefined>(
    (best, v) => (!best || v.snapshot_version > best.snapshot_version ? v : best),
    undefined,
  );
  const liveVersion = live?.snapshot_version ?? gateways.reduce((m, g) => Math.max(m, g.active_version), 0);

  const stations: StationState[] = gateways.map((gw) => {
    const ageS = ageSeconds(gw.last_heartbeat_at, now);
    let service: Service = 'good';
    let note = `v${gw.active_version}`;
    if (gw.status === 'partitioned' || ageS === null || ageS >= FAIL_CLOSED_S) {
      service = 'suspended';
      note = gw.status === 'partitioned' ? 'partitioned, failing closed' : 'lease expired, failing closed';
    } else if (gw.status === 'degraded' || gw.active_version < liveVersion || ageS >= LATE_S) {
      service = 'delayed';
      note = gw.active_version < liveVersion ? `on v${gw.active_version}, behind v${liveVersion}` : `v${gw.active_version}, heartbeat late`;
    }
    return { gw, service, ageS, note };
  });

  const goodCount = stations.filter((s) => s.service === 'good').length;
  const fleetService: Service =
    gateways.length === 0 || goodCount === 0 ? 'suspended' : goodCount < gateways.length || hiddenStale > 0 ? 'delayed' : 'good';

  const services = useMemo(() => {
    const m = new Map<string, Set<string>>();
    for (const r of routesQ.data?.routes ?? []) {
      if (!m.has(r.service_id)) m.set(r.service_id, new Set());
      m.get(r.service_id)!.add(r.http_method);
    }
    return [...m.entries()].map(([id, methods]) => ({ id, methods: [...methods].sort() })).sort((a, b) => a.id.localeCompare(b.id));
  }, [routesQ.data]);

  const denials = deniesQ.data?.events ?? [];
  const denialCountLabel = deniesQ.data?.next_cursor ? `${denials.length}+` : String(denials.length);
  const buckets = useMemo(() => {
    const b = new Array<number>(BUCKETS).fill(0);
    const end = deniesQ.dataUpdatedAt || Date.now();
    for (const e of denials) {
      const idx = BUCKETS - 1 - Math.floor((end - new Date(e.timestamp).getTime()) / (HOUR_MS / BUCKETS));
      if (idx >= 0 && idx < BUCKETS) b[idx] += 1;
    }
    return b;
  }, [denials, deniesQ.dataUpdatedAt]);
  const peak = Math.max(1, ...buckets);

  const activeQuarantines = (quarantineQ.data?.quarantines ?? []).filter((q) => q.status === 'active');

  const updatedAt = gatewaysQ.dataUpdatedAt;
  const freshS = updatedAt ? Math.max(0, Math.round((now - updatedAt) / 1000)) : null;
  const stale = gatewaysQ.isError || (freshS !== null && freshS > 20);
  const refreshing = gatewaysQ.isFetching || deniesQ.isFetching;
  const refreshAll = () => {
    gatewaysQ.refetch();
    versionsQ.refetch();
    deniesQ.refetch();
    quarantineQ.refetch();
    routesQ.refetch();
  };

  return (
    <div>
      <header className="flex flex-wrap items-end justify-between gap-4 mb-6">
        <div>
          <h1 className="text-[28px] font-display">Overview</h1>
          <p className={`mt-1 text-sm num ${stale ? 'text-rose-600 font-semibold' : 'text-slate-500'}`} role="status">
            {gatewaysQ.isError
              ? 'Control plane not answering. Figures below may be out of date.'
              : freshS === null
                ? 'Loading live state…'
                : `Updated ${freshS} s ago. Refreshes every 5 s.`}
          </p>
        </div>
        <button className="btn btn-secondary" onClick={refreshAll} disabled={refreshing}>
          <RefreshCw className={refreshing ? 'animate-spin' : ''} />
          Refresh
        </button>
      </header>

      {/* Status strip: one bordered board, four cells, the way a station status board reads. */}
      <section aria-label="Current state" className="panel grid grid-cols-1 sm:grid-cols-2 xl:grid-cols-4 divide-y sm:divide-y-0 divide-mist">
        <button
          onClick={() => onNavigate('topology')}
          className="flex flex-col items-start text-left p-5 hover:bg-linen transition-colors sm:border-r sm:border-b xl:border-b-0 border-mist"
        >
          <p className="text-xs font-semibold text-slate-400">Gateways</p>
          <p className="mt-1 text-[28px] font-bold num leading-tight">
            {gatewaysQ.isLoading ? '–' : `${goodCount} of ${gateways.length}`}
          </p>
          <div className="mt-2">
            {gatewaysQ.isLoading ? <span className="text-sm text-slate-500">Checking</span> : (
              <StatusWord service={fleetService}>
                {fleetService === 'good' ? 'Good service' : fleetService === 'delayed' ? 'Delays on some gateways' : 'No gateway in service'}
              </StatusWord>
            )}
          </div>
        </button>

        <button
          onClick={() => onNavigate('studio')}
          className="flex flex-col items-start text-left p-5 hover:bg-linen transition-colors sm:border-b xl:border-b-0 xl:border-r border-mist"
        >
          <p className="text-xs font-semibold text-slate-400">Live policy</p>
          <p className="mt-1 text-[28px] font-bold num leading-tight">{liveVersion ? `v${liveVersion}` : '–'}</p>
          <p className="mt-2 text-[13px] text-slate-400 truncate">
            {live ? `Published ${clock(live.published_at)} by ${live.published_by}` : 'No published version'}
          </p>
        </button>

        <button
          onClick={() => onNavigate('audit')}
          className="flex flex-col items-start text-left p-5 hover:bg-linen transition-colors sm:border-r border-mist"
        >
          <p className="text-xs font-semibold text-slate-400">Denied requests, last hour</p>
          <div className="mt-1 flex items-end justify-between gap-4">
            <p className="text-[28px] font-bold num leading-tight">{deniesQ.isLoading ? '–' : denialCountLabel}</p>
            <div className="flex items-end gap-[3px] h-9" aria-hidden="true">
              {buckets.map((n, i) => (
                <span
                  key={i}
                  className="w-[7px] rounded-[1px]"
                  style={{
                    height: n === 0 ? 2 : Math.max(4, Math.round((n / peak) * 36)),
                    background: n === 0 ? 'var(--color-mist)' : 'var(--color-ink)',
                  }}
                />
              ))}
            </div>
          </div>
          <p className="mt-2 text-[13px] text-slate-400">
            {buckets[BUCKETS - 1]} in the last 5 min · bars are 5-minute counts
          </p>
        </button>

        <button
          onClick={() => onNavigate('quarantine')}
          className="flex flex-col items-start text-left p-5 hover:bg-linen transition-colors"
        >
          <p className="text-xs font-semibold text-slate-400">Quarantined principals</p>
          <p className="mt-1 text-[28px] font-bold num leading-tight">{quarantineQ.isLoading ? '–' : activeQuarantines.length}</p>
          <div className="mt-2">
            {activeQuarantines.length > 0 ? (
              <span className="badge badge-bad"><span className="dot" aria-hidden="true" />Blocked on every gateway</span>
            ) : (
              <span className="text-[13px] text-slate-400">None active</span>
            )}
          </div>
        </button>
      </section>

      <LineDiagram stations={stations} services={services} hiddenStale={hiddenStale} loading={gatewaysQ.isLoading} />

      <section className="panel mt-6" aria-labelledby="recent-denials">
        <div className="panel-head">
          <h2 id="recent-denials" className="panel-title">Latest denials</h2>
          <button className="btn btn-ghost btn-sm" onClick={() => onNavigate('audit')}>
            Open audit log
            <ArrowRight />
          </button>
        </div>
        {deniesQ.isLoading ? (
          <div className="p-5 space-y-3" aria-busy="true">
            {[0, 1, 2].map((i) => <div key={i} className="h-4 bg-linen rounded-sm animate-pulse" />)}
          </div>
        ) : denials.length === 0 ? (
          <p className="px-5 py-8 text-sm text-slate-400">
            No requests were denied in the last hour. Every request that reached a gateway matched an allow rule.
          </p>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="text-left">
                  <th scope="col" className="px-5 h-10">Time (local)</th>
                  <th scope="col" className="px-5 h-10">Principal</th>
                  <th scope="col" className="px-5 h-10">Service</th>
                  <th scope="col" className="px-5 h-10">Request</th>
                  <th scope="col" className="px-5 h-10">Reason</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-slate-800">
                {denials.slice(0, 8).map((e) => (
                  <tr key={e.event_id}>
                    <td className="px-5 h-11 font-mono text-[13px] whitespace-nowrap">{clock(e.timestamp)}</td>
                    <td className="px-5 h-11 font-mono text-[13px] max-w-[220px] truncate" title={e.principal_id}>{e.principal_id || 'anonymous'}</td>
                    <td className="px-5 h-11"><ServiceBullet service={e.service_id || 'unknown'} /></td>
                    <td className="px-5 h-11 font-mono text-[13px] whitespace-nowrap">
                      <span className="text-slate-500">{e.http_method}</span> {e.request_path}
                    </td>
                    <td className="px-5 h-11 font-mono text-[12px] text-rose-700 whitespace-nowrap">{e.reason_code}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>
    </div>
  );
};

/* ---------------------------------------------------------------------------------------------
 * The request path drawn as a line diagram: clients → load balancer → gateway stations → services.
 * A station that is late or behind turns into an open ring and its segment is dashed.
 * ------------------------------------------------------------------------------------------- */

interface LineDiagramProps {
  stations: StationState[];
  services: { id: string; methods: string[] }[];
  hiddenStale: number;
  loading: boolean;
}

const ROW = 100;
const TOP = 64;
const X = { clients: 60, lb: 250, split: 400, gw: 500, merge: 840, svc: 930 };
const STROKE = 8;

const LineDiagram: React.FC<LineDiagramProps> = ({ stations, services, hiddenStale, loading }) => {
  const rows = Math.max(stations.length, services.length, 1);
  const height = TOP * 2 + (rows - 1) * ROW;
  const mid = TOP + ((rows - 1) * ROW) / 2;
  const gwY = (i: number) => TOP + ((rows - stations.length) * ROW) / 2 + i * ROW;
  const svcY = (i: number) => TOP + ((rows - services.length) * ROW) / 2 + i * ROW;
  const color = (s: Service) => (s === 'good' ? 'var(--color-ink)' : s === 'delayed' ? 'var(--color-warn)' : 'var(--color-bad)');
  const anyLive = stations.some((s) => s.service !== 'suspended');

  return (
    <section className="panel mt-6" aria-labelledby="request-path">
      <div className="panel-head">
        <h2 id="request-path" className="panel-title">Request path</h2>
        <p className="text-xs text-slate-500 hidden sm:block">
          Heartbeat bars run to 60 s, where a gateway's lease expires and it fails closed.
        </p>
      </div>
      <VerticalLine stations={stations} services={services} />
      <div className="bg-linen overflow-x-auto hidden md:block">
        {loading ? (
          <div className="h-56 animate-pulse" aria-busy="true" />
        ) : (
          <svg
            viewBox={`0 0 1120 ${height}`}
            className="block min-w-[880px] w-full"
            role="img"
            aria-label={`Requests flow from clients through the load balancer to ${stations.length} gateways and on to ${services.length} services.`}
          >
            {/* trunk: clients → load balancer → split */}
            <line x1={X.clients} y1={mid} x2={X.split} y2={mid} stroke="var(--color-ink)" strokeWidth={STROKE} strokeLinecap="round" />

            {stations.map((s, i) => {
              const y = gwY(i);
              const dash = s.service === 'good' ? undefined : '14 10';
              return (
                <g key={s.gw.gateway_id}>
                  <path
                    d={`M${X.split} ${mid} L${X.split} ${y} L${X.merge} ${y}`}
                    fill="none"
                    stroke={color(s.service)}
                    strokeWidth={STROKE}
                    strokeLinejoin="round"
                    strokeDasharray={dash}
                  />
                </g>
              );
            })}

            {/* merge: gateways → services */}
            {services.map((svc, i) => (
              <path
                key={svc.id}
                d={`M${X.merge} ${mid} L${X.merge} ${svcY(i)} L${X.svc} ${svcY(i)}`}
                fill="none"
                stroke={anyLive ? 'var(--color-ink)' : 'var(--color-mist)'}
                strokeWidth={STROKE}
                strokeLinejoin="round"
              />
            ))}
            {stations.length > 0 && (
              <line x1={X.merge} y1={Math.min(...stations.map((_, i) => gwY(i)))} x2={X.merge} y2={Math.max(...stations.map((_, i) => gwY(i)))} stroke="var(--color-ink)" strokeWidth={STROKE} />
            )}

            <Station x={X.clients} y={mid} />
            <text x={X.clients} y={mid + 38} textAnchor="middle" className="fill-[var(--color-ink)] text-[15px] font-bold">Clients</text>
            <text x={X.clients} y={mid + 56} textAnchor="middle" className="fill-[var(--color-ash)] text-[12px]">JWT · mTLS</text>

            <Station x={X.lb} y={mid} />
            <text x={X.lb} y={mid + 38} textAnchor="middle" className="fill-[var(--color-ink)] text-[15px] font-bold">Load balancer</text>
            <text x={X.lb} y={mid + 56} textAnchor="middle" className="fill-[var(--color-ash)] text-[12px]">:8080 · :9443</text>

            {stations.map((s, i) => {
              const y = gwY(i);
              const age = s.ageS ?? FAIL_CLOSED_S;
              const barW = Math.min(1, age / FAIL_CLOSED_S) * 120;
              return (
                <g key={`st-${s.gw.gateway_id}`}>
                  <Station x={X.gw} y={y} service={s.service} />
                  <text x={X.gw + 24} y={y - 32} className="fill-[var(--color-ink)] text-[15px] font-bold">{s.gw.gateway_id}</text>
                  <text x={X.gw + 24} y={y - 15} className="text-[12px] font-semibold" style={{ fill: s.service === 'good' ? 'var(--color-charcoal)' : color(s.service) }}>
                    {SERVICE_WORD[s.service]} · {s.note}
                  </text>
                  {/* heartbeat age: exact length, 120 px = 60 s */}
                  <rect x={X.gw + 24} y={y + 14} width={120} height={4} rx={1} fill="var(--color-mist)" />
                  <rect x={X.gw + 24} y={y + 14} width={Math.max(2, barW)} height={4} rx={1} style={{ fill: color(s.service) }} />
                  <text x={X.gw + 152} y={y + 19} className="fill-[var(--color-ash)] text-[11px] num">{formatAge(s.ageS)}</text>
                </g>
              );
            })}

            {stations.length === 0 && (
              <text x={X.gw} y={mid + 5} className="fill-[var(--color-bad)] text-[14px] font-semibold">
                No gateway connected. Every request is refused.
              </text>
            )}

            {services.map((svc, i) => {
              const y = svcY(i);
              const r = routeFor(svc.id);
              return (
                <g key={`svc-${svc.id}`}>
                  <circle cx={X.svc + 14} cy={y} r={17} fill={r.color} />
                  <text x={X.svc + 14} y={y + 5} textAnchor="middle" className="fill-white text-[15px] font-bold">{r.letter}</text>
                  <text x={X.svc + 42} y={y - 2} className="fill-[var(--color-ink)] text-[15px] font-bold">{svc.id}</text>
                  <text x={X.svc + 42} y={y + 15} className="fill-[var(--color-ash)] text-[12px]">{svc.methods.join(' · ')}</text>
                </g>
              );
            })}
          </svg>
        )}
      </div>
      {hiddenStale > 0 && (
        <p className="px-5 py-3 border-t border-mist text-sm text-amber-300 font-semibold">
          {hiddenStale} more gateway{hiddenStale === 1 ? ' has' : 's have'} stopped sending heartbeats and {hiddenStale === 1 ? 'is' : 'are'} not drawn.
        </p>
      )}
    </section>
  );
};

const Station: React.FC<{ x: number; y: number; service?: Service }> = ({ x, y, service = 'good' }) => {
  if (service === 'suspended') {
    return <rect x={x - 11} y={y - 11} width={22} height={22} fill="var(--color-bad)" />;
  }
  if (service === 'delayed') {
    return <circle cx={x} cy={y} r={11} fill="var(--color-linen)" stroke="var(--color-warn)" strokeWidth={6} strokeDasharray="5 3" />;
  }
  return <circle cx={x} cy={y} r={11} fill="#fff" stroke="var(--color-ink)" strokeWidth={7} />;
};

/** Phone layout of the same diagram: one vertical line, stops listed top to bottom. */
const VerticalLine: React.FC<{ stations: StationState[]; services: { id: string; methods: string[] }[] }> = ({ stations, services }) => {
  const stops: { key: string; title: string; detail: string; service?: Service; bullet?: string }[] = [
    { key: 'clients', title: 'Clients', detail: 'JWT · mTLS' },
    { key: 'lb', title: 'Load balancer', detail: ':8080 · :9443' },
    ...stations.map((s) => ({
      key: s.gw.gateway_id,
      title: s.gw.gateway_id,
      detail: `${SERVICE_WORD[s.service]} · ${s.note} · heartbeat ${formatAge(s.ageS)}`,
      service: s.service,
    })),
    ...services.map((svc) => ({ key: `svc-${svc.id}`, title: svc.id, detail: svc.methods.join(' · '), bullet: svc.id })),
  ];
  return (
    <ol className="md:hidden bg-linen px-5 py-4">
      {stops.map((stop, i) => (
        <li key={stop.key} className="relative flex gap-4 pb-5 last:pb-0">
          {i < stops.length - 1 && <span aria-hidden="true" className="absolute left-[10px] top-5 bottom-0 w-[6px] bg-ink" />}
          <span className="relative z-10 shrink-0 mt-0.5">
            {stop.bullet ? (
              <span className="flex w-[26px] justify-center"><ServiceBullet service={stop.bullet} withName={false} /></span>
            ) : (
              <svg width="26" height="26" viewBox="-13 -13 26 26" aria-hidden="true">
                <Station x={0} y={0} service={stop.service} />
              </svg>
            )}
          </span>
          <span className="min-w-0">
            <span className="block font-bold">{stop.title}</span>
            <span
              className={`block text-[13px] ${stop.service === 'delayed' ? 'text-amber-300 font-semibold' : stop.service === 'suspended' ? 'text-rose-600 font-semibold' : 'text-slate-500'}`}
            >
              {stop.detail}
            </span>
          </span>
        </li>
      ))}
    </ol>
  );
};
