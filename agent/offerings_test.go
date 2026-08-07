package agent

import (
	"net/http"
	"net/http/httptest"
	"testing"

	odp "github.com/offering-protocol/odp-go"
)

func TestOfferingDetailsBundleSchemaNormalizeActionsAndResolveOpenAPI(t *testing.T) {
	document := `{"description":"Catalog","http":{"endpoint_base":"/odp","openapi":{"url":"https://api.example/openapi.json"}},"language":"en","localizations":["en"],"name":"Example","odp_version":"1.0","operations":[{"authentication":"not-required","name":"get-offering"},{"authentication":"not-required","name":"list-offerings"}]}`
	offering := `{"actions":[{"authentication":"not-required","http":{"href":"/downloads/item","method":"GET"},"id":"download","rel":"download"},{"authentication":"required","id":"purchase","openapi":{"operation_id":"purchase"},"rel":"purchase"}],"attributes":{"memory":80},"id":"item","name":"GPU","odp_version":"1.0","schema":{"url":"https://schemas.example/offering.json"}}`
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/odp+json")
		if request.URL.Path == "/.well-known/odp" {
			_, _ = writer.Write([]byte(document))
			return
		}
		if request.URL.Path == "/odp/offerings/item" {
			_, _ = writer.Write([]byte(offering))
			return
		}
		http.NotFound(writer, request)
	}))
	defer server.Close()
	supporting := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch request.URL.String() {
		case "https://schemas.example/offering.json":
			return jsonResponse(request, `{"$schema":"https://json-schema.org/draft/2020-12/schema","properties":{"memory":{"minimum":1,"type":"integer"}},"required":["memory"],"type":"object"}`, "application/schema+json"), nil
		case "https://api.example/openapi.json":
			return jsonResponse(request, `{"openapi":"3.1.0","info":{"title":"Purchase","version":"1"},"paths":{"/purchase":{"post":{"operationId":"purchase","responses":{"200":{"description":"Purchased"}}}}}}`, "application/json"), nil
		default:
			panic("unexpected supporting request: " + request.URL.String())
		}
	})}
	client, err := NewServiceClient(ServiceClientOptions{ServiceURL: server.URL, SupportingHTTPClient: supporting})
	if err != nil {
		t.Fatal(err)
	}
	details, err := client.GetOfferingDetails(t.Context(), "item")
	if err != nil {
		t.Fatal(err)
	}
	if len(details.Actions) != 2 || details.Actions[0].HTTP.URL != server.URL+"/downloads/item" {
		t.Fatalf("actions = %#v", details.Actions)
	}
	if details.AttributeSchema == nil || details.Offering.Attributes == nil || len(details.Issues) != 0 {
		t.Fatalf("details = %#v", details)
	}
	resolved, err := client.ResolveAction(t.Context(), "item", "purchase")
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Operation["operationId"] != "purchase" || resolved.OpenAPIDocument["openapi"] != "3.1.0" {
		t.Fatalf("resolved = %#v", resolved)
	}
}

func TestOfferingDetailsOmitInvalidAttributesAndDuplicateActions(t *testing.T) {
	actions, issues := normalizeActions([]odp.Action{
		{Authentication: odp.AuthenticationNotRequired, HTTP: &odp.HTTPActionTarget{Href: "/one", Method: http.MethodGet}, ID: "duplicate", Rel: odp.ActionDownload},
		{Authentication: odp.AuthenticationNotRequired, HTTP: &odp.HTTPActionTarget{Href: "/two", Method: http.MethodGet}, ID: "duplicate", Rel: odp.ActionDownload},
	}, "https://service.example", "")
	if len(actions) != 0 || len(issues) != 1 || issues[0].ActionID != "duplicate" {
		t.Fatalf("actions = %#v, issues = %#v", actions, issues)
	}
}

func TestOfferingDetailsReportOpenAPIActionWithoutDocumentURL(t *testing.T) {
	actions, issues := normalizeActions([]odp.Action{{
		Authentication: odp.AuthenticationNotRequired,
		ID:             "quote",
		OpenAPI:        &odp.OpenAPIActionTarget{OperationID: "createQuote"},
		Rel:            odp.ActionQuote,
	}}, "https://service.example", "")
	if len(actions) != 0 || len(issues) != 1 || issues[0].ActionID != "quote" || issues[0].Message != "OpenAPI Action has no OpenAPI document URL" {
		t.Fatalf("actions = %#v, issues = %#v", actions, issues)
	}
}
