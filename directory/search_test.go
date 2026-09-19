package directory_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/offering-protocol/odp-go/directory"
)

func mixedResult(kind string) map[string]any {
	var service map[string]any
	if err := json.Unmarshal([]byte(serviceResult), &service); err != nil {
		panic(err)
	}
	service["service_id"] = "ca0304cc-ab28-43e5-af94-7bdf11b40c6e"
	result := map[string]any{"type": kind, "service": service, "indexed_at": "2026-09-18T12:00:00Z"}
	if kind == "collection" {
		result["collection"] = map[string]any{"id": "Weather", "name": "Weather forecasts", "description": "Forecasts and conditions."}
	}
	return result
}

func mixedBody(t *testing.T, items ...any) string {
	t.Helper()
	encoded, err := json.Marshal(map[string]any{"items": items})
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func TestMixedSearchSequence(t *testing.T) {
	service := mixedResult("service")
	service["available_through"] = map[string]any{"service_id": "platform", "service_origin": "https://platform.example", "name": "Platform", "extra": true}
	service["extra"] = "retained"
	collection := mixedResult("collection")
	collection["collection"].(map[string]any)["extra"] = true
	unknown := map[string]any{"type": "future", "nested": map[string]any{"untouched": true}}
	var calls int
	value := client(t, func(request *http.Request) (*http.Response, error) {
		calls++
		if request.Method != "POST" || request.URL.String() != "https://sandbox.inflowpay.ai/v1/directory/search" {
			t.Fatalf("request = %s %s", request.Method, request.URL)
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		if string(body) != `{"filters":{"keywords":["weather"]},"limit":10,"query":"forecast","types":["service","collection"]}` {
			t.Fatalf("body = %s", body)
		}
		return response(200, mixedBody(t, service, collection, unknown, false), nil), nil
	}, directory.Sandbox)
	types := []string{"service", "collection"}
	keywords := []string{"weather"}
	issues := []directory.Issue{}
	search := value.Search(t.Context(), directory.DirectorySearchRequest{
		SearchRequest: directory.SearchRequest{Query: "forecast", Limit: 10, Filters: &directory.ServiceFilters{Keywords: keywords}}, Types: types,
	}, directory.IterationOptions{MaxItems: 2, OnIssue: func(issue directory.Issue) { issues = append(issues, issue) }})
	types[0], keywords[0] = "mutated", "mutated"
	if calls != 0 {
		t.Fatal("search was not lazy")
	}
	for result, err := range search.Responses {
		if err != nil || len(result.Items) != 3 || len(result.Issues) != 1 || result.Issues[0].Index != 3 || result.Issues[0].Scope != directory.IssueResult {
			t.Fatalf("result = %#v, %v", result, err)
		}
		if result.Items[0].AvailableThrough.Name != "Platform" || string(result.Items[0].Additional["extra"]) != `"retained"` || string(result.Items[0].AvailableThrough.Additional["extra"]) != "true" {
			t.Fatalf("Service attribution or additional fields = %#v", result.Items[0])
		}
		if result.Items[1].Collection.ID != "Weather" || result.Items[1].Service.ServiceID == "" || result.Items[1].IndexedAt.Equal(result.Items[1].Service.IndexedAt) || string(result.Items[1].Collection.Additional["extra"]) != "true" {
			t.Fatalf("Collection = %#v", result.Items[1])
		}
		if result.Items[2].Type != "future" || result.Items[2].Service != nil || !strings.Contains(string(result.Items[2].Raw), `"untouched":true`) {
			t.Fatalf("unknown = %#v", result.Items[2])
		}
	}
	count := 0
	for _, err := range search.Items {
		if err != nil {
			t.Fatal(err)
		}
		count++
	}
	if calls != 2 || count != 2 || len(issues) != 1 {
		t.Fatalf("calls=%d, count=%d, issues=%v", calls, count, issues)
	}
}

func TestMixedSearchRejectsMalformedKnownEntries(t *testing.T) {
	mutations := []func(map[string]any){
		func(v map[string]any) { v["type"] = "" },
		func(v map[string]any) { v["service"] = nil },
		func(v map[string]any) { delete(v["service"].(map[string]any), "service_id") },
		func(v map[string]any) { v["indexed_at"] = "yesterday" },
		func(v map[string]any) { delete(v, "indexed_at") },
		func(v map[string]any) { v["collection"] = false },
		func(v map[string]any) { v["collection"] = nil },
		func(v map[string]any) { v["collection"].(map[string]any)["id"] = "../outside" },
		func(v map[string]any) { v["collection"].(map[string]any)["name"] = "" },
		func(v map[string]any) { v["collection"].(map[string]any)["description"] = nil },
		func(v map[string]any) { v["collection"].(map[string]any)["description"] = 1 },
		func(v map[string]any) { v["collection"].(map[string]any)["description"] = strings.Repeat("x", 1025) },
	}
	for index, mutate := range mutations {
		item := mixedResult("collection")
		mutate(item)
		value := client(t, func(*http.Request) (*http.Response, error) {
			return response(200, mixedBody(t, item, mixedResult("collection")), nil), nil
		}, directory.Production)
		for result, err := range value.Search(t.Context(), directory.DirectorySearchRequest{}, directory.IterationOptions{}).Responses {
			if err != nil || len(result.Items) != 1 || len(result.Issues) != 1 || result.Issues[0].Index != 0 {
				t.Fatalf("case %d: %#v, %v", index, result, err)
			}
		}
	}
	for _, reference := range []any{nil, false, map[string]any{}, map[string]any{"service_id": "x"}, map[string]any{"service_id": "x", "service_origin": "http://localhost"}, map[string]any{"service_id": "x", "service_origin": "https://platform.example", "name": nil}} {
		item := mixedResult("service")
		item["available_through"] = reference
		value := client(t, func(*http.Request) (*http.Response, error) {
			return response(200, mixedBody(t, item), nil), nil
		}, directory.Production)
		for result, err := range value.Search(t.Context(), directory.DirectorySearchRequest{}, directory.IterationOptions{}).Responses {
			if err != nil || len(result.Items) != 0 || len(result.Issues) != 1 {
				t.Fatalf("reference %v: %#v, %v", reference, result, err)
			}
		}
	}
}

func TestMixedSearchOptionalFields(t *testing.T) {
	for _, description := range []any{"", nil} {
		item := mixedResult("collection")
		if description == nil {
			delete(item["collection"].(map[string]any), "description")
		} else {
			item["collection"].(map[string]any)["description"] = description
		}
		service := mixedResult("service")
		service["available_through"] = map[string]any{"service_id": "x", "service_origin": "https://platform.example"}
		value := client(t, func(*http.Request) (*http.Response, error) {
			return response(200, mixedBody(t, item, service), nil), nil
		}, directory.Production)
		for result, err := range value.Search(t.Context(), directory.DirectorySearchRequest{}, directory.IterationOptions{}).Responses {
			if err != nil || len(result.Items) != 2 || len(result.Issues) != 0 {
				t.Fatalf("result = %#v, %v", result, err)
			}
		}
	}
}

func TestMixedSearchBoundsAndContinuation(t *testing.T) {
	var methods []string
	value := client(t, func(request *http.Request) (*http.Response, error) {
		methods = append(methods, request.Method)
		return response(200, `{"items":[],"next":"/v1/directory/search?cursor=opaque","facets":{"keywords":[{"value":"weather","count":12}]}}`, nil), nil
	}, directory.Production)
	search := value.Search(t.Context(), directory.DirectorySearchRequest{}, directory.IterationOptions{MaxPages: 1})
	var next string
	for result, err := range search.Responses {
		if err != nil || result.Facets.Keywords[0].Count != 12 {
			t.Fatalf("result=%#v, err=%v", result, err)
		}
		next = result.Next
	}
	for _, err := range value.ContinueSearch(t.Context(), next, directory.IterationOptions{MaxPages: 1}).Responses {
		if err != nil {
			t.Fatal(err)
		}
	}
	if strings.Join(methods, ",") != "POST,GET" {
		t.Fatalf("methods=%v", methods)
	}
	foundLoop := false
	for _, err := range value.ContinueSearch(t.Context(), next, directory.IterationOptions{}).Items {
		if err != nil {
			foundLoop = strings.Contains(err.Error(), "loop")
		}
	}
	if !foundLoop {
		t.Fatal("loop was not reported")
	}
	before := len(methods)
	for _, err := range value.ContinueSearch(t.Context(), "https://elsewhere.example", directory.IterationOptions{}).Items {
		if err == nil {
			t.Fatal("cross-origin continuation accepted")
		}
	}
	if len(methods) != before {
		t.Fatal("cross-origin request sent")
	}
}

func TestMixedSearchValidationBeforeTransport(t *testing.T) {
	value := client(t, func(*http.Request) (*http.Response, error) {
		t.Fatal("invalid request reached transport")
		return nil, nil
	}, directory.Production)
	for _, request := range []directory.DirectorySearchRequest{
		{Types: []string{}}, {Types: []string{"service", "service"}}, {Types: []string{"future"}},
		{Types: []string{"service", "collection", "service"}}, {SearchRequest: directory.SearchRequest{Limit: -1}},
	} {
		for _, err := range value.Search(t.Context(), request, directory.IterationOptions{}).Items {
			if err == nil {
				t.Fatal("invalid search accepted")
			}
		}
	}
	for _, options := range []directory.IterationOptions{{MaxItems: -1}, {MaxItems: 10_001}, {MaxPages: -1}} {
		for _, search := range []directory.SearchSequence[directory.Result]{value.Search(t.Context(), directory.DirectorySearchRequest{}, options), value.ContinueSearch(t.Context(), "/next", options)} {
			for _, err := range search.Items {
				if err == nil {
					t.Fatal("invalid options accepted")
				}
			}
		}
	}
}

func TestMixedSearchTransportAndCancellation(t *testing.T) {
	for _, body := range []string{`{}`, `{"items":null}`, `{"items":{}}`, `{"items":[],"facets":false}`} {
		value := client(t, func(*http.Request) (*http.Response, error) {
			return response(200, body, nil), nil
		}, directory.Production)
		for _, err := range value.Search(t.Context(), directory.DirectorySearchRequest{}, directory.IterationOptions{}).Items {
			if err == nil {
				t.Fatal("invalid envelope accepted")
			}
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	value := client(t, func(request *http.Request) (*http.Response, error) {
		return nil, request.Context().Err()
	}, directory.Production)
	for _, err := range value.Search(ctx, directory.DirectorySearchRequest{}, directory.IterationOptions{}).Items {
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation = %v", err)
		}
	}
}

func TestMixedSuggestionsUseSeparateEndpoint(t *testing.T) {
	var paths []string
	value := client(t, func(request *http.Request) (*http.Response, error) {
		paths = append(paths, request.URL.Path)
		if request.Method != "GET" || request.URL.Query().Get("prefix") != "we" || request.URL.Query().Get("limit") != "10" {
			t.Fatalf("request=%s %s", request.Method, request.URL)
		}
		return response(200, `{"items":["AccuWeather","Atlas","Atlas"]}`, nil), nil
	}, directory.Production)
	for _, suggest := range []func(context.Context, directory.SuggestionRequest) ([]string, error){value.Suggest, value.SuggestServices} {
		items, err := suggest(t.Context(), directory.SuggestionRequest{Prefix: "we", Limit: 10})
		if err != nil || strings.Join(items, ",") != "AccuWeather,Atlas" {
			t.Fatalf("items=%v, err=%v", items, err)
		}
	}
	if strings.Join(paths, ",") != "/v1/directory/suggestions,/v1/services/suggestions" {
		t.Fatalf("paths=%v", paths)
	}
}
