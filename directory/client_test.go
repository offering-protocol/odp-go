package directory_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	odp "github.com/offering-protocol/odp-go"
	"github.com/offering-protocol/odp-go/directory"
)

const serviceResult = `{
  "service_origin": "https://compute.example",
  "name": "Compute",
  "description": "GPU compute",
  "documentation_url": "/developers/",
  "language": "en",
  "localizations": ["en"],
  "keywords": ["gpu"],
  "operations": [{"authentication":"not-required","name":"get-offering"},{"authentication":"not-required","name":"list-offerings"}],
  "protocols": {"payments": [{"authentication":"not-required","name":"mpp","options":["inflow","solana"]}]},
  "indexed_at": "2026-08-02T00:00:00Z",
  "status_url": "https://status.compute.example/",
  "support_url": "/support/",
  "website_url": "/compute/"
}`

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func response(status int, body string, headers http.Header) *http.Response {
	if headers == nil {
		headers = make(http.Header)
	}
	if headers.Get("Content-Type") == "" {
		headers.Set("Content-Type", "application/json")
	}
	return &http.Response{
		StatusCode: status,
		Header:     headers,
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func client(t *testing.T, transport roundTripFunc, environment directory.Environment) *directory.Client {
	t.Helper()
	value, err := directory.New(directory.Options{
		Environment: environment,
		HTTPClient:  &http.Client{Transport: transport},
	})
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestSearchPagesUsesCanonicalOriginAndStructuredFilters(t *testing.T) {
	var target string
	var method string
	var body string
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		target = request.URL.String()
		method = request.Method
		contents, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		body = string(contents)
		return response(http.StatusOK, `{"items":[`+serviceResult+`],"facets":{"enrollment":[{"value":{"name":"aep"},"count":1}],"keywords":[{"value":"gpu","count":1}],"operations":[{"value":{"authentication":"required","name":"get-offering"},"count":1}],"payment_options":[{"value":{"name":"mpp","option":"inflow"},"count":1},{"value":{"name":"mpp","option":"solana"},"count":1}],"payments":[{"value":{"authentication":"not-required","name":"mpp","options":["inflow","solana"]},"count":1}]}}`, nil), nil
	})

	pages := client(t, transport, directory.Production).SearchPages(t.Context(), directory.SearchRequest{
		Query: "compute",
		Filters: &directory.ServiceFilters{
			Enrollment: []odp.EnrollmentProtocol{{Name: odp.ProtocolAEP}},
			Keywords:   []string{"gpu", "accelerator"},
			Operations: []directory.OperationFilter{{Authentication: odp.AuthenticationRequired, Name: odp.OperationGetOffering}},
			Payments: []directory.PaymentFilter{{
				Name: odp.ProtocolMPP, Options: []odp.PaymentOption{odp.PaymentOptionInflow, odp.PaymentOptionSolana},
			}},
		},
		Limit: 25,
	}, directory.IterationOptions{})
	page, err := first(pages)
	if err != nil {
		t.Fatal(err)
	}
	if target != "https://api.inflowpay.ai/v1/services/search" {
		t.Fatalf("target = %q", target)
	}
	if method != http.MethodPost {
		t.Fatalf("method = %q", method)
	}
	wantBody := `{"filters":{"enrollment":[{"name":"aep"}],"keywords":["gpu","accelerator"],"operations":[{"authentication":"required","name":"get-offering"}],"payments":[{"name":"mpp","options":["inflow","solana"]}]},"limit":25,"query":"compute"}`
	if body != wantBody {
		t.Fatalf("body = %s, want %s", body, wantBody)
	}
	if len(page.Items) != 1 || page.Items[0].ServiceOrigin != "https://compute.example" {
		t.Fatalf("items = %#v", page.Items)
	}
	service := page.Items[0]
	if service.DocumentationURL != "/developers/" || service.StatusURL != "https://status.compute.example/" || service.SupportURL != "/support/" || service.WebsiteURL != "/compute/" {
		t.Fatalf("service links = %#v", service)
	}
	if page.Facets == nil || len(page.Facets.Keywords) != 1 || page.Facets.Keywords[0].Value != "gpu" || page.Facets.Keywords[0].Count != 1 {
		t.Fatalf("facets = %#v", page.Facets)
	}
	if len(page.Facets.Enrollment) != 1 || page.Facets.Enrollment[0].Value.Name != odp.ProtocolAEP || len(page.Facets.Operations) != 1 || page.Facets.Operations[0].Value.Authentication != odp.AuthenticationRequired || len(page.Facets.PaymentOptions) != 2 || page.Facets.PaymentOptions[0].Value.Option != odp.PaymentOptionInflow || len(page.Facets.Payments) != 1 || page.Facets.Payments[0].Value.Name != odp.ProtocolMPP || len(page.Facets.Payments[0].Value.Options) != 2 {
		t.Fatalf("descriptor facets = %#v", page.Facets)
	}
}

func TestSearchPagesUsesSandboxOnlyWhenSelected(t *testing.T) {
	var target string
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		target = request.URL.String()
		return response(http.StatusOK, `{"items":[]}`, nil), nil
	})
	value := client(t, transport, directory.Sandbox)
	if value.Environment() != directory.Sandbox {
		t.Fatalf("environment = %q", value.Environment())
	}
	if _, err := first(value.SearchPages(t.Context(), directory.SearchRequest{}, directory.IterationOptions{})); err != nil {
		t.Fatal(err)
	}
	if target != "https://sandbox.inflowpay.ai/v1/services/search" {
		t.Fatalf("target = %q", target)
	}
}

