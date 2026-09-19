package directory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	odp "github.com/offering-protocol/odp-go"
)

func (client *Client) Search(ctx context.Context, request DirectorySearchRequest, options IterationOptions) SearchSequence[Result] {
	body, validationError := validateDirectorySearchRequest(request)
	maxPages, budgetError := pageBudget(options.MaxPages)
	if validationError == nil {
		validationError = budgetError
	}
	responses := func(yield func(SearchResponse[Result], error) bool) {
		if validationError != nil {
			yield(SearchResponse[Result]{}, validationError)
			return
		}
		traverse(client, ctx, client.originURL.JoinPath("v1", "directory", "search"), http.MethodPost, body, maxPages, parseDirectorySearchPage, yield)
	}
	return sequence(responses, options)
}

func (client *Client) ContinueSearch(ctx context.Context, next string, options IterationOptions) SearchSequence[Result] {
	maxPages, validationError := pageBudget(options.MaxPages)
	responses := func(yield func(SearchResponse[Result], error) bool) {
		if validationError != nil {
			yield(SearchResponse[Result]{}, validationError)
			return
		}
		target, err := client.continuationURL(next)
		if err != nil {
			yield(SearchResponse[Result]{}, err)
			return
		}
		traverse(client, ctx, target, http.MethodGet, nil, maxPages, parseDirectorySearchPage, yield)
	}
	return sequence(responses, options)
}

func sequence[Item any](responses iter.Seq2[SearchResponse[Item], error], options IterationOptions) SearchSequence[Item] {
	items := func(yield func(Item, error) bool) {
		var zero Item
		if options.MaxItems < 0 || options.MaxItems > maximumItems {
			yield(zero, fmt.Errorf("maxItems must be an integer from 1 through %d", maximumItems))
			return
		}
		count := 0
		for page, err := range responses {
			if err != nil {
				yield(zero, err)
				return
			}
			for _, issue := range page.Issues {
				if options.OnIssue != nil {
					options.OnIssue(issue)
				}
			}
			for _, item := range page.Items {
				count++
				if !yield(item, nil) || (options.MaxItems != 0 && count >= options.MaxItems) {
					return
				}
			}
		}
	}
	return SearchSequence[Item]{Items: items, Responses: responses}
}

func validateDirectorySearchRequest(request DirectorySearchRequest) ([]byte, error) {
	base, err := normalizedSearchRequest(request.SearchRequest)
	if err != nil {
		return nil, err
	}
	if request.Types != nil && (len(request.Types) == 0 || len(request.Types) > 2 || !unique(request.Types)) {
		return nil, errors.New("types must contain distinct service or collection values")
	}
	for _, value := range request.Types {
		if value != "service" && value != "collection" {
			return nil, errors.New("types must contain distinct service or collection values")
		}
	}
	return json.Marshal(DirectorySearchRequest{SearchRequest: base, Types: request.Types})
}

func parseDirectorySearchPage(data []byte) (SearchResponse[Result], error) {
	return parsePage(data, parseResult, IssueResult)
}

func parseResult(data []byte) (Result, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		return Result{}, err
	}
	kind, err := requiredText(object["type"], "type", 1, 128)
	if err != nil {
		return Result{}, err
	}
	if kind != "service" && kind != "collection" {
		return Result{Type: kind, Raw: append(json.RawMessage(nil), data...)}, nil
	}
	service, err := parseService(object["service"])
	if err != nil {
		return Result{}, err
	}
	serviceID, err := requiredText(service.Additional["service_id"], "service_id", 1, 128)
	if err != nil {
		return Result{}, err
	}
	delete(service.Additional, "service_id")
	stamp, err := requiredText(object["indexed_at"], "indexed_at", 1, 64)
	if err != nil {
		return Result{}, err
	}
	indexedAt, err := time.Parse(time.RFC3339Nano, strings.ToUpper(stamp))
	if err != nil {
		return Result{}, errors.New("indexed_at must be a date-time")
	}
	result := Result{Type: kind, Service: &IndexedService{Service: service, ServiceID: serviceID}, IndexedAt: indexedAt}
	if kind == "service" {
		result.Additional = cloneAdditional(object, "type", "service", "indexed_at", "available_through")
		if raw, present := object["available_through"]; present {
			reference, err := parseServiceReference(raw)
			if err != nil {
				return Result{}, err
			}
			result.AvailableThrough = &reference
		}
		return result, nil
	}
	var collection map[string]json.RawMessage
	if err := json.Unmarshal(object["collection"], &collection); err != nil {
		return Result{}, err
	}
	id, err := requiredText(collection["id"], "collection.id", 1, 128)
	if err != nil || !odp.IsLocalResourceIdentifier(id) {
		return Result{}, errors.New("collection.id must be a local resource identifier")
	}
	name, err := requiredText(collection["name"], "collection.name", 1, 128)
	if err != nil {
		return Result{}, err
	}
	var description string
	if raw, present := collection["description"]; present {
		if string(raw) == "null" || json.Unmarshal(raw, &description) != nil || utf8.RuneCountInString(description) > 1024 {
			return Result{}, errors.New("collection.description is invalid")
		}
	}
	result.Collection = &CollectionSummary{ID: id, Name: name, Description: description, Additional: cloneAdditional(collection, "id", "name", "description")}
	result.Additional = cloneAdditional(object, "type", "service", "indexed_at", "collection")
	return result, nil
}

func parseServiceReference(data []byte) (ServiceReference, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		return ServiceReference{}, err
	}
	id, err := requiredText(object["service_id"], "service_id", 1, 128)
	if err != nil {
		return ServiceReference{}, err
	}
	origin, err := requiredText(object["service_origin"], "service_origin", 1, 2048)
	if err != nil {
		return ServiceReference{}, err
	}
	canonical, err := odp.DeriveServiceOrigin(origin)
	if err != nil || canonical != origin || !publicHTTPSOrigin(origin) {
		return ServiceReference{}, errors.New("Attribution origin must be a canonical public HTTPS origin")
	}
	var name string
	if raw, present := object["name"]; present {
		name, err = requiredText(raw, "name", 1, 128)
		if err != nil {
			return ServiceReference{}, err
		}
	}
	return ServiceReference{ServiceID: id, ServiceOrigin: origin, Name: name, Additional: cloneAdditional(object, "service_id", "service_origin", "name")}, nil
}
