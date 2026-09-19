package directory_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/offering-protocol/odp-go/directory"
)

func withHeaders(status int, body string, values map[string]string) *http.Response {
	headers := make(http.Header)
	for name, value := range values {
		headers.Set(name, value)
	}
	return response(status, body, headers)
}

func TestResponsesAreRejectedOnTheirWireShape(t *testing.T) {
	cases := map[string]struct {
		build   func() *http.Response
		wantErr string
	}{
		"declared over the limit": {
			build: func() *http.Response {
				result := response(http.StatusOK, `{"items":[]}`, nil)
				result.ContentLength = 524_289
				return result
			},
			wantErr: "byte limit",
		},
		"wrong media type": {
			build: func() *http.Response {
				return withHeaders(http.StatusOK, `{"items":[]}`, map[string]string{"Content-Type": "text/plain"})
			},
			wantErr: "application/json",
		},
		"absent media type": {
			build: func() *http.Response {
				return &http.Response{Body: http.NoBody, Header: http.Header{}, StatusCode: http.StatusOK}
			},
			wantErr: "application/json",
		},
		"not UTF-8": {
			build:   func() *http.Response { return response(http.StatusOK, string([]byte{0xff, 0xfe}), nil) },
			wantErr: "UTF-8",
		},
		"not JSON": {
			build:   func() *http.Response { return response(http.StatusOK, "{not json", nil) },
			wantErr: "valid JSON",
		},
		"two JSON values": {
			build:   func() *http.Response { return response(http.StatusOK, `{"items":[]} {"items":[]}`, nil) },
			wantErr: "one JSON value",
		},
	}
	for name, test := range cases {
		client := serve(t, func(*http.Request) (*http.Response, error) { return test.build(), nil })
		_, err := collectPages(client.SearchServices(t.Context(), directory.SearchRequest{}, directory.IterationOptions{}).Responses)
		if err == nil || !strings.Contains(err.Error(), test.wantErr) {
			t.Errorf("%s: error = %v, want %q", name, err, test.wantErr)
		}
	}
}

func TestRedirectsRewriteTheMethodTheirStatusRequires(t *testing.T) {
	for _, test := range []struct {
		status int
		want   string
	}{
		{status: http.StatusMovedPermanently, want: http.MethodGet},
		{status: http.StatusFound, want: http.MethodGet},
		{status: http.StatusSeeOther, want: http.MethodGet},
		{status: http.StatusTemporaryRedirect, want: http.MethodPost},
		{status: http.StatusPermanentRedirect, want: http.MethodPost},
	} {
		var followed string
		var body []byte
		client := serve(t, func(request *http.Request) (*http.Response, error) {
			if request.URL.Path == "/v1/services/search" {
				return withHeaders(test.status, "", map[string]string{"Location": "/v1/moved"}), nil
			}
			followed = request.Method
			if request.Body != nil {
				buffer := make([]byte, 64)
				read, _ := request.Body.Read(buffer)
				body = buffer[:read]
			}
			return response(http.StatusOK, `{"items":[]}`, nil), nil
		})
		if _, err := collectPages(client.SearchServices(t.Context(), directory.SearchRequest{Query: "gpu"}, directory.IterationOptions{}).Responses); err != nil {
			t.Fatalf("status %d: %v", test.status, err)
		}
		if followed != test.want {
			t.Errorf("status %d followed with %s, want %s", test.status, followed, test.want)
		}
		// A rewritten GET carries no body; a preserved POST carries the original one.
		if (test.want == http.MethodPost) != (len(body) > 0) {
			t.Errorf("status %d body = %q with method %s", test.status, body, followed)
		}
	}
}

