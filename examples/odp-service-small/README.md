# Small ODP Service

This example exposes the required ODP Service baseline backed by an in-memory catalog. It publishes
a Service Document, one Collection, and one free digital Offering with a working `download` Action.

Run it from the repository root:

```sh
go run ./examples/odp-service-small
```

The default address is `localhost:4101`. Use `-addr` when another address is needed:

```sh
go run ./examples/odp-service-small -addr localhost:4201
```

Inspect the same discovery sequence an Agent follows:

```sh
curl -H 'Accept: application/odp+json' http://localhost:4101/.well-known/odp
curl -H 'Accept: application/odp+json' http://localhost:4101/odp/offerings
curl -H 'Accept: application/odp+json' http://localhost:4101/odp/offerings/agent-guide
curl http://localhost:4101/downloads/agent-guide.txt
```

The list response is terse and omits the Action. Retrieving `agent-guide` returns the Full Offering
with the Action target. The final request executes that advertised Action.