func TestSearchPagesFiltersUnknownProtocols(t *testing.T) {
	result := strings.Replace(serviceResult, `"protocols": {"payments": [{"authentication":"not-required","name":"mpp","options":["inflow","solana"]}]}`, `"protocols":{"enrollment":[{"name":"future-enrollment"}],"payments":[{"authentication":"not-required","name":"future-payment"},{"authentication":"not-required","name":"mpp"}],"trust":[{"name":"future-trust"},{"name":"tap"}]}`, 1)
	transport := roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return response(http.StatusOK, `{"items":[`+result+`]}`, nil), nil
	})
	page, err := first(client(t, transport, directory.Production).SearchPages(t.Context(), directory.SearchRequest{}, directory.IterationOptions{}))
	if err != nil {
		t.Fatal(err)
	}
	protocols := page.Items[0].Protocols
	if protocols == nil || len(protocols.Enrollment) != 0 || len(protocols.Payments) != 1 || protocols.Payments[0].Name != odp.ProtocolMPP || len(protocols.Trust) != 1 || protocols.Trust[0].Name != odp.ProtocolTAP {
		t.Fatalf("protocols = %#v", protocols)
	}
}

func TestSearchPagesRejectsMalformedKnownProtocol(t *testing.T) {
	result := strings.Replace(serviceResult, `"name":"mpp","options"`, `"name":"mpp","unexpected":true,"options"`, 1)
	transport := roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return response(http.StatusOK, `{"items":[`+result+`]}`, nil), nil
	})
	_, err := first(client(t, transport, directory.Production).SearchPages(t.Context(), directory.SearchRequest{}, directory.IterationOptions{}))
	if err == nil {
		t.Fatal("Directory accepted a malformed recognized protocol descriptor")
	}
}

