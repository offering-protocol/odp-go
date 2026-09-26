# ODP directory package

Package `directory` searches indexed Services and Collections from ODP and OpenAPI sources. It does
not crawl catalogs or index Offerings. Each mixed result identifies its exact discovery document.
The Agent package navigates ODP catalogs only; it does not execute OpenAPI operations.

The production origin is fixed at `https://api.inflowpay.ai`. Select `Sandbox` to use
the fixed `https://sandbox.inflowpay.ai` environment. Callers cannot configure another
origin.

## Search Services and Collections

```go
directoryClient, err := directory.New(directory.Options{})
if err != nil {
	return err
}

search := directoryClient.Search(ctx, directory.DirectorySearchRequest{
	SearchRequest: directory.SearchRequest{Query: "weather forecast", Limit: 25},
}, directory.IterationOptions{MaxItems: 25})

for result, err := range search.Items {
	if err != nil {
		return err
	}
	switch result.Type {
	case "service":
		fmt.Printf("Service: %s (%s), source: %s\n", result.Service.Name,
			result.Service.Source.Type, result.Service.Source.URL)
	case "collection":
		fmt.Printf("Collection: %s, ID %s, through %s\n",
			result.Collection.Name, result.Collection.ID, result.Service.ServiceOrigin)
	default:
		fmt.Printf("Unsupported result type: %s\n", result.Type)
	}
}
```

Omit `Types` to select both types, or provide `Types: []string{"collection"}` or `[]string{"service"}`.
The list must be nonempty and distinct. Filters apply to the owning Service for either type.

A result's `Service.ServiceID` identifies the indexed Service. Several source documents can share
an API origin, so the origin alone does not identify an imported Service. `Service.Source.URL`
preserves the exact document path and query; it can be hosted on a different origin from the API.

Check `Service.Source.Type` before choosing the next operation:

- `SourceODP`: inspect the Service's live ODP document. For a Collection, call the Agent client's
  `GetCollection` with the case-sensitive `Collection.ID`.
- `SourceOpenAPI`: read `Service.Source.URL` with an OpenAPI-aware client. A Collection ID identifies
  a Directory presentation group, not an ODP Collection endpoint. Its source is the parent's document.
- Any other value: display the metadata or report an unsupported source. Do not assume ODP.

`Source.X402Discovery` means that a supporting fixed-path x402 discovery document was detected.
It does not prove that an operation accepts payment or that the caller can execute it. Imported
description, language and localizations may be absent; their Go values are empty strings or nil
slices. Imported results do not populate ODP `Operations`. Native ODP results retain their required
metadata validation. `SearchServices` returns native ODP Services only.

Mixed search requires the Directory's source-aware response format. A missing or empty source URL
is reported as a record issue; the client does not infer a document URL from the API origin.

`Result.IndexedAt` reports Collection freshness; `Result.Service.IndexedAt` reports its parent's
freshness. A Service may have `Publisher` attribution with `PublisherID`, `Name`, and `WebsiteURL`.
The website is a display link, not a discovery or execution target. Additional response members
are retained; publisher attribution may be absent or null. A Collection's attribution
is its owning `Service`.

Unknown types retain the wire type in `Type` and complete JSON in `Raw`; their `Service` and
`Collection` pointers are nil. Do not treat them as Services. Known types are validated and
retain additive metadata in `Additional`. Nested Service parsing omits unverified execution
metadata such as endpoint paths, just as Service-only search does.

Filter mixed search or suggestions by source using the same `ServiceFilters`:

```go
filters := &directory.ServiceFilters{
	Sources: []directory.SourceType{directory.SourceOpenAPI},
}
search := directoryClient.Search(ctx, directory.DirectorySearchRequest{
	SearchRequest: directory.SearchRequest{Query: "weather", Filters: filters},
}, directory.IterationOptions{})
names, err := directoryClient.Suggest(ctx, directory.SuggestionRequest{
	Prefix: "weather", Filters: filters,
})
```

Omit `Sources` for all sources. A supplied list must contain one or two distinct values from
`SourceODP` and `SourceOpenAPI`. Values within the list are alternatives; other filter categories
are combined with it. Collection filters use the owning Service's source. Source filters do not
make native `SearchServices` return OpenAPI entries.

The mixed endpoint returns at most 100 results (also its default limit), without continuation.
An absent `Next` does not promise that all matches were returned; refine the query or filters.
Collection search eligibility does not depend on permission to show a Directory landing card.
Mixed facets count all matching targets, not just returned items: a Service and two Collections
count as three. Descriptor facets use the owning Service's metadata.

## Search only Services

`SearchServices` lazily traverses directory pages and yields validated Service summaries. Filters
are structured and work without natural-language interpretation.

```go
directoryClient, err := directory.New(directory.Options{})
if err != nil {
	return err
}

request := directory.SearchRequest{
	Query: "compute",
	Filters: &directory.ServiceFilters{
		Keywords: []string{"gpu", "accelerator"},
		Payments: []directory.PaymentFilter{{
			Name: odp.ProtocolMPP,
			Options: []odp.PaymentOption{odp.PaymentOptionInflow, odp.PaymentOptionSolana},
		}},
	},
	Limit: 25,
}

for candidate, err := range directoryClient.SearchServices(ctx, request, directory.IterationOptions{}).Items {
	if err != nil {
		return err
	}
	fmt.Printf("%s: %s\n", candidate.Name, candidate.ServiceOrigin)
}
```

