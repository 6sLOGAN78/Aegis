import React, { useEffect, useRef, useState } from 'react';
import '../utils/monaco-setup';
import Editor, { type BeforeMount, type OnMount } from '@monaco-editor/react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { CheckCircle2, XCircle, AlertTriangle, RotateCcw, Upload, PlayCircle, Loader2, X } from 'lucide-react';
import { PageHeader } from './PageHeader';
import { Dialog } from './Dialog';
import { apiClient, ApiError } from '../api/client';
import { newIdempotencyKey } from '../api/idempotency';
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

export const DEFAULT_POLICY_ID = 'mvp-authz';

interface PolicyStudioProps {
  session: SessionInfo | null;
  // Candidate Rego held by the parent; survives tab switches (PolicyStudio unmounts).
  candidateRego?: string;
  onCandidateRegoChange?: (rego: string) => void;
  // Snapshot version the current edit session started from. Publish sends
  // If-Match for THIS version (not the live latest) so a concurrent publish by
  // another operator yields 412 instead of being silently overwritten.
  baseVersion?: number;
  onBaseVersionChange?: (version: number | undefined) => void;
}

export const PolicyStudio: React.FC<PolicyStudioProps> = ({
  session,
  candidateRego,
  onCandidateRegoChange,
  baseVersion: baseVersionProp,
  onBaseVersionChange,
}) => {
  const queryClient = useQueryClient();
  const [policyId, setPolicyId] = useState(DEFAULT_POLICY_ID);
  const [policyName, setPolicyName] = useState('Zero-Trust Gateway Ingress Policy');
  // Seed from the parent's candidate so a remount (tab switch) never discards unsaved edits.
  const [regoCode, setRegoCode] = useState<string>(candidateRego ?? DEFAULT_REGO_TEMPLATE);
  // Edit-session base version; mirrored to the parent so it survives remounts.
  const [baseVersion, setBaseVersionState] = useState<number | undefined>(baseVersionProp);
  const setBaseVersion = (v: number | undefined) => {
    setBaseVersionState(v);
    if (onBaseVersionChange) onBaseVersionChange(v);
  };
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
  // Version the rollback dialog was opened against (If-Match for rollback).
  const [rollbackBaseVersion, setRollbackBaseVersion] = useState<number | null>(null);

  const isSecOps = session?.role === 'sec-ops';

  // Fetch version history for this policy
  const { data: versionsData, isLoading: isLoadingVersions } = useQuery<PolicyVersionListResponse>({
    queryKey: ['policy-versions', policyId],
    queryFn: () => apiClient.get<PolicyVersionListResponse>(`/control/v1/policies/${policyId}/versions`),
    refetchInterval: 10000,
  });

  const versions = versionsData?.versions || [];
  const latestVersion = versions.length > 0 ? versions[0].snapshot_version : 1;
  // Live latest ETag (display only; refreshed by the 10s refetch).
  // ETag sent on publish: the version this edit session is based on. Falls back
  // to the live value only before any versions have loaded.
  const editBaseVersion = baseVersion ?? latestVersion;
  const editETag = `"${editBaseVersion}"`;
  const hasNewerVersion = baseVersion !== undefined && latestVersion > baseVersion;
  // Dirty means the editor differs from the published source it was based on.
  const baseSource = versionsData?.versions?.find((v) => v.snapshot_version === editBaseVersion)?.source_rego;
  const isDirty = baseSource !== undefined && regoCode !== baseSource;

  const handleEditorBeforeMount: BeforeMount = (monaco) => {
    registerRegoLanguage(monaco);
    // Editor theme drawn from the console palette (index.css @theme).
    monaco.editor.defineTheme('aegis-signage', {
      base: 'vs',
      inherit: true,
      rules: [
        { token: 'comment', foreground: '6b7079', fontStyle: 'italic' },
        { token: 'keyword', foreground: '0039a6' },
        { token: 'string', foreground: '996633' },
        { token: 'string.raw', foreground: '996633' },
        { token: 'number', foreground: 'b933ad' },
        { token: 'operator', foreground: '4b4f56' },
        { token: 'identifier', foreground: '111214' },
      ],
      colors: {
        'editor.background': '#ffffff',
        'editor.foreground': '#111214',
        'editorLineNumber.foreground': '#9aa0a8',
        'editorLineNumber.activeForeground': '#111214',
        'editor.lineHighlightBackground': '#f3f4f5',
        'editor.lineHighlightBorder': '#00000000',
        'editor.selectionBackground': '#0039a629',
        'editorCursor.foreground': '#111214',
        'editorIndentGuide.background1': '#eef0f2',
        'editorWidget.background': '#ffffff',
        'editorWidget.border': '#d5d7da',
        'scrollbarSlider.background': '#c4c8cd99',
        'scrollbarSlider.hoverBackground': '#9aa0a8',
      },
    });
  };

  // Hydrate the editor from the latest stored policy source, once per policy ID.
  // If the parent already holds a candidate (remount after a tab switch), treat it
  // as unsaved operator work and never overwrite it with the server copy.
  // Changing the Policy Identifier re-hydrates for the new ID unless the operator
  // has unsaved edits. Note: the versions endpoint currently returns the global
  // snapshot history (snapshots are fleet-wide), so list[0] is the latest
  // published snapshot source rather than a per-policy record.
  // The editor covers most of the viewport. It takes the mouse wheel only while focused
  // (click into it); otherwise the wheel scrolls the page, so the page never gets stuck.
  const handleEditorMount: OnMount = (editor) => {
    const setWheel = (handle: boolean) => editor.updateOptions({ scrollbar: { handleMouseWheel: handle, alwaysConsumeMouseWheel: false } });
    setWheel(false);
    editor.onDidFocusEditorWidget(() => setWheel(true));
    editor.onDidBlurEditorWidget(() => setWheel(false));
  };
  const hydratedForRef = useRef<string | null>(null);
  const userEditedRef = useRef(candidateRego !== undefined);
  useEffect(() => {
    const list = versionsData?.versions;
    if (!list) return;
    if (hydratedForRef.current === policyId || userEditedRef.current) {
      // An edit session that started before versions loaded gets its base
      // pinned to the first version list it sees, never bumped by later refetches.
      if (baseVersion === undefined) {
        setBaseVersion(list.length > 0 ? list[0].snapshot_version : 1);
      }
      return;
    }
    if (list.length === 0) return;
    hydratedForRef.current = policyId;
    const source = list[0].source_rego;
    if (source) {
      setRegoCode(source);
      if (onCandidateRegoChange) onCandidateRegoChange(source);
      setBaseVersion(list[0].snapshot_version);
    }
    // baseVersion/setBaseVersion intentionally omitted: only versions/policyId drive hydration.
  }, [policyId, versionsData, onCandidateRegoChange]);

  // Explicitly discard local edits and reload the latest stored source.
  const handleReloadLatest = () => {
    const list = versionsData?.versions;
    if (!list || list.length === 0) return;
    userEditedRef.current = false;
    hydratedForRef.current = policyId;
    const source = list[0].source_rego || DEFAULT_REGO_TEMPLATE;
    setRegoCode(source);
    if (onCandidateRegoChange) onCandidateRegoChange(source);
    setBaseVersion(list[0].snapshot_version);
  };

  const handleCodeChange = (value?: string) => {
    if (!userEditedRef.current && baseVersion === undefined && versionsData) {
      // First edit on an unhydrated (template) editor: pin the base now.
      setBaseVersion(latestVersion);
    }
    userEditedRef.current = true;
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

  // Fresh Idempotency-Key per publish attempt; re-minted after every outcome
  const publishKeyRef = useRef<string | null>(null);
  if (publishKeyRef.current === null) publishKeyRef.current = newIdempotencyKey();

  // Publish Mutation
  const publishMutation = useMutation({
    mutationFn: async () => {
      setPublishError(null);
      // Save current editor content as draft first so published bytes match the editor
      await apiClient.post(
        '/control/v1/policies',
        { policy_id: policyId, name: 'authz.rego', source_rego: regoCode },
        { idempotencyKey: newIdempotencyKey() }
      );
      return apiClient.post<PolicyPublishResponse>(
        `/control/v1/policies/${policyId}/publish`,
        { comment: publishComment || 'Broadcast via Policy Studio console' },
        { ifMatch: editETag, idempotencyKey: publishKeyRef.current ?? newIdempotencyKey() }
      );
    },
    onSuccess: (data) => {
      publishKeyRef.current = newIdempotencyKey();
      // Editor content is now the published snapshot; rebase the session on it.
      setBaseVersion(data.snapshot_version);
      setIsPublishModalOpen(false);
      setPublishComment('');
      setSuccessBanner(
        `Snapshot v${data.snapshot_version} successfully signed with Ed25519 and broadcast to fleet.`
      );
      queryClient.invalidateQueries({ queryKey: ['policy-versions', policyId] });
      queryClient.invalidateQueries({ queryKey: ['gateways'] });
    },
    onError: (err) => {
      publishKeyRef.current = newIdempotencyKey();
      if (err instanceof ApiError) {
        if (err.status === 412) {
          setPublishError(
            'Publication conflict: ETag mismatch. Another operator published a newer version since your edit began. Review it (Load latest) before republishing.'
          );
        } else if (err.status === 422) {
          setPublishError(err.message || 'Policy failed validation (422).');
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
        { ifMatch: `"${rollbackBaseVersion ?? latestVersion}"`, idempotencyKey: newIdempotencyKey() }
      );
    },
    onSuccess: (data) => {
      setIsRollbackModalOpen(false);
      setSelectedRollbackVersion(null);
      setRollbackBaseVersion(null);
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
    <div>
      <PageHeader
        title="Policy"
        description="Edit the Rego policy every gateway enforces. Publishing signs a new snapshot and streams it to all gateways."
        meta={<span className="badge num">Live v{latestVersion}</span>}
        actions={
          <>
            <button
              onClick={() => validateMutation.mutate()}
              disabled={validateMutation.isPending}
              className="btn btn-secondary"
            >
              {validateMutation.isPending ? <Loader2 className="animate-spin" /> : <PlayCircle />}
              Validate
            </button>
            <button
              onClick={() => {
                setPublishError(null);
                setIsPublishModalOpen(true);
              }}
              disabled={!isSecOps}
              title={!isSecOps ? 'Publishing needs the sec-ops role' : undefined}
              className="btn btn-primary"
            >
              <Upload />
              Publish
            </button>
          </>
        }
      />

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

      {hasNewerVersion && (
        <div role="alert" className="mb-4 panel px-4 py-3 flex flex-wrap items-center justify-between gap-3 text-sm">
          <span className="flex items-center gap-2 text-amber-300">
            <AlertTriangle className="w-4 h-4 shrink-0" />
            v{latestVersion} was published after you started editing v{baseVersion}. Load it before publishing.
          </span>
          <button onClick={handleReloadLatest} className="btn btn-secondary btn-sm">Load v{latestVersion}</button>
        </div>
      )}

      <div className="grid grid-cols-1 lg:grid-cols-12 gap-6 items-start">
        <section className="lg:col-span-8 panel overflow-hidden">
          <div className="panel-head">
            <h2 className="panel-title">
              authz.rego <span className="ml-2 font-mono text-xs font-normal text-slate-500">package aegis.authz</span>
            </h2>
            {isDirty && <span className="badge badge-warn">Unpublished changes</span>}
          </div>
          <div className="h-[min(600px,65vh)] min-h-80 w-full">
            <Editor
              height="100%"
              defaultLanguage={regoLanguageId}
              language={regoLanguageId}
              theme="aegis-signage"
              value={regoCode}
              onChange={handleCodeChange}
              beforeMount={handleEditorBeforeMount}
              onMount={handleEditorMount}
              options={{
                fontSize: 13,
                lineHeight: 20,
                fontFamily: '"JetBrains Mono", "SF Mono", Menlo, Monaco, Consolas, monospace',
                tabSize: 2,
                minimap: { enabled: false },
                lineNumbers: 'on',
                renderLineHighlight: 'line',
                scrollBeyondLastLine: false,
                wordWrap: 'on',
                automaticLayout: true,
                padding: { top: 12, bottom: 12 },
                overviewRulerLanes: 0,
                hideCursorInOverviewRuler: true,
                stickyScroll: { enabled: false },
              }}
            />
          </div>
        </section>

        <div className="lg:col-span-4 space-y-6">
          <section className="panel" aria-live="polite">
            <div className="panel-head">
              <h2 className="panel-title">Validation</h2>
              {validationResult?.valid && <span className="badge badge-ok"><CheckCircle2 className="w-3 h-3" />Compiles</span>}
              {validationResult && !validationResult.valid && (
                <span className="badge badge-bad"><XCircle className="w-3 h-3" />Fails</span>
              )}
            </div>
            <div className="p-5 text-sm">
              {validationError ? (
                <pre className="font-mono text-xs whitespace-pre-wrap text-rose-600 overflow-x-auto">{validationError}</pre>
              ) : validationResult?.valid ? (
                <p className="text-slate-300">
                  {validationResult.test_count > 0
                    ? `${validationResult.passed_count} of ${validationResult.test_count} policy tests passed.`
                    : 'The policy compiles. It has no embedded tests.'}
                </p>
              ) : (
                <p className="text-slate-500">Validate to compile the draft and run its policy tests.</p>
              )}
            </div>
          </section>

          <section className="panel">
            <div className="panel-head">
              <h2 className="panel-title">Details</h2>
            </div>
            <div className="p-5 space-y-4">
              <div>
                <label htmlFor="policy-id" className="label">Policy ID</label>
                <input
                  id="policy-id"
                  type="text"
                  value={policyId}
                  onChange={(e) => setPolicyId(e.target.value)}
                  className="field font-mono text-[13px]"
                />
              </div>
              <div>
                <label htmlFor="policy-name" className="label">Name</label>
                <input
                  id="policy-name"
                  type="text"
                  value={policyName}
                  onChange={(e) => setPolicyName(e.target.value)}
                  className="field"
                />
              </div>
              <p className="text-xs text-slate-500 num">Editing from v{editBaseVersion}.</p>
            </div>
          </section>

          <section className="panel overflow-hidden">
            <div className="panel-head">
              <h2 className="panel-title">
                History <span className="ml-1 text-slate-500 num">{versions.length}</span>
              </h2>
            </div>
            {isLoadingVersions ? (
              <div className="divide-y divide-mist" aria-busy="true" aria-label="Loading versions">
                {[1, 2].map((n) => (
                  <div key={n} className="px-5 h-14 flex items-center animate-pulse">
                    <div className="h-3.5 w-40 bg-linen rounded" />
                  </div>
                ))}
              </div>
            ) : versions.length === 0 ? (
              <p className="px-5 py-8 text-sm text-slate-500 text-center">Nothing published yet.</p>
            ) : (
              <ol className="divide-y divide-mist max-h-72 overflow-y-auto">
                {versions.map((ver) => (
                  <li key={ver.version_id} className="flex items-center justify-between gap-3 px-5 py-3">
                    <div className="min-w-0">
                      <div className="flex items-center gap-2 text-sm">
                        <span className="font-medium text-slate-100 num">v{ver.snapshot_version}</span>
                        {ver.snapshot_version === latestVersion && <span className="badge badge-ok">Live</span>}
                      </div>
                      <p className="mt-0.5 text-xs text-slate-500 truncate">
                        {ver.published_by || 'sec-ops'} ·{' '}
                        <time dateTime={ver.published_at} className="num">
                          {new Date(ver.published_at).toLocaleString(undefined, {
                            month: 'short',
                            day: 'numeric',
                            hour: '2-digit',
                            minute: '2-digit',
                          })}
                        </time>
                      </p>
                    </div>
                    {isSecOps && ver.snapshot_version < latestVersion && (
                      <button
                        onClick={() => {
                          setSelectedRollbackVersion(ver);
                          setRollbackBaseVersion(latestVersion);
                          setRollbackError(null);
                          setIsRollbackModalOpen(true);
                        }}
                        aria-label={`Roll back to v${ver.snapshot_version}`}
                        className="btn btn-secondary btn-sm"
                      >
                        <RotateCcw />
                        Roll back
                      </button>
                    )}
                  </li>
                ))}
              </ol>
            )}
          </section>
        </div>
      </div>

      {isPublishModalOpen && (
        <Dialog title="Publish policy" onClose={() => setIsPublishModalOpen(false)}>
          <p className="text-sm text-slate-400">
            Publishes <strong className="font-medium text-slate-100 num">v{editBaseVersion + 1}</strong>, replacing
            v{editBaseVersion} on every gateway. The snapshot is signed with the control-plane key before it is sent.
          </p>
          {publishError && (
            <p role="alert" className="mt-4 text-sm text-rose-600">{publishError}</p>
          )}
          <div className="mt-5">
            <label htmlFor="publish-comment" className="label">
              Change note <span className="font-normal text-slate-500">(optional)</span>
            </label>
            <input
              id="publish-comment"
              type="text"
              value={publishComment}
              onChange={(e) => setPublishComment(e.target.value)}
              placeholder="Require GET for the orders read rule"
              className="field"
            />
          </div>
          <div className="mt-6 flex justify-end gap-2">
            <button onClick={() => setIsPublishModalOpen(false)} className="btn btn-ghost">Cancel</button>
            <button onClick={() => publishMutation.mutate()} disabled={publishMutation.isPending} className="btn btn-primary">
              {publishMutation.isPending && <Loader2 className="animate-spin" />}
              {publishMutation.isPending ? 'Publishing…' : `Publish v${editBaseVersion + 1}`}
            </button>
          </div>
        </Dialog>
      )}

      {isRollbackModalOpen && selectedRollbackVersion && (
        <Dialog
          title={`Roll back to v${selectedRollbackVersion.snapshot_version}`}
          onClose={() => setIsRollbackModalOpen(false)}
        >
          <p className="text-sm text-slate-400">
            The v{selectedRollbackVersion.snapshot_version} policy is republished as{' '}
            <strong className="font-medium text-slate-100 num">v{(rollbackBaseVersion ?? latestVersion) + 1}</strong>.
            Version numbers only move forward, so gateways never see a lower version.
          </p>
          {rollbackError && <p role="alert" className="mt-4 text-sm text-rose-600">{rollbackError}</p>}
          <div className="mt-5">
            <label htmlFor="rollback-comment" className="label">Reason</label>
            <input
              id="rollback-comment"
              type="text"
              value={rollbackComment}
              onChange={(e) => setRollbackComment(e.target.value)}
              placeholder="v3 denied finance users on payments"
              className="field"
            />
          </div>
          <div className="mt-6 flex justify-end gap-2">
            <button onClick={() => setIsRollbackModalOpen(false)} className="btn btn-ghost">Cancel</button>
            <button onClick={() => rollbackMutation.mutate()} disabled={rollbackMutation.isPending} className="btn btn-danger">
              {rollbackMutation.isPending && <Loader2 className="animate-spin" />}
              {rollbackMutation.isPending ? 'Rolling back…' : 'Roll back'}
            </button>
          </div>
        </Dialog>
      )}
    </div>
  );
};
