import React, { useState } from 'react';
import { useMutation } from '@tanstack/react-query';
import { Play, CheckCircle2, XCircle, Clock, AlertCircle, FileJson, Sparkles } from 'lucide-react';
import { apiClient, ApiError } from '../api/client';
import type { PolicySimulationResponse } from '../api/types';

interface PresetOption {
  label: string;
  context: Record<string, unknown>;
}

const PRESETS: PresetOption[] = [
  {
    label: 'Developer Orders (Allow)',
    context: {
      method: 'GET',
      path: '/api/orders',
      principal: {
        id: 'usr_dev_alice',
        roles: ['developer'],
      },
      client_ip: '192.168.1.50',
    },
  },
  {
    label: 'Developer Admin (Deny)',
    context: {
      method: 'GET',
      path: '/api/admin/users',
      principal: {
        id: 'usr_dev_alice',
        roles: ['developer'],
      },
      client_ip: '192.168.1.50',
    },
  },
  {
    label: 'Workload Payments (Allow)',
    context: {
      method: 'POST',
      path: '/api/payments',
      principal: {
        id: 'spiffe://aegis.local/service/orders',
        kind: 'workload',
        roles: ['orders-service'],
      },
      client_ip: '10.0.4.12',
    },
  },
];

interface PolicySimulatorProps {
  candidateRego?: string;
}

