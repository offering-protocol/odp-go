package directory

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	// A failure message travels into logs and error reporting, so an error body is read and
	// rendered far more tightly than a successful one.
	maximumErrorBytes = 16_384
	maximumErrorRunes = 2_048
	maximumItems      = 10_000
	// The page budget a caller gets by default, and the bound that stops a runaway traversal.
	// They are separate: stopping at the caller's budget is what the caller asked for, while
	// reaching the guard means the directory never stopped offering continuations.
	defaultPages         = 16
	maximumPages         = 10_000
	maximumRedirects     = 5
	maximumResponseBytes = 524_288
)

func New(options Options) (*Client, error) {
	environment := options.Environment
	if environment == "" {
		environment = Production
	}
	origin := productionOrigin
	switch environment {
	case Production:
	case Sandbox:
		origin = sandboxOrigin
	default:
		return nil, fmt.Errorf("unsupported Directory environment %q", environment)
	}
	base := options.HTTPClient
	if base == nil {
		base = http.DefaultClient
	}
	httpClient := *base
	httpClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	originURL, err := url.Parse(origin)
	if err != nil {
		return nil, fmt.Errorf("parse Directory origin: %w", err)
	}
	return &Client{environment: environment, httpClient: &httpClient, originURL: originURL}, nil
}

func (client *Client) SearchServices(ctx context.Context, request SearchRequest, options IterationOptions) SearchSequence[Service] {
	body, validationError := validateSearchRequest(request)
	maxPages, budgetError := pageBudget(options.MaxPages)
	if validationError == nil {
		validationError = budgetError
	}
	responses := func(yield func(SearchResponse[Service], error) bool) {
		if validationError != nil {
			yield(SearchResponse[Service]{}, validationError)
			return
		}
		traverse(client, ctx, client.originURL.JoinPath("v1", "services", "search"), http.MethodPost, body, maxPages, parseSearchPage, yield)
	}
	return sequence(responses, options)
}

func (client *Client) ContinueSearchServices(ctx context.Context, next string, options IterationOptions) SearchSequence[Service] {
	maxPages, validationError := pageBudget(options.MaxPages)
	responses := func(yield func(SearchResponse[Service], error) bool) {
		if validationError != nil {
			yield(SearchResponse[Service]{}, validationError)
			return
		}
		target, err := client.continuationURL(next)
		if err != nil {
			yield(SearchResponse[Service]{}, err)
			return
		}
		traverse(client, ctx, target, http.MethodGet, nil, maxPages, parseSearchPage, yield)
	}
	return sequence(responses, options)
}

func (client *Client) SuggestServices(ctx context.Context, request SuggestionRequest) ([]string, error) {
	return client.suggest(ctx, "services", request)
}

func (client *Client) Suggest(ctx context.Context, request SuggestionRequest) ([]string, error) {
	return client.suggest(ctx, "directory", request)
}

