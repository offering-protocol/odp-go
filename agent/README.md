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
inspection.Capabilities.Enrollment
inspection.Capabilities.Payments
```

The client validates `/.well-known/odp`, accepts at most five same-origin redirects, and applies a
four-hour fallback freshness when the Service does not publish HTTP cache metadata. Supply a
`Cache` for persistent storage; otherwise each client owns an in-memory cache.

The default HTTP clients resolve every destination, reject non-public addresses, pin connections to
the validated address, and ignore environment proxy settings. Local HTTP development is disabled
by default. Set `AllowLocalNetwork: true` only for an explicit `localhost`, `127.0.0.1`, or `::1`
Service. A caller-supplied `HTTPClient` or `SupportingHTTPClient` owns equivalent network policy.

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

offering, err := client.GetOffering(ctx, "gpu-h100", odp.RepresentationFull)

details, err := client.GetOfferingDetails(ctx, "gpu-h100")
```

The same client lists, retrieves, and searches Collections and lists the direct Offerings in a
Collection. Page methods expose continuations and search refinements when page-level metadata is
needed.

Short-lived clients can resume a Service-provided continuation with the corresponding
`ContinueList...` or `ContinueSearch...` item or page method. The client retrieves the opaque
reference with GET and applies the same-origin, response, redirect, and traversal limits.

Service Documents use a four-hour fallback freshness, Collections use one hour, and Offerings use
five minutes. Service-provided `Cache-Control` or `Expires` metadata takes precedence. Search
responses are cached only when the Service supplies explicit freshness metadata. Supplying a custom
HTTP client disables catalog caching unless `CachePartition` identifies its stable access context;
this prevents authenticated or payment-dependent representations from sharing a public cache key.

`RequestError` preserves ODP Problem Details and response headers, exposes a stable error code, and
marks rate-limit and server failures as retryable so application transports can compose AEP, MPP,
and x402 challenges.

### Compose authentication and payment transport

`ServiceClientOptions.HTTPClient` is the boundary for AEP credentials, MPP payments, x402 payments,
and application-specific HTTP policy. For example, an application can add an existing
credential through a standard `http.RoundTripper`:

```go
type credentialTransport struct {
	credential string
	next       http.RoundTripper
}

func (transport credentialTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	prepared := request.Clone(request.Context())
	prepared.Header.Set("Authorization", "Bearer "+transport.credential)
	return transport.next.RoundTrip(prepared)
}

httpClient := &http.Client{Transport: credentialTransport{
	credential: credential,
	next:       http.DefaultTransport,
}}

client, err := agent.NewServiceClient(agent.ServiceClientOptions{
	CachePartition: principalID,
	HTTPClient:     httpClient,
	ServiceURL:     "https://compute.example",
})
```

A live AEP, MPP, or x402 implementation can perform its challenge flow at the same transport
boundary. Set `CachePartition` to a stable identifier for the authenticated principal and access
context. Do not reuse one partition across anonymous users or different credentials.

## Use search capabilities and Offering details

Capability methods combine Service-wide definitions with the selected Collection and resolve linked
definition pages. Duplicate definitions and Sorts that reference unavailable Filters are omitted and
reported through `SearchCapabilityCatalog.Issues`.

```go
capabilities, err := client.GetOfferingSearchCapabilities(ctx, "accelerators")
if err != nil {
	return err
}

memory := capabilities.Filters["accelerator-memory"]
```

`GetOfferingDetails` validates Attributes against the Offering's JSON Schema, bundles external
schema references, and converts Action targets to absolute URLs. Unavailable schemas, invalid
Attributes, and unusable Actions are omitted from the corresponding enriched fields and reported in
`OfferingDetails.Issues`; the protocol Offering remains available in `OfferingDetails.Offering`.

```go
details, err := client.GetOfferingDetails(ctx, "gpu-h100")
if err != nil {
	return err
}

resolved, err := client.ResolveAction(ctx, "gpu-h100", "purchase")
if err != nil {
	return err
}
```

`ResolveAction` returns an HTTP Action's request schema or an OpenAPI 3.1 document and its uniquely
selected operation. It does not invoke the Action. An OpenAPI Action may omit its URL when the
Service Document declares `http.openapi.url`; an Action URL overrides that Service-wide default.
Supporting schemas and OpenAPI documents use a separate anonymous HTTP client and must use HTTPS.
Supply `SupportingHTTPClient` only when those requests need custom network transport; keep it free
of Service credentials. Attribute Schemas use a 24-hour fallback freshness, while OpenAPI documents
require explicit HTTP freshness metadata. Cross-document schema composition uses `$ref`;
`$dynamicRef` accepts only a fragment reference such as `#node`.

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

Inspection filters unrecognized enrollment, payment, and trust protocol descriptors for compatible
Agent processing. Recognized descriptors remain subject to current-version validation.

See the [runnable Agent example](../examples/odp-agent-discovery/README.md), which clearly labels and
isolates its mock directory while querying live ODP Services.

## Related documentation

- [Directory integration](../directory/README.md)
- [Service integration](../service/README.md)
- [Protocol models and validation](../README.md#protocol-core)
- [Normative specification and schemas](https://www.offeringprotocol.org/)
