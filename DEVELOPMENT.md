# Development

## Requirements

- Go 1.25 or newer.
- A sibling `odp-specs` checkout for shared conformance work.

## Verification

Run the complete repository gate before merging:

```sh
make verify
```

The gate checks Go formatting, module-file drift, static analysis, race detection, tests, and
coverage generation. Continuous integration runs it on Go 1.25 and 1.26.

Use standard Go commands while iterating:

```sh
go test ./...
go test -race ./agent/...
go vet ./...
```

## Package boundaries

The root `odp` package owns transport-independent protocol behavior. `agent`, `directory`, and
`service` are role packages. Shared implementation details that are not public API belong under
`internal` when they are introduced.

The normative protocol is maintained in `offering-protocol/odp-specs`. Confirm schema, wire, and
conformance behavior there before implementing or changing it in Go.

## Releases

Stable Go module releases use semantic-version tags such as `v0.1.0` and matching GitHub releases.
The release gate includes shared conformance, Node.js interoperability, and clean external module
consumption before a tag is created.
