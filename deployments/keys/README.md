# Dev signing keys

Dev/demo keys only. Never reuse them outside local compose or demo clusters; production keys come from Secrets.

Layout:

- `jwt-current.key` and `snapshot-current.key` are private Ed25519 signer keys (base64, 0600). Mount them only into the demo issuer (JWT) and the control plane (snapshot).
- `keyset-jwt/` and `keyset-snapshot/` hold public keys (`current.pub`) and are mounted into gateways as directories.

Rotation drill: generate a fresh pair in a temp location with
`go run ./scripts/certificates -keypair <prefix>` (writes `<prefix>.key` and `<prefix>.pub`), then copy the `.pub` into the keyset directory under a new name.
