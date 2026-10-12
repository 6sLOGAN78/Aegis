# Contributing to Aegis

Thanks for your interest in improving Aegis. This project is a security reference system, so changes are held to a simple bar: **every behaviour change is tested, and security paths are tested negatively** (the request that must be refused, not only the one that must succeed).

## Getting set up

1. Install the prerequisites listed in the [README](README.md#prerequisites).
2. Fork and clone the repository, then confirm the baseline is green:

   ```bash
   go build ./... && go vet ./...
   go test -race -count=1 -p 1 ./...
   opa test policies/rego policies/tests
   ```

3. Bring up a profile to try changes end to end: `make distributed-up`, then `make compose-smoke PROFILE=distributed`.

## Workflow

- Branch from `main`: `feat/<topic>`, `fix/<topic>`, or `docs/<topic>`.
- Keep pull requests focused — one logical change per PR.
- Use [Conventional Commits](https://www.conventionalcommits.org/): `feat(gateway): ...`, `fix(control): ...`, `docs(runbooks): ...`, `test(rotation): ...`.
- Open the PR against `main` and fill in the template. CI must pass.

## Coding standards

- **Go:** `gofmt` and `go vet` clean. Return errors with context (`fmt.Errorf("...: %w", err)`); never swallow them on a security path.
- **Fail closed:** when a dependency needed for an authorization decision is unavailable (Redis, the policy snapshot, the audit spool, the security-state marker), the answer is a structured `503`, never "allow".
- **Generated code:** files under `pkg/api` come from `scripts/generate.sh`. Edit `api/proto` or `api/openapi/control-v1.yaml`, then regenerate; never edit generated files by hand.
- **Policy:** Rego changes in `policies/rego` need matching tests in `policies/tests`.
- **Config:** a new environment variable must be added to the compose profiles and Kubernetes manifests in the same change — `tests/compose` and `tests/manifests` derive the expected keys from the Go source and will fail otherwise.
- **Secrets:** never commit real credentials. Dev material lives only in `deployments/certs` and `deployments/keys`.

## Tests

| Change touches | Run at least |
|---|---|
| Any Go package | `go test -race ./<pkg>/...` |
| Gateway request path, identity, revocation | `make test-security` |
| Control plane, snapshots, gRPC | `go test -race ./internal/control/... ./tests/chaos/... ./tests/integration/...` |
| Compose or Kubernetes files | `make compose-test manifest-test` |
| Runtime behaviour across services | `make distributed-up && make compose-smoke PROFILE=distributed` |

## Documentation

Update the README, runbooks, or an ADR when you change behaviour operators rely on. Significant design changes get a new ADR in `docs/adr/` (copy the format of the existing ones).

## Reporting bugs and requesting features

Use the GitHub issue templates. For security vulnerabilities, follow [SECURITY.md](SECURITY.md) instead — do not open a public issue.

## Code of conduct

Be respectful and constructive. This project follows the [Contributor Covenant 2.1](https://www.contributor-covenant.org/version/2/1/code_of_conduct/).
