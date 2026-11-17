import React, { useEffect, useRef, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Filter, Copy, Check, X, ChevronLeft, ChevronRight, Loader2 } from 'lucide-react';
import { PageHeader } from './PageHeader';
import { ServiceBullet } from './ServiceBullet';
import { apiClient } from '../api/client';
import type { AuditEvent, AuditEventListResponse } from '../api/types';

// A request writes a decision row before forwarding and a completion row after the backend replies.
const RECORD_LABEL: Record<string, string> = { decision: 'Decision', completion: 'Completion', denial: 'Denial' };

export const AuditStream: React.FC = () => {
  // Query Filters State
  const [decision, setDecision] = useState<string>('all');
  const [serviceId, setServiceId] = useState<string>('');
  const [principalId, setPrincipalId] = useState<string>('');
  const [timeRange, setTimeRange] = useState<'1h' | '24h' | '7d'>('24h');

  // Active query parameters applied on "Filter Audit Events" click
  const [appliedFilters, setAppliedFilters] = useState({
    decision: 'all',
    serviceId: '',
    principalId: '',
    timeRange: '24h' as '1h' | '24h' | '7d',
  });

  // Cursor Pagination State
  const [cursorStack, setCursorStack] = useState<string[]>([]);
  const [currentCursor, setCurrentCursor] = useState<string | undefined>(undefined);

  // Detail Drawer State
  const [selectedEvent, setSelectedEvent] = useState<AuditEvent | null>(null);
  const [copied, setCopied] = useState(false);
  const closeRef = useRef<HTMLButtonElement>(null);

  // The detail drawer takes focus when it opens and closes on Escape.
  useEffect(() => {
    if (!selectedEvent) return;
    closeRef.current?.focus();
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setSelectedEvent(null);
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [selectedEvent]);

  // Compute from_timestamp based on applied time range
  const getFromTimestamp = (range: '1h' | '24h' | '7d'): string => {
    const now = Date.now();
    switch (range) {
      case '1h':
        return new Date(now - 3600 * 1000).toISOString();
      case '7d':
        return new Date(now - 7 * 86400 * 1000).toISOString();
      case '24h':
      default:
        return new Date(now - 86400 * 1000).toISOString();
    }
  };

  const { data, isLoading, isFetching } = useQuery<AuditEventListResponse>({
    queryKey: ['audit-events', appliedFilters, currentCursor],
    queryFn: async () => {
      const params = new URLSearchParams();
      if (appliedFilters.decision && appliedFilters.decision !== 'all') {
        params.set('decision', appliedFilters.decision);
      }
      if (appliedFilters.serviceId) {
        params.set('service_id', appliedFilters.serviceId);
      }
      if (appliedFilters.principalId) {
        params.set('principal_id', appliedFilters.principalId);
      }
      params.set('from_timestamp', getFromTimestamp(appliedFilters.timeRange));
      if (currentCursor) {
        params.set('cursor', currentCursor);
      }
      params.set('limit', '50');

      return apiClient.get<AuditEventListResponse>(`/control/v1/audit-events?${params.toString()}`);
    },
    refetchInterval: 15000,
  });

  const events = data?.events || [];
  const nextCursor = data?.next_cursor;

  const handleApplyFilters = () => {
    setAppliedFilters({
      decision,
      serviceId,
      principalId,
      timeRange,
    });
    setCurrentCursor(undefined);
    setCursorStack([]);
  };

  const handleResetFilters = () => {
    setDecision('all');
    setServiceId('');
    setPrincipalId('');
    setTimeRange('24h');
    setAppliedFilters({
      decision: 'all',
      serviceId: '',
      principalId: '',
      timeRange: '24h',
    });
    setCurrentCursor(undefined);
    setCursorStack([]);
  };

  const handleNextPage = () => {
    if (nextCursor) {
      if (currentCursor) {
        setCursorStack((prev) => [...prev, currentCursor]);
      } else {
        setCursorStack(['']);
      }
      setCurrentCursor(nextCursor);
    }
  };

  const handlePreviousPage = () => {
    if (cursorStack.length > 0) {
      const prevStack = [...cursorStack];
      const prevCursor = prevStack.pop();
      setCursorStack(prevStack);
      setCurrentCursor(prevCursor === '' ? undefined : prevCursor);
    }
  };

  const handleCopyJSON = () => {
    if (selectedEvent) {
      navigator.clipboard.writeText(JSON.stringify(selectedEvent, null, 2));
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    }
  };

  const fieldIds = { decision: 'audit-decision', service: 'audit-service', principal: 'audit-principal', range: 'audit-range' };

  return (
    <div>
      <PageHeader
        title="Audit log"
        description="Every policy decision and request completion the gateways recorded, newest first."
      />

      <form
        onSubmit={(e) => {
          e.preventDefault();
          handleApplyFilters();
        }}
        className="mb-4 grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-[repeat(4,minmax(0,1fr))_auto] gap-3 items-end"
      >
        <div>
          <label htmlFor={fieldIds.decision} className="label">Decision</label>
          <select id={fieldIds.decision} value={decision} onChange={(e) => setDecision(e.target.value)} className="field">
            <option value="all">All decisions</option>
            <option value="allow">Allowed</option>
            <option value="deny">Denied</option>
          </select>
        </div>
        <div>
          <label htmlFor={fieldIds.service} className="label">Service</label>
          <input
            id={fieldIds.service}
            type="text"
            value={serviceId}
            onChange={(e) => setServiceId(e.target.value)}
            placeholder="orders"
            className="field"
          />
        </div>
        <div>
          <label htmlFor={fieldIds.principal} className="label">Principal</label>
          <input
            id={fieldIds.principal}
            type="text"
            value={principalId}
            onChange={(e) => setPrincipalId(e.target.value)}
            placeholder="usr_alice"
            className="field"
          />
        </div>
        <div>
          <label htmlFor={fieldIds.range} className="label">Time range</label>
          <select
            id={fieldIds.range}
            value={timeRange}
            onChange={(e) => setTimeRange(e.target.value as '1h' | '24h' | '7d')}
            className="field"
          >
            <option value="1h">Last hour</option>
            <option value="24h">Last 24 hours</option>
            <option value="7d">Last 7 days</option>
          </select>
        </div>
        <div className="flex gap-2">
          <button type="submit" disabled={isFetching} className="btn btn-primary h-10">
            {isFetching ? <Loader2 className="animate-spin" /> : <Filter />}
            Apply filters
          </button>
          <button type="button" onClick={handleResetFilters} className="btn btn-ghost h-10">
            Clear
          </button>
        </div>
      </form>

      <section className="panel overflow-hidden">
        {isLoading ? (
          <div className="divide-y divide-mist" aria-busy="true" aria-label="Loading audit events">
            {[1, 2, 3, 4, 5].map((n) => (
              <div key={n} className="flex items-center gap-6 px-5 h-11 animate-pulse">
                <div className="h-3.5 w-16 bg-linen rounded" />
                <div className="h-3.5 w-36 bg-linen rounded" />
                <div className="h-3.5 w-48 bg-linen rounded" />
              </div>
            ))}
          </div>
        ) : events.length === 0 ? (
          <div className="px-5 py-16 text-center">
            <p className="text-sm text-slate-300">No events match these filters</p>
            <p className="mt-1 text-sm text-slate-500 max-w-sm mx-auto">
              Widen the time range or clear the filters. New events arrive every 15 seconds.
            </p>
            <button onClick={handleResetFilters} className="btn btn-secondary mt-4">Clear filters</button>
          </div>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full text-left text-[13px]">
              <thead>
                <tr>
                  <th scope="col" className="px-4 h-10">Decision</th>
                  <th scope="col" className="px-4 h-10">Record</th>
                  <th scope="col" className="px-4 h-10">Time (UTC)</th>
                  <th scope="col" className="px-4 h-10">Request</th>
                  <th scope="col" className="px-4 h-10">Principal</th>
                  <th scope="col" className="px-4 h-10">Service</th>
                  <th scope="col" className="px-4 h-10">Reason</th>
                  <th scope="col" className="px-4 h-10 text-right">Status</th>
                  <th scope="col" className="px-4 h-10 text-right">Latency</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-mist">
                {events.map((evt) => (
                  <tr
                    key={evt.event_id}
                    onClick={() => setSelectedEvent(evt)}
                    onKeyDown={(e) => {
                      if (e.key === 'Enter' || e.key === ' ') {
                        e.preventDefault();
                        setSelectedEvent(evt);
                      }
                    }}
                    tabIndex={0}
                    aria-label={`Open event ${evt.event_id}`}
                    className="hover:bg-linen focus-visible:bg-linen cursor-pointer"
                  >
                    <td className="px-4 h-11 whitespace-nowrap">
                      {evt.decision === 'allow' ? (
                        <span className="inline-flex items-center gap-2 text-slate-300"><span className="dot bg-emerald-500" aria-hidden="true" />Allowed</span>
                      ) : (
                        <span className="inline-flex items-center gap-2 text-rose-600 font-semibold"><span className="dot rounded-[1px]" aria-hidden="true" />Denied</span>
                      )}
                    </td>
                    <td className="px-4 h-11 whitespace-nowrap text-slate-400">{RECORD_LABEL[evt.event_type] ?? evt.event_type}</td>
                    <td className="px-4 h-11 whitespace-nowrap text-slate-400 font-mono text-[12px]">
                      <time dateTime={evt.timestamp}>
                        {new Date(evt.timestamp).toISOString().replace('T', ' ').substring(0, 19)}
                      </time>
                    </td>
                    <td className="px-4 h-11 whitespace-nowrap font-mono text-[12px]">
                      <span className="text-slate-500 mr-2">{evt.http_method || 'GET'}</span>
                      <span className="text-slate-100">{evt.request_path || '/'}</span>
                    </td>
                    <td className="px-4 h-11 whitespace-nowrap font-mono text-[12px] text-slate-300 truncate max-w-[160px]" title={evt.principal_id}>
                      {evt.principal_id}
                    </td>
                    <td className="px-4 h-11 whitespace-nowrap text-slate-300"><ServiceBullet service={evt.service_id || 'unknown'} /></td>
                    <td className="px-4 h-11 whitespace-nowrap font-mono text-[12px] text-slate-400">{evt.reason_code}</td>
                    <td
                      className={`px-4 h-11 whitespace-nowrap text-right num ${
                        !evt.status_code
                          ? 'text-slate-500'
                          : evt.status_code < 400
                          ? 'text-slate-300'
                          : evt.status_code < 500
                          ? 'text-amber-300'
                          : 'text-rose-600'
                      }`}
                    >
                      {evt.status_code || '—'}
                    </td>
                    <td className="px-4 h-11 whitespace-nowrap text-right text-slate-400 num">
                      {evt.event_type !== 'decision' && evt.duration_ms !== undefined ? `${evt.duration_ms} ms` : '—'}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}

        <div className="flex items-center justify-between px-4 h-12 border-t border-mist text-sm text-slate-500">
          <span className="num">
            {events.length} {events.length === 1 ? 'event' : 'events'} on this page
          </span>
          <div className="flex items-center gap-2">
            <button onClick={handlePreviousPage} disabled={cursorStack.length === 0} className="btn btn-secondary btn-sm">
              <ChevronLeft />
              Previous
            </button>
            <button onClick={handleNextPage} disabled={!nextCursor} className="btn btn-secondary btn-sm">
              Next
              <ChevronRight />
            </button>
          </div>
        </div>
      </section>

      {selectedEvent && (
        <div className="fixed inset-0 z-50 flex justify-end bg-[#111214]/30" onClick={() => setSelectedEvent(null)}>
          <aside
            role="dialog"
            aria-modal="true"
            aria-labelledby="audit-drawer-title"
            onClick={(e) => e.stopPropagation()}
            className="w-full max-w-xl bg-paper border-l border-mist h-full shadow-xl flex flex-col"
          >
            <div className="flex items-start justify-between gap-4 px-6 py-5 border-b border-mist">
              <div className="min-w-0">
                <h2 id="audit-drawer-title" className="text-lg font-medium text-slate-100">Audit event</h2>
                <p className="mt-0.5 font-mono text-xs text-slate-500 truncate">{selectedEvent.event_id}</p>
              </div>
              <button
                ref={closeRef}
                onClick={() => setSelectedEvent(null)}
                aria-label="Close event details"
                className="btn btn-ghost btn-sm -mr-2"
              >
                <X />
              </button>
            </div>

            <div className="p-6 flex-1 overflow-y-auto space-y-6">
              <dl className="grid grid-cols-2 gap-x-6 gap-y-4 text-sm">
                <div>
                  <dt className="label">Decision</dt>
                  <dd className={selectedEvent.decision === 'allow' ? 'text-emerald-400' : 'text-rose-600'}>
                    {selectedEvent.decision === 'allow' ? 'Allowed' : 'Denied'}
                  </dd>
                </div>
                <div>
                  <dt className="label">Reason code</dt>
                  <dd className="font-mono text-[13px] text-slate-100 break-all">{selectedEvent.reason_code}</dd>
                </div>
                <div>
                  <dt className="label">Principal kind</dt>
                  <dd className="text-slate-100">{selectedEvent.principal_kind || 'user'}</dd>
                </div>
                <div>
                  <dt className="label">Policy snapshot</dt>
                  <dd className="text-slate-100 num">v{selectedEvent.snapshot_version}</dd>
                </div>
              </dl>

              <div>
                <div className="flex items-center justify-between mb-2">
                  <h3 className="text-sm font-medium text-slate-100">Raw event</h3>
                  <button onClick={handleCopyJSON} className="btn btn-secondary btn-sm" aria-live="polite">
                    {copied ? <Check /> : <Copy />}
                    {copied ? 'Copied' : 'Copy JSON'}
                  </button>
                </div>
                <pre className="bg-linen border border-mist rounded-lg p-4 font-mono text-xs text-slate-300 overflow-x-auto leading-relaxed">
                  {JSON.stringify(selectedEvent, null, 2)}
                </pre>
              </div>
            </div>
          </aside>
        </div>
      )}
    </div>
  );
};
