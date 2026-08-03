# ODP Agent discovery

This example uses the Go Agent package to inspect live ODP Services, list terse Offerings, and fetch
each Offering's full details.

The example composes a **mock directory** from the reachable Service origins supplied on the command
line. The mock is isolated in `mock_directory.go`; it is not the canonical ODP directory and does
not imitate directory ranking, filtering, facets, or suggestions.

Start the small Service in one terminal:

```sh
go run ./examples/odp-service-small
```

Run the Agent in another:

```sh
go run ./examples/odp-agent-discovery
```

The Agent probes `http://localhost:4101` by default. Supply one or more Service origins to query a
different set:

```sh
go run ./examples/odp-agent-discovery \
  -service http://localhost:4101 \
  -service http://localhost:4102
```

Unreachable candidates are omitted before the mock directory is created. The output identifies the
Service, prints its validated ODP Service Document, prints each terse Offering returned by catalog
navigation, and then prints the corresponding full Offering response.
