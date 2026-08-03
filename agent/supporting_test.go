package agent

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (roundTrip roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}

func TestResolveSchemaBundlesExternalReferencesAndValidates(t *testing.T) {
	documents := map[string]string{
		"https://schemas.example/root.json": `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"gpu":{"$ref":"gpu.json#/$defs/gpu"}},"required":["gpu"]}`,
		"https://schemas.example/gpu.json":  `{"$schema":"https://json-schema.org/draft/2020-12/schema","$defs":{"gpu":{"type":"string","minLength":2}}}`,
	}
	client := supportingTestClient(t, documents)
	bundled, validator, err := client.resolveSchema(t.Context(), "https://schemas.example/root.json")
	if err != nil {
		t.Fatal(err)
	}
	if bundled["$defs"] == nil {
		t.Fatal("bundled schema omitted external definitions")
	}
	if err := validator.Validate(map[string]any{"gpu": "h100"}); err != nil {
		t.Fatalf("valid attributes: %v", err)
	}
	if err := validator.Validate(map[string]any{"gpu": "x"}); err == nil {
		t.Fatal("invalid attributes passed validation")
	}
}

func TestAttributeSchemaUsesFallbackCache(t *testing.T) {
	var requests atomic.Int64
	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests.Add(1)
		return jsonResponse(request, `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object"}`, "application/schema+json"), nil
	})}
	client, err := NewServiceClient(ServiceClientOptions{ServiceURL: "https://service.example", SupportingHTTPClient: httpClient})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := client.resolveSchema(t.Context(), "https://schemas.example/root.json"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := client.resolveSchema(t.Context(), "https://schemas.example/root.json"); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 1 {
		t.Fatalf("schema requests = %d", requests.Load())
	}
}

func TestResolveOpenAPIFindsExactlyOneOperation(t *testing.T) {
	document := `{"openapi":"3.1.0","info":{"title":"Purchase","version":"1"},"paths":{"/purchase":{"post":{"operationId":"purchase","responses":{"200":{"description":"Purchased"}}}}}}`
	client := supportingTestClient(t, map[string]string{"https://api.example/openapi.json": document})
	resolved, operation, err := client.resolveOpenAPI(t.Context(), "https://api.example/openapi.json", "purchase")
	if err != nil {
		t.Fatal(err)
	}
	if resolved["openapi"] != "3.1.0" || operation["operationId"] != "purchase" {
		t.Fatalf("resolved = %#v, operation = %#v", resolved, operation)
	}
	if _, _, err := client.resolveOpenAPI(t.Context(), "https://api.example/openapi.json", "missing"); err == nil {
		t.Fatal("missing operation resolved")
	}
}

func TestSupportingDocumentsAreAnonymousAndRequireHTTPS(t *testing.T) {
	var authorization string
	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		authorization = request.Header.Get("Authorization")
		return jsonResponse(request, `{"value":true}`, "application/json"), nil
	})}
	client, err := NewServiceClient(ServiceClientOptions{HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { panic("catalog transport used") })}, ServiceURL: "https://service.example", SupportingHTTPClient: httpClient})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.supportingJSON(context.Background(), "https://schema.example/value", "test", "application/json", []string{"application/json"}, 1024, 0); err != nil {
		t.Fatal(err)
	}
	if authorization != "" {
		t.Fatalf("supporting Authorization = %q", authorization)
	}
	if _, err := client.supportingJSON(context.Background(), "http://schema.example/value", "test", "application/json", []string{"application/json"}, 1024, 0); err == nil {
		t.Fatal("HTTP supporting URL accepted")
	}
}

func supportingTestClient(t *testing.T, documents map[string]string) *ServiceClient {
	t.Helper()
	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		document, found := documents[request.URL.String()]
		if !found {
			return &http.Response{Body: io.NopCloser(strings.NewReader("")), Header: http.Header{"Content-Type": {"application/problem+json"}}, Request: request, StatusCode: http.StatusNotFound}, nil
		}
		contentType := "application/schema+json"
		if strings.Contains(document, `"openapi"`) {
			contentType = "application/json"
		}
		return jsonResponse(request, document, contentType), nil
	})}
	client, err := NewServiceClient(ServiceClientOptions{ServiceURL: "https://service.example", SupportingHTTPClient: httpClient})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func jsonResponse(request *http.Request, body, contentType string) *http.Response {
	return &http.Response{Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{"Content-Type": {contentType}}, Request: request, StatusCode: http.StatusOK}
}
