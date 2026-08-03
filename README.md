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

## Development

Go 1.25 or newer is required. Run the complete merge gate with:

```sh
make verify
```

See [DEVELOPMENT.md](./DEVELOPMENT.md) for the contributor workflow and
[`odp-specs`](https://github.com/offering-protocol/odp-specs) for the normative draft, schemas,
examples, and test vectors.

## Security

See [SECURITY.md](./SECURITY.md) for vulnerability reporting.

## License

MIT.
