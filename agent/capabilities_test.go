package agent

import (
	"net/http"
	"net/http/httptest"
	"testing"

	odp "github.com/offering-protocol/odp-go"
)

func TestOfferingSearchCapabilitiesComposeServiceAndCollectionScopes(t *testing.T) {
	document := `{"description":"Catalog","http":{"endpoint_base":"/odp"},"language":"en","localizations":["en"],"name":"Example","odp_version":"1.0","operations":[{"authentication":"not-required","name":"get-offering"},{"authentication":"not-required","name":"list-offerings"},{"authentication":"not-required","name":"search-offerings"}],"search_capabilities":{"filters":{"inline":[{"description":"Region","id":"region","operators":["eq"],"title":"Region","type":"string"}]}}}`
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/odp+json")
		_, _ = writer.Write([]byte(document))
	}))
	defer server.Close()
	client, err := NewServiceClient(ServiceClientOptions{ServiceURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	collection := &odp.Collection{SearchCapabilities: &odp.SearchCapabilities{
		Filters: &odp.FilterCapabilitySource{Inline: []odp.FilterDefinition{{Description: "Memory", ID: "memory", Operators: []odp.FilterOperator{odp.OperatorGreaterThanOrEqual}, Title: "Memory", Type: odp.FilterInteger}}},
		Sorts:   &odp.SortCapabilitySource{Inline: []odp.SortDefinition{{Description: "Memory ascending", ID: "memory-ascending", Keys: []odp.SortKey{{Direction: odp.SortAscending, FilterID: "memory", Missing: odp.MissingLast}}, Title: "Memory ascending"}}},
	}}
	capabilities, err := client.resolveSearchCapabilities(t.Context(), collection)
	if err != nil {
		t.Fatal(err)
	}
	if len(capabilities.Filters) != 2 || len(capabilities.Sorts) != 1 || len(capabilities.Sorts["memory-ascending"].Filters) != 1 {
		t.Fatalf("capabilities = %#v", capabilities)
	}
}

func TestOfferingSearchCapabilitiesOmitDuplicateDefinitions(t *testing.T) {
	document := `{"description":"Catalog","http":{"endpoint_base":"/odp"},"language":"en","localizations":["en"],"name":"Example","odp_version":"1.0","operations":[{"authentication":"not-required","name":"get-offering"},{"authentication":"not-required","name":"list-offerings"},{"authentication":"not-required","name":"search-offerings"}],"search_capabilities":{"filters":{"inline":[{"description":"First","id":"region","operators":["eq"],"title":"Region","type":"string"},{"description":"Second","id":"region","operators":["eq"],"title":"Region","type":"string"}]}}}`
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/odp+json")
		_, _ = writer.Write([]byte(document))
	}))
	defer server.Close()
	client, err := NewServiceClient(ServiceClientOptions{ServiceURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	capabilities, err := client.GetOfferingSearchCapabilities(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(capabilities.Filters) != 0 || len(capabilities.Issues) != 1 {
		t.Fatalf("capabilities = %#v", capabilities)
	}
}
