# Security Policy

Aegis is an educational reference implementation of a zero-trust gateway. It has not undergone an independent security audit, and it is not intended to protect production systems as-is.

## Reporting a vulnerability

Please **do not** open a public issue for security problems.

Report privately through [GitHub Security Advisories](https://github.com/6sLOGAN78/Aegis/security/advisories/new). Include:

- the affected component and commit,
- steps to reproduce or a proof of concept,
- the impact you observed or expect.

You can expect an acknowledgement within 7 days. Fixes are developed in a private advisory and released with credit to the reporter unless you prefer otherwise.

## Supported versions

Only the latest commit on `main` receives fixes.

## Development key material is public

The repository intentionally commits a **development** PKI and signing keys (`deployments/certs/`, `deployments/keys/`) so the stack runs out of the box. Treat every one of those keys as compromised:

- never deploy them anywhere reachable by untrusted parties;
- generate your own with `go run ./scripts/certificates -out <dir> -keys-dir <dir>` (after removing the root CA files to mint a new CA), and provision them via `deployments/kubernetes/secrets/create-secrets.sh` with `AEGIS_CERTS_DIR` / `AEGIS_KEYS_DIR` pointing at your secure copies.

Demo passwords (`admin-secret`, `demo-*-pass`) are likewise for local use only.

## Known security limitations

Tracked and documented in the README's [Known limitations](README.md#known-limitations):

- Postgres connections are not TLS-encrypted.
- Quarantine write serialisation is per process; run a single control-plane replica.
- Kubernetes manifests have not been exercised on a live cluster.

The threat model and invariants are in [`docs/threat-model/threat-model.md`](docs/threat-model/threat-model.md); the latest audit findings are in [`docs/audit/security-audit-report.md`](docs/audit/security-audit-report.md).
