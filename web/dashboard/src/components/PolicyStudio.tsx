import React, { useState } from 'react';
import Editor, { type BeforeMount } from '@monaco-editor/react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import {
  CheckCircle2,
  XCircle,
  AlertTriangle,
  RotateCcw,
  Upload,
  PlayCircle,
  Clock,
  User,
  Tag,
  Loader2,
  X,
  FileCode,
} from 'lucide-react';
import { apiClient, ApiError } from '../api/client';
import { registerRegoLanguage, regoLanguageId } from '../utils/rego-monarch';
import type {
  PolicyValidationResponse,
  PolicyPublishResponse,
  PolicyVersionListResponse,
  PolicyVersion,
  SessionInfo,
} from '../api/types';

const DEFAULT_REGO_TEMPLATE = `package aegis.authz

import rego.v1

default allow := false

# Base decision structure
decision := {
    "allow": allow,
    "reason_code": reason_code
}

default reason_code := "DENIED_DEFAULT"

allow if {
    input.method == "GET"
    "developer" in input.principal.roles
}

reason_code := "ALLOWED" if {
    allow
}
`;

interface PolicyStudioProps {
  session: SessionInfo | null;
  onCandidateRegoChange?: (rego: string) => void;
}

export const PolicyStudio: React.FC<PolicyStudioProps> = ({ session, onCandidateRegoChange }) => {
  const queryClient = useQueryClient();
  const [policyId, setPolicyId] = useState('pol_authz_core');
  const [policyName, setPolicyName] = useState('Zero-Trust Gateway Ingress Policy');
  const [regoCode, setRegoCode] = useState<string>(DEFAULT_REGO_TEMPLATE);
  const [validationResult, setValidationResult] = useState<PolicyValidationResponse | null>(null);
  const [validationError, setValidationError] = useState<string | null>(null);

  // Publish Modal State
  const [isPublishModalOpen, setIsPublishModalOpen] = useState(false);
  const [publishComment, setPublishComment] = useState('');
  const [publishError, setPublishError] = useState<string | null>(null);
  const [successBanner, setSuccessBanner] = useState<string | null>(null);

  // Rollback Modal State
  const [isRollbackModalOpen, setIsRollbackModalOpen] = useState(false);
  const [selectedRollbackVersion, setSelectedRollbackVersion] = useState<PolicyVersion | null>(null);
  const [rollbackComment, setRollbackComment] = useState('');
  const [rollbackError, setRollbackError] = useState<string | null>(null);

  const isSecOps = session?.role === 'sec-ops';

  // Fetch version history for this policy
  const { data: versionsData, isLoading: isLoadingVersions } = useQuery<PolicyVersionListResponse>({
    queryKey: ['policy-versions', policyId],
    queryFn: () => apiClient.get<PolicyVersionListResponse>(`/control/v1/policies/${policyId}/versions`),
    refetchInterval: 10000,
  });

  const versions = versionsData?.versions || [];
  const latestVersion = versions.length > 0 ? versions[0].snapshot_version : 1;
  const currentETag = `"${latestVersion}"`;

  const handleEditorBeforeMount: BeforeMount = (monaco) => {
    registerRegoLanguage(monaco);
  };

  const handleCodeChange = (value?: string) => {
    const updated = value || '';
    setRegoCode(updated);
    if (onCandidateRegoChange) {
      onCandidateRegoChange(updated);
    }
  };

  // Validate Mutation
  const validateMutation = useMutation({
    mutationFn: async () => {
      setValidationError(null);
      return apiClient.post<PolicyValidationResponse>(`/control/v1/policies/${policyId}/validate`, {
        candidate_rego: regoCode,
      });
    },
    onSuccess: (data) => {
      setValidationResult(data);
      if (!data.valid && data.errors && data.errors.length > 0) {
        setValidationError(data.errors.join('\n'));
      }
    },
    onError: (err) => {
      if (err instanceof ApiError) {
        setValidationError(err.message);
      } else {
        setValidationError('Validation request failed.');
      }
      setValidationResult(null);
    },
  });

  // Publish Mutation
  const publishMutation = useMutation({
    mutationFn: async () => {
      setPublishError(null);
      return apiClient.post<PolicyPublishResponse>(
        `/control/v1/policies/${policyId}/publish`,
        { comment: publishComment || 'Broadcast via Policy Studio console' },
        { ifMatch: currentETag }
      );
    },
    onSuccess: (data) => {
      setIsPublishModalOpen(false);
      setPublishComment('');
      setSuccessBanner(
        `Snapshot v${data.snapshot_version} successfully signed with Ed25519 and broadcast to fleet.`
      );
      queryClient.invalidateQueries({ queryKey: ['policy-versions', policyId] });
      queryClient.invalidateQueries({ queryKey: ['gateways'] });
    },
    onError: (err) => {
      if (err instanceof ApiError) {
        if (err.status === 412) {
          setPublishError('Publication conflict: ETag mismatch. Another operator published a newer version.');
        } else {
          setPublishError(err.message || 'Snapshot publication failed.');
        }
      } else {
        setPublishError('Snapshot publication failed due to network error.');
      }
    },
  });

  // Rollback Mutation
  const rollbackMutation = useMutation({
    mutationFn: async () => {
      if (!selectedRollbackVersion) return;
      setRollbackError(null);
      return apiClient.post<{ snapshot_version: number }>(
        `/control/v1/policies/${policyId}/rollback`,
        {
          target_version_id: selectedRollbackVersion.version_id,
          comment: rollbackComment || `Monotonic rollback to historical v${selectedRollbackVersion.snapshot_version}`,
        },
        { ifMatch: currentETag }
      );
    },
    onSuccess: (data) => {
      setIsRollbackModalOpen(false);
      setSelectedRollbackVersion(null);
      setRollbackComment('');
      setSuccessBanner(
        `Rollback broadcast complete: republished historical snapshot under monotonic v${data?.snapshot_version}.`
      );
      queryClient.invalidateQueries({ queryKey: ['policy-versions', policyId] });
      queryClient.invalidateQueries({ queryKey: ['gateways'] });
    },
    onError: (err) => {
      if (err instanceof ApiError) {
        setRollbackError(err.message || 'Rollback execution failed.');
      } else {
        setRollbackError('Rollback execution failed due to network error.');
      }
    },
  });

  return (
    <div className="space-y-6">
      {/* Top Banner Notifications */}
      {successBanner && (
        <div className="flex items-center justify-between bg-emerald-500/10 border border-emerald-500/30 px-4 py-3 rounded-xl text-emerald-300 text-sm">
          <div className="flex items-center space-x-2">
            <CheckCircle2 className="w-5 h-5 shrink-0 text-emerald-400" />
            <span>{successBanner}</span>
          </div>
          <button
            onClick={() => setSuccessBanner(null)}
            className="text-emerald-400 hover:text-emerald-200"
            aria-label="Dismiss banner"
          >
            <X className="w-4 h-4" />
          </button>
        </div>
      )}

      {/* Header and Action Controls */}
      <div className="flex flex-col lg:flex-row lg:items-center justify-between gap-4 bg-slate-900 border border-slate-800 p-6 rounded-xl">
        <div className="space-y-1">
          <div className="flex items-center space-x-3">
            <FileCode className="w-6 h-6 text-cyan-400" />
            <h1 className="text-xl font-semibold text-slate-100">Policy Studio & Rego Authoring</h1>
          </div>
          <p className="text-sm text-slate-400">
            Author declarative authorization rules with real-time in-browser syntax highlighting and monotonic fleet publication
          </p>
        </div>

        <div className="flex items-center space-x-3">
          <button
            onClick={() => validateMutation.mutate()}
            disabled={validateMutation.isPending}
            className="flex items-center space-x-2 bg-slate-800 hover:bg-slate-700 text-slate-200 px-4 py-2 rounded-lg text-sm font-semibold transition disabled:opacity-50"
          >
            {validateMutation.isPending ? (
              <Loader2 className="w-4 h-4 animate-spin text-cyan-400" />
            ) : (
              <PlayCircle className="w-4 h-4 text-cyan-400" />
            )}
            <span>Validate Rego Syntax</span>
          </button>

          <button
            onClick={() => {
              setPublishError(null);
              setIsPublishModalOpen(true);
            }}
            disabled={!isSecOps}
            title={!isSecOps ? 'Publishing snapshots requires sec-ops role' : undefined}
            className="flex items-center space-x-2 bg-cyan-600 hover:bg-cyan-500 text-white px-4 py-2 rounded-lg text-sm font-semibold transition disabled:opacity-40 disabled:cursor-not-allowed"
          >
            <Upload className="w-4 h-4" />
            <span>Publish Policy Snapshot</span>
          </button>
        </div>
      </div>

      {/* Main Split Authoring Workspace */}
      <div className="grid grid-cols-1 lg:grid-cols-12 gap-6">
        {/* Left Pane (60% ~ 7 cols): Monaco Rego Editor */}
        <div className="lg:col-span-7 flex flex-col bg-slate-900 border border-slate-800 rounded-xl overflow-hidden shadow-lg">
          <div className="flex items-center justify-between px-4 py-3 bg-slate-950/80 border-b border-slate-800">
            <div className="flex items-center space-x-3">
              <span className="text-xs font-semibold uppercase tracking-wider text-slate-400">
                Rego v1 Policy Source
              </span>
              <span className="text-xs font-mono text-cyan-400 bg-cyan-500/10 px-2 py-0.5 rounded border border-cyan-500/20">
                package aegis.authz
              </span>
            </div>
            <div className="text-xs text-slate-500 font-mono">Monarch Tokenizer Active</div>
          </div>

          <div className="h-[560px] w-full">
            <Editor
              height="100%"
              defaultLanguage={regoLanguageId}
              language={regoLanguageId}
              theme="vs-dark"
              value={regoCode}
              onChange={handleCodeChange}
              beforeMount={handleEditorBeforeMount}
              options={{
                fontSize: 13,
                fontFamily: '"JetBrains Mono", "SF Mono", Menlo, Monaco, Consolas, monospace',
                tabSize: 2,
                minimap: { enabled: true },
                lineNumbers: 'on',
                scrollBeyondLastLine: false,
                wordWrap: 'on',
                automaticLayout: true,
                padding: { top: 12, bottom: 12 },
              }}
            />
          </div>
        </div>

        {/* Right Pane (40% ~ 5 cols): Validation Status & Version History */}
        <div className="lg:col-span-5 flex flex-col space-y-6">
          {/* Policy Metadata Card */}
          <div className="bg-slate-900 border border-slate-800 rounded-xl p-5 space-y-4">
            <h3 className="text-base font-semibold text-slate-100 flex items-center space-x-2">
              <Tag className="w-4 h-4 text-cyan-400" />
              <span>Policy Metadata</span>
            </h3>

            <div className="grid grid-cols-2 gap-4 text-sm">
              <div>
                <label className="block text-xs font-semibold text-slate-400 mb-1">Policy Identifier</label>
                <input
                  type="text"
                  value={policyId}
                  onChange={(e) => setPolicyId(e.target.value)}
                  className="w-full bg-slate-950 border border-slate-800 rounded-lg px-3 py-1.5 text-xs text-slate-200 font-mono focus:ring-1 focus:ring-cyan-500 focus:outline-none"
                />
              </div>
              <div>
                <label className="block text-xs font-semibold text-slate-400 mb-1">Policy Name</label>
                <input
                  type="text"
                  value={policyName}
                  onChange={(e) => setPolicyName(e.target.value)}
                  className="w-full bg-slate-950 border border-slate-800 rounded-lg px-3 py-1.5 text-xs text-slate-200 focus:ring-1 focus:ring-cyan-500 focus:outline-none"
                />
              </div>
            </div>

            <div className="flex items-center justify-between pt-2 border-t border-slate-800/80 text-xs text-slate-400">
              <span>Active Snapshot ETag:</span>
              <span className="font-mono text-cyan-400">{currentETag}</span>
            </div>
          </div>

          {/* Syntax Validation Card */}
          <div className="bg-slate-900 border border-slate-800 rounded-xl p-5 space-y-4">
            <div className="flex items-center justify-between">
              <h3 className="text-base font-semibold text-slate-100">Rego AST Validation</h3>
              {validationResult?.valid && (
                <span className="flex items-center space-x-1 text-xs font-semibold px-2 py-0.5 rounded-full bg-emerald-500/10 text-emerald-400 border border-emerald-500/30">
                  <CheckCircle2 className="w-3.5 h-3.5" />
                  <span>Valid Rego AST</span>
                </span>
              )}
              {validationResult && !validationResult.valid && (
                <span className="flex items-center space-x-1 text-xs font-semibold px-2 py-0.5 rounded-full bg-rose-500/10 text-rose-400 border border-rose-500/30">
                  <XCircle className="w-3.5 h-3.5" />
                  <span>Compilation Failed</span>
                </span>
              )}
            </div>

            {validationError ? (
              <div className="bg-rose-500/10 border border-rose-500/30 p-3 rounded-lg text-rose-300 text-xs space-y-1">
                <div className="font-semibold flex items-center space-x-1.5">
                  <AlertTriangle className="w-4 h-4 text-rose-400 shrink-0" />
                  <span>Syntax / Compilation Error Details</span>
                </div>
                <pre className="font-mono whitespace-pre-wrap text-[11px] mt-1 text-rose-200 overflow-x-auto">
                  {validationError}
                </pre>
              </div>
            ) : validationResult?.valid ? (
              <div className="bg-emerald-500/10 border border-emerald-500/20 p-3 rounded-lg text-emerald-300 text-xs space-y-1">
                <div className="font-semibold flex items-center space-x-1.5">
                  <CheckCircle2 className="w-4 h-4 text-emerald-400" />
                  <span>In-Memory OPA AST Checks Passed</span>
                </div>
                <p className="text-[11px] text-emerald-200/80">
                  {validationResult.test_count > 0
                    ? `Passed ${validationResult.passed_count} of ${validationResult.test_count} embedded unit tests.`
                    : 'Rego AST compiled cleanly with Rego v1 grammar specifications.'}
                </p>
              </div>
            ) : (
              <div className="text-xs text-slate-400 py-3 text-center border border-dashed border-slate-800 rounded-lg">
                Click "Validate Rego Syntax" to compile candidate AST and execute embedded policy tests.
              </div>
            )}
          </div>

          {/* Historical Snapshot Versions Card */}
          <div className="bg-slate-900 border border-slate-800 rounded-xl p-5 space-y-4 flex-1">
            <div className="flex items-center justify-between">
              <h3 className="text-base font-semibold text-slate-100 flex items-center space-x-2">
                <Clock className="w-4 h-4 text-cyan-400" />
                <span>Published Versions Log</span>
              </h3>
              <span className="text-xs font-mono text-slate-400">
                {versions.length} published
              </span>
            </div>

            {isLoadingVersions ? (
              <div className="flex justify-center py-8">
                <Loader2 className="w-6 h-6 animate-spin text-cyan-400" />
              </div>
            ) : versions.length === 0 ? (
              <div className="text-center py-6 border border-dashed border-slate-800 rounded-lg text-slate-500 text-xs">
                No Historical Versions Logged. Validate the Rego syntax and publish the initial version.
              </div>
            ) : (
              <div className="space-y-2.5 max-h-60 overflow-y-auto pr-1">
                {versions.map((ver) => (
                  <div
                    key={ver.version_id}
                    className="flex items-center justify-between p-3 bg-slate-950 border border-slate-800 rounded-lg hover:border-slate-700 transition"
                  >
                    <div className="space-y-1">
                      <div className="flex items-center space-x-2">
                        <span className="text-xs font-mono font-semibold px-2 py-0.5 rounded bg-cyan-500/10 text-cyan-400 border border-cyan-500/20">
                          v{ver.snapshot_version}
                        </span>
                        <span className="text-xs text-slate-300 font-medium truncate max-w-[120px]">
                          {ver.policy_id}
                        </span>
                      </div>
                      <div className="flex items-center space-x-3 text-[11px] text-slate-400">
                        <span className="flex items-center space-x-1">
                          <User className="w-3 h-3 text-slate-500" />
                          <span>{ver.published_by || 'sec-ops'}</span>
                        </span>
                        <span>{new Date(ver.published_at).toLocaleTimeString()}</span>
                      </div>
                    </div>

                    {isSecOps && ver.snapshot_version < latestVersion && (
                      <button
                        onClick={() => {
                          setSelectedRollbackVersion(ver);
                          setRollbackError(null);
                          setIsRollbackModalOpen(true);
                        }}
                        className="flex items-center space-x-1 text-xs text-rose-400 hover:text-rose-300 bg-rose-500/10 hover:bg-rose-500/20 px-2.5 py-1 rounded border border-rose-500/30 transition"
                      >
                        <RotateCcw className="w-3 h-3" />
                        <span>Rollback</span>
                      </button>
                    )}
                  </div>
                ))}
              </div>
            )}
          </div>
        </div>
      </div>

      {/* Monotonic Publish Confirmation Modal */}
      {isPublishModalOpen && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/80 backdrop-blur-md p-4">
          <div className="w-full max-w-lg bg-slate-900 border border-slate-800 rounded-xl p-6 shadow-2xl space-y-6">
            <div className="flex justify-between items-start">
              <div>
                <h3 className="text-lg font-semibold text-slate-100">
                  Monotonic Snapshot Publication
                </h3>
                <p className="text-xs text-slate-400 mt-1">
                  Enforces forward-only monotonic progression ($N \to N+1$) across connected gateway replicas
                </p>
              </div>
              <button
                onClick={() => setIsPublishModalOpen(false)}
                className="text-slate-400 hover:text-slate-200"
                aria-label="Close modal window"
              >
                <X className="w-5 h-5" />
              </button>
            </div>

            {/* Version Progression Badge */}
            <div className="flex items-center justify-between bg-slate-950 p-4 rounded-xl border border-slate-800 font-mono text-sm">
              <div className="flex flex-col">
                <span className="text-[10px] uppercase font-semibold text-slate-500 tracking-wider">
                  Current Version
                </span>
                <span className="text-base text-slate-300 font-bold">v{latestVersion}</span>
              </div>
              <div className="text-cyan-400 font-bold text-lg">→</div>
              <div className="flex flex-col text-right">
                <span className="text-[10px] uppercase font-semibold text-cyan-400 tracking-wider">
                  Next Monotonic Version
                </span>
                <span className="text-base text-cyan-300 font-bold">v{latestVersion + 1}</span>
              </div>
            </div>

            <div className="text-xs text-slate-400 space-y-2">
              <div className="flex justify-between">
                <span>Optimistic Concurrency ETag:</span>
                <span className="font-mono text-slate-200">{currentETag}</span>
              </div>
              <p className="text-slate-500 text-[11px]">
                Upon confirmation, this configuration is digitally signed with the control plane Ed25519 key and broadcast to all gateway replicas over gRPC snapshot streams.
              </p>
            </div>

            {publishError && (
              <div className="bg-rose-500/10 border border-rose-500/30 p-3 rounded-lg text-rose-300 text-xs">
                {publishError}
              </div>
            )}

            <div>
              <label className="block text-xs font-semibold text-slate-300 uppercase tracking-wider mb-1.5">
                Publication Intent / Comment (Optional)
              </label>
              <input
                type="text"
                value={publishComment}
                onChange={(e) => setPublishComment(e.target.value)}
                placeholder="e.g. Add strict route check for orders API"
                className="w-full bg-slate-950 border border-slate-800 rounded-lg px-3.5 py-2 text-sm text-slate-100 focus:ring-2 focus:ring-cyan-500 focus:outline-none"
              />
            </div>

            <div className="flex justify-end space-x-3 pt-2">
              <button
                onClick={() => setIsPublishModalOpen(false)}
                className="px-4 py-2 text-sm font-medium text-slate-400 hover:text-slate-200"
              >
                Dismiss
              </button>
              <button
                onClick={() => publishMutation.mutate()}
                disabled={publishMutation.isPending}
                className="flex items-center space-x-2 bg-cyan-600 hover:bg-cyan-500 text-white px-5 py-2 rounded-lg text-sm font-semibold transition disabled:opacity-50"
              >
                {publishMutation.isPending ? (
                  <>
                    <Loader2 className="w-4 h-4 animate-spin" />
                    <span>Broadcasting Snapshot...</span>
                  </>
                ) : (
                  <span>Publish Policy Snapshot</span>
                )}
              </button>
            </div>
          </div>
        </div>
      )}

      {/* Monotonic Rollback Confirmation Modal */}
      {isRollbackModalOpen && selectedRollbackVersion && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/80 backdrop-blur-md p-4">
          <div className="w-full max-w-lg bg-slate-900 border border-rose-500/40 rounded-xl p-6 shadow-2xl space-y-6">
            <div className="flex justify-between items-start">
              <div>
                <h3 className="text-lg font-semibold text-slate-100 flex items-center space-x-2 text-rose-400">
                  <AlertTriangle className="w-5 h-5 shrink-0" />
                  <span>Monotonic Policy Rollback Confirmation</span>
                </h3>
                <p className="text-xs text-slate-400 mt-1">
                  Revert to historical configuration from Snapshot v{selectedRollbackVersion.snapshot_version}
                </p>
              </div>
              <button
                onClick={() => setIsRollbackModalOpen(false)}
                className="text-slate-400 hover:text-slate-200"
                aria-label="Close modal window"
              >
                <X className="w-5 h-5" />
              </button>
            </div>

            <div className="bg-rose-500/10 border border-rose-500/30 p-3 rounded-lg text-rose-300 text-xs">
              <span className="font-semibold">Invariant 9 Monotonicity Guard:</span> To ensure replicas never reject decreasing versions, this action will NOT decrement the version number. Historical configuration will be signed and published as monotonic version{' '}
              <span className="font-mono font-bold text-rose-200">v{latestVersion + 1}</span>.
            </div>

            {rollbackError && (
              <div className="bg-rose-500/10 border border-rose-500/30 p-3 rounded-lg text-rose-300 text-xs">
                {rollbackError}
              </div>
            )}

            <div>
              <label className="block text-xs font-semibold text-slate-300 uppercase tracking-wider mb-1.5">
                Rollback Justification
              </label>
              <input
                type="text"
                value={rollbackComment}
                onChange={(e) => setRollbackComment(e.target.value)}
                placeholder="e.g. Incident response: bad role definition"
                className="w-full bg-slate-950 border border-slate-800 rounded-lg px-3.5 py-2 text-sm text-slate-100 focus:ring-2 focus:ring-rose-500 focus:outline-none"
              />
            </div>

            <div className="flex justify-end space-x-3 pt-2">
              <button
                onClick={() => setIsRollbackModalOpen(false)}
                className="px-4 py-2 text-sm font-medium text-slate-400 hover:text-slate-200"
              >
                Keep Active Policy
              </button>
              <button
                onClick={() => rollbackMutation.mutate()}
                disabled={rollbackMutation.isPending}
                className="flex items-center space-x-2 bg-rose-600 hover:bg-rose-500 text-white px-5 py-2 rounded-lg text-sm font-semibold transition disabled:opacity-50"
              >
                {rollbackMutation.isPending ? (
                  <>
                    <Loader2 className="w-4 h-4 animate-spin" />
                    <span>Rolling Back...</span>
                  </>
                ) : (
                  <span>Execute Version Rollback</span>
                )}
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
};
