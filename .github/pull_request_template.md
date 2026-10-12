## Summary

<!-- What does this change and why? Link the issue if there is one. -->

## Type of change

- [ ] Bug fix
- [ ] New feature
- [ ] Security hardening
- [ ] Documentation / tooling

## Checklist

- [ ] `go build ./... && go vet ./...` pass
- [ ] Tests added or updated (including negative tests for security paths)
- [ ] `go test -race -count=1 -p 1 ./...` and `opa test policies/rego policies/tests` pass
- [ ] New env vars are reflected in compose profiles and Kubernetes manifests
- [ ] Fails closed on dependency loss where authorization depends on it
- [ ] Docs / runbooks / ADR updated if operator-visible behaviour changed
- [ ] No real secrets committed

## How was this tested?

<!-- Commands run, profiles exercised, screenshots for console changes. -->
