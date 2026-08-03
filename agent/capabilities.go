package agent

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"

	odp "github.com/offering-protocol/odp-go"
)

const (
	maximumCapabilityPages = 16
	maximumFilters         = 1024
	maximumSorts           = 128
)

func (client *ServiceClient) GetCollectionSearchCapabilities(ctx context.Context, id string) (SearchCapabilityCatalog, error) {
	collection, err := client.GetCollection(ctx, id, odp.RepresentationFull)
	if err != nil {
		return SearchCapabilityCatalog{}, err
	}
	return client.resolveSearchCapabilities(ctx, &collection)
}

func (client *ServiceClient) GetOfferingSearchCapabilities(ctx context.Context, collectionID string) (SearchCapabilityCatalog, error) {
	var collection *odp.Collection
	if collectionID != "" {
		value, err := client.GetCollection(ctx, collectionID, odp.RepresentationFull)
		if err != nil {
			return SearchCapabilityCatalog{}, err
		}
		collection = &value
	}
	return client.resolveSearchCapabilities(ctx, collection)
}

func (client *ServiceClient) resolveSearchCapabilities(ctx context.Context, collection *odp.Collection) (SearchCapabilityCatalog, error) {
	inspection, err := client.Inspect(ctx)
	if err != nil {
		return SearchCapabilityCatalog{}, err
	}
	result := SearchCapabilityCatalog{Filters: map[string]odp.FilterDefinition{}, Sorts: map[string]ResolvedSortDefinition{}}
	if !supports(inspection.Document, odp.OperationSearchOfferings) {
		if collection != nil && collection.SearchCapabilities != nil {
			result.Issues = append(result.Issues, CapabilityIssue{Kind: CapabilityKindFilters, Message: "Collection search capabilities require the search-offerings operation.", Scope: CapabilityScopeCollection})
		}
		return result, nil
	}
	sorts := map[string]odp.SortDefinition{}
	sortScopes := map[string]CapabilityScope{}
	for _, source := range []struct {
		capabilities *odp.SearchCapabilities
		scope        CapabilityScope
	}{{inspection.Document.SearchCapabilities, CapabilityScopeService}, {collectionCapabilities(collection), CapabilityScopeCollection}} {
		if source.capabilities == nil {
			continue
		}
		client.addFilters(ctx, &result, source.scope, source.capabilities.Filters)
		client.addSorts(ctx, &result, sorts, sortScopes, source.scope, source.capabilities.Sorts)
	}
	for id, sort := range sorts {
		resolved := ResolvedSortDefinition{SortDefinition: sort}
		missing := false
		for _, key := range sort.Keys {
			filter, found := result.Filters[key.FilterID]
			if !found {
				missing = true
				break
			}
			resolved.Filters = append(resolved.Filters, filter)
		}
		if missing {
			result.Issues = append(result.Issues, CapabilityIssue{Kind: CapabilityKindSorts, Message: fmt.Sprintf("Sort %s references an unavailable filter.", id), Scope: sortScopes[id]})
			continue
		}
		result.Sorts[id] = resolved
	}
	return result, nil
}

func collectionCapabilities(collection *odp.Collection) *odp.SearchCapabilities {
	if collection == nil {
		return nil
	}
	return collection.SearchCapabilities
}

func (client *ServiceClient) addFilters(ctx context.Context, result *SearchCapabilityCatalog, scope CapabilityScope, source *odp.FilterCapabilitySource) {
	if source == nil {
		return
	}
	values := append([]odp.FilterDefinition(nil), source.Inline...)
	if source.Linked != nil {
		pages, err := client.loadFilterPages(ctx, source.Linked.Href)
		if err != nil {
			result.Issues = append(result.Issues, CapabilityIssue{Kind: CapabilityKindFilters, Message: err.Error(), Scope: scope})
			return
		}
		values = pages
	}
	duplicates := duplicateFilterIDs(values, result.Filters)
	for id := range duplicates {
		delete(result.Filters, id)
	}
	accepted := 0
	for _, value := range values {
		if !duplicates[value.ID] {
			accepted++
		}
	}
	if len(result.Filters)+accepted > maximumFilters {
		result.Issues = append(result.Issues, CapabilityIssue{Kind: CapabilityKindFilters, Message: "Effective filters exceed their limit.", Scope: scope})
		return
	}
	for _, value := range values {
		if !duplicates[value.ID] {
			result.Filters[value.ID] = value
		}
	}
	if len(duplicates) != 0 {
		result.Issues = append(result.Issues, CapabilityIssue{Kind: CapabilityKindFilters, Message: "Duplicate filters: " + duplicateNames(duplicates), Scope: scope})
	}
}

