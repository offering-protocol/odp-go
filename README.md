# Offering Discovery Protocol for Go

[![CI](https://github.com/offering-protocol/odp-go/actions/workflows/ci.yml/badge.svg)](https://github.com/offering-protocol/odp-go/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/offering-protocol/odp-go.svg)](https://pkg.go.dev/github.com/offering-protocol/odp-go)
[![Go](https://img.shields.io/badge/Go-1.25%2B-00ADD8?logo=go&logoColor=white)](https://go.dev/)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](./LICENSE)

Official Go software development kit for the
[Offering Discovery Protocol](https://www.offeringprotocol.org/), the open protocol for discovering
Services and navigating their Offerings.

ODP separates Service discovery from catalog discovery. An Agent searches the canonical directory
for candidate Services, inspects each Service's live ODP document, and then navigates or searches
that Service's Collections and Offerings.

## Module

```text
github.com/offering-protocol/odp-go
├── odp        Protocol models, validation, identity, references, and pagination
├── agent      Agent-oriented discovery and catalog navigation
├── directory  Canonical production and sandbox directory client
└── service    Service document, catalog operations, and integration helpers
```

The root import path uses package name `odp`.

```go
import (
	odp "github.com/offering-protocol/odp-go"
	"github.com/offering-protocol/odp-go/agent"
	"github.com/offering-protocol/odp-go/directory"
	"github.com/offering-protocol/odp-go/service"
)
```

Role packages depend toward the root `odp` package; `agent` composes `directory`. The root package
does not depend on role packages, and `service` does not depend on Agent or directory behavior.

## Protocol core

The root package validates wire documents against the exact schemas published by `odp-specs`, then
decodes them into typed Go values. JSON members permitted by the protocol's additive evolution rules
are preserved in `Additional` and survive a marshal round trip.

```go
document, err := odp.ParseServiceDocument(body)
if err != nil {
	var validation *odp.ValidationError
	if errors.As(err, &validation) {
		log.Printf("invalid Service Document: %+v", validation.Issues)
	}
	return err
}

origin, err := odp.DeriveServiceOrigin(serviceDocumentURL)
if err != nil {
	return err
}

offeringURL, err := odp.BuildOperationURL(
	document.HTTP.EndpointBase,
	odp.OperationGetOffering,
	origin,
	"gpu-a100",
)
```

Pagination uses Go iterators, carries cancellation through `context.Context`, rejects continuation
loops, and enforces the protocol's 16-page traversal limit.

```go
for offering, err := range odp.IterateItems(ctx, firstPage, loadPage) {
	if err != nil {
		return err
	}
	consume(offering)
}
```

Collection search distinguishes an omitted hierarchy constraint, a root-Collection constraint, and
a specific parent:

```go
unconstrained := odp.CollectionSearchRequest{ODPVersion: odp.Version, Query: "desk"}
roots := odp.CollectionSearchRequest{ODPVersion: odp.Version, ParentID: odp.Null[string]()}
children := odp.CollectionSearchRequest{ODPVersion: odp.Version, ParentID: odp.Some("office")}
```

## Directory discovery

Package `directory` searches candidate Services through the canonical production directory or its
fixed sandbox environment. It validates cached Service summaries, follows opaque same-origin
continuations, exposes structured facets, and provides keyword suggestions.

```go
directoryClient, err := directory.New(directory.Options{})
if err != nil {
	return err
}

for candidate, err := range directoryClient.SearchServices(ctx, directory.SearchRequest{
	Filters: &directory.ServiceFilters{
		Keywords: []string{"gpu"},
		Payments: []odp.Protocol{odp.ProtocolMPP},
	},
}, directory.IterationOptions{MaxItems: 20}) {
	if err != nil {
		return err
	}
	inspect(candidate.ServiceOrigin)
}
```

See the [directory package guide](./directory/README.md) for page traversal, suggestions, and
sandbox usage.

## Agent integration

Package `agent` inspects live Service Documents and provides validated, lazy Collection and Offering
navigation. Its federated discovery client searches candidate Services through `directory`, queries
their catalogs with bounded concurrency, and emits results in directory order.

Run the small Service and Agent examples in separate terminals:

```sh
go run ./examples/odp-service-small
go run ./examples/odp-agent-discovery
```

The Agent example uses a clearly labeled mock directory and performs live inspection, listing, and
full Offering retrieval against every reachable configured Service. See the
[Agent package guide](./agent/README.md) for caching, transport composition, and API usage.

## Service integration

Package `service` implements the ODP HTTP runtime for Go's standard `net/http` stack. Small Services
can use its validated static catalog; large Services provide storage-backed operation functions.
Optional functions directly control the operations advertised by the Service Document.

Run the complete small-Service example with:

```sh
go run ./examples/odp-service-small
```

See the [Service package guide](./service/README.md) and
[runnable example](./examples/odp-service-small/README.md).

## Development

Go 1.25 or newer is required. Run the complete merge gate with:

```sh
make verify
```

When an `odp-specs` checkout is available, verify the bundled schemas and conformance vectors with
`ODP_SPECS_DIR=/path/to/odp-specs make spec-sync`. Continuous integration runs both gates.

See [DEVELOPMENT.md](./DEVELOPMENT.md) for the contributor workflow and
[`odp-specs`](https://github.com/offering-protocol/odp-specs) for the normative draft, schemas,
examples, and test vectors.

## Security

See [SECURITY.md](./SECURITY.md) for vulnerability reporting.

## License

MIT.
