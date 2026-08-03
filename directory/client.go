package directory

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	maximumItems         = 10_000
	maximumPages         = 16
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
	return &Client{environment: environment, httpClient: &httpClient, origin: origin}, nil
}

func (client *Client) SearchPages(ctx context.Context, request SearchRequest, options IterationOptions) iter.Seq2[SearchPage, error] {
	body, validationError := validateSearchRequest(request)
	maxPages := options.MaxPages
	if maxPages == 0 {
		maxPages = maximumPages
	}
	if maxPages < 1 || maxPages > maximumPages {
		validationError = errors.New("maxPages must be an integer from 1 through 16")
	}
	return func(yield func(SearchPage, error) bool) {
		if validationError != nil {
			yield(SearchPage{}, validationError)
			return
		}
		current := client.origin + "/v1/services/search"
		method := http.MethodPost
		requestBody := body
		for pageNumber := 0; pageNumber < maxPages; pageNumber++ {
			data, err := client.requestJSON(ctx, method, current, requestBody)
			if err != nil {
				yield(SearchPage{}, err)
				return
			}
			page, err := parseSearchPage(data)
			if err != nil {
				yield(SearchPage{}, err)
				return
			}
			if !yield(page, nil) || page.Next == "" {
				return
			}
			current, err = client.continuationURL(page.Next)
			if err != nil {
				yield(SearchPage{}, err)
				return
			}
			method = http.MethodGet
			requestBody = nil
		}
	}
}

func (client *Client) SearchServices(ctx context.Context, request SearchRequest, options IterationOptions) iter.Seq2[Service, error] {
	maxItems := options.MaxItems
	var validationError error
	if maxItems < 0 || maxItems > maximumItems {
		validationError = errors.New("maxItems must be an integer from 1 through 10000")
	}
	return func(yield func(Service, error) bool) {
		if validationError != nil {
			yield(Service{}, validationError)
			return
		}
		count := 0
		for page, err := range client.SearchPages(ctx, request, options) {
			if err != nil {
				yield(Service{}, err)
				return
			}
			for _, service := range page.Items {
				if maxItems != 0 && count >= maxItems {
					return
				}
				count++
				if !yield(service, nil) {
					return
				}
			}
		}
	}
}

func (client *Client) SuggestServices(ctx context.Context, request SuggestionRequest) ([]string, error) {
	prefix, err := requireText(request.Prefix, "prefix", 1, 128)
	if err != nil {
		return nil, err
	}
	if request.Limit < 0 || request.Limit > 25 {
		return nil, errors.New("limit must be an integer from 1 through 25")
	}
	target := client.origin + "/v1/services/suggestions?prefix=" + url.QueryEscape(prefix)
	if request.Limit != 0 {
		target += "&limit=" + strconv.Itoa(request.Limit)
	}
	data, err := client.requestJSON(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	return parseSuggestions(data)
}

func (client *Client) requestJSON(ctx context.Context, method, target string, body []byte) ([]byte, error) {
	current, err := url.Parse(target)
	if err != nil {
		return nil, fmt.Errorf("parse Directory URL: %w", err)
	}
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
		message := string(data)
		if message == "" {
			message = fmt.Sprintf("Directory request failed with HTTP %d", response.StatusCode)
		}
		return nil, &RequestError{Header: response.Header.Clone(), Message: message, Status: response.StatusCode}
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

func (client *Client) continuationURL(reference string) (string, error) {
	base, _ := url.Parse(client.origin)
	resolved, err := base.Parse(reference)
	if err != nil {
		return "", fmt.Errorf("parse Directory continuation: %w", err)
	}
	if !client.sameOrigin(resolved) {
		return "", errors.New("Directory continuation must remain on the canonical origin")
	}
	return resolved.String(), nil
}

func (client *Client) sameOrigin(candidate *url.URL) bool {
	origin, err := url.Parse(client.origin)
	return err == nil && candidate.User == nil && strings.EqualFold(candidate.Scheme, origin.Scheme) && strings.EqualFold(candidate.Host, origin.Host)
}

func redirectStatus(status int) bool {
	return status == http.StatusMovedPermanently || status == http.StatusFound || status == http.StatusSeeOther || status == http.StatusTemporaryRedirect || status == http.StatusPermanentRedirect
}
