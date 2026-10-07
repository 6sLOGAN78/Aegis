import React, { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import {
  ScrollText,
  Filter,
  CheckCircle2,
  XCircle,
  Copy,
  Check,
  X,
  ChevronLeft,
  ChevronRight,
  Shield,
  Loader2,
  Calendar,
} from 'lucide-react';
import { apiClient } from '../api/client';
import type { AuditEvent, AuditEventListResponse } from '../api/types';

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

  return (
    <div className="space-y-6">
      {/* View Header */}
      <div className="flex flex-col lg:flex-row lg:items-center justify-between gap-4 bg-slate-900 border border-slate-800 p-6 rounded-xl">
        <div className="space-y-1">
          <div className="flex items-center space-x-3">
            <ScrollText className="w-6 h-6 text-cyan-400" />
            <h1 className="text-xl font-semibold text-slate-100">Audit Stream & Authorization Logs</h1>
          </div>
          <p className="text-sm text-slate-400">
            Durable, immutable pre-forward policy decisions and request completions indexed in partitioned PostgreSQL
          </p>
        </div>
      </div>

      {/* Query Filter Toolbar */}
      <div className="bg-slate-900 border border-slate-800 p-4 rounded-xl space-y-3">
        <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-5 gap-3">
          {/* Decision Dropdown */}
          <div>
            <label className="block text-xs font-semibold text-slate-400 mb-1">Decision</label>
            <select
              value={decision}
              onChange={(e) => setDecision(e.target.value)}
              className="w-full bg-slate-950 border border-slate-800 rounded-lg px-3 py-2 text-xs text-slate-200 focus:ring-1 focus:ring-cyan-500 focus:outline-none"
            >
              <option value="all">All Decisions</option>
              <option value="allow">Allow Only</option>
              <option value="deny">Deny Only</option>
            </select>
          </div>

          {/* Service ID Input */}
          <div>
            <label className="block text-xs font-semibold text-slate-400 mb-1">Service ID</label>
            <input
              type="text"
              value={serviceId}
              onChange={(e) => setServiceId(e.target.value)}
              placeholder="e.g. orders, payments"
              className="w-full bg-slate-950 border border-slate-800 rounded-lg px-3 py-2 text-xs text-slate-200 focus:ring-1 focus:ring-cyan-500 focus:outline-none font-mono"
            />
          </div>

          {/* Principal ID Input */}
          <div>
            <label className="block text-xs font-semibold text-slate-400 mb-1">Principal ID</label>
            <input
              type="text"
              value={principalId}
              onChange={(e) => setPrincipalId(e.target.value)}
              placeholder="e.g. usr_alice"
              className="w-full bg-slate-950 border border-slate-800 rounded-lg px-3 py-2 text-xs text-slate-200 focus:ring-1 focus:ring-cyan-500 focus:outline-none font-mono"
            />
          </div>

          {/* Time Range Selector */}
          <div>
            <label className="block text-xs font-semibold text-slate-400 mb-1">Time Window</label>
            <div className="relative">
              <select
                value={timeRange}
                onChange={(e) => setTimeRange(e.target.value as '1h' | '24h' | '7d')}
                className="w-full bg-slate-950 border border-slate-800 rounded-lg px-3 py-2 text-xs text-slate-200 focus:ring-1 focus:ring-cyan-500 focus:outline-none appearance-none"
              >
                <option value="1h">Last 1 Hour</option>
                <option value="24h">Last 24 Hours</option>
                <option value="7d">Last 7 Days</option>
              </select>
              <Calendar className="w-3.5 h-3.5 text-slate-500 absolute right-3 top-2.5 pointer-events-none" />
            </div>
          </div>

          {/* Filter Action Button */}
          <div className="flex items-end space-x-2">
            <button
              onClick={handleApplyFilters}
              disabled={isFetching}
              className="flex-1 flex items-center justify-center space-x-1.5 bg-cyan-600 hover:bg-cyan-500 text-white px-4 py-2 rounded-lg text-xs font-semibold transition disabled:opacity-50"
            >
              {isFetching ? (
                <Loader2 className="w-3.5 h-3.5 animate-spin" />
              ) : (
                <Filter className="w-3.5 h-3.5" />
              )}
              <span>Filter Audit Events</span>
            </button>
            <button
              onClick={handleResetFilters}
              aria-label="Clear active query filters"
              title="Clear active query filters"
              className="bg-slate-800 hover:bg-slate-700 text-slate-300 px-3 py-2 rounded-lg text-xs font-semibold transition"
            >
              Reset
            </button>
          </div>
        </div>
      </div>

      {/* High-density Audit Table */}
      <div className="bg-slate-900 border border-slate-800 rounded-xl overflow-hidden shadow-lg">
        {isLoading ? (
          <div className="flex flex-col items-center justify-center py-16 space-y-3 text-slate-400">
            <Loader2 className="w-8 h-8 animate-spin text-cyan-400" />
            <span className="text-xs">Querying partitioned PostgreSQL audit records...</span>
          </div>
        ) : events.length === 0 ? (
          /* Empty State conforming to Copy Contract */
          <div className="flex flex-col items-center justify-center text-center p-12 space-y-4">
            <div className="p-3 bg-slate-950 rounded-xl border border-slate-800">
              <Shield className="w-8 h-8 text-cyan-400" />
            </div>
            <div className="space-y-1 max-w-md">
              <h3 className="text-base font-semibold text-slate-200">
                No Audit Events Recorded In Time Range
              </h3>
              <p className="text-xs text-slate-400 leading-relaxed">
                No authorization decisions or gateway completions matched the active query filters. Adjust the time window or clear principal filters to inspect historical logs.
              </p>
            </div>
            <button
              onClick={handleResetFilters}
              className="bg-slate-800 hover:bg-slate-700 text-slate-200 text-xs font-semibold px-4 py-2 rounded-lg transition"
            >
              Reset Query Filters
            </button>
          </div>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full text-left border-collapse text-xs">
              <thead>
                <tr className="bg-slate-950/80 border-b border-slate-800 text-[11px] font-semibold uppercase tracking-wider text-slate-400">
                  <th className="py-3 px-4">Decision</th>
                  <th className="py-3 px-4">Timestamp (UTC)</th>
                  <th className="py-3 px-4">Method & Path</th>
                  <th className="py-3 px-4">Principal ID</th>
                  <th className="py-3 px-4">Service</th>
                  <th className="py-3 px-4">Reason Code</th>
                  <th className="py-3 px-4">Status</th>
                  <th className="py-3 px-4 text-right">Latency</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-slate-800/60 font-mono">
                {events.map((evt) => (
                  <tr
                    key={evt.event_id}
                    onClick={() => setSelectedEvent(evt)}
                    className="hover:bg-slate-800/40 cursor-pointer transition"
                  >
                    {/* Decision Badge */}
                    <td className="py-3 px-4 whitespace-nowrap">
                      {evt.decision === 'allow' ? (
                        <span className="inline-flex items-center space-x-1 font-semibold px-2 py-0.5 rounded-full bg-emerald-500/10 text-emerald-400 border border-emerald-500/30">
                          <CheckCircle2 className="w-3 h-3" />
                          <span>ALLOW</span>
                        </span>
                      ) : (
                        <span className="inline-flex items-center space-x-1 font-semibold px-2 py-0.5 rounded-full bg-rose-500/10 text-rose-400 border border-rose-500/30">
                          <XCircle className="w-3 h-3" />
                          <span>DENY</span>
                        </span>
                      )}
                    </td>

                    {/* Timestamp */}
                    <td className="py-3 px-4 whitespace-nowrap text-slate-400 text-[11px]">
                      {new Date(evt.timestamp).toISOString().replace('T', ' ').substring(0, 19)}
                    </td>

                    {/* Method & Path */}
                    <td className="py-3 px-4 whitespace-nowrap">
                      <span className="font-semibold text-cyan-400 mr-2">{evt.http_method || 'GET'}</span>
                      <span className="text-slate-200">{evt.request_path || '/'}</span>
                    </td>

                    {/* Principal */}
                    <td className="py-3 px-4 whitespace-nowrap text-slate-300 truncate max-w-[140px]">
                      {evt.principal_id}
                    </td>

                    {/* Service */}
                    <td className="py-3 px-4 whitespace-nowrap text-slate-400 font-sans">
                      {evt.service_id}
                    </td>

                    {/* Reason Code */}
                    <td className="py-3 px-4 whitespace-nowrap">
                      <span className="bg-slate-950 px-2 py-0.5 rounded border border-slate-800 text-[11px] text-slate-300">
                        {evt.reason_code}
                      </span>
                    </td>

                    {/* HTTP Status */}
                    <td className="py-3 px-4 whitespace-nowrap">
                      <span
                        className={`text-[11px] font-bold ${
                          (evt.status_code || 200) < 400
                            ? 'text-emerald-400'
                            : (evt.status_code || 500) < 500
                            ? 'text-amber-400'
                            : 'text-rose-400'
                        }`}
                      >
                        {evt.status_code || '—'}
                      </span>
                    </td>

                    {/* Latency */}
                    <td className="py-3 px-4 whitespace-nowrap text-right text-slate-400">
                      {evt.duration_ms !== undefined ? `${evt.duration_ms} ms` : '—'}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}

        {/* Pagination Toolbar */}
        <div className="flex items-center justify-between px-4 py-3 bg-slate-950/80 border-t border-slate-800 text-xs text-slate-400">
          <div>
            Showing <span className="font-semibold text-slate-200">{events.length}</span> records
          </div>
          <div className="flex items-center space-x-2">
            <button
              onClick={handlePreviousPage}
              disabled={cursorStack.length === 0}
              className="flex items-center space-x-1 px-3 py-1.5 rounded-lg bg-slate-850 hover:bg-slate-800 text-slate-300 disabled:opacity-40 disabled:cursor-not-allowed border border-slate-800"
            >
              <ChevronLeft className="w-3.5 h-3.5" />
              <span>Previous</span>
            </button>
            <button
              onClick={handleNextPage}
              disabled={!nextCursor}
              className="flex items-center space-x-1 px-3 py-1.5 rounded-lg bg-slate-850 hover:bg-slate-800 text-slate-300 disabled:opacity-40 disabled:cursor-not-allowed border border-slate-800"
            >
              <span>Next Page</span>
              <ChevronRight className="w-3.5 h-3.5" />
            </button>
          </div>
        </div>
      </div>

      {/* Slide-over Detail Drawer */}
      {selectedEvent && (
        <div className="fixed inset-0 z-50 flex justify-end bg-black/60 backdrop-blur-xs">
          <div className="w-full max-w-xl bg-slate-900 border-l border-slate-800 h-full shadow-2xl flex flex-col animate-in slide-in-from-right duration-200">
            {/* Drawer Header */}
            <div className="flex items-center justify-between p-6 border-b border-slate-800">
              <div className="space-y-1">
                <h3 className="text-lg font-semibold text-slate-100 flex items-center space-x-2">
                  <span>Audit Record Inspection</span>
                </h3>
                <span className="text-xs font-mono text-cyan-400">{selectedEvent.event_id}</span>
              </div>
              <button
                onClick={() => setSelectedEvent(null)}
                aria-label="Close modal window"
                title="Close modal window"
                className="text-slate-400 hover:text-slate-200 p-1 rounded-lg hover:bg-slate-800"
              >
                <X className="w-5 h-5" />
              </button>
            </div>

            {/* Drawer Body: Formatted Event Details */}
            <div className="p-6 flex-1 overflow-y-auto space-y-6">
              <div className="grid grid-cols-2 gap-4 bg-slate-950 p-4 rounded-xl border border-slate-800 text-xs">
                <div>
                  <span className="text-slate-500 uppercase font-semibold text-[10px]">Decision:</span>
                  <div className="mt-1 font-semibold">
                    {selectedEvent.decision === 'allow' ? (
                      <span className="text-emerald-400">ALLOW (Granted)</span>
                    ) : (
                      <span className="text-rose-400">DENY (Blocked)</span>
                    )}
                  </div>
                </div>
                <div>
                  <span className="text-slate-500 uppercase font-semibold text-[10px]">Reason Code:</span>
                  <div className="mt-1 font-mono text-slate-200">{selectedEvent.reason_code}</div>
                </div>
                <div>
                  <span className="text-slate-500 uppercase font-semibold text-[10px]">Principal Kind:</span>
                  <div className="mt-1 text-slate-200">{selectedEvent.principal_kind || 'user'}</div>
                </div>
                <div>
                  <span className="text-slate-500 uppercase font-semibold text-[10px]">Snapshot Version:</span>
                  <div className="mt-1 font-mono text-slate-200">v{selectedEvent.snapshot_version}</div>
                </div>
              </div>

              {/* Raw JSON Payload */}
              <div className="space-y-2">
                <div className="flex items-center justify-between">
                  <span className="text-xs font-semibold uppercase tracking-wider text-slate-400">
                    Raw JSON Event Payload
                  </span>
                  <button
                    onClick={handleCopyJSON}
                    aria-label="Copy JSON payload to clipboard"
                    title="Copy JSON payload to clipboard"
                    className="flex items-center space-x-1 text-xs text-cyan-400 hover:text-cyan-300 bg-cyan-500/10 px-2.5 py-1 rounded border border-cyan-500/20 transition"
                  >
                    {copied ? <Check className="w-3.5 h-3.5 text-emerald-400" /> : <Copy className="w-3.5 h-3.5" />}
                    <span>{copied ? 'Copied!' : 'Copy JSON'}</span>
                  </button>
                </div>
                <pre className="bg-slate-950 border border-slate-800 rounded-xl p-4 font-mono text-[11px] text-slate-300 overflow-x-auto leading-relaxed">
                  {JSON.stringify(selectedEvent, null, 2)}
                </pre>
              </div>
            </div>
          </div>
        </div>
      )}
    </div>
  );
};