func TestSearchServicesFollowsOpaqueContinuationWithGet(t *testing.T) {
	var methods []string
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		methods = append(methods, request.Method)
		if request.URL.Query().Has("cursor") {
			return response(http.StatusOK, `{"items":[`+strings.Replace(serviceResult, "https://compute.example", "https://storage.example", 1)+`]}`, nil), nil
		}
		return response(http.StatusOK, `{"items":[`+serviceResult+`],"next":"/v1/services/search?cursor=opaque"}`, nil), nil
	})
	var origins []string
	for service, err := range client(t, transport, directory.Production).SearchServices(t.Context(), directory.SearchRequest{}, directory.IterationOptions{}) {
		if err != nil {
			t.Fatal(err)
		}
		origins = append(origins, service.ServiceOrigin)
	}
	if strings.Join(origins, ",") != "https://compute.example,https://storage.example" {
		t.Fatalf("origins = %v", origins)
	}
	if strings.Join(methods, ",") != "POST,GET" {
		t.Fatalf("methods = %v", methods)
	}
}

func TestSuggestServicesUsesSelectedEnvironment(t *testing.T) {
	var target string
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		target = request.URL.String()
		return response(http.StatusOK, `{"items":["gpu","gpu compute"]}`, nil), nil
	})
	values, err := client(t, transport, directory.Sandbox).SuggestServices(t.Context(), directory.SuggestionRequest{Prefix: "gp", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(values, ",") != "gpu,gpu compute" {
		t.Fatalf("suggestions = %v", values)
	}
	if target != "https://sandbox.inflowpay.ai/v1/services/suggestions?prefix=gp&limit=5" {
		t.Fatalf("target = %q", target)
	}
}

func TestSuggestServicesAcceptsEmptyResult(t *testing.T) {
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return response(http.StatusOK, `{"items":[]}`, nil), nil
	})
	values, err := client(t, transport, directory.Production).SuggestServices(t.Context(), directory.SuggestionRequest{Prefix: "unmatched"})
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 0 {
		t.Fatalf("suggestions = %v", values)
	}
}

func TestSearchRejectsCrossOriginContinuation(t *testing.T) {
	transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
		return response(http.StatusOK, `{"items":[],"next":"https://other.example/search"}`, nil), nil
	})
	var received error
	for _, err := range client(t, transport, directory.Production).SearchPages(t.Context(), directory.SearchRequest{}, directory.IterationOptions{}) {
		if err != nil {
			received = err
		}
	}
	if received == nil || !strings.Contains(received.Error(), "canonical origin") {
		t.Fatalf("error = %v", received)
	}
}

func TestRequestErrorPreservesResponseDetails(t *testing.T) {
	headers := make(http.Header)
	headers.Set("Retry-After", "30")
	transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
		return response(http.StatusServiceUnavailable, "Unavailable", headers), nil
	})
	_, err := first(client(t, transport, directory.Production).SearchPages(t.Context(), directory.SearchRequest{}, directory.IterationOptions{}))
	var requestError *directory.RequestError
	if !errors.As(err, &requestError) {
		t.Fatalf("error = %v", err)
	}
	if requestError.Status != http.StatusServiceUnavailable || requestError.Message != "Unavailable" || requestError.Header.Get("Retry-After") != "30" {
		t.Fatalf("request error = %#v", requestError)
	}
}

func TestSearchStopsReadingOversizedResponse(t *testing.T) {
	transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
		return response(http.StatusOK, strings.Repeat("x", 524_289), nil), nil
	})
	_, err := first(client(t, transport, directory.Production).SearchPages(t.Context(), directory.SearchRequest{}, directory.IterationOptions{}))
	if err == nil || !strings.Contains(err.Error(), "byte limit") {
		t.Fatalf("error = %v", err)
	}
}

