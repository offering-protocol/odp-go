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
	enrollment, err := enrollmentFilters(filters.Enrollment)
	if err != nil {
		return ServiceFilters{}, err
	}
	operationFilters, err := validateOperationFilters(filters.Operations)
	if err != nil {
		return ServiceFilters{}, err
	}
	paymentFilters, err := validatePaymentFilters(filters.Payments)
	if err != nil {
		return ServiceFilters{}, err
	}
	return ServiceFilters{Enrollment: enrollment, Keywords: keywords, Operations: operationFilters, Payments: paymentFilters}, nil
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
	if raw, ok := object["documentation_url"]; ok {
		if err := json.Unmarshal(raw, &document.DocumentationURL); err != nil {
			return Service{}, errors.New("documentation_url is invalid")
		}
	}
	if err := decodeRequired(object, "language", &document.Language); err != nil {
		return Service{}, err
	}
	if err := decodeRequired(object, "localizations", &document.Localizations); err != nil {
		return Service{}, err
	}
	if err := decodeRequired(object, "operations", &document.Operations); err != nil {
		return Service{}, err
	}
	if raw, ok := object["keywords"]; ok {
		if err := json.Unmarshal(raw, &document.Keywords); err != nil {
			return Service{}, errors.New("keywords are invalid")
		}
	}
	for name, destination := range map[string]*string{
		"status_url":  &document.StatusURL,
		"support_url": &document.SupportURL,
		"website_url": &document.WebsiteURL,
	} {
		if raw, ok := object[name]; ok {
			if err := json.Unmarshal(raw, destination); err != nil {
				return Service{}, fmt.Errorf("%s is invalid", name)
			}
		}
	}
	encoded, _ := json.Marshal(document)
	if protocols, ok := object["protocols"]; ok {
		var encodedDocument map[string]json.RawMessage
		if err := json.Unmarshal(encoded, &encodedDocument); err != nil {
			return Service{}, errors.New("protocols are invalid")
		}
		encodedDocument["protocols"] = protocols
		encoded, _ = json.Marshal(encodedDocument)
	}
	document, err = odp.ParseAgentServiceDocument(encoded)
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
		Additional:  cloneAdditional(object, "service_origin", "name", "description", "documentation_url", "language", "localizations", "keywords", "operations", "protocols", "indexed_at", "status_url", "support_url", "website_url"),
		Description: document.Description, DocumentationURL: document.DocumentationURL, IndexedAt: indexedAt, Keywords: document.Keywords,
		Language: document.Language, Localizations: document.Localizations, Name: document.Name,
		Operations: document.Operations, Protocols: document.Protocols, ServiceOrigin: serviceOrigin,
		StatusURL: document.StatusURL, SupportURL: document.SupportURL, WebsiteURL: document.WebsiteURL,
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
	enrollment, err := parseDescriptorFacet(object["enrollment"], "enrollment", parseEnrollment)
	if err != nil {
		return Facets{}, err
	}
	operationFacets, err := parseDescriptorFacet(object["operations"], "operations", parseOperation)
	if err != nil {
		return Facets{}, err
	}
	paymentOptions, err := parseDescriptorFacet(object["payment_options"], "payment_options", parsePaymentOptionFacet)
	if err != nil {
		return Facets{}, err
	}
	payments, err := parseDescriptorFacet(object["payments"], "payments", parsePayment)
	if err != nil {
		return Facets{}, err
	}
	return Facets{
		Enrollment: enrollment, Keywords: keywords, Operations: operationFacets,
		PaymentOptions: paymentOptions, Payments: payments,
	}, nil
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

