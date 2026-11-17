import React, { useState } from 'react';
import { useMutation } from '@tanstack/react-query';
import { Play, CheckCircle2, XCircle, Clock, AlertCircle } from 'lucide-react';
import { PageHeader } from './PageHeader';
import { apiClient, ApiError } from '../api/client';
import type { PolicySimulationResponse } from '../api/types';

interface PresetOption {
  label: string;
  context: Record<string, unknown>;
}

// Contexts mirror the gateway's OPA input (policies/rego/authz.rego):
// principal.{kind,id,roles}, resource.service, request.{method,path}.
const PRESETS: PresetOption[] = [
  {
    label: 'Developer reads orders',
    context: {
      principal: { kind: 'user', id: 'usr_dev_alice', roles: ['developer'] },
      resource: { service: 'orders' },
      request: { method: 'GET', path: '/api/orders' },
    },
  },
  {
    label: 'Developer opens admin',
    context: {
      principal: { kind: 'user', id: 'usr_dev_alice', roles: ['developer'] },
      resource: { service: 'admin' },
      request: { method: 'GET', path: '/api/admin/users' },
    },
  },
  {
    label: 'Orders service pays',
    context: {
      principal: { kind: 'workload', id: 'spiffe://aegis.local/workload/orders', roles: [] },
      resource: { service: 'payments' },
      request: { method: 'POST', path: '/api/payments' },
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
        setParseError(`Request input is not valid JSON: ${msg}`);
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
        setParseError(err.message || 'The simulation failed. Check the policy compiles, then try again.');
      }
    },
  });

  const handleApplyPreset = (index: number) => {
    setActivePreset(index);
    setInputContext(JSON.stringify(PRESETS[index].context, null, 2));
    setParseError(null);
  };

  return (
    <div>
      <PageHeader
        title="Policy simulator"
        description={`Evaluate a request against ${candidateRego ? 'the policy in the editor, including unpublished changes,' : 'the live policy'} without sending traffic. Results come from the same OPA engine the gateways run.`}
        actions={
          <button
            onClick={() => simulateMutation.mutate()}
            disabled={simulateMutation.isPending}
            className="btn btn-primary"
          >
            <Play />
            {simulateMutation.isPending ? 'Evaluating…' : 'Run simulation'}
          </button>
        }
      />

      <div className="grid grid-cols-1 lg:grid-cols-2 gap-6">
        <section className="panel flex flex-col min-w-0">
          <div className="panel-head">
            <h2 className="panel-title">Request input</h2>
          </div>

          <div className="p-5 flex-1 flex flex-col gap-3">
            <div className="flex flex-wrap items-center gap-1.5" role="group" aria-label="Example requests">
              <span className="text-xs text-slate-500 mr-1">Examples</span>
              {PRESETS.map((preset, idx) => (
                <button
                  key={preset.label}
                  onClick={() => handleApplyPreset(idx)}
                  aria-pressed={activePreset === idx}
                  className={`btn btn-sm ${activePreset === idx ? 'btn-secondary border-ink' : 'btn-ghost'}`}
                >
                  {preset.label}
                </button>
              ))}
            </div>
            <label htmlFor="sim-input" className="sr-only">Request input JSON</label>
            <textarea
              id="sim-input"
              value={inputContext}
              onChange={(e) => {
                setInputContext(e.target.value);
                setActivePreset(null);
                setParseError(null);
              }}
              rows={16}
              aria-invalid={parseError ? true : undefined}
              aria-describedby={parseError ? 'sim-error' : undefined}
              className="field flex-1 font-mono text-[13px] leading-relaxed resize-none bg-linen"
              spellCheck={false}
            />
            {parseError && (
              <p id="sim-error" role="alert" className="flex items-start gap-2 text-sm text-rose-600">
                <AlertCircle className="w-4 h-4 shrink-0 mt-0.5" />
                <span>{parseError}</span>
              </p>
            )}
          </div>
        </section>

        <section className="panel flex flex-col min-w-0" aria-live="polite">
          <div className="panel-head">
            <h2 className="panel-title">Decision</h2>
            {result && (
              <span className="flex items-center gap-1.5 text-xs text-slate-500 num">
                <Clock className="w-3.5 h-3.5" />
                {(result.duration_us / 1000).toFixed(2)} ms
              </span>
            )}
          </div>

          <div className="p-5 flex-1 flex flex-col">
            {result ? (
              <dl className="space-y-5">
                <div className="flex items-center gap-3">
                  {result.allow ? (
                    <CheckCircle2 className="w-8 h-8 text-emerald-500 shrink-0" />
                  ) : (
                    <XCircle className="w-8 h-8 text-rose-500 shrink-0" />
                  )}
                  <div>
                    <dt className="sr-only">Outcome</dt>
                    <dd className={`text-2xl font-display ${result.allow ? 'text-emerald-400' : 'text-rose-500'}`}>
                      {result.allow ? 'Allowed' : 'Denied'}
                    </dd>
                  </div>
                </div>

                <div>
                  <dt className="label">Reason code</dt>
                  <dd className="font-mono text-sm text-slate-100">{result.reason_code}</dd>
                </div>

                {result.diagnostics && Object.keys(result.diagnostics).length > 0 && (
                  <div>
                    <dt className="label">Diagnostics</dt>
                    <dd>
                      <pre className="font-mono text-xs text-slate-300 bg-linen border border-mist rounded-lg p-3 overflow-x-auto">
                        {JSON.stringify(result.diagnostics, null, 2)}
                      </pre>
                    </dd>
                  </div>
                )}
              </dl>
            ) : (
              <div className="m-auto max-w-xs text-center py-10">
                <p className="text-sm text-slate-300">No simulation run yet.</p>
                <p className="mt-1 text-sm text-slate-500">
                  Edit the request on the left or pick an example, then run the simulation.
                </p>
              </div>
            )}
          </div>
        </section>
      </div>
    </div>
  );
};
