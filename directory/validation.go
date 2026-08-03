package directory

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	odp "github.com/offering-protocol/odp-go"
)

var operations = []odp.Operation{
	odp.OperationGetCollection,
	odp.OperationGetOffering,
	odp.OperationListCollectionOfferings,
	odp.OperationListCollections,
	odp.OperationListOfferings,
	odp.OperationSearchCollections,
	odp.OperationSearchOfferings,
}

func validateSearchRequest(request SearchRequest) ([]byte, error) {
	validated := SearchRequest{}
	if request.Query != "" {
		query, err := requireText(request.Query, "query", 1, 512)
		if err != nil {
			return nil, err
		}
		validated.Query = query
	}
	if request.Limit < 0 || request.Limit > 100 {
		return nil, errors.New("limit must be an integer from 1 through 100")
	}
	validated.Limit = request.Limit
	if request.Filters != nil {
		filters, err := validateFilters(*request.Filters)
		if err != nil {
			return nil, err
		}
		validated.Filters = &filters
	}
	return json.Marshal(validated)
}

func validateFilters(filters ServiceFilters) (ServiceFilters, error) {
	keywords, err := uniqueText(filters.Keywords, "keywords", 32, 64)
	if err != nil {
		return ServiceFilters{}, err
	}
	onboarding, err := protocols(filters.Onboarding, "onboarding", []odp.Protocol{odp.ProtocolAEP})
	if err != nil {
		return ServiceFilters{}, err
	}
	payments, err := protocols(filters.Payments, "payments", []odp.Protocol{odp.ProtocolMPP, odp.ProtocolX402})
	if err != nil {
		return ServiceFilters{}, err
	}
	if filters.Operations != nil && (len(filters.Operations) == 0 || len(filters.Operations) > len(operations) || !unique(filters.Operations)) {
		return ServiceFilters{}, errors.New("operations are invalid")
	}
	for _, operation := range filters.Operations {
		if !slices.Contains(operations, operation) {
			return ServiceFilters{}, errors.New("operations are invalid")
		}
	}
	return ServiceFilters{Keywords: keywords, Onboarding: onboarding, Operations: filters.Operations, Payments: payments}, nil
}

func parseSearchPage(data []byte) (SearchPage, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		return SearchPage{}, err
	}
	var itemValues []json.RawMessage
	if err := json.Unmarshal(object["items"], &itemValues); err != nil || len(itemValues) > 100 {
		return SearchPage{}, errors.New("Directory search page items are invalid")
	}
	items := make([]Service, len(itemValues))
	for index, item := range itemValues {
		parsed, err := parseService(item)
		if err != nil {
			return SearchPage{}, fmt.Errorf("Directory Service result %d: %w", index, err)
		}
		items[index] = parsed
	}
	next, err := optionalText(object["next"], "next", 2048)
	if err != nil {
		return SearchPage{}, err
	}
	var facets *Facets
	if raw, ok := object["facets"]; ok {
		parsed, err := parseFacets(raw)
		if err != nil {
			return SearchPage{}, err
		}
		facets = &parsed
	}
	return SearchPage{
		Additional: cloneAdditional(object, "items", "next", "facets"), Facets: facets, Items: items, Next: next,
	}, nil
}

func parseService(data []byte) (Service, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		return Service{}, err
	}
	serviceOrigin, err := requiredText(object["service_origin"], "service_origin", 1, 2048)
	if err != nil {
		return Service{}, err
	}
	canonical, err := odp.DeriveServiceOrigin(serviceOrigin)
	if err != nil || canonical != serviceOrigin {
		return Service{}, errors.New("Directory Service origin must be a canonical HTTPS origin")
	}
	var document odp.ServiceDocument
	document.ODPVersion = odp.Version
	document.HTTP = odp.HTTPConfiguration{EndpointBase: "/"}
	if err := decodeRequired(object, "name", &document.Name); err != nil {
		return Service{}, err
	}
	if err := decodeRequired(object, "description", &document.Description); err != nil {
		return Service{}, err
	}
	if err := decodeRequired(object, "language", &document.Language); err != nil {
		return Service{}, err
	}
	if err := decodeRequired(object, "localizations", &document.Localizations); err != nil {
		return Service{}, err
	}
	if err := decodeRequired(object, "operations", &document.Operations.Supported); err != nil {
		return Service{}, err
	}
	if raw, ok := object["keywords"]; ok {
		if err := json.Unmarshal(raw, &document.Keywords); err != nil {
			return Service{}, errors.New("keywords are invalid")
		}
	}
	if raw, ok := object["protocols"]; ok {
		var protocols odp.ServiceProtocols
		if err := json.Unmarshal(raw, &protocols); err != nil {
			return Service{}, errors.New("protocols are invalid")
		}
		document.Protocols = &protocols
	}
	encoded, _ := json.Marshal(document)
	document, err = odp.ParseServiceDocument(encoded)
	if err != nil {
		return Service{}, err
	}
	indexedText, err := requiredText(object["indexed_at"], "indexed_at", 1, 64)
	if err != nil {
		return Service{}, err
	}
	indexedAt, err := time.Parse(time.RFC3339Nano, indexedText)
	if err != nil {
		return Service{}, errors.New("indexed_at must be a date-time")
	}
	return Service{
		Additional:  cloneAdditional(object, "service_origin", "name", "description", "language", "localizations", "keywords", "operations", "protocols", "indexed_at"),
		Description: document.Description, IndexedAt: indexedAt, Keywords: document.Keywords,
		Language: document.Language, Localizations: document.Localizations, Name: document.Name,
		Operations: document.Operations.Supported, Protocols: document.Protocols, ServiceOrigin: serviceOrigin,
	}, nil
}

