package odp_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	odp "github.com/offering-protocol/odp-go"
)

func TestOfferingPreservesAdditiveMembers(t *testing.T) {
	input := []byte(`{
		"odp_version":"1.0",
		"id":"gpu-a100",
		"name":"A100",
		"future":{"enabled":true},
		"price":{"type":"quote","future_price":"negotiable"},
		"actions":[{"authentication":"not-required","id":"quote","rel":"quote","http":{"href":"/quote","method":"POST"}}]
	}`)
	offering, err := odp.ParseOffering(input)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := offering.Additional["future"]; !ok {
		t.Fatal("top-level additive member was not preserved")
	}
	if offering.Price == nil || offering.Price.Additional["future_price"] == nil {
		t.Fatal("nested price additive member was not preserved")
	}
	encoded, err := json.Marshal(offering)
	if err != nil {
		t.Fatal(err)
	}
	var before, after any
	if err := json.Unmarshal(input, &before); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encoded, &after); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("round trip changed JSON\nbefore: %s\nafter:  %s", input, encoded)
	}
}

func TestServiceDocumentParsesBrandingAndOpenAPI(t *testing.T) {
	document, err := odp.ParseServiceDocument([]byte(`{"branding":{"icon":{"src":"/branding/icon.svg"},"logo":{"src":"/branding/logo.webp","type":"image/webp"}},"description":"Catalog","http":{"endpoint_base":"/odp","openapi":{"url":"/openapi.json"}},"language":"en","localizations":["en"],"name":"Example","odp_version":"1.0","operations":[{"authentication":"not-required","name":"get-offering"},{"authentication":"not-required","name":"list-offerings"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if document.Branding == nil || document.Branding.Icon.Type != "" || document.HTTP.OpenAPI == nil || document.HTTP.OpenAPI.URL != "/openapi.json" {
		t.Fatalf("document = %#v", document)
	}
}

func TestServiceDocumentParsesResourceLinks(t *testing.T) {
	document, err := odp.ParseServiceDocument([]byte(`{"description":"Catalog","documentation_url":"/developers/","http":{"endpoint_base":"/odp"},"language":"en","localizations":["en"],"name":"Example","odp_version":"1.0","operations":[{"authentication":"not-required","name":"get-offering"},{"authentication":"not-required","name":"list-offerings"}],"status_url":"https://status.example.com/","support_url":"/support/","website_url":"/store/"}`))
	if err != nil {
		t.Fatal(err)
	}
	if document.DocumentationURL != "/developers/" || document.StatusURL != "https://status.example.com/" || document.SupportURL != "/support/" || document.WebsiteURL != "/store/" {
		t.Fatalf("document = %#v", document)
	}
}

func TestServiceDocumentParsesMCPEndpoints(t *testing.T) {
	document, err := odp.ParseServiceDocument([]byte(`{"description":"Catalog","http":{"endpoint_base":"/odp"},"language":"en","localizations":["en"],"mcp":[{"description":"Browse the public catalog.","name":"Catalog","type":"streamable-http","url":"/mcp"}],"name":"Example","odp_version":"1.0","operations":[{"authentication":"not-required","name":"get-offering"},{"authentication":"not-required","name":"list-offerings"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(document.MCP) != 1 || document.MCP[0].Description != "Browse the public catalog." || document.MCP[0].Name != "Catalog" || document.MCP[0].Type != odp.MCPEndpointStreamableHTTP || document.MCP[0].URL != "/mcp" {
		t.Fatalf("document = %#v", document)
	}
}

func TestServiceDocumentParsesPaymentOptions(t *testing.T) {
	document, err := odp.ParseServiceDocument([]byte(`{"description":"Catalog","http":{"endpoint_base":"/odp"},"language":"en","localizations":["en"],"name":"Example","odp_version":"1.0","operations":[{"authentication":"not-required","name":"get-offering"},{"authentication":"not-required","name":"list-offerings"}],"protocols":{"payments":[{"authentication":"not-required","name":"mpp","options":["card","inflow","solana"]}]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if document.Protocols == nil || len(document.Protocols.Payments) != 1 || len(document.Protocols.Payments[0].Options) != 3 || document.Protocols.Payments[0].Options[1] != odp.PaymentOptionInflow {
		t.Fatalf("document = %#v", document)
	}
	if !odp.IsPaymentOption(odp.PaymentOptionBase) || odp.IsPaymentOption("future-option") {
		t.Fatal("payment option vocabulary mismatch")
	}
}

func TestKnownMemberCannotBeInjectedThroughAdditionalMembers(t *testing.T) {
	offering := odp.Offering{
		Additional: odp.AdditionalMembers{
			"description": json.RawMessage(`"injected"`),
			"future":      json.RawMessage(`true`),
		},
		ID:         "one",
		Name:       "One",
		ODPVersion: odp.Version,
	}
	data, err := json.Marshal(offering)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		t.Fatal(err)
	}
	if _, exists := object["description"]; exists {
		t.Fatal("additional member replaced an omitted known field")
	}
	if _, exists := object["future"]; !exists {
		t.Fatal("additive member was lost")
	}
}

func TestBuildOperationURL(t *testing.T) {
	got, err := odp.BuildOperationURL("/odp/", odp.OperationGetOffering, "https://market.example", "gpu~1")
	if err != nil {
		t.Fatal(err)
	}
	if want := "https://market.example/odp/offerings/gpu~1"; got.String() != want {
		t.Fatalf("BuildOperationURL() = %q, want %q", got, want)
	}
	if _, err := odp.BuildOperationURL("/odp", odp.OperationGetOffering, "https://market.example", "../admin"); err == nil {
		t.Fatal("BuildOperationURL() accepted a path-unsafe identifier")
	}
}

func TestResolveContinuationRejectsAnotherOrigin(t *testing.T) {
	if _, err := odp.ResolveContinuation("https://other.example/page", "https://market.example"); err == nil {
		t.Fatal("ResolveContinuation() accepted a cross-origin URL")
	}
}

func TestDeriveServiceOriginCanonicalizesIdentity(t *testing.T) {
	origin, err := odp.DeriveServiceOrigin("https://BÜCHER.example:443/.well-known/odp")
	if err != nil {
		t.Fatal(err)
	}
	if want := "https://xn--bcher-kva.example"; origin != want {
		t.Fatalf("DeriveServiceOrigin() = %q, want %q", origin, want)
	}
}

func TestParseRejectsTrailingJSON(t *testing.T) {
	data := []byte(`{"odp_version":"1.0","items":[]} {}`)
	if _, err := odp.ParsePage[json.RawMessage](data); err == nil {
		t.Fatal("ParsePage() accepted multiple JSON values")
	}
}

func TestLocalizedResourcesRejectInvalidLanguageTags(t *testing.T) {
	tests := []struct {
		name  string
		parse func([]byte) error
		value string
	}{
		{
			name:  "collection with incomplete extension",
			parse: func(data []byte) error { _, err := odp.ParseCollection(data); return err },
			value: `{"odp_version":"1.0","id":"compute","name":"Compute","language":"en-a","localizations":["en-a"]}`,
		},
		{
			name:  "offering with repeated variant",
			parse: func(data []byte) error { _, err := odp.ParseOffering(data); return err },
			value: `{"odp_version":"1.0","id":"gpu","name":"GPU","language":"sl-rozaj-rozaj","localizations":["sl-rozaj-rozaj"]}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.parse([]byte(test.value)); err == nil {
				t.Fatal("parser accepted an invalid language tag")
			}
		})
	}
}

func TestResourceImages(t *testing.T) {
	for name, value := range map[string]string{
		"collection": `{"odp_version":"1.0","id":"compute","name":"Compute","images":[{"alt":"GPU hardware","src":"/images/gpu.jpg"}]}`,
		"offering":   `{"odp_version":"1.0","id":"gpu","name":"GPU","images":[{"height":1200,"src":"/images/gpu.webp","type":"image/webp","width":1200}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			var err error
			if name == "collection" {
				_, err = odp.ParseCollection([]byte(value))
			} else {
				_, err = odp.ParseOffering([]byte(value))
			}
			if err != nil {
				t.Fatal(err)
			}
		})
	}

	if _, err := odp.ParseOffering([]byte(`{"odp_version":"1.0","id":"gpu","name":"GPU","images":[{"src":"/images/gpu.webp"},{"src":"/images/gpu.webp"}]}`)); err == nil {
		t.Fatal("ParseOffering() accepted duplicate image sources")
	}
}

func TestProtocolParsers(t *testing.T) {
	tests := []struct {
		name  string
		parse func([]byte) error
		value string
	}{
		{
			name:  "collection",
			value: `{"odp_version":"1.0","id":"compute","name":"Compute"}`,
			parse: func(data []byte) error { _, err := odp.ParseCollection(data); return err },
		},
		{
			name:  "offering",
			value: `{"odp_version":"1.0","id":"gpu","name":"GPU"}`,
			parse: func(data []byte) error { _, err := odp.ParseOffering(data); return err },
		},
		{
			name:  "collection search",
			value: `{"odp_version":"1.0","parent_id":null}`,
			parse: func(data []byte) error { _, err := odp.ParseCollectionSearchRequest(data); return err },
		},
		{
			name:  "offering search",
			value: `{"odp_version":"1.0","query":"gpu"}`,
			parse: func(data []byte) error { _, err := odp.ParseOfferingSearchRequest(data); return err },
		},
		{
			name:  "offering search response",
			value: `{"odp_version":"1.0","items":[]}`,
			parse: func(data []byte) error { _, err := odp.ParseOfferingSearchResponse(data); return err },
		},
		{
			name:  "filter definition",
			value: `{"id":"memory","title":"Memory","description":"Memory size","type":"integer","operators":["eq","gte"]}`,
			parse: func(data []byte) error { _, err := odp.ParseFilterDefinition(data); return err },
		},
		{
			name:  "sort definition",
			value: `{"id":"price-low","title":"Price","description":"Lowest price first","keys":[{"filter_id":"price","direction":"ascending","missing":"last"}]}`,
			parse: func(data []byte) error { _, err := odp.ParseSortDefinition(data); return err },
		},
		{
			name:  "filter definition page",
			value: `{"odp_version":"1.0","items":[]}`,
			parse: func(data []byte) error { _, err := odp.ParseFilterDefinitionPage(data); return err },
		},
		{
			name:  "sort definition page",
			value: `{"odp_version":"1.0","items":[]}`,
			parse: func(data []byte) error { _, err := odp.ParseSortDefinitionPage(data); return err },
		},
		{
			name:  "resource identity",
			value: `{"service":"https://market.example","type":"offering","id":"gpu"}`,
			parse: func(data []byte) error { _, err := odp.ParseResourceIdentity(data); return err },
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.parse([]byte(test.value)); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCollectionSearchParentIDStates(t *testing.T) {
	tests := []struct {
		name     string
		parentID odp.Optional[string]
		want     string
	}{
		{name: "omitted", want: `{"odp_version":"1.0","query":"desk"}`},
		{name: "root", parentID: odp.Null[string](), want: `{"odp_version":"1.0","parent_id":null,"query":"desk"}`},
		{name: "child", parentID: odp.Some("office"), want: `{"odp_version":"1.0","parent_id":"office","query":"desk"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := odp.CollectionSearchRequest{ODPVersion: odp.Version, ParentID: test.parentID, Query: "desk"}
			data, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != test.want {
				t.Fatalf("Marshal() = %s, want %s", data, test.want)
			}
			parsed, err := odp.ParseCollectionSearchRequest(data)
			if err != nil {
				t.Fatal(err)
			}
			if parsed.ParentID.IsPresent() != test.parentID.IsPresent() || parsed.ParentID.IsNull() != test.parentID.IsNull() {
				t.Fatalf("ParentID state was not preserved")
			}
		})
	}
}

func TestIterateItems(t *testing.T) {
	first := odp.Page[string]{Items: []string{"a", "b"}, Next: "/page/2", ODPVersion: odp.Version}
	loader := func(_ context.Context, next string) (odp.Page[string], error) {
		if next != "/page/2" {
			t.Fatalf("loader next = %q", next)
		}
		return odp.Page[string]{Items: []string{"c"}, ODPVersion: odp.Version}, nil
	}
	var items []string
	for item, err := range odp.IterateItems(context.Background(), first, loader) {
		if err != nil {
			t.Fatal(err)
		}
		items = append(items, item)
	}
	if !reflect.DeepEqual(items, []string{"a", "b", "c"}) {
		t.Fatalf("items = %v", items)
	}
}

func TestIteratePagesDetectsLoop(t *testing.T) {
	first := odp.Page[string]{Next: "/same", ODPVersion: odp.Version}
	loader := func(context.Context, string) (odp.Page[string], error) { return first, nil }
	var got error
	for _, err := range odp.IteratePages(context.Background(), first, loader) {
		if err != nil {
			got = err
		}
	}
	if !errors.Is(got, odp.ErrPaginationLoop) {
		t.Fatalf("error = %v, want ErrPaginationLoop", got)
	}
}
