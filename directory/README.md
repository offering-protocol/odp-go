# ODP directory package

Package `directory` searches the one canonical ODP directory for candidate Services. It does not
search Service catalogs. After discovery, an Agent inspects each result's live ODP document and
queries that Service's Collections and Offerings.

The production origin is fixed at `https://directory.offeringprotocol.org`. Select `Sandbox` to use
the fixed `https://sandbox.offeringprotocol.org` environment. Callers cannot configure another
origin.

## Search Services

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
		Payments: []directory.PaymentFilter{{Name: odp.ProtocolMPP}},
	},
	Limit: 25,
}

for candidate, err := range directoryClient.SearchServices(ctx, request, directory.IterationOptions{}) {
	if err != nil {
		return err
	}
	fmt.Printf("%s: %s\n", candidate.Name, candidate.ServiceOrigin)
}
```

Use `SearchPages` when facet counts or page-level additive members are needed:

```go
for page, err := range directoryClient.SearchPages(ctx, request, directory.IterationOptions{}) {
	if err != nil {
		return err
	}
	consume(page.Items, page.Facets)
}
```

Opaque continuation links are followed with `GET` on the selected canonical origin. Traversal is
limited to 16 pages by default and at most 10,000 results when `MaxItems` is configured. Stopping
iteration stops network activity.

## Suggestions

Suggestions help an Agent discover keyword vocabulary without downloading a global keyword list.

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
