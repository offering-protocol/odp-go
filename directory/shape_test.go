package directory_test

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/offering-protocol/odp-go/directory"
)

type failingBody struct{}

func (failingBody) Read([]byte) (int, error) { return 0, errors.New("connection reset by peer") }

func (failingBody) Close() error { return nil }

func TestUnreadableBodiesSurfaceTheirError(t *testing.T) {
	client := serve(t, func(*http.Request) (*http.Response, error) {
		headers := make(http.Header)
		headers.Set("Content-Type", "application/json")
		return &http.Response{Body: failingBody{}, Header: headers, StatusCode: http.StatusOK}, nil
	})
	_, err := collectPages(client.SearchServices(t.Context(), directory.SearchRequest{}, directory.IterationOptions{}).Responses)
	if err == nil || !strings.Contains(err.Error(), "connection reset") {
		t.Fatalf("error = %v", err)
	}
}

func TestFailureMessagesStayWithinTheirBudget(t *testing.T) {
	// A detail longer than the rune clamp is quoted up to the clamp and marked as cut short.
	long := serve(t, func(*http.Request) (*http.Response, error) {
		return withHeaders(http.StatusBadRequest, `{"detail":"`+strings.Repeat("d", 4_096)+`"}`,
			map[string]string{"Content-Type": "application/problem+json"}), nil
	})
	_, err := collectPages(long.SearchServices(t.Context(), directory.SearchRequest{}, directory.IterationOptions{}).Responses)
	if err == nil {
		t.Fatal("oversized detail accepted")
	}
	if want := "Directory request failed with HTTP 400: " + strings.Repeat("d", 2_048) + "…"; err.Error() != want {
		t.Fatalf("message = %q", err.Error())
	}
	var failure *directory.RequestError
	if !errors.As(err, &failure) || failure.Retryable {
		t.Fatalf("failure = %#v", failure)
	}

	// A body past the error byte limit is cut before it is decoded, which leaves it unparseable,
	// so the failure is reported without quoting anything from it.
	huge := serve(t, func(*http.Request) (*http.Response, error) {
		return withHeaders(http.StatusServiceUnavailable, `{"detail":"`+strings.Repeat("h", 32_768)+`"}`,
			map[string]string{"Content-Type": "application/json"}), nil
	})
	_, err = collectPages(huge.SearchServices(t.Context(), directory.SearchRequest{}, directory.IterationOptions{}).Responses)
	if err == nil || err.Error() != "Directory request failed with HTTP 503" {
		t.Fatalf("message = %v", err)
	}
	if !errors.As(err, &failure) || !failure.Retryable {
		t.Fatalf("failure = %#v", failure)
	}
}

func TestContinuationsAreCheckedBeforeTheyAreFollowed(t *testing.T) {
	cases := map[string]struct {
		next    string
		wantErr string
	}{
		"unparseable":  {next: "https://[::1/v1/services/search", wantErr: "parse Directory continuation"},
		"another port": {next: "https://api.inflowpay.ai:8443/v1/services/search", wantErr: "canonical origin"},
		"default port": {next: "HTTPS://API.inflowpay.ai:443/v1/services/search", wantErr: ""},
		"userinfo":     {next: "https://user@api.inflowpay.ai/v1/services/search", wantErr: "canonical origin"},
	}
	for name, test := range cases {
		pages := 0
		client := serve(t, func(*http.Request) (*http.Response, error) {
			pages++
			if pages > 1 {
				return response(http.StatusOK, `{"items":[]}`, nil), nil
			}
			return response(http.StatusOK, `{"items":[],"next":"`+test.next+`"}`, nil), nil
		})
		_, err := collectPages(client.SearchServices(t.Context(), directory.SearchRequest{}, directory.IterationOptions{}).Responses)
		switch {
		case test.wantErr == "" && err != nil:
			t.Errorf("%s: %v", name, err)
		case test.wantErr != "" && (err == nil || !strings.Contains(err.Error(), test.wantErr)):
			t.Errorf("%s: error = %v, want %q", name, err, test.wantErr)
		}
	}
}

func TestOptionalServiceMembersAreCheckedForTheirType(t *testing.T) {
	for name, members := range map[string]string{
		"documentation_url": `"documentation_url":7`,
		"keywords":          `"keywords":"gpu"`,
		"localizations":     `"localizations":"en"`,
		"status_url":        `"status_url":7`,
		"support_url":       `"support_url":7`,
		"website_url":       `"website_url":7`,
	} {
		client := serve(t, always(page(recordWith(members))))
		pages, err := collectPages(client.SearchServices(t.Context(), directory.SearchRequest{}, directory.IterationOptions{}).Responses)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(pages) != 1 || len(pages[0].Items) != 0 || len(pages[0].Issues) != 1 {
			t.Fatalf("%s: items = %d, issues = %v", name, len(pages[0].Items), pages[0].Issues)
		}
	}
}

func TestFacetShapesAreRejectedMemberByMember(t *testing.T) {
	oversized := make([]string, 0, 101)
	for index := range cap(oversized) {
		oversized = append(oversized, `{"value":{"name":"aep"},"count":`+string(rune('0'+index%10))+`}`)
	}
	for name, facets := range map[string]string{
		"more than a hundred entries": `"enrollment":[` + strings.Join(oversized, ",") + `]`,
		"entries of the wrong shape":  `"enrollment":{"name":"aep"}`,
		"a negative count":            `"enrollment":[{"value":{"name":"aep"},"count":-1}]`,
		"an enrollment with extras":   `"enrollment":[{"value":{"name":"aep","extra":1},"count":1}]`,
		"a payment with one member":   `"payments":[{"value":{"name":"mpp"},"count":1}]`,
		"a payment without a name":    `"payments":[{"value":{"authentication":"not-required","options":["inflow"]},"count":1}]`,
		"an option without an option": `"payment_options":[{"value":{"name":"mpp","other":"inflow"},"count":1}]`,
		"an option with three keys":   `"payment_options":[{"value":{"name":"mpp","option":"inflow","extra":1},"count":1}]`,
		"an option naming no scheme":  `"payment_options":[{"value":{"name":"tap","option":"inflow"},"count":1}]`,
	} {
		client := serve(t, always(`{"items":[],"facets":{`+facets+`}}`))
		if _, err := collectPages(client.SearchServices(t.Context(), directory.SearchRequest{}, directory.IterationOptions{}).Responses); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestSuggestionItemsMustBeText(t *testing.T) {
	client := serve(t, always(`{"items":{"first":"gpu"}}`))
	if _, err := client.SuggestServices(t.Context(), directory.SuggestionRequest{Prefix: "g"}); err == nil ||
		!strings.Contains(err.Error(), "suggestions are invalid") {
		t.Fatalf("error = %v", err)
	}
}
