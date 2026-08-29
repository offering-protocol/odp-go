package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	odp "github.com/offering-protocol/odp-go"
	"github.com/offering-protocol/odp-go/agent"
	"github.com/offering-protocol/odp-go/service"
)

func TestServiceClientInspectsAndNavigatesRealService(t *testing.T) {
	catalog, err := service.NewStaticCatalog(service.StaticCatalogOptions{
		Collections: []odp.Collection{{ID: "compute", Name: "Compute", ODPVersion: odp.Version}},
		Offerings: []odp.Offering{
			{CollectionIDs: []string{"compute"}, Description: "Accelerator", ID: "gpu", Name: "GPU", ODPVersion: odp.Version},
			{ID: "storage", Name: "Storage", ODPVersion: odp.Version},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := service.New(service.Options{
		Catalog: catalog,
		Document: odp.ServiceDocument{
			Description: "Example catalog", HTTP: odp.HTTPConfiguration{EndpointBase: "/odp"},
			Language: "en", Localizations: []string{"en"}, Name: "Example",
			Protocols: &odp.ServiceProtocols{
				Enrollment: []odp.EnrollmentProtocol{{Name: odp.ProtocolAEP}},
				Trust:      []odp.TrustProtocol{{Name: odp.ProtocolTAP}},
			},
		},
		OperationAuthentication: map[odp.Operation]odp.AuthenticationRequirement{
			odp.OperationGetOffering: odp.AuthenticationOptional,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int64
	var wellKnownRequests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		if request.URL.Path == "/.well-known/odp" {
			wellKnownRequests.Add(1)
		}
		runtime.ServeHTTP(writer, request)
	}))
	defer server.Close()

	client, err := agent.NewServiceClient(agent.ServiceClientOptions{AllowLocalNetwork: true, InitialPageSize: 1, ServiceURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	inspection, err := client.Inspect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if inspection.Freshness != agent.FreshnessFetched || inspection.Document.Name != "Example" {
		t.Fatalf("inspection = %#v", inspection)
	}
	if len(inspection.Capabilities.Operations) == 0 || !hasOperationAuthentication(inspection.Capabilities.Operations, odp.OperationGetOffering, odp.AuthenticationOptional) {
		t.Fatalf("operation capabilities = %#v", inspection.Capabilities.Operations)
	}
	if len(inspection.Capabilities.Trust) != 1 || inspection.Capabilities.Trust[0].Name != odp.ProtocolTAP {
		t.Fatalf("trust capabilities = %#v", inspection.Capabilities.Trust)
	}
	cached, err := client.Inspect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if cached.Freshness != agent.FreshnessFresh {
		t.Fatalf("freshness = %q", cached.Freshness)
	}

	var offeringIDs []string
	for offering, err := range client.ListOfferings(t.Context(), agent.ListOptions{}) {
		if err != nil {
			t.Fatal(err)
		}
		offeringIDs = append(offeringIDs, offering.ID)
	}
	if strings.Join(offeringIDs, ",") != "gpu,storage" {
		t.Fatalf("Offerings = %v", offeringIDs)
	}
	var collectionIDs []string
	for collection, err := range client.ListCollections(t.Context(), agent.ListOptions{}) {
		if err != nil {
			t.Fatal(err)
		}
		collectionIDs = append(collectionIDs, collection.ID)
	}
	if strings.Join(collectionIDs, ",") != "compute" {
		t.Fatalf("Collections = %v", collectionIDs)
	}
	members := 0
	for offering, err := range client.ListCollectionOfferings(t.Context(), "compute", agent.ListOptions{}) {
		if err != nil {
			t.Fatal(err)
		}
		if offering.ID != "gpu" {
			t.Fatalf("member = %q", offering.ID)
		}
		members++
	}
	if members != 1 {
		t.Fatalf("member count = %d", members)
	}
	detail, err := client.GetOffering(t.Context(), "gpu", odp.RepresentationFull)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Description != "Accelerator" {
		t.Fatalf("detail = %#v", detail)
	}
	collection, err := client.GetCollection(t.Context(), "compute", odp.RepresentationFull)
	if err != nil {
		t.Fatal(err)
	}
	if collection.Name != "Compute" {
		t.Fatalf("Collection = %#v", collection)
	}
	if wellKnownRequests.Load() != 1 || requests.Load() < 6 {
		t.Fatalf("requests = %d, well-known requests = %d", requests.Load(), wellKnownRequests.Load())
	}
}

func TestServiceClientInspectionFiltersUnknownProtocols(t *testing.T) {
	document := `{"description":"Catalog","http":{"endpoint_base":"/odp"},"language":"en","localizations":["en"],"name":"Example","odp_version":"1.0","operations":[{"authentication":"not-required","name":"get-offering"},{"authentication":"not-required","name":"list-offerings"}],"protocols":{"payments":[{"authentication":"not-required","name":"future-payment"},{"authentication":"not-required","name":"mpp"}],"trust":[{"name":"future-trust"},{"name":"tap"}]}}`
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/odp+json")
		_, _ = writer.Write([]byte(document))
	}))
	defer server.Close()
	client, err := agent.NewServiceClient(agent.ServiceClientOptions{AllowLocalNetwork: true, ServiceURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	inspection, err := client.Inspect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(inspection.Capabilities.Payments) != 1 || inspection.Capabilities.Payments[0].Name != odp.ProtocolMPP || len(inspection.Capabilities.Trust) != 1 || inspection.Capabilities.Trust[0].Name != odp.ProtocolTAP {
		t.Fatalf("capabilities = %#v", inspection.Capabilities)
	}
}

func hasOperationAuthentication(operations []odp.OperationDescriptor, name odp.Operation, authentication odp.AuthenticationRequirement) bool {
	for _, operation := range operations {
		if operation.Name == name && operation.Authentication == authentication {
			return true
		}
	}
	return false
}

func TestServiceClientSearchesValidatedRequest(t *testing.T) {
	var received *odp.OfferingSearchRequest
	runtime := newSearchService(t, func(_ context.Context, request *odp.OfferingSearchRequest, _ service.CatalogRequest) (odp.OfferingPage[odp.Offering], error) {
		received = request
		return odp.OfferingPage[odp.Offering]{
			Items: []odp.Offering{{ID: "gpu", Name: "GPU"}}, ODPVersion: odp.Version,
			Refinements: []odp.RefinementGroup{{FilterID: "region", Values: []odp.RefinementBucket{{Count: 1, Value: "us-west"}}}},
		}, nil
	})
	server := httptest.NewServer(runtime)
	defer server.Close()
	client, err := agent.NewServiceClient(agent.ServiceClientOptions{AllowLocalNetwork: true, ServiceURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	pages := client.SearchOfferingPages(t.Context(), agent.OfferingSearchOptions{Query: "gpu", Refinements: []string{"region"}})
	page, err := first(pages)
	if err != nil {
		t.Fatal(err)
	}
	if received == nil || received.Query != "gpu" || received.Limit != 50 {
		t.Fatalf("search request = %#v", received)
	}
	if len(page.Refinements) != 1 || page.Items[0].ID != "gpu" {
		t.Fatalf("page = %#v", page)
	}
}

func TestServiceClientReturnsProblemDetails(t *testing.T) {
	catalog, err := service.NewStaticCatalog(service.StaticCatalogOptions{Offerings: []odp.Offering{{ID: "gpu", Name: "GPU", ODPVersion: odp.Version}}})
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := service.New(service.Options{Catalog: catalog, Document: serviceDocument()})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(runtime)
	defer server.Close()
	client, err := agent.NewServiceClient(agent.ServiceClientOptions{AllowLocalNetwork: true, ServiceURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.GetOffering(t.Context(), "missing", odp.RepresentationFull)
	var requestError *agent.RequestError
	if !errors.As(err, &requestError) || requestError.Code != "NOT_FOUND" || requestError.Retryable || requestError.Status != http.StatusNotFound || requestError.Problem == nil || requestError.Problem.Code != "NOT_FOUND" {
		t.Fatalf("error = %#v", err)
	}
}

func TestServiceClientRejectsCrossOriginRedirectAndOversizedDocument(t *testing.T) {
	tests := []struct {
		name      string
		transport roundTripFunc
		contains  string
	}{
		{
			name: "cross origin redirect",
			transport: func(*http.Request) (*http.Response, error) {
				headers := make(http.Header)
				headers.Set("Location", "https://other.example/.well-known/odp")
				return response(http.StatusTemporaryRedirect, "", headers, ""), nil
			},
			contains: "changed Service origin",
		},
		{
			name: "oversized document",
			transport: func(*http.Request) (*http.Response, error) {
				return response(http.StatusOK, strings.Repeat("x", 65_537), nil, service.MediaType), nil
			},
			contains: "byte limit",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client, err := agent.NewServiceClient(agent.ServiceClientOptions{
				CachePartition: "test", HTTPClient: &http.Client{Transport: test.transport}, ServiceURL: "https://service.example",
			})
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.Inspect(t.Context())
			if err == nil || !strings.Contains(err.Error(), test.contains) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestServiceClientCachesOnlyValidatedRepresentationsAndRevalidates(t *testing.T) {
	t.Run("invalid response is not cached", func(t *testing.T) {
		var requests atomic.Int64
		transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
			if requests.Add(1) == 1 {
				return response(http.StatusOK, `{"name":"incomplete"}`, nil, service.MediaType), nil
			}
			return response(http.StatusOK, documentJSON("get-offering", "list-offerings"), nil, service.MediaType), nil
		})
		client, err := agent.NewServiceClient(agent.ServiceClientOptions{CachePartition: "test", HTTPClient: &http.Client{Transport: transport}, ServiceURL: "https://service.example"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := client.Inspect(t.Context()); err == nil {
			t.Fatal("expected invalid Service Document")
		}
		if _, err := client.Inspect(t.Context()); err != nil {
			t.Fatal(err)
		}
		if requests.Load() != 2 {
			t.Fatalf("requests = %d", requests.Load())
		}
	})

	t.Run("expired response is conditionally revalidated", func(t *testing.T) {
		var requests atomic.Int64
		transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
			if requests.Add(1) == 1 {
				headers := make(http.Header)
				headers.Set("Cache-Control", "max-age=0")
				headers.Set("ETag", `"document-1"`)
				return response(http.StatusOK, documentJSON("get-offering", "list-offerings"), headers, service.MediaType), nil
			}
			if request.Header.Get("If-None-Match") != `"document-1"` {
				t.Fatalf("If-None-Match = %q", request.Header.Get("If-None-Match"))
			}
			return response(http.StatusNotModified, "", http.Header{"Cache-Control": []string{"max-age=60"}}, ""), nil
		})
		client, err := agent.NewServiceClient(agent.ServiceClientOptions{CachePartition: "test", HTTPClient: &http.Client{Transport: transport}, ServiceURL: "https://service.example"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := client.Inspect(t.Context()); err != nil {
			t.Fatal(err)
		}
		inspection, err := client.Inspect(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if inspection.Freshness != agent.FreshnessRevalidated || requests.Load() != 2 {
			t.Fatalf("inspection = %#v, requests = %d", inspection, requests.Load())
		}
	})

	t.Run("no-store removes a prior cache entry", func(t *testing.T) {
		var requests atomic.Int64
		transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
			count := requests.Add(1)
			headers := make(http.Header)
			if count == 1 {
				headers.Set("Cache-Control", "max-age=0")
				headers.Set("ETag", `"document-1"`)
			} else {
				headers.Set("Cache-Control", "no-store")
			}
			return response(http.StatusOK, documentJSON("get-offering", "list-offerings"), headers, service.MediaType), nil
		})
		client, err := agent.NewServiceClient(agent.ServiceClientOptions{CachePartition: "test", HTTPClient: &http.Client{Transport: transport}, ServiceURL: "https://service.example"})
		if err != nil {
			t.Fatal(err)
		}
		for range 3 {
			if _, err := client.Inspect(t.Context()); err != nil {
				t.Fatal(err)
			}
		}
		if requests.Load() != 3 {
			t.Fatalf("requests = %d", requests.Load())
		}
	})

	t.Run("revalidation without freshness preserves a stale policy", func(t *testing.T) {
		var requests atomic.Int64
		transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
			if requests.Add(1) == 1 {
				headers := make(http.Header)
				headers.Set("Cache-Control", "max-age=0")
				headers.Set("ETag", `"document-1"`)
				return response(http.StatusOK, documentJSON("get-offering", "list-offerings"), headers, service.MediaType), nil
			}
			if request.Header.Get("If-None-Match") != `"document-1"` {
				t.Fatalf("If-None-Match = %q", request.Header.Get("If-None-Match"))
			}
			return response(http.StatusNotModified, "", nil, ""), nil
		})
		client, err := agent.NewServiceClient(agent.ServiceClientOptions{CachePartition: "test", HTTPClient: &http.Client{Transport: transport}, ServiceURL: "https://service.example"})
		if err != nil {
			t.Fatal(err)
		}
		for range 3 {
			if _, err := client.Inspect(t.Context()); err != nil {
				t.Fatal(err)
			}
		}
		if requests.Load() != 3 {
			t.Fatalf("requests = %d", requests.Load())
		}
	})

	t.Run("revalidation starts at the cached same-origin final URL", func(t *testing.T) {
		var paths []string
		transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
			paths = append(paths, request.URL.Path)
			if request.URL.Path == "/.well-known/odp" {
				headers := make(http.Header)
				headers.Set("Location", "/discovery/odp")
				return response(http.StatusTemporaryRedirect, "", headers, ""), nil
			}
			if len(paths) == 2 {
				headers := make(http.Header)
				headers.Set("Cache-Control", "max-age=0")
				headers.Set("ETag", `"document-1"`)
				return response(http.StatusOK, documentJSON("get-offering", "list-offerings"), headers, service.MediaType), nil
			}
			return response(http.StatusNotModified, "", http.Header{"Cache-Control": []string{"max-age=60"}}, ""), nil
		})
		client, err := agent.NewServiceClient(agent.ServiceClientOptions{CachePartition: "test", HTTPClient: &http.Client{Transport: transport}, ServiceURL: "https://service.example"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := client.Inspect(t.Context()); err != nil {
			t.Fatal(err)
		}
		inspection, err := client.Inspect(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if strings.Join(paths, ",") != "/.well-known/odp,/discovery/odp,/discovery/odp" || inspection.FinalURL != "https://service.example/discovery/odp" {
			t.Fatalf("paths = %v, final URL = %q", paths, inspection.FinalURL)
		}
	})

	t.Run("Age reduces remaining freshness", func(t *testing.T) {
		var requests atomic.Int64
		transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
			requests.Add(1)
			headers := make(http.Header)
			headers.Set("Age", "60")
			headers.Set("Cache-Control", "max-age=60")
			return response(http.StatusOK, documentJSON("get-offering", "list-offerings"), headers, service.MediaType), nil
		})
		client, err := agent.NewServiceClient(agent.ServiceClientOptions{CachePartition: "test", HTTPClient: &http.Client{Transport: transport}, ServiceURL: "https://service.example"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := client.Inspect(t.Context()); err != nil {
			t.Fatal(err)
		}
		if _, err := client.Inspect(t.Context()); err != nil {
			t.Fatal(err)
		}
		if requests.Load() != 2 {
			t.Fatalf("requests = %d", requests.Load())
		}
	})
}

func TestServiceClientPartitionsSharedCacheByAccessContext(t *testing.T) {
	var requests atomic.Int64
	transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
		requests.Add(1)
		return response(http.StatusOK, documentJSON("get-offering", "list-offerings"), nil, service.MediaType), nil
	})
	cache := agent.NewMemoryCache()
	for _, partition := range []string{"principal:alice", "principal:bob", "principal:alice"} {
		client, err := agent.NewServiceClient(agent.ServiceClientOptions{
			Cache: cache, CachePartition: partition, HTTPClient: &http.Client{Transport: transport}, ServiceURL: "https://service.example",
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := client.Inspect(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if requests.Load() != 2 {
		t.Fatalf("requests = %d, want 2", requests.Load())
	}
}

func TestServiceClientRejectsExcessiveResponseDepthAndProblemSize(t *testing.T) {
	t.Run("Service Document depth", func(t *testing.T) {
		deep := strings.Repeat("[", 9) + "0" + strings.Repeat("]", 9)
		body := strings.TrimSuffix(documentJSON("get-offering", "list-offerings"), "}") + `,"extension":` + deep + `}`
		transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
			return response(http.StatusOK, body, nil, service.MediaType), nil
		})
		client, err := agent.NewServiceClient(agent.ServiceClientOptions{CachePartition: "test", HTTPClient: &http.Client{Transport: transport}, ServiceURL: "https://service.example"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := client.Inspect(t.Context()); err == nil || !strings.Contains(err.Error(), "nesting-depth") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("Problem Details bytes", func(t *testing.T) {
		transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
			if request.URL.Path == "/.well-known/odp" {
				return response(http.StatusOK, documentJSON("get-offering", "list-offerings"), nil, service.MediaType), nil
			}
			return response(http.StatusInternalServerError, strings.Repeat("x", 16_385), nil, "text/plain"), nil
		})
		client, err := agent.NewServiceClient(agent.ServiceClientOptions{CachePartition: "test", HTTPClient: &http.Client{Transport: transport}, ServiceURL: "https://service.example"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := client.GetOffering(t.Context(), "gpu", odp.RepresentationFull); err == nil || !strings.Contains(err.Error(), "byte limit") {
			t.Fatalf("error = %v", err)
		}
	})
}

func TestServiceClientHonorsPageAndItemBounds(t *testing.T) {
	var listRequests atomic.Int64
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == "/.well-known/odp" {
			return response(http.StatusOK, documentJSON("list-offerings", "get-offering"), nil, service.MediaType), nil
		}
		listRequests.Add(1)
		return response(http.StatusOK, `{"odp_version":"1.0","items":[{"id":"gpu","name":"GPU"}],"next":"/odp/offerings?cursor=next"}`, nil, service.MediaType), nil
	})
	client, err := agent.NewServiceClient(agent.ServiceClientOptions{CachePartition: "test", HTTPClient: &http.Client{Transport: transport}, ServiceURL: "https://service.example"})
	if err != nil {
		t.Fatal(err)
	}
	items := 0
	for _, err := range client.ListOfferings(t.Context(), agent.ListOptions{MaxItems: 1, MaxPages: 1}) {
		if err != nil {
			t.Fatal(err)
		}
		items++
	}
	if items != 1 || listRequests.Load() != 1 {
		t.Fatalf("items = %d, requests = %d", items, listRequests.Load())
	}
}

func TestServiceClientResumesValidatedOfferingContinuation(t *testing.T) {
	var method string
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		method = request.Method
		return response(http.StatusOK, `{"odp_version":"1.0","items":[{"id":"gpu","name":"GPU"}]}`, nil, service.MediaType), nil
	})
	client, err := agent.NewServiceClient(agent.ServiceClientOptions{CachePartition: "test", HTTPClient: &http.Client{Transport: transport}, ServiceURL: "https://service.example"})
	if err != nil {
		t.Fatal(err)
	}
	pages := 0
	for page, err := range client.ContinueSearchOfferingPages(t.Context(), "/odp/offerings/search?cursor=opaque", agent.ContinuationOptions{MaxPages: 1}) {
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Items) != 1 || page.Items[0].ID != "gpu" {
			t.Fatalf("page = %#v", page)
		}
		pages++
	}
	if pages != 1 || method != http.MethodGet {
		t.Fatalf("pages = %d, method = %q", pages, method)
	}
	for _, err := range client.ContinueListOfferings(t.Context(), "https://other.example/odp/offerings?cursor=opaque", agent.ContinuationOptions{}) {
		if err == nil || !strings.Contains(err.Error(), "Service origin") {
			t.Fatalf("error = %v", err)
		}
	}
}

func newSearchService(t *testing.T, search func(context.Context, *odp.OfferingSearchRequest, service.CatalogRequest) (odp.OfferingPage[odp.Offering], error)) *service.Service {
	t.Helper()
	runtime, err := service.New(service.Options{
		Catalog: service.Catalog{
			GetOffering: func(context.Context, string, service.CatalogRequest) (*odp.Offering, error) { return nil, nil },
			ListOfferings: func(context.Context, service.CatalogRequest) (odp.Page[odp.Offering], error) {
				return odp.Page[odp.Offering]{Items: []odp.Offering{}, ODPVersion: odp.Version}, nil
			},
			SearchOfferings: search,
		},
		Document: serviceDocument(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return runtime
}

func serviceDocument() odp.ServiceDocument {
	return odp.ServiceDocument{
		Description: "Example catalog", HTTP: odp.HTTPConfiguration{EndpointBase: "/odp"},
		Language: "en", Localizations: []string{"en"}, Name: "Example",
	}
}

func documentJSON(operations ...string) string {
	descriptors := make([]map[string]string, len(operations))
	for index, operation := range operations {
		descriptors[index] = map[string]string{"authentication": "not-required", "name": operation}
	}
	value := map[string]any{
		"description": "Example catalog", "http": map[string]string{"endpoint_base": "/odp"},
		"language": "en", "localizations": []string{"en"}, "name": "Example",
		"odp_version": "1.0", "operations": descriptors,
	}
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func response(status int, body string, headers http.Header, contentType string) *http.Response {
	if headers == nil {
		headers = make(http.Header)
	}
	if contentType != "" {
		headers.Set("Content-Type", contentType)
	}
	return &http.Response{Body: io.NopCloser(strings.NewReader(body)), Header: headers, StatusCode: status}
}

func first[Value any](sequence func(func(Value, error) bool)) (Value, error) {
	var value Value
	var result error
	sequence(func(item Value, err error) bool {
		value = item
		result = err
		return false
	})
	return value, result
}