export const PolicySimulator: React.FC<PolicySimulatorProps> = ({ candidateRego }) => {
  const [inputContext, setInputContext] = useState<string>(
    JSON.stringify(PRESETS[0].context, null, 2)
  );
  const [result, setResult] = useState<PolicySimulationResponse | null>(null);
  const [parseError, setParseError] = useState<string | null>(null);
  const [activePreset, setActivePreset] = useState<number | null>(0);

  const simulateMutation = useMutation({
    mutationFn: async () => {
      setParseError(null);
      let parsed: Record<string, unknown>;
      try {
        parsed = JSON.parse(inputContext);
      } catch (err) {
        const msg = err instanceof Error ? err.message : 'Invalid JSON format';
        setParseError(`JSON Parsing Error: ${msg}`);
        throw new Error(msg);
      }

      return apiClient.post<PolicySimulationResponse>('/control/v1/policies/simulate', {
        candidate_rego: candidateRego || undefined,
        input_context: parsed,
      });
    },
    onSuccess: (data) => {
      setResult(data);
    },
    onError: (err) => {
      if (err instanceof ApiError) {
        setParseError(err.message || 'Policy simulation evaluation failed.');
      }
    },
  });

  const handleApplyPreset = (index: number) => {
    setActivePreset(index);
    setInputContext(JSON.stringify(PRESETS[index].context, null, 2));
    setParseError(null);
  };

  return (
    <div className="space-y-6">
      {/* View Header */}
      <div className="flex flex-col lg:flex-row lg:items-center justify-between gap-4 bg-slate-900 border border-slate-800 p-6 rounded-xl">
        <div className="space-y-1">
          <div className="flex items-center space-x-3">
            <FileJson className="w-6 h-6 text-cyan-400" />
            <h1 className="text-xl font-semibold text-slate-100">Dry-Run Policy Simulator</h1>
          </div>
          <p className="text-sm text-slate-400">
            Evaluate candidate authorization rules against synthetic JSON request contexts in-memory with microsecond telemetry
          </p>
        </div>

        <button
          onClick={() => simulateMutation.mutate()}
          disabled={simulateMutation.isPending}
          className="flex items-center space-x-2 bg-cyan-600 hover:bg-cyan-500 text-white px-5 py-2.5 rounded-lg text-sm font-semibold transition disabled:opacity-50 shrink-0"
        >
          <Play className={`w-4 h-4 fill-current ${simulateMutation.isPending ? 'animate-pulse' : ''}`} />
          <span>{simulateMutation.isPending ? 'Evaluating In-Memory...' : 'Execute Dry-Run Simulation'}</span>
        </button>
      </div>

      {/* Preset Context Selector Bar */}
      <div className="flex flex-wrap items-center gap-2 bg-slate-900 border border-slate-800 p-3 rounded-xl">
        <span className="text-xs font-semibold uppercase tracking-wider text-slate-400 flex items-center space-x-1.5 mr-2">
          <Sparkles className="w-3.5 h-3.5 text-cyan-400" />
          <span>Synthetic Presets:</span>
        </span>
        {PRESETS.map((preset, idx) => (
          <button
            key={preset.label}
            onClick={() => handleApplyPreset(idx)}
            className={`text-xs px-3 py-1.5 rounded-lg font-medium transition border ${
              activePreset === idx
                ? 'bg-cyan-500/10 text-cyan-300 border-cyan-500/40'
                : 'bg-slate-950 text-slate-400 border-slate-800 hover:text-slate-200 hover:border-slate-700'
            }`}
          >
            {preset.label}
          </button>
        ))}
      </div>

      {/* Side-by-side Comparative Grid */}
      <div className="grid grid-cols-1 lg:grid-cols-2 gap-6">
        {/* Left Panel: Request Context Editor */}
        <div className="flex flex-col bg-slate-900 border border-slate-800 rounded-xl overflow-hidden shadow-lg">
          <div className="flex items-center justify-between px-4 py-3 bg-slate-950/80 border-b border-slate-800">
            <h3 className="text-xs font-semibold uppercase tracking-wider text-slate-300">
              Synthetic Request Context (JSON)
            </h3>
            {candidateRego ? (
              <span className="text-[11px] font-mono text-cyan-400 bg-cyan-500/10 px-2 py-0.5 rounded border border-cyan-500/20">
                Evaluating Candidate Draft
              </span>
            ) : (
              <span className="text-[11px] font-mono text-slate-400 bg-slate-800 px-2 py-0.5 rounded">
                Evaluating Active Policy
              </span>
            )}
          </div>

          <div className="p-4 flex-1 flex flex-col space-y-3">
            <textarea
              value={inputContext}
              onChange={(e) => {
                setInputContext(e.target.value);
                setActivePreset(null);
                setParseError(null);
              }}
              rows={16}
              className="w-full flex-1 bg-slate-950 border border-slate-800 rounded-lg p-4 font-mono text-xs text-slate-200 focus:ring-2 focus:ring-cyan-500 focus:outline-none resize-none leading-relaxed"
              spellCheck={false}
            />

            {parseError && (
              <div className="flex items-start space-x-2 bg-rose-500/10 border border-rose-500/30 p-3 rounded-lg text-rose-300 text-xs">
                <AlertCircle className="w-4 h-4 shrink-0 mt-0.5" />
                <span>{parseError}</span>
              </div>
            )}
          </div>
        </div>

        {/* Right Panel: Decision Inspector Card */}
        <div className="flex flex-col bg-slate-900 border border-slate-800 rounded-xl overflow-hidden shadow-lg">
          <div className="px-4 py-3 bg-slate-950/80 border-b border-slate-800">
            <h3 className="text-xs font-semibold uppercase tracking-wider text-slate-300">
              Decision Outcome Card
            </h3>
          </div>

          <div className="p-6 flex-1 flex flex-col justify-center">
            {result ? (
              <div className="space-y-6">
                {/* Large Decision Outcome Badge */}
                <div
                  className={`p-6 rounded-xl border flex items-center justify-between ${
                    result.allow
                      ? 'bg-emerald-500/10 border-emerald-500/30'
                      : 'bg-rose-500/10 border-rose-500/30'
                  }`}
                >
                  <div className="flex items-center space-x-4">
                    {result.allow ? (
                      <CheckCircle2 className="w-12 h-12 text-emerald-400 shrink-0" />
                    ) : (
                      <XCircle className="w-12 h-12 text-rose-400 shrink-0" />
                    )}
                    <div>
                      <div
                        className={`text-2xl font-bold tracking-tight ${
                          result.allow ? 'text-emerald-400' : 'text-rose-400'
                        }`}
                      >
                        {result.allow ? 'ALLOW' : 'DENY'}
                      </div>
                      <div className="text-xs text-slate-400 mt-0.5">
                        {result.allow
                          ? 'Request meets zero-trust authorization requirements'
                          : 'Request denied by policy default-deny rules'}
                      </div>
                    </div>
                  </div>

                  {/* Microsecond Latency Timer */}
                  <div className="flex items-center space-x-2 bg-slate-950/80 px-3 py-2 rounded-lg border border-slate-800 font-mono text-xs text-slate-300">
                    <Clock className="w-4 h-4 text-cyan-400" />
                    <span>{(result.duration_us / 1000).toFixed(2)} ms</span>
                  </div>
                </div>

                {/* Reason Code Section */}
                <div className="bg-slate-950 p-4 rounded-xl border border-slate-800 space-y-2">
                  <div className="text-xs font-semibold uppercase tracking-wider text-slate-400">
                    Reason Code
                  </div>
                  <div className="font-mono text-sm font-semibold text-slate-100 bg-slate-900 px-3 py-1.5 rounded-lg border border-slate-800 inline-block">
                    {result.reason_code}
                  </div>
                </div>

                {/* Diagnostics details if present */}
                {result.diagnostics && Object.keys(result.diagnostics).length > 0 && (
                  <div className="bg-slate-950 p-4 rounded-xl border border-slate-800 space-y-2">
                    <div className="text-xs font-semibold uppercase tracking-wider text-slate-400">
                      Diagnostic Attributes
                    </div>
                    <pre className="font-mono text-xs text-slate-300 bg-slate-900 p-3 rounded-lg overflow-x-auto">
                      {JSON.stringify(result.diagnostics, null, 2)}
                    </pre>
                  </div>
                )}
              </div>
            ) : (
              /* Empty State adhering to Copy Contract */
              <div className="flex flex-col items-center justify-center text-center p-8 space-y-4 border border-dashed border-slate-800 rounded-xl my-auto">
                <div className="p-3 bg-slate-950 rounded-xl border border-slate-800">
                  <Play className="w-8 h-8 text-cyan-400" />
                </div>
                <div className="space-y-1.5 max-w-sm">
                  <h4 className="text-base font-semibold text-slate-200">
                    Simulation Awaiting Request Context
                  </h4>
                  <p className="text-xs text-slate-400 leading-relaxed">
                    Enter a synthetic JSON request payload in the editor on the left and click execute to inspect allow/deny decisions, reason codes, and microsecond latencies.
                  </p>
                </div>
                <button
                  onClick={() => handleApplyPreset(0)}
                  className="bg-slate-800 hover:bg-slate-700 text-slate-200 text-xs font-semibold px-4 py-2 rounded-lg transition"
                >
                  Load Sample Context
                </button>
              </div>
            )}
          </div>
        </div>
      </div>
    </div>
  );
};