Options within one payment filter are alternatives. The example matches Services that accept either
InFlow or Solana through MPP. A protocol-only `PaymentFilter{Name: odp.ProtocolMPP}` matches any
Service that advertises MPP. `Facets.Payments` reports protocol counts, `Facets.PaymentOptions`
reports each protocol-option count independently, and `Facets.Trust` reports trust protocol counts.

Every search returns a `SearchSequence` with independent lazy `Items` and `Responses` iterators.
Iterating both performs two searches. Creating a sequence performs no network requests and
captures the request values. Use `Responses` for facets, issues, additive fields or continuation:

```go
for page, err := range directoryClient.SearchServices(ctx, request, directory.IterationOptions{}).Responses {
	if err != nil {
		return err
	}
	fmt.Printf("Results: %d, facets: %+v\n", len(page.Items), page.Facets)
}
```

Opaque continuation links are followed with `GET` on the selected canonical origin. A continuation
that leaves that origin, repeats a page already visited, or cannot be resolved ends the traversal
with an error. Stopping iteration stops network activity.

`IterationOptions.MaxPages` is the caller's page budget and defaults to 16. Reaching it ends the
sequence without an error, and the last response keeps `Next`. Resume mixed search using
`ContinueSearch`, or Service-only search using `ContinueSearchServices`:

```go
var resume string
for page, err := range directoryClient.SearchServices(ctx, request, directory.IterationOptions{MaxPages: 4}).Responses {
	if err != nil {
		return err
	}
	fmt.Printf("Results: %d, facets: %+v\n", len(page.Items), page.Facets)
	resume = page.Next
}
if resume != "" {
	for page, err := range directoryClient.ContinueSearchServices(ctx, resume, directory.IterationOptions{}).Responses {
		if err != nil {
			return err
		}
		fmt.Printf("Results: %d\n", len(page.Items))
	}
}
```

A budget above 10,000 pages is rejected, and a directory that offers a continuation for 10,000
consecutive pages ends the traversal with an error rather than quietly appearing exhausted.
`MaxItems` bounds item iteration at up to 10,000 results; zero leaves it unbounded within the
request budget. Response iteration does not truncate responses to that item limit. Pass a
`context.Context` to cancel requests. Both search families share the same transport policies.

### Migrating existing Go callers

This is a breaking Go API change. Replace `SearchPages(...)` with
`SearchServices(...).Responses`, and `ContinueSearchPages(...)` with
`ContinueSearchServices(...).Responses`. Append `.Items` to existing `SearchServices(...)` and
`ContinueSearchServices(...)` iteration. Replace `SearchPage` with `SearchResponse[Service]`.
There are no deprecated aliases. The Agent's cross-Service Offering discovery remains Service-only.

## Malformed results

One unusable record does not discard the page it arrived on. A record that fails validation is
omitted from `Items` and reported in `SearchResponse.Issues` with its original index and the reason;
`IterationOptions.OnIssue` receives the same reports while iterating `Items`:

```go
options := directory.IterationOptions{OnIssue: func(issue directory.Issue) {
	log.Printf("directory %s result %d: %s", issue.Scope, issue.Index, issue.Message)
}}
```

A malformed page envelope, by contrast, still fails the traversal.

## Suggestions

`Suggest` matches indexed names, descriptions and keywords, returning **names of matching
Services and Collections**, not the matching text itself. Despite `Prefix`, matching uses
substrings and whitespace-separated alternative terms. Names are deduplicated, with a maximum
and default of 25. These are candidate queries, not resource identifiers. Pass a selected string
to `Search`. Collection surfacing permission does not restrict suggestions.

```go
names, err := directoryClient.Suggest(ctx, directory.SuggestionRequest{
    Prefix: "we", Limit: 10,
    Filters: &directory.ServiceFilters{Keywords: []string{"weather"}},
})
```

`Suggest` sends POST `/v1/directory/suggestions`. Its optional `Filters` use the same
structure as search, including AEP, keywords, ODP operations, payments, sources and trust.
Collection filters apply to the owning Service; the output remains names only.

`SuggestServices` uses GET and retains keyword-prefix suggestions for Service-only discovery.
It does not accept filters:

```go
suggestions, err := directoryClient.SuggestServices(ctx, directory.SuggestionRequest{
	Prefix: "gp",
	Limit:  5,
})
```

## Sandbox

```go
directoryClient, err := directory.New(directory.Options{
	Environment: directory.Sandbox,
})
```

`Options.HTTPClient` permits transport policy and test injection while preserving the selected
canonical origin. The client accepts up to five same-origin redirects, bounds response bodies, and
returns non-success responses as `*directory.RequestError` with the status and response headers.
`RequestError.Retryable` marks the 429 and 5xx statuses worth retrying, and `Message` quotes only a
structured field of a JSON error document, stripped of control characters and clamped in length.

Directory results filter unrecognized enrollment, payment, and trust descriptors while preserving
recognized, validated protocol capabilities.

## Related documentation

- [Agent integration](../agent/README.md)
- [Protocol models and validation](../README.md#protocol-core)
- [Canonical directory](https://directory.inflowpay.ai/)
- [Normative specification and schemas](https://www.offeringprotocol.org/)