func (client *Client) suggest(ctx context.Context, resource string, request SuggestionRequest) ([]string, error) {
	prefix, err := requireText(request.Prefix, "prefix", 1, 128)
	if err != nil {
		return nil, err
	}
	if request.Limit < 0 || request.Limit > 25 {
		return nil, errors.New("limit must be an integer from 1 through 25")
	}
	target := client.originURL.JoinPath("v1", resource, "suggestions")
	query := url.Values{"prefix": []string{prefix}}
	if request.Limit != 0 {
		query.Set("limit", strconv.Itoa(request.Limit))
	}
	target.RawQuery = query.Encode()
	data, err := client.requestJSON(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	return parseSuggestions(data)
}

func pageBudget(requested int) (int, error) {
	if requested == 0 {
		return defaultPages, nil
	}
	if requested < 1 || requested > maximumPages {
		return 0, fmt.Errorf("maxPages must be an integer from 1 through %d", maximumPages)
	}
	return requested, nil
}

// traverse walks the continuation chain, yielding each page until the caller stops, the directory
// stops offering one, or a budget runs out.
func traverse[Item any](client *Client, ctx context.Context, start *url.URL, method string, body []byte, maxPages int, parse func([]byte) (SearchResponse[Item], error), yield func(SearchResponse[Item], error) bool) {
	current := start
	requestBody := body
	visited := map[string]struct{}{}
	for pageNumber := 0; pageNumber < maxPages; pageNumber++ {
		data, err := client.requestJSON(ctx, method, current, requestBody)
		if err != nil {
			yield(SearchResponse[Item]{}, err)
			return
		}
		page, err := parse(data)
		if err != nil {
			yield(SearchResponse[Item]{}, err)
			return
		}
		if !yield(page, nil) || page.Next == "" {
			return
		}
		// The budget is checked before the continuation is resolved, so the page just yielded
		// keeps a Next the caller can resume from and no error is raised about a page that was
		// never going to be requested.
		if pageNumber+1 >= maxPages {
			if maxPages == maximumPages {
				yield(SearchResponse[Item]{}, fmt.Errorf("Directory pagination exceeded its %d-page traversal limit", maximumPages))
			}
			return
		}
		next, err := client.continuationURL(page.Next)
		if err != nil {
			yield(SearchResponse[Item]{}, err)
			return
		}
		// A continuation has to advance. Without this a directory that repeats one link keeps the
		// caller reading the same page until the budget runs out.
		key := traversalKey(next)
		if _, seen := visited[key]; seen {
			yield(SearchResponse[Item]{}, errors.New("Directory pagination loop detected"))
			return
		}
		visited[key] = struct{}{}
		current = next
		method = http.MethodGet
		requestBody = nil
	}
}

func (client *Client) requestJSON(ctx context.Context, method string, target *url.URL, body []byte) ([]byte, error) {
	current := target
	for redirects := 0; ; redirects++ {
		var requestBody io.Reader
		if body != nil {
			requestBody = bytes.NewReader(body)
		}
		request, err := http.NewRequestWithContext(ctx, method, current.String(), requestBody)
		if err != nil {
			return nil, fmt.Errorf("create Directory request: %w", err)
		}
		request.Header.Set("Accept", "application/json")
		if body != nil {
			request.Header.Set("Content-Type", "application/json")
		}
		response, err := client.httpClient.Do(request)
		if err != nil {
			return nil, err
		}
		if !redirectStatus(response.StatusCode) {
			return client.consumeResponse(response)
		}
		_ = response.Body.Close()
		if redirects == maximumRedirects {
			return nil, errors.New("Directory response exceeded its redirect limit")
		}
		location := response.Header.Get("Location")
		if location == "" {
			return nil, errors.New("Directory redirect omitted Location")
		}
		next, err := current.Parse(location)
		if err != nil {
			return nil, fmt.Errorf("parse Directory redirect: %w", err)
		}
		if !client.sameOrigin(next) {
			return nil, errors.New("Directory redirect changed origin")
		}
		if response.StatusCode == http.StatusSeeOther || ((response.StatusCode == http.StatusMovedPermanently || response.StatusCode == http.StatusFound) && method == http.MethodPost) {
			method = http.MethodGet
			body = nil
		}
		current = next
	}
}

func (client *Client) consumeResponse(response *http.Response) ([]byte, error) {
	defer response.Body.Close()
	if response.ContentLength > maximumResponseBytes {
		return nil, errors.New("Directory response exceeds its byte limit")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maximumResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maximumResponseBytes {
		return nil, errors.New("Directory response exceeds its byte limit")
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return nil, &RequestError{
			Header: response.Header.Clone(), Message: failureMessage(response.Header, data, response.StatusCode),
			Retryable: response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500,
			Status:    response.StatusCode,
		}
	}
	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || !strings.EqualFold(mediaType, "application/json") {
		return nil, errors.New("Directory response must use application/json")
	}
	if !utf8.Valid(data) {
		return nil, errors.New("Directory response must use UTF-8")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, errors.New("Directory response must contain valid JSON")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, errors.New("Directory response must contain one JSON value")
	}
	return data, nil
}

// failureMessage describes a failed request without repeating whatever the response happened to
// contain. Only a structured field of a JSON error document is quoted, and only after the control
// characters that would let it forge a log line or drive a terminal are removed.
func failureMessage(header http.Header, data []byte, status int) string {
	summary := fmt.Sprintf("Directory request failed with HTTP %d", status)
	mediaType, _, err := mime.ParseMediaType(header.Get("Content-Type"))
	if err != nil || (!strings.EqualFold(mediaType, "application/json") && !strings.EqualFold(mediaType, "application/problem+json")) {
		return summary
	}
	if len(data) > maximumErrorBytes {
		data = data[:maximumErrorBytes]
	}
	var document struct {
		Detail  string `json:"detail"`
		Message string `json:"message"`
		Title   string `json:"title"`
	}
	if json.Unmarshal(data, &document) != nil {
		return summary
	}
	for _, candidate := range []string{document.Detail, document.Title, document.Message} {
		detail := printableText(candidate)
		if detail == "" {
			continue
		}
		if runes := []rune(detail); len(runes) > maximumErrorRunes {
			detail = string(runes[:maximumErrorRunes]) + "…"
		}
		return summary + ": " + detail
	}
	return summary
}

func printableText(value string) string {
	cleaned := strings.Map(func(character rune) rune {
		if character < 0x20 || character == 0x7f || (character >= 0x80 && character <= 0x9f) {
			return ' '
		}
		return character
	}, value)
	return strings.Join(strings.Fields(cleaned), " ")
}

func (client *Client) continuationURL(reference string) (*url.URL, error) {
	resolved, err := client.originURL.Parse(reference)
	if err != nil {
		return nil, fmt.Errorf("parse Directory continuation: %w", err)
	}
	if !client.sameOrigin(resolved) {
		return nil, errors.New("Directory continuation must remain on the canonical origin")
	}
	return resolved, nil
}

func (client *Client) sameOrigin(candidate *url.URL) bool {
	return candidate.User == nil && canonicalOrigin(candidate) == canonicalOrigin(client.originURL)
}

// canonicalOrigin renders an origin the way two spellings of the same one compare equal: lowercase
// host, and no port when it is the default for the scheme.
func canonicalOrigin(value *url.URL) string {
	scheme := strings.ToLower(value.Scheme)
	host := strings.ToLower(value.Hostname())
	port := value.Port()
	if (scheme == "https" && port == "443") || (scheme == "http" && port == "80") {
		port = ""
	}
	if port != "" {
		host = net.JoinHostPort(host, port)
	}
	return scheme + "://" + host
}

// traversalKey identifies a page for loop detection, so alternating the case of the host or the
// spelling of an escape cannot present one page as two.
func traversalKey(value *url.URL) string {
	return canonicalOrigin(value) + value.EscapedPath() + "?" + value.Query().Encode()
}

func redirectStatus(status int) bool {
	return status == http.StatusMovedPermanently || status == http.StatusFound || status == http.StatusSeeOther || status == http.StatusTemporaryRedirect || status == http.StatusPermanentRedirect
}