func TestRedirectsAreBoundedAndOriginLocked(t *testing.T) {
	cases := map[string]struct {
		location string
		wantErr  string
	}{
		"absent Location":  {location: "", wantErr: "omitted Location"},
		"another origin":   {location: "https://evil.example/v1", wantErr: "changed origin"},
		"scheme relative":  {location: "//evil.example/v1", wantErr: "changed origin"},
		"carries userinfo": {location: "https://user@api.inflowpay.ai/v1", wantErr: "changed origin"},
		"endless":          {location: "/v1/again", wantErr: "redirect limit"},
	}
	for name, test := range cases {
		client := serve(t, func(*http.Request) (*http.Response, error) {
			values := map[string]string{}
			if test.location != "" {
				values["Location"] = test.location
			}
			return withHeaders(http.StatusFound, "", values), nil
		})
		_, err := collectPages(client.SearchServices(t.Context(), directory.SearchRequest{}, directory.IterationOptions{}).Responses)
		if err == nil || !strings.Contains(err.Error(), test.wantErr) {
			t.Errorf("%s: error = %v, want %q", name, err, test.wantErr)
		}
	}
	// Five hops are followed; the sixth request is the one that answers.
	hops := 0
	client := serve(t, func(*http.Request) (*http.Response, error) {
		hops++
		if hops <= 5 {
			return withHeaders(http.StatusFound, "", map[string]string{"Location": fmt.Sprintf("/v1/hop/%d", hops)}), nil
		}
		return response(http.StatusOK, `{"items":[]}`, nil), nil
	})
	if _, err := collectPages(client.SearchServices(t.Context(), directory.SearchRequest{}, directory.IterationOptions{}).Responses); err != nil {
		t.Fatalf("five redirects: %v", err)
	}
	if hops != 6 {
		t.Fatalf("requests = %d, want six", hops)
	}
}

func TestRunawayTraversalIsReportedRatherThanTruncated(t *testing.T) {
	requests := 0
	client := serve(t, func(*http.Request) (*http.Response, error) {
		requests++
		return response(http.StatusOK, fmt.Sprintf(`{"items":[],"next":"/v1/services/search?cursor=%d"}`, requests), nil), nil
	})
	// A directory that never stops offering a continuation is a runaway, not an exhausted search,
	// so the guard reports rather than ending the sequence as though the results ran out.
	_, err := collectPages(client.SearchServices(t.Context(), directory.SearchRequest{}, directory.IterationOptions{MaxPages: 10_000}).Responses)
	if err == nil || !strings.Contains(err.Error(), "traversal limit") {
		t.Fatalf("error = %v after %d requests", err, requests)
	}
	if requests != 10_000 {
		t.Fatalf("requests = %d", requests)
	}
}

func TestCallerPageBudgetEndsTheSequenceQuietly(t *testing.T) {
	requests := 0
	client := serve(t, func(*http.Request) (*http.Response, error) {
		requests++
		return response(http.StatusOK, fmt.Sprintf(`{"items":[],"next":"/v1/services/search?cursor=%d"}`, requests), nil), nil
	})
	pages, err := collectPages(client.SearchServices(t.Context(), directory.SearchRequest{}, directory.IterationOptions{MaxPages: 2}).Responses)
	if err != nil || len(pages) != 2 || requests != 2 {
		t.Fatalf("pages = %d, requests = %d, err = %v", len(pages), requests, err)
	}
}

func TestCallersCanStopEarly(t *testing.T) {
	requests := 0
	client := serve(t, func(*http.Request) (*http.Response, error) {
		requests++
		return response(http.StatusOK, `{"items":[`+record+`],"next":"/v1/services/search?cursor=c"}`, nil), nil
	})
	count := 0
	for range client.SearchServices(t.Context(), directory.SearchRequest{}, directory.IterationOptions{}).Responses {
		count++
		break
	}
	if count != 1 || requests != 1 {
		t.Fatalf("pages = %d, requests = %d", count, requests)
	}
	services := 0
	for range client.SearchServices(t.Context(), directory.SearchRequest{}, directory.IterationOptions{}).Items {
		services++
		break
	}
	if services != 1 {
		t.Fatalf("services = %d", services)
	}
}

func TestSuggestionRequestsCarryTheirLimit(t *testing.T) {
	var target string
	client := serve(t, func(request *http.Request) (*http.Response, error) {
		target = request.URL.String()
		return response(http.StatusOK, `{"items":["gpu"]}`, nil), nil
	})
	if _, err := client.SuggestServices(t.Context(), directory.SuggestionRequest{Limit: 5, Prefix: "g pu&"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(target, "prefix=g+pu%26") || !strings.Contains(target, "limit=5") {
		t.Fatalf("target = %q", target)
	}
	failing := serve(t, func(*http.Request) (*http.Response, error) {
		return withHeaders(http.StatusInternalServerError, `{"detail":"boom"}`, map[string]string{"Content-Type": "application/json"}), nil
	})
	if _, err := failing.SuggestServices(t.Context(), directory.SuggestionRequest{Prefix: "g"}); err == nil {
		t.Fatal("failed suggestion accepted")
	}
}