func (client *ServiceClient) addSorts(ctx context.Context, result *SearchCapabilityCatalog, target map[string]odp.SortDefinition, scopes map[string]CapabilityScope, scope CapabilityScope, source *odp.SortCapabilitySource) {
	if source == nil {
		return
	}
	values := append([]odp.SortDefinition(nil), source.Inline...)
	if source.Linked != nil {
		pages, err := client.loadSortPages(ctx, source.Linked.Href)
		if err != nil {
			result.Issues = append(result.Issues, CapabilityIssue{Kind: CapabilityKindSorts, Message: err.Error(), Scope: scope})
			return
		}
		values = pages
	}
	duplicates := duplicateSortIDs(values, target)
	for id := range duplicates {
		delete(target, id)
		delete(scopes, id)
	}
	accepted := 0
	for _, value := range values {
		if !duplicates[value.ID] {
			accepted++
		}
	}
	if len(target)+accepted > maximumSorts {
		result.Issues = append(result.Issues, CapabilityIssue{Kind: CapabilityKindSorts, Message: "Effective sorts exceed their limit.", Scope: scope})
		return
	}
	for _, value := range values {
		if !duplicates[value.ID] {
			target[value.ID] = value
			scopes[value.ID] = scope
		}
	}
	if len(duplicates) != 0 {
		result.Issues = append(result.Issues, CapabilityIssue{Kind: CapabilityKindSorts, Message: "Duplicate sorts: " + duplicateNames(duplicates), Scope: scope})
	}
}

func duplicateNames(duplicates map[string]bool) string {
	ids := make([]string, 0, len(duplicates))
	for id := range duplicates {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return strings.Join(ids, ", ")
}

func duplicateFilterIDs(values []odp.FilterDefinition, existing map[string]odp.FilterDefinition) map[string]bool {
	seen := map[string]bool{}
	duplicates := map[string]bool{}
	for _, value := range values {
		if seen[value.ID] || existing[value.ID].ID != "" {
			duplicates[value.ID] = true
		}
		seen[value.ID] = true
	}
	return duplicates
}

func duplicateSortIDs(values []odp.SortDefinition, existing map[string]odp.SortDefinition) map[string]bool {
	seen := map[string]bool{}
	duplicates := map[string]bool{}
	for _, value := range values {
		if seen[value.ID] || existing[value.ID].ID != "" {
			duplicates[value.ID] = true
		}
		seen[value.ID] = true
	}
	return duplicates
}

func (client *ServiceClient) loadFilterPages(ctx context.Context, reference string) ([]odp.FilterDefinition, error) {
	values := []odp.FilterDefinition{}
	err := client.loadCapabilityPages(ctx, reference, func(data []byte) (string, error) {
		page, err := odp.ParseFilterDefinitionPage(data)
		if err == nil {
			values = append(values, page.Items...)
		}
		return page.Next, err
	})
	return values, err
}

func (client *ServiceClient) loadSortPages(ctx context.Context, reference string) ([]odp.SortDefinition, error) {
	values := []odp.SortDefinition{}
	err := client.loadCapabilityPages(ctx, reference, func(data []byte) (string, error) {
		page, err := odp.ParseSortDefinitionPage(data)
		if err == nil {
			values = append(values, page.Items...)
		}
		return page.Next, err
	})
	return values, err
}

func (client *ServiceClient) loadCapabilityPages(ctx context.Context, reference string, parse func([]byte) (string, error)) error {
	visited := map[string]bool{}
	next := reference
	for page := 0; next != "" && page < maximumCapabilityPages; page++ {
		target, err := resolveReference(next, client.serviceOrigin)
		if err != nil {
			return err
		}
		if visited[target] {
			return errors.New("ODP capability pagination loop detected")
		}
		visited[target] = true
		result, err := request(ctx, client.client, http.MethodGet, target, nil, client.acceptLanguage, client.maxRedirects, maximumResourceBytes, client.resourceCache, cacheKey(client.partition, http.MethodGet, target, client.acceptLanguage, nil), CollectionFallback, nil)
		if err != nil {
			return err
		}
		next, err = parse(result.body)
		if err != nil {
			return err
		}
	}
	if next != "" {
		return errors.New("ODP capability source exceeded 16 pages")
	}
	return nil
}

func resolveReference(reference, origin string) (string, error) {
	base, err := url.Parse(origin)
	if err != nil {
		return "", err
	}
	target, err := base.Parse(reference)
	if err != nil {
		return "", err
	}
	if target.Scheme != "https" && target.Scheme != "http" {
		return "", errors.New("ODP reference must use HTTP or HTTPS")
	}
	return target.String(), nil
}
