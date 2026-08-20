# AGENTS.md

## Repository

This module contains the official Go packages for ODP:

- Root package `odp`: transport-independent protocol primitives.
- `directory`: canonical directory client.
- `agent`: Agent-side composition.
- `service`: Service-side integration.

The normative protocol is maintained in `offering-protocol/odp-specs`. Check that source before
implementing or changing wire behavior.

## Verification

Check out `odp-specs` beside this repository or set `ODP_SPECS_DIR`, then run `make verify` before
merging. Use `make spec-update` to update bundled schemas and conformance vectors from that source.
Public APIs are the exported identifiers outside `internal` and must be backed by tests and
authoritative protocol behavior.

## Conventions

- Support Go 1.25 and newer; continuous integration covers Go 1.25 and 1.26.
- Use the standard library when it is sufficient and justify every additional dependency.
- Accept `context.Context` for operations that perform input/output or may block.
- Return errors rather than logging from library packages.
- Keep dependency direction aligned with the package responsibilities above.
- Keep implementation details under `internal` unless callers require them.
- Describe current behavior; do not leave speculative or historical comments.
- Keep public APIs small, idiomatic, and backed by tests and authoritative protocol behavior.
