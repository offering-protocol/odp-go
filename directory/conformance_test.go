package directory_test

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	odp "github.com/offering-protocol/odp-go"
	"github.com/offering-protocol/odp-go/directory"
)

// record is a minimal Directory Service result; members are spliced in by the tests that need them.
const record = `{"service_origin":"https://compute.example","name":"Compute","description":"Accelerators",` +
	`"language":"en","localizations":["en"],"indexed_at":"2026-01-01T00:00:00Z",` +
	`"operations":[{"authentication":"not-required","name":"list-offerings"},{"authentication":"not-required","name":"get-offering"}]}`

func recordWith(members string) string {
	return strings.TrimSuffix(record, "}") + "," + members + "}"
}

func page(body string) string { return `{"items":[` + body + `]}` }

// serve builds a client whose every request is answered by handler.
func serve(t *testing.T, handler func(*http.Request) (*http.Response, error)) *directory.Client {
	t.Helper()
	client, err := directory.New(directory.Options{HTTPClient: &http.Client{Transport: roundTripFunc(handler)}})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

// always answers every request with one JSON body.
func always(body string) func(*http.Request) (*http.Response, error) {
	return func(*http.Request) (*http.Response, error) {
		return response(http.StatusOK, body, nil), nil
	}
}

func collectPages(sequence func(func(directory.SearchResponse[directory.Service], error) bool)) ([]directory.SearchResponse[directory.Service], error) {
	var pages []directory.SearchResponse[directory.Service]
	var failure error
	sequence(func(value directory.SearchResponse[directory.Service], err error) bool {
		if err != nil {
			failure = err
			return false
		}
		pages = append(pages, value)
		return true
	})
	return pages, failure
}

func collectServices(sequence func(func(directory.Service, error) bool)) ([]directory.Service, error) {
	var services []directory.Service
	var failure error
	sequence(func(value directory.Service, err error) bool {
		if err != nil {
			failure = err
			return false
		}
		services = append(services, value)
		return true
	})
	return services, failure
}

func TestServiceOriginsMustBePublicAndSecure(t *testing.T) {
	// A directory advertises origins it does not control, so an origin a caller might dereference
	// is held to more than the syntax a locally chosen Service URL is held to.
	refused := []string{
		"http://localhost:3000", "https://localhost", "https://api.localhost",
		"http://127.0.0.1:8080", "https://127.0.0.1", "https://[::1]",
		"https://169.254.169.254", "https://10.0.0.5", "https://192.168.1.1",
		"https://172.16.0.1", "https://100.64.0.1", "https://[fc00::1]", "https://[fe80::1]",
	}
	for _, origin := range refused {
		body := page(strings.Replace(record, "https://compute.example", origin, 1))
		pages, err := collectPages(serve(t, always(body)).SearchServices(t.Context(), directory.SearchRequest{}, directory.IterationOptions{}).Responses)
		if err != nil {
			t.Fatalf("%s: %v", origin, err)
		}
		if len(pages) != 1 || len(pages[0].Items) != 0 || len(pages[0].Issues) != 1 {
			t.Errorf("%s: page = %#v", origin, pages[0])
		}
	}
	for _, origin := range []string{"https://compute.example", "https://8.8.8.8"} {
		body := page(strings.Replace(record, "https://compute.example", origin, 1))
		pages, err := collectPages(serve(t, always(body)).SearchServices(t.Context(), directory.SearchRequest{}, directory.IterationOptions{}).Responses)
		if err != nil || len(pages[0].Items) != 1 {
			t.Errorf("%s: page = %#v, %v", origin, pages, err)
		}
	}
}

func TestUnverifiedServiceDocumentMembersDoNotReachTheCaller(t *testing.T) {
	// ROLE-03: a directory supplies discovery metadata. Members that only the Service itself can
	// assert must not arrive looking like something the caller may act on.
	members := `"http":{"endpoint_base":"/evil"},"odp_version":"9.9","mcp":[{"name":"x","type":"streamable-http","url":"/mcp"}],` +
		`"payment_origins":["https://evil.example"],"search_capabilities":{"filters":{"inline":[]}},"branding":{},"rank":3`
	pages, err := collectPages(serve(t, always(page(recordWith(members)))).SearchServices(t.Context(), directory.SearchRequest{}, directory.IterationOptions{}).Responses)
	if err != nil || len(pages[0].Items) != 1 {
		t.Fatalf("pages = %#v, %v", pages, err)
	}
	additional := pages[0].Items[0].Additional
	if len(additional) != 1 {
		t.Fatalf("additional = %#v", additional)
	}
	if _, kept := additional["rank"]; !kept {
		t.Fatalf("a member the directory may assert was dropped: %#v", additional)
	}
}

func TestOneUnusableRecordDoesNotDiscardItsPage(t *testing.T) {
	broken := strings.Replace(record, `"name":"Compute"`, `"name":""`, 1)
	body := page(broken + "," + record + "," + strings.Replace(record, "compute.example", "storage.example", 1))
	pages, err := collectPages(serve(t, always(body)).SearchServices(t.Context(), directory.SearchRequest{}, directory.IterationOptions{}).Responses)
	if err != nil {
		t.Fatal(err)
	}
	if len(pages[0].Items) != 2 || len(pages[0].Issues) != 1 || pages[0].Issues[0].Index != 0 {
		t.Fatalf("page = %#v", pages[0])
	}
	// An item traversal cannot see the page, so the issues reach it through the callback.
	var seen []directory.Issue
	options := directory.IterationOptions{OnIssue: func(issue directory.Issue) { seen = append(seen, issue) }}
	services, err := collectServices(serve(t, always(body)).SearchServices(t.Context(), directory.SearchRequest{}, options).Items)
	if err != nil || len(services) != 2 || len(seen) != 1 {
		t.Fatalf("services = %d, issues = %#v, %v", len(services), seen, err)
	}
}

func TestPaginationBudgetsAndResumption(t *testing.T) {
	body := func(cursor int) string {
		return `{"items":[` + record + `],"next":"/v1/services/search?cursor=` + fmt.Sprint(cursor) + `"}`
	}
	requests := 0
	handler := func(*http.Request) (*http.Response, error) {
		requests++
		return response(http.StatusOK, body(requests), nil), nil
	}

	// A caller's own page budget stops the traversal without an error, and the last page keeps a
	// continuation to resume from.
	client := serve(t, handler)
	pages, err := collectPages(client.SearchServices(t.Context(), directory.SearchRequest{}, directory.IterationOptions{MaxPages: 3}).Responses)
	if err != nil || len(pages) != 3 || pages[2].Next == "" {
		t.Fatalf("pages = %d, err = %v", len(pages), err)
	}
	resumed, err := collectPages(client.ContinueSearchServices(t.Context(), pages[2].Next, directory.IterationOptions{MaxPages: 2}).Responses)
	if err != nil || len(resumed) != 2 {
		t.Fatalf("resumed = %d, err = %v", len(resumed), err)
	}
	services, err := collectServices(client.ContinueSearchServices(t.Context(), pages[2].Next, directory.IterationOptions{MaxItems: 2}).Items)
	if err != nil || len(services) != 2 {
		t.Fatalf("resumed services = %d, err = %v", len(services), err)
	}
	// A page budget above the old sixteen is honoured rather than refused.
	many, err := collectPages(client.SearchServices(t.Context(), directory.SearchRequest{}, directory.IterationOptions{MaxPages: 40}).Responses)
	if err != nil || len(many) != 40 {
		t.Fatalf("pages = %d, err = %v", len(many), err)
	}
	if _, err := collectPages(client.ContinueSearchServices(t.Context(), "https://evil.example/p", directory.IterationOptions{}).Responses); err == nil {
		t.Fatal("off-origin continuation resumed")
	}
	if _, err := collectServices(client.ContinueSearchServices(t.Context(), "x", directory.IterationOptions{MaxItems: -1}).Items); err == nil {
		t.Fatal("invalid item budget accepted")
	}
	if _, err := collectPages(client.ContinueSearchServices(t.Context(), "x", directory.IterationOptions{MaxPages: -1}).Responses); err == nil {
		t.Fatal("invalid page budget accepted")
	}
}

func TestItemBudgetOnAPageBoundaryDoesNotFetchAnotherPage(t *testing.T) {
	requests := 0
	client := serve(t, func(*http.Request) (*http.Response, error) {
		requests++
		return response(http.StatusOK, `{"items":[`+record+`,`+strings.Replace(record, "compute.example", "storage.example", 1)+`],"next":"/v1/services/search?cursor=next"}`, nil), nil
	})
	services, err := collectServices(client.SearchServices(t.Context(), directory.SearchRequest{}, directory.IterationOptions{MaxItems: 2}).Items)
	if err != nil || len(services) != 2 {
		t.Fatalf("services = %d, err = %v", len(services), err)
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want the budget met without pulling another page", requests)
	}
}

func TestContinuationsMustAdvanceAndStayOnOrigin(t *testing.T) {
	cases := map[string]struct {
		next    string
		wantErr string
	}{
		"repeats itself":   {next: "/v1/services/search", wantErr: "loop"},
		"another origin":   {next: "https://evil.example/v1", wantErr: "canonical origin"},
		"scheme relative":  {next: "//evil.example/v1", wantErr: "canonical origin"},
		"carries userinfo": {next: "https://user@api.inflowpay.ai/v1", wantErr: "canonical origin"},
		"alternating case": {next: "https://API.INFLOWPAY.AI/v1/services/search", wantErr: "loop"},
		"explicit 443":     {next: "https://api.inflowpay.ai:443/v1/services/search", wantErr: "loop"},
	}
	for name, test := range cases {
		client := serve(t, func(*http.Request) (*http.Response, error) {
			return response(http.StatusOK, `{"items":[`+record+`],"next":"`+test.next+`"}`, nil), nil
		})
		_, err := collectPages(client.SearchServices(t.Context(), directory.SearchRequest{}, directory.IterationOptions{}).Responses)
		if err == nil || !strings.Contains(err.Error(), test.wantErr) {
			t.Errorf("%s: error = %v, want %q", name, err, test.wantErr)
		}
	}
	// The default port and host case are spellings of the same origin, so a continuation that uses
	// them is followed rather than refused.
	page := 0
	client := serve(t, func(*http.Request) (*http.Response, error) {
		page++
		if page == 1 {
			return response(http.StatusOK, `{"items":[`+record+`],"next":"https://API.INFLOWPAY.AI:443/v1/services/search?cursor=c"}`, nil), nil
		}
		return response(http.StatusOK, `{"items":[`+record+`]}`, nil), nil
	})
	pages, err := collectPages(client.SearchServices(t.Context(), directory.SearchRequest{}, directory.IterationOptions{}).Responses)
	if err != nil || len(pages) != 2 {
		t.Fatalf("pages = %d, err = %v", len(pages), err)
	}
}

func TestFailureMessagesAreSafeToLog(t *testing.T) {
	cases := map[string]struct {
		body        string
		contentType string
		status      int
		want        string
	}{
		"problem detail": {
			body: `{"title":"Too Many Requests","detail":"slow down"}`, contentType: "application/problem+json",
			status: 429, want: "Directory request failed with HTTP 429: slow down",
		},
		"problem title only": {
			body: `{"title":"Too Many Requests"}`, contentType: "application/json",
			status: 429, want: "Directory request failed with HTTP 429: Too Many Requests",
		},
		"message member": {
			body: `{"message":"nope"}`, contentType: "application/json",
			status: 400, want: "Directory request failed with HTTP 400: nope",
		},
		"terminal escapes stripped": {
			body: "{\"detail\":\"a\\u001b[2Jb\\nc\"}", contentType: "application/json",
			status: 500, want: "Directory request failed with HTTP 500: a [2Jb c",
		},
		"html page is not quoted": {
			body: "<html><body>nginx</body></html>", contentType: "text/html",
			status: 502, want: "Directory request failed with HTTP 502",
		},
		"unparseable json": {
			body: "{not json", contentType: "application/json",
			status: 500, want: "Directory request failed with HTTP 500",
		},
		"no usable member": {
			body: `{"code":"X"}`, contentType: "application/json",
			status: 404, want: "Directory request failed with HTTP 404",
		},
		"empty body": {
			body: "", contentType: "", status: 503, want: "Directory request failed with HTTP 503",
		},
	}
	for name, test := range cases {
		headers := make(http.Header)
		if test.contentType != "" {
			headers.Set("Content-Type", test.contentType)
		}
		client := serve(t, func(*http.Request) (*http.Response, error) {
			return response(test.status, test.body, headers), nil
		})
		_, err := collectPages(client.SearchServices(t.Context(), directory.SearchRequest{}, directory.IterationOptions{}).Responses)
		var failure *directory.RequestError
		if !errors.As(err, &failure) {
			t.Errorf("%s: error = %v", name, err)
			continue
		}
		if failure.Error() != test.want {
			t.Errorf("%s: message = %q, want %q", name, failure.Error(), test.want)
		}
	}
}

func TestOverlongFailureDetailIsTruncated(t *testing.T) {
	headers := make(http.Header)
	headers.Set("Content-Type", "application/json")
	client := serve(t, func(*http.Request) (*http.Response, error) {
		return response(http.StatusBadRequest, `{"detail":"`+strings.Repeat("x", 4000)+`"}`, headers), nil
	})
	_, err := collectPages(client.SearchServices(t.Context(), directory.SearchRequest{}, directory.IterationOptions{}).Responses)
	if err == nil || !strings.HasSuffix(err.Error(), "…") || len([]rune(err.Error())) > 2_100 {
		t.Fatalf("message length = %d", len([]rune(err.Error())))
	}
}

func TestSuggestionsAreBoundedRatherThanRefused(t *testing.T) {
	items := make([]string, 40)
	for index := range items {
		items[index] = fmt.Sprintf(`"suggestion-%d"`, index%30)
	}
	client := serve(t, always(`{"items":[`+strings.Join(items, ",")+`]}`))
	// Bounding and de-duplicating what a directory returns is this client's job; asserting a limit
	// it never sent is not.
	suggestions, err := client.SuggestServices(t.Context(), directory.SuggestionRequest{Prefix: "sug"})
	if err != nil || len(suggestions) != 25 {
		t.Fatalf("suggestions = %d, %v", len(suggestions), err)
	}
	if suggestions[0] != "suggestion-0" || suggestions[1] != "suggestion-1" {
		t.Fatalf("order = %v", suggestions[:2])
	}
	duplicated := serve(t, always(`{"items":["a","a","b"]}`))
	suggestions, err = duplicated.SuggestServices(t.Context(), directory.SuggestionRequest{Prefix: "a", Limit: 5})
	if err != nil || strings.Join(suggestions, ",") != "a,b" {
		t.Fatalf("deduplicated = %v, %v", suggestions, err)
	}
	for name, body := range map[string]string{
		"null items":    `{"items":null}`,
		"absent items":  `{}`,
		"blank item":    `{"items":["  "]}`,
		"not an object": `[]`,
	} {
		if _, err := serve(t, always(body)).SuggestServices(t.Context(), directory.SuggestionRequest{Prefix: "a"}); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	for name, request := range map[string]directory.SuggestionRequest{
		"blank prefix": {Prefix: " "},
		"long prefix":  {Prefix: strings.Repeat("p", 129)},
		"limit high":   {Prefix: "a", Limit: 26},
		"limit low":    {Prefix: "a", Limit: -1},
	} {
		if _, err := serve(t, always(`{"items":[]}`)).SuggestServices(t.Context(), request); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestFacetMembersAreCheckedByName(t *testing.T) {
	// encoding/json matches member names case-insensitively, so counting members is not the same
	// as knowing which members arrived.
	body := `{"items":[],"facets":{"payment_options":[{"count":1,"value":{"NAME":"mpp","OPTION":"base"}}]}}`
	if _, err := collectPages(serve(t, always(body)).SearchServices(t.Context(), directory.SearchRequest{}, directory.IterationOptions{}).Responses); err == nil {
		t.Fatal("mis-cased payment option facet accepted")
	}
	valid := `{"items":[],"facets":{"payment_options":[{"count":1,"value":{"name":"mpp","option":"base"}}]}}`
	pages, err := collectPages(serve(t, always(valid)).SearchServices(t.Context(), directory.SearchRequest{}, directory.IterationOptions{}).Responses)
	if err != nil || pages[0].Facets == nil || len(pages[0].Facets.PaymentOptions) != 1 {
		t.Fatalf("facets = %#v, %v", pages, err)
	}
}

func TestFacetShapesAreValidated(t *testing.T) {
	cases := map[string]string{
		"facets not an object":   `{"items":[],"facets":[]}`,
		"items missing":          `{}`,
		"items null":             `{"items":null}`,
		"keyword facet blank":    `{"items":[],"facets":{"keywords":[{"count":1,"value":""}]}}`,
		"keyword count negative": `{"items":[],"facets":{"keywords":[{"count":-1,"value":"a"}]}}`,
		"keyword count decimal":  `{"items":[],"facets":{"keywords":[{"count":1.5,"value":"a"}]}}`,
		"keyword count string":   `{"items":[],"facets":{"keywords":[{"count":"5","value":"a"}]}}`,
		"enrollment unknown":     `{"items":[],"facets":{"enrollment":[{"count":1,"value":{"name":"other"}}]}}`,
		"operation unknown":      `{"items":[],"facets":{"operations":[{"count":1,"value":{"authentication":"not-required","name":"fly"}}]}}`,
		"operation extra member": `{"items":[],"facets":{"operations":[{"count":1,"value":{"authentication":"not-required","name":"list-offerings","x":1}}]}}`,
		"payment unknown":        `{"items":[],"facets":{"payments":[{"count":1,"value":{"authentication":"required","name":"other"}}]}}`,
		"payment extra member":   `{"items":[],"facets":{"payments":[{"count":1,"value":{"authentication":"required","name":"mpp","x":1}}]}}`,
		"payment missing member": `{"items":[],"facets":{"payments":[{"count":1,"value":{"name":"mpp","options":["base"]}}]}}`,
		"payment bad option":     `{"items":[],"facets":{"payments":[{"count":1,"value":{"authentication":"required","name":"mpp","options":["gold"]}}]}}`,
		"facet not an array":     `{"items":[],"facets":{"keywords":{}}}`,
	}
	for name, body := range cases {
		if _, err := collectPages(serve(t, always(body)).SearchServices(t.Context(), directory.SearchRequest{}, directory.IterationOptions{}).Responses); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// A count written with an exponent is still a whole number.
	body := `{"items":[],"facets":{"keywords":[{"count":1e3,"value":"gpu"}]}}`
	pages, err := collectPages(serve(t, always(body)).SearchServices(t.Context(), directory.SearchRequest{}, directory.IterationOptions{}).Responses)
	if err != nil || pages[0].Facets.Keywords[0].Count != 1000 {
		t.Fatalf("facets = %#v, %v", pages, err)
	}
	full := `{"items":[],"facets":{"keywords":[{"count":2,"value":"gpu"}],` +
		`"enrollment":[{"count":1,"value":{"name":"aep"}}],` +
		`"operations":[{"count":1,"value":{"authentication":"not-required","name":"list-offerings"}}],` +
		`"payments":[{"count":1,"value":{"authentication":"required","name":"x402"}}]}}`
	pages, err = collectPages(serve(t, always(full)).SearchServices(t.Context(), directory.SearchRequest{}, directory.IterationOptions{}).Responses)
	if err != nil {
		t.Fatal(err)
	}
	facets := pages[0].Facets
	if len(facets.Keywords) != 1 || len(facets.Enrollment) != 1 || len(facets.Operations) != 1 || len(facets.Payments) != 1 {
		t.Fatalf("facets = %#v", facets)
	}
}

func TestServiceRecordsAreValidatedMemberByMember(t *testing.T) {
	cases := map[string]string{
		"absent origin":         strings.Replace(record, `"service_origin":"https://compute.example",`, "", 1),
		"origin with path":      strings.Replace(record, "https://compute.example", "https://compute.example/odp", 1),
		"absent name":           strings.Replace(record, `"name":"Compute",`, "", 1),
		"absent description":    strings.Replace(record, `"description":"Accelerators",`, "", 1),
		"absent language":       strings.Replace(record, `"language":"en",`, "", 1),
		"absent operations":     strings.Replace(record, `,"operations":[{"authentication":"not-required","name":"list-offerings"},{"authentication":"not-required","name":"get-offering"}]`, "", 1),
		"absent indexed_at":     strings.Replace(record, `"indexed_at":"2026-01-01T00:00:00Z",`, "", 1),
		"indexed_at not a time": strings.Replace(record, "2026-01-01T00:00:00Z", "December 17, 1995", 1),
		"indexed_at no zone":    strings.Replace(record, "2026-01-01T00:00:00Z", "2026-01-01 00:00:00", 1),
		"empty documentation":   recordWith(`"documentation_url":""`),
		"empty status":          recordWith(`"status_url":""`),
		"bad documentation":     recordWith(`"documentation_url":"javascript:alert(1)"`),
		"duplicate keywords":    recordWith(`"keywords":["a","a"]`),
		"protocols null":        recordWith(`"protocols":null`),
		"not an object":         `[]`,
	}
	for name, item := range cases {
		pages, err := collectPages(serve(t, always(page(item))).SearchServices(t.Context(), directory.SearchRequest{}, directory.IterationOptions{}).Responses)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if len(pages[0].Items) != 0 || len(pages[0].Issues) != 1 {
			t.Errorf("%s: accepted, page = %#v", name, pages[0])
		}
	}
	// An unrecognised protocol is filtered out rather than making the record unusable, so the
	// Service survives with the protocols this version understands.
	unknown := recordWith(`"protocols":{"trust":[{"name":"tap"}],"payments":[{"authentication":"required","name":"other"}]}`)
	filtered, err := collectPages(serve(t, always(page(unknown))).SearchServices(t.Context(), directory.SearchRequest{}, directory.IterationOptions{}).Responses)
	if err != nil || len(filtered[0].Items) != 1 {
		t.Fatalf("unknown protocol = %#v, %v", filtered, err)
	}
	if protocols := filtered[0].Items[0].Protocols; protocols == nil || len(protocols.Payments) != 0 || len(protocols.Trust) != 1 {
		t.Fatalf("protocols = %#v", filtered[0].Items[0].Protocols)
	}

	// RFC 3339 permits a lowercase separator and zone designator.
	lowercase := strings.Replace(record, "2026-01-01T00:00:00Z", "2026-01-01t00:00:00z", 1)
	pages, err := collectPages(serve(t, always(page(lowercase))).SearchServices(t.Context(), directory.SearchRequest{}, directory.IterationOptions{}).Responses)
	if err != nil || len(pages[0].Items) != 1 {
		t.Fatalf("lowercase date-time = %#v, %v", pages, err)
	}
	// A record carrying every optional member it may carry survives intact.
	complete := recordWith(`"documentation_url":"https://compute.example/docs","keywords":["gpu"],` +
		`"status_url":"https://compute.example/status","support_url":"https://compute.example/support",` +
		`"website_url":"https://compute.example","protocols":{"trust":[{"name":"tap"}]}`)
	pages, err = collectPages(serve(t, always(page(complete))).SearchServices(t.Context(), directory.SearchRequest{}, directory.IterationOptions{}).Responses)
	if err != nil || len(pages[0].Items) != 1 {
		t.Fatalf("complete record = %#v, %v", pages, err)
	}
	service := pages[0].Items[0]
	if service.DocumentationURL == "" || service.StatusURL == "" || service.SupportURL == "" ||
		service.WebsiteURL == "" || len(service.Keywords) != 1 || service.Protocols == nil {
		t.Fatalf("service = %#v", service)
	}
	if service.IndexedAt.IsZero() || service.Language != "en" || len(service.Operations) != 2 {
		t.Fatalf("service = %#v", service)
	}
}

func TestPageEnvelopeIsValidated(t *testing.T) {
	items := make([]string, 101)
	for index := range items {
		items[index] = strings.Replace(record, "compute.example", fmt.Sprintf("s%d.example", index), 1)
	}
	cases := map[string]string{
		"not an object":  `[]`,
		"absent items":   `{}`,
		"items not list": `{"items":{}}`,
		"too many items": `{"items":[` + strings.Join(items, ",") + `]}`,
		"next too long":  `{"items":[],"next":"/` + strings.Repeat("p", 2100) + `"}`,
		"next empty":     `{"items":[],"next":""}`,
	}
	for name, body := range cases {
		if _, err := collectPages(serve(t, always(body)).SearchServices(t.Context(), directory.SearchRequest{}, directory.IterationOptions{}).Responses); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// A final page is commonly spelled with an explicit null continuation.
	pages, err := collectPages(serve(t, always(`{"items":[],"next":null,"total":7}`)).SearchServices(t.Context(), directory.SearchRequest{}, directory.IterationOptions{}).Responses)
	if err != nil || len(pages) != 1 || pages[0].Next != "" {
		t.Fatalf("pages = %#v, %v", pages, err)
	}
	if _, kept := pages[0].Additional["total"]; !kept {
		t.Fatalf("additional = %#v", pages[0].Additional)
	}
}

func TestSearchRequestFiltersAreValidated(t *testing.T) {
	operationFilters := make([]directory.OperationFilter, 0, 21)
	for _, name := range []odp.Operation{
		odp.OperationGetCollection, odp.OperationGetOffering, odp.OperationListCollectionOfferings,
		odp.OperationListCollections, odp.OperationListOfferings, odp.OperationSearchCollections,
		odp.OperationSearchOfferings,
	} {
		for _, authentication := range []odp.AuthenticationRequirement{
			odp.AuthenticationNotRequired, odp.AuthenticationOptional, odp.AuthenticationRequired,
		} {
			operationFilters = append(operationFilters, directory.OperationFilter{Authentication: authentication, Name: name})
		}
	}
	// Every distinct operation-and-authentication pair is expressible, so the bound is on the pair
	// rather than on the seven operation names.
	accepted := directory.SearchRequest{Filters: &directory.ServiceFilters{Operations: operationFilters}}
	if _, err := collectPages(serve(t, always(`{"items":[]}`)).SearchServices(t.Context(), accepted, directory.IterationOptions{}).Responses); err != nil {
		t.Fatalf("21 operation filters rejected: %v", err)
	}
	payments := make([]directory.PaymentFilter, 0, 3)
	for _, option := range []odp.PaymentOption{"base", "solana", "ethereum"} {
		payments = append(payments, directory.PaymentFilter{Authentication: odp.AuthenticationRequired, Name: odp.ProtocolMPP, Options: []odp.PaymentOption{option}})
	}
	if _, err := collectPages(serve(t, always(`{"items":[]}`)).SearchServices(t.Context(),
		directory.SearchRequest{Filters: &directory.ServiceFilters{Payments: payments}}, directory.IterationOptions{}).Responses); err != nil {
		t.Fatalf("three payment filters rejected: %v", err)
	}

	duplicate := []directory.OperationFilter{{Name: odp.OperationListOfferings}, {Name: odp.OperationListOfferings}}
	duplicatePayments := []directory.PaymentFilter{
		{Authentication: odp.AuthenticationRequired, Name: odp.ProtocolMPP},
		{Authentication: odp.AuthenticationRequired, Name: odp.ProtocolMPP},
	}
	refused := map[string]directory.ServiceFilters{
		"unknown operation":      {Operations: []directory.OperationFilter{{Name: "fly"}}},
		"empty operations":       {Operations: []directory.OperationFilter{}},
		"duplicate operations":   {Operations: duplicate},
		"bad operation auth":     {Operations: []directory.OperationFilter{{Authentication: "maybe", Name: odp.OperationListOfferings}}},
		"unknown payment":        {Payments: []directory.PaymentFilter{{Name: "other"}}},
		"empty payments":         {Payments: []directory.PaymentFilter{}},
		"duplicate payments":     {Payments: duplicatePayments},
		"optional payment auth":  {Payments: []directory.PaymentFilter{{Authentication: odp.AuthenticationOptional, Name: odp.ProtocolMPP}}},
		"unknown payment option": {Payments: []directory.PaymentFilter{{Authentication: odp.AuthenticationRequired, Name: odp.ProtocolMPP, Options: []odp.PaymentOption{"gold"}}}},
		"empty payment options":  {Payments: []directory.PaymentFilter{{Authentication: odp.AuthenticationRequired, Name: odp.ProtocolMPP, Options: []odp.PaymentOption{}}}},
		"repeated option":        {Payments: []directory.PaymentFilter{{Authentication: odp.AuthenticationRequired, Name: odp.ProtocolMPP, Options: []odp.PaymentOption{"base", "base"}}}},
		"unknown enrollment":     {Enrollment: []odp.EnrollmentProtocol{{Name: "other"}}},
		"two enrollments":        {Enrollment: []odp.EnrollmentProtocol{{Name: odp.ProtocolAEP}, {Name: odp.ProtocolAEP}}},
		"empty keywords":         {Keywords: []string{}},
		"duplicate keywords":     {Keywords: []string{"a", "a"}},
		"blank keyword":          {Keywords: []string{" "}},
	}
	for name, filters := range refused {
		request := directory.SearchRequest{Filters: &filters}
		if _, err := collectPages(serve(t, always(`{"items":[]}`)).SearchServices(t.Context(), request, directory.IterationOptions{}).Responses); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := collectPages(serve(t, always(`{"items":[]}`)).SearchServices(t.Context(),
		directory.SearchRequest{Query: strings.Repeat("q", 513)}, directory.IterationOptions{}).Responses); err == nil {
		t.Error("overlong query accepted")
	}
}

func TestRequestValidationReportsTheRequestBeforeTheBudget(t *testing.T) {
	// A caller who fixes the budget should not then discover a second complaint about the body.
	request := directory.SearchRequest{Limit: 9999}
	_, err := collectPages(serve(t, always(`{"items":[]}`)).SearchServices(t.Context(), request, directory.IterationOptions{MaxPages: 99_999}).Responses)
	if err == nil || !strings.Contains(err.Error(), "limit must be an integer from 1 through 100") {
		t.Fatalf("error = %v", err)
	}
}

func TestEnvironmentsAndOptions(t *testing.T) {
	if _, err := directory.New(directory.Options{Environment: "nowhere"}); err == nil {
		t.Fatal("unknown environment accepted")
	}
	sandbox, err := directory.New(directory.Options{Environment: directory.Sandbox})
	if err != nil || sandbox.Environment() != directory.Sandbox {
		t.Fatalf("sandbox = %v, %v", sandbox.Environment(), err)
	}
	production, err := directory.New(directory.Options{})
	if err != nil || production.Environment() != directory.Production {
		t.Fatalf("production = %v, %v", production.Environment(), err)
	}
}
