# ODP Service package

Package `service` integrates ODP with Go's standard `net/http` stack. `Service` implements
`http.Handler` and owns the well-known document, fixed operation routes, representation defaults,
request validation, bounded JSON parsing, media negotiation, localization headers, response
validation, and Problem Details.

## Small catalogs

`NewStaticCatalog` supplies the required Offering operations from an in-memory catalog. Configuring
Collections also enables Collection listing, retrieval, and direct Offering membership. It validates
identifiers and relationships at construction and uses opaque, integrity-protected stateless
continuations.

```go
catalog, err := service.NewStaticCatalog(service.StaticCatalogOptions{
	Offerings: []odp.Offering{
		{
			ID:         "gpu-h100",
			Name:       "H100 GPU",
			ODPVersion: odp.Version,
			Price:      &odp.PricePreview{Type: odp.PriceQuote},
		},
	},
})
if err != nil {
	return err
}

odpService, err := service.New(service.Options{
	Catalog: catalog,
	Document: odp.ServiceDocument{
		Description:   "On-demand compute resources",
		HTTP:          odp.HTTPConfiguration{EndpointBase: "/odp"},
		Language:      "en",
		Localizations: []string{"en"},
		Name:          "Example Compute",
	},
})
```

Mount `odpService` at `/.well-known/odp` and its configured endpoint base. Authentication, AEP, MPP,
x402, rate limiting, and application policy compose as ordinary HTTP middleware.

## Storage-backed catalogs

Large Services configure `Catalog` with functions backed by their own storage and indexes. The
runtime invokes only the function required for the incoming operation; it does not materialize,
sort, or inspect the complete catalog. Each function receives the request context, original HTTP
request, normalized representation, preferred language, limit, and opaque cursor.

Optional operation functions are the source of truth for Service Document advertisement. Search
functions receive a validated request on the initial `POST` and `nil` on a continuation `GET`, so a
Service can use either server-managed or integrity-protected stateless continuation state.

Catalog functions can return `*service.Error` for an intentional ODP Problem Details response.
Unexpected errors become a generic `500` response without exposing application details.