func parseFacets(data []byte) (Facets, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		return Facets{}, errors.New("Directory facets must be an object")
	}
	keywords, err := parseFacet[string](object["keywords"], "keywords", nil)
	if err != nil {
		return Facets{}, err
	}
	onboarding, err := parseFacet(object["onboarding"], "onboarding", []odp.Protocol{odp.ProtocolAEP})
	if err != nil {
		return Facets{}, err
	}
	operationFacets, err := parseFacet(object["operations"], "operations", operations)
	if err != nil {
		return Facets{}, err
	}
	payments, err := parseFacet(object["payments"], "payments", []odp.Protocol{odp.ProtocolMPP, odp.ProtocolX402})
	if err != nil {
		return Facets{}, err
	}
	return Facets{Keywords: keywords, Onboarding: onboarding, Operations: operationFacets, Payments: payments}, nil
}

func parseFacet[Value ~string](data []byte, name string, allowed []Value) ([]Facet[Value], error) {
	if data == nil {
		return nil, nil
	}
	var entries []struct {
		Count json.Number `json:"count"`
		Value Value       `json:"value"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&entries); err != nil || len(entries) > 100 {
		return nil, fmt.Errorf("%s facets are invalid", name)
	}
	result := make([]Facet[Value], len(entries))
	for index, entry := range entries {
		value, err := requireText(string(entry.Value), name+" facet value", 1, 128)
		if err != nil || (allowed != nil && !slices.Contains(allowed, Value(value))) {
			return nil, fmt.Errorf("%s facet value is invalid", name)
		}
		count, err := strconvInt64(entry.Count.String())
		if err != nil || count < 0 || count > 9_007_199_254_740_991 {
			return nil, fmt.Errorf("%s facet count is invalid", name)
		}
		result[index] = Facet[Value]{Count: count, Value: Value(value)}
	}
	return result, nil
}

func parseSuggestions(data []byte) ([]string, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		return nil, errors.New("Directory suggestions must be an object")
	}
	var items []string
	if err := json.Unmarshal(object["items"], &items); err != nil {
		return nil, errors.New("suggestions are invalid")
	}
	return uniqueText(items, "suggestions", 25, 128)
}

func requiredText(data []byte, name string, minimum, maximum int) (string, error) {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return "", fmt.Errorf("%s is invalid", name)
	}
	return requireText(value, name, minimum, maximum)
}

func optionalText(data []byte, name string, maximum int) (string, error) {
	if data == nil {
		return "", nil
	}
	return requiredText(data, name, 1, maximum)
}

func requireText(value, name string, minimum, maximum int) (string, error) {
	length := utf8.RuneCountInString(value)
	if length < minimum || length > maximum || strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("%s is invalid", name)
	}
	return value, nil
}

func uniqueText(values []string, name string, maximumItems, maximumLength int) ([]string, error) {
	if values == nil {
		return nil, nil
	}
	if len(values) == 0 || len(values) > maximumItems || !unique(values) {
		return nil, fmt.Errorf("%s is invalid", name)
	}
	for _, value := range values {
		if _, err := requireText(value, name, 1, maximumLength); err != nil {
			return nil, err
		}
	}
	return values, nil
}

func protocols(values []odp.Protocol, name string, allowed []odp.Protocol) ([]odp.Protocol, error) {
	if values == nil {
		return nil, nil
	}
	if len(values) == 0 || len(values) > len(allowed) || !unique(values) {
		return nil, fmt.Errorf("%s are invalid", name)
	}
	for _, value := range values {
		if !slices.Contains(allowed, value) {
			return nil, fmt.Errorf("%s are invalid", name)
		}
	}
	return values, nil
}

func unique[Value comparable](values []Value) bool {
	seen := make(map[Value]struct{}, len(values))
	for _, value := range values {
		if _, exists := seen[value]; exists {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}

func decodeRequired(object map[string]json.RawMessage, name string, target any) error {
	data, ok := object[name]
	if !ok || json.Unmarshal(data, target) != nil {
		return fmt.Errorf("%s is invalid", name)
	}
	return nil
}

func strconvInt64(value string) (int64, error) {
	var number int64
	_, err := fmt.Sscan(value, &number)
	return number, err
}
