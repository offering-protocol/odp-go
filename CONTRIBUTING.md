# Contributing

Contributions are welcome through GitHub issues and pull requests.

Before opening a pull request:

1. Confirm protocol behavior against `offering-protocol/odp-specs`.
2. Keep public APIs idiomatic, minimal, and supported by tests.
3. Check out `odp-specs` beside this repository or set `ODP_SPECS_DIR`, then run `make verify`.
4. Update integration documentation when caller-visible behavior changes.

Use focused commits and describe the current behavior rather than its history. Security reports
must follow [SECURITY.md](./SECURITY.md).
