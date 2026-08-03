# ODP Agent package

Package `agent` composes the Agent side of ODP. It inspects live Service Documents, navigates one
Service's catalog, and performs bounded discovery across Services returned by the canonical
directory.

## Inspect a Service

```go
client, err := agent.NewServiceClient(agent.ServiceClientOptions{
	ServiceURL: "https://compute.example",
})
if err != nil {
	return err
}

inspection, err := client.Inspect(ctx)
if err != nil {
	return err
}

inspection.Capabilities.Operations
inspection.Capabilities.Onboarding
inspection.Capabilities.Payments
```

The client validates `/.well-known/odp`, accepts at most five same-origin redirects, and applies a
four-hour fallback freshness when the Service does not publish HTTP cache metadata. Supply a
`Cache` for persistent storage; otherwise each client owns an in-memory cache.

## Navigate a catalog

List and search methods return lazy Go iterators. Iteration stops network activity when the caller
stops consuming results.

```go
for offering, err := range client.ListOfferings(ctx, agent.ListOptions{MaxItems: 20}) {
	if err != nil {
		return err
	}
	consume(offering)
}

for offering, err := range client.SearchOfferings(ctx, agent.OfferingSearchOptions{
	Query:    "gpu",
	MaxItems: 10,
}) {
	if err != nil {
		return err
	}
	consume(offering)
}

details, err := client.GetOffering(ctx, "gpu-h100", odp.RepresentationFull)
```

The same client lists, retrieves, and searches Collections and lists the direct Offerings in a
Collection. Page methods expose continuations and search refinements when page-level metadata is
needed.

Service Documents use a four-hour fallback freshness, Collections use one hour, and Offerings use
five minutes. Service-provided `Cache-Control` or `Expires` metadata takes precedence. Search
responses are cached only when the Service supplies explicit freshness metadata. Supplying a custom
HTTP client disables catalog caching unless `CachePartition` identifies its stable access context;
this prevents authenticated or payment-dependent representations from sharing a public cache key.

`RequestError` preserves ODP Problem Details and response headers, exposes a stable error code, and
marks rate-limit and server failures as retryable so application transports can compose AEP, MPP,
and x402 challenges.

## Discover Offerings across Services

`Agent` searches Services through the canonical directory, then queries each selected Service with
bounded concurrency. Events remain in directory order. A failed Service produces an issue event
without discarding successful results from other Services.

```go
odpAgent, err := agent.New(agent.AgentOptions{Environment: directory.Sandbox})
if err != nil {
	return err
}

request := agent.FederatedSearchRequest{
	Services: directory.SearchRequest{
		Filters: &directory.ServiceFilters{Keywords: []string{"gpu"}},
	},
	Offerings: agent.OfferingSearchOptions{Query: "accelerator"},
}

for event, err := range odpAgent.SearchOfferingsAcrossServices(ctx, request) {
	if err != nil {
		return err
	}
	switch event.Type {
	case agent.DiscoveryOffering:
		consume(event.Service, *event.Offering)
	case agent.DiscoveryIssue:
		report(event.Service, event.Err)
	}
}
```

The defaults search at most 10 Services, retain at most 10 terse Offerings from each, and run four
Service requests concurrently. The directory origin is fixed by the selected production or sandbox
environment.

See the [runnable Agent example](../examples/odp-agent-discovery/README.md), which clearly labels and
isolates its mock directory while querying live ODP Services.