func parseDescriptorFacet[Value any](data []byte, name string, parse func(json.RawMessage) (Value, error)) ([]Facet[Value], error) {
	if data == nil {
		return nil, nil
	}
	var entries []struct {
		Count json.Number     `json:"count"`
		Value json.RawMessage `json:"value"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&entries); err != nil || len(entries) > 100 {
		return nil, fmt.Errorf("%s facets are invalid", name)
	}
	result := make([]Facet[Value], len(entries))
	for index, entry := range entries {
		value, err := parse(entry.Value)
		if err != nil {
			return nil, fmt.Errorf("%s facet value is invalid", name)
		}
		count, err := strconvInt64(entry.Count.String())
		if err != nil || count < 0 || count > 9_007_199_254_740_991 {
			return nil, fmt.Errorf("%s facet count is invalid", name)
		}
		result[index] = Facet[Value]{Count: count, Value: value}
	}
	return result, nil
}

func enrollmentFilters(values []odp.EnrollmentProtocol) ([]odp.EnrollmentProtocol, error) {
	if values == nil {
		return nil, nil
	}
	if len(values) != 1 || values[0].Name != odp.ProtocolAEP {
		return nil, errors.New("enrollment is invalid")
	}
	return values, nil
}

func validateOperationFilters(values []OperationFilter) ([]OperationFilter, error) {
	if values == nil {
		return nil, nil
	}
	if len(values) == 0 || len(values) > len(operations) {
		return nil, errors.New("operations are invalid")
	}
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if !slices.Contains(operations, value.Name) || (value.Authentication != "" && !validAuthentication(value.Authentication, true)) {
			return nil, errors.New("operations are invalid")
		}
		identity := string(value.Name) + "\x00" + string(value.Authentication)
		if _, exists := seen[identity]; exists {
			return nil, errors.New("operations are invalid")
		}
		seen[identity] = struct{}{}
	}
	return values, nil
}

func validatePaymentFilters(values []PaymentFilter) ([]PaymentFilter, error) {
	if values == nil {
		return nil, nil
	}
	if len(values) == 0 || len(values) > 2 {
		return nil, errors.New("payments are invalid")
	}
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if (value.Name != odp.ProtocolMPP && value.Name != odp.ProtocolX402) || (value.Authentication != "" && !validAuthentication(value.Authentication, false)) {
			return nil, errors.New("payments are invalid")
		}
		if err := validatePaymentOptions(value.Options); err != nil {
			return nil, err
		}
		identity := string(value.Name) + "\x00" + string(value.Authentication) + "\x00" + paymentOptionIdentity(value.Options)
		if _, exists := seen[identity]; exists {
			return nil, errors.New("payments are invalid")
		}
		seen[identity] = struct{}{}
	}
	return values, nil
}

func parseEnrollment(data json.RawMessage) (odp.EnrollmentProtocol, error) {
	var object map[string]json.RawMessage
	if json.Unmarshal(data, &object) != nil || len(object) != 1 {
		return odp.EnrollmentProtocol{}, errors.New("enrollment descriptor is invalid")
	}
	var name odp.Protocol
	if json.Unmarshal(object["name"], &name) != nil || name != odp.ProtocolAEP {
		return odp.EnrollmentProtocol{}, errors.New("enrollment descriptor is invalid")
	}
	return odp.EnrollmentProtocol{Name: name}, nil
}

func parseOperation(data json.RawMessage) (odp.OperationDescriptor, error) {
	var object map[string]json.RawMessage
	if json.Unmarshal(data, &object) != nil || len(object) != 2 {
		return odp.OperationDescriptor{}, errors.New("operation descriptor is invalid")
	}
	var value odp.OperationDescriptor
	if json.Unmarshal(object["authentication"], &value.Authentication) != nil || json.Unmarshal(object["name"], &value.Name) != nil || !validAuthentication(value.Authentication, true) || !slices.Contains(operations, value.Name) {
		return odp.OperationDescriptor{}, errors.New("operation descriptor is invalid")
	}
	return value, nil
}

func parsePayment(data json.RawMessage) (odp.PaymentProtocol, error) {
	var object map[string]json.RawMessage
	if json.Unmarshal(data, &object) != nil || len(object) < 2 || len(object) > 3 {
		return odp.PaymentProtocol{}, errors.New("payment descriptor is invalid")
	}
	if _, exists := object["authentication"]; !exists {
		return odp.PaymentProtocol{}, errors.New("payment descriptor is invalid")
	}
	if _, exists := object["name"]; !exists {
		return odp.PaymentProtocol{}, errors.New("payment descriptor is invalid")
	}
	for name := range object {
		if name != "authentication" && name != "name" && name != "options" {
			return odp.PaymentProtocol{}, errors.New("payment descriptor is invalid")
		}
	}
	var value odp.PaymentProtocol
	if json.Unmarshal(data, &value) != nil || !validAuthentication(value.Authentication, false) || (value.Name != odp.ProtocolMPP && value.Name != odp.ProtocolX402) || validatePaymentOptions(value.Options) != nil {
		return odp.PaymentProtocol{}, errors.New("payment descriptor is invalid")
	}
	return value, nil
}

func parsePaymentOptionFacet(data json.RawMessage) (PaymentOptionFacetValue, error) {
	var object map[string]json.RawMessage
	if json.Unmarshal(data, &object) != nil || len(object) != 2 {
		return PaymentOptionFacetValue{}, errors.New("payment option facet is invalid")
	}
	var value PaymentOptionFacetValue
	if json.Unmarshal(data, &value) != nil || (value.Name != odp.ProtocolMPP && value.Name != odp.ProtocolX402) || !odp.IsPaymentOption(value.Option) {
		return PaymentOptionFacetValue{}, errors.New("payment option facet is invalid")
	}
	return value, nil
}

func validatePaymentOptions(values []odp.PaymentOption) error {
	if values == nil {
		return nil
	}
	if len(values) == 0 || len(values) > 16 {
		return errors.New("payment options are invalid")
	}
	seen := make(map[odp.PaymentOption]struct{}, len(values))
	for _, value := range values {
		if !odp.IsPaymentOption(value) {
			return errors.New("payment options are invalid")
		}
		if _, exists := seen[value]; exists {
			return errors.New("payment options must be unique")
		}
		seen[value] = struct{}{}
	}
	return nil
}

func paymentOptionIdentity(values []odp.PaymentOption) string {
	parts := make([]string, len(values))
	for index, value := range values {
		parts[index] = string(value)
	}
	slices.Sort(parts)
	return strings.Join(parts, "\x00")
}

func validAuthentication(value odp.AuthenticationRequirement, optional bool) bool {
	return value == odp.AuthenticationNotRequired || value == odp.AuthenticationRequired || (optional && value == odp.AuthenticationOptional)
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
	if items != nil && len(items) == 0 {
		return items, nil
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