func TestRedirectPolicy(t *testing.T) {
	t.Run("same origin", func(t *testing.T) {
		var methods []string
		transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
			methods = append(methods, request.Method)
			if len(methods) == 1 {
				headers := make(http.Header)
				headers.Set("Location", "/v1/services/search?redirected=true")
				return response(http.StatusFound, "", headers), nil
			}
			return response(http.StatusOK, `{"items":[]}`, nil), nil
		})
		if _, err := first(client(t, transport, directory.Production).SearchPages(t.Context(), directory.SearchRequest{}, directory.IterationOptions{})); err != nil {
			t.Fatal(err)
		}
		if strings.Join(methods, ",") != "POST,GET" {
			t.Fatalf("methods = %v", methods)
		}
	})

	t.Run("cross origin", func(t *testing.T) {
		transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
			headers := make(http.Header)
			headers.Set("Location", "https://other.example/search")
			return response(http.StatusTemporaryRedirect, "", headers), nil
		})
		_, err := first(client(t, transport, directory.Production).SearchPages(t.Context(), directory.SearchRequest{}, directory.IterationOptions{}))
		if err == nil || !strings.Contains(err.Error(), "changed origin") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("limit", func(t *testing.T) {
		transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
			headers := make(http.Header)
			headers.Set("Location", "/again")
			return response(http.StatusTemporaryRedirect, "", headers), nil
		})
		_, err := first(client(t, transport, directory.Production).SearchPages(t.Context(), directory.SearchRequest{}, directory.IterationOptions{}))
		if err == nil || !strings.Contains(err.Error(), "redirect limit") {
			t.Fatalf("error = %v", err)
		}
	})
}

func TestSearchValidation(t *testing.T) {
	tests := []struct {
		name    string
		request directory.SearchRequest
		options directory.IterationOptions
	}{
		{name: "empty keywords", request: directory.SearchRequest{Filters: &directory.ServiceFilters{Keywords: []string{}}}},
		{name: "empty operations", request: directory.SearchRequest{Filters: &directory.ServiceFilters{Operations: []directory.OperationFilter{}}}},
		{name: "unsupported payment", request: directory.SearchRequest{Filters: &directory.ServiceFilters{Payments: []directory.PaymentFilter{{Name: "other"}}}}},
		{name: "unsupported payment option", request: directory.SearchRequest{Filters: &directory.ServiceFilters{Payments: []directory.PaymentFilter{{Name: odp.ProtocolMPP, Options: []odp.PaymentOption{"future-option"}}}}}},
		{name: "duplicate payment option", request: directory.SearchRequest{Filters: &directory.ServiceFilters{Payments: []directory.PaymentFilter{{Name: odp.ProtocolMPP, Options: []odp.PaymentOption{odp.PaymentOptionSolana, odp.PaymentOptionSolana}}}}}},
		{name: "limit", request: directory.SearchRequest{Limit: 101}},
		{name: "max items", options: directory.IterationOptions{MaxItems: 10_001}},
		{name: "max pages", options: directory.IterationOptions{MaxPages: 17}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
				t.Fatal("transport called for invalid input")
				return nil, nil
			})
			_, err := first(client(t, transport, directory.Production).SearchServices(t.Context(), test.request, test.options))
			if err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestSearchPreservesAdditiveMembersAndHonorsItemLimit(t *testing.T) {
	transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
		firstService := strings.Replace(serviceResult, `"indexed_at":`, `"rank": 1, "indexed_at":`, 1)
		secondService := strings.Replace(serviceResult, "https://compute.example", "https://storage.example", 1)
		return response(http.StatusOK, `{"trace":"abc","items":[`+firstService+`,`+secondService+`]}`, nil), nil
	})
	var services []directory.Service
	for service, err := range client(t, transport, directory.Production).SearchServices(t.Context(), directory.SearchRequest{}, directory.IterationOptions{MaxItems: 1}) {
		if err != nil {
			t.Fatal(err)
		}
		services = append(services, service)
	}
	if len(services) != 1 || string(services[0].Additional["rank"]) != "1" {
		t.Fatalf("services = %#v", services)
	}
}

func TestSearchUsesContext(t *testing.T) {
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		<-request.Context().Done()
		return nil, request.Context().Err()
	})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := first(client(t, transport, directory.Production).SearchPages(ctx, directory.SearchRequest{}, directory.IterationOptions{}))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
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
