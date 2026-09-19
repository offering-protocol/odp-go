package directory

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	odp "github.com/offering-protocol/odp-go"
)

// unverifiedMembers are Service Document members a directory cannot authoritatively assert. An
// Agent MUST retrieve them from the Service itself before treating them as current (ROLE-03), so
// they are dropped here rather than handed to a caller who might not (ROLE-06).
var unverifiedMembers = []string{"branding", "http", "mcp", "odp_version", "payment_origins", "search_capabilities"}

// A payment filter is identified by its protocol, authentication and option set together, so the
// bound is on distinct combinations rather than on the two protocol names.
const maximumPaymentFilters = 32

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
	validated, err := normalizedSearchRequest(request)
	if err != nil {
		return nil, err
	}
	return json.Marshal(validated)
}

func normalizedSearchRequest(request SearchRequest) (SearchRequest, error) {
	validated := SearchRequest{}
	if request.Query != "" {
		query, err := requireText(request.Query, "query", 1, 512)
		if err != nil {
			return SearchRequest{}, err
		}
		validated.Query = query
	}
	if request.Limit < 0 || request.Limit > 100 {
		return SearchRequest{}, errors.New("limit must be an integer from 1 through 100")
	}
	validated.Limit = request.Limit
	if request.Filters != nil {
		filters, err := validateFilters(*request.Filters)
		if err != nil {
			return SearchRequest{}, err
		}
		validated.Filters = &filters
	}
	return validated, nil
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
	trust, err := validateTrustFilters(filters.Trust)
	if err != nil {
		return ServiceFilters{}, err
	}
	return ServiceFilters{Enrollment: enrollment, Keywords: keywords, Operations: operationFilters, Payments: paymentFilters, Trust: trust}, nil
}

func parseSearchPage(data []byte) (SearchResponse[Service], error) {
	return parsePage(data, parseService, IssueService)
}

func parsePage[Item any](data []byte, parse func([]byte) (Item, error), scope IssueScope) (SearchResponse[Item], error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		return SearchResponse[Item]{}, err
	}
	itemsData, present := object["items"]
	if !present || string(itemsData) == "null" {
		return SearchResponse[Item]{}, errors.New("Directory search page items are invalid")
	}
	var itemValues []json.RawMessage
	if err := json.Unmarshal(itemsData, &itemValues); err != nil || len(itemValues) > 100 {
		return SearchResponse[Item]{}, errors.New("Directory search page items are invalid")
	}
	items := make([]Item, 0, len(itemValues))
	var issues []Issue
	for index, item := range itemValues {
		parsed, err := parse(item)
		if err != nil {
			issues = append(issues, Issue{Index: index, Message: err.Error(), Scope: scope})
			continue
		}
		items = append(items, parsed)
	}
	next, err := optionalText(object["next"], "next", 2048)
	if err != nil {
		return SearchResponse[Item]{}, err
	}
	var facets *Facets
	if raw, ok := object["facets"]; ok {
		parsed, err := parseFacets(raw)
		if err != nil {
			return SearchResponse[Item]{}, err
		}
		facets = &parsed
	}
	return SearchResponse[Item]{
		Additional: cloneAdditional(object, "items", "next", "facets"), Facets: facets,
		Issues: issues, Items: items, Next: next,
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
	if err != nil || canonical != serviceOrigin || !publicHTTPSOrigin(serviceOrigin) {
		return Service{}, errors.New("Directory Service origin must be a canonical public HTTPS origin")
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
	// ServiceDocument omits its optional members when empty, which would hide a member the
	// directory did send but sent empty. Those are spliced back as raw JSON so the schema sees
	// exactly what arrived, the way `protocols` already is.
	encoded, err := json.Marshal(document)
	if err != nil {
		return Service{}, errors.New("Directory Service result could not be validated")
	}
	var encodedDocument map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &encodedDocument); err != nil {
		return Service{}, errors.New("Directory Service result could not be validated")
	}
	for _, name := range []string{"documentation_url", "keywords", "protocols", "status_url", "support_url", "website_url"} {
		if raw, ok := object[name]; ok {
			encodedDocument[name] = raw
		}
	}
	encoded, err = json.Marshal(encodedDocument)
	if err != nil {
		return Service{}, errors.New("Directory Service result could not be validated")
	}
	document, err = odp.ParseAgentServiceDocument(encoded)
	if err != nil {
		return Service{}, err
	}
	indexedText, err := requiredText(object["indexed_at"], "indexed_at", 1, 64)
	if err != nil {
		return Service{}, err
	}
	indexedAt, err := time.Parse(time.RFC3339Nano, strings.ToUpper(indexedText))
	if err != nil {
		return Service{}, errors.New("indexed_at must be a date-time")
	}
	known := append([]string{
		"service_origin", "name", "description", "documentation_url", "language", "localizations",
		"keywords", "operations", "protocols", "indexed_at", "status_url", "support_url", "website_url",
	}, unverifiedMembers...)
	return Service{
		Additional:  cloneAdditional(object, known...),
		Description: document.Description, DocumentationURL: document.DocumentationURL, IndexedAt: indexedAt, Keywords: document.Keywords,
		Language: document.Language, Localizations: document.Localizations, Name: document.Name,
		Operations: document.Operations, Protocols: document.Protocols, ServiceOrigin: serviceOrigin,
		StatusURL: document.StatusURL, SupportURL: document.SupportURL, WebsiteURL: document.WebsiteURL,
	}, nil
}

// publicHTTPSOrigin reports whether an origin a directory advertises is one a caller can safely
// dereference. DeriveServiceOrigin allows loopback HTTP for local development, which is right for
// a Service URL a caller chose and wrong for one a third party supplied.
func publicHTTPSOrigin(origin string) bool {
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Scheme != "https" {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return false
	}
	address, err := netip.ParseAddr(strings.Trim(host, "[]"))
	if err != nil {
		return true
	}
	address = address.Unmap()
	return address.IsGlobalUnicast() && !address.IsPrivate() && !address.IsLoopback() &&
		!address.IsLinkLocalUnicast() && !netip.MustParsePrefix("100.64.0.0/10").Contains(address)
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
	trust, err := parseDescriptorFacet(object["trust"], "trust", parseTrust)
	if err != nil {
		return Facets{}, err
	}
	return Facets{
		Enrollment: enrollment, Keywords: keywords, Operations: operationFacets,
		PaymentOptions: paymentOptions, Payments: payments, Trust: trust,
	}, nil
}

func parseFacet[Value ~string](data []byte, name string, allowed []Value) ([]Facet[Value], error) {
	if data == nil {
		return nil, nil
	}
	var entries []struct {
		Count json.RawMessage `json:"count"`
		Value Value           `json:"value"`
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
		count, err := facetCount(entry.Count)
		if err != nil || count < 0 {
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
		Count json.RawMessage `json:"count"`
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
		count, err := facetCount(entry.Count)
		if err != nil || count < 0 {
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
	if len(values) == 0 || len(values) > len(operations)*3 {
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
	if len(values) == 0 || len(values) > maximumPaymentFilters {
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

func validateTrustFilters(values []odp.TrustProtocol) ([]odp.TrustProtocol, error) {
	if values == nil {
		return nil, nil
	}
	if len(values) != 1 || values[0].Name != odp.ProtocolTAP {
		return nil, errors.New("trust is invalid")
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
	if _, found := object["name"]; !found {
		return PaymentOptionFacetValue{}, errors.New("payment option facet is invalid")
	}
	if _, found := object["option"]; !found {
		return PaymentOptionFacetValue{}, errors.New("payment option facet is invalid")
	}
	var value PaymentOptionFacetValue
	if json.Unmarshal(data, &value) != nil || (value.Name != odp.ProtocolMPP && value.Name != odp.ProtocolX402) || !odp.IsPaymentOption(value.Option) {
		return PaymentOptionFacetValue{}, errors.New("payment option facet is invalid")
	}
	return value, nil
}

func parseTrust(data json.RawMessage) (odp.TrustProtocol, error) {
	var object map[string]json.RawMessage
	if json.Unmarshal(data, &object) != nil || len(object) != 1 {
		return odp.TrustProtocol{}, errors.New("trust descriptor is invalid")
	}
	var value odp.TrustProtocol
	if json.Unmarshal(data, &value) != nil || value.Name != odp.ProtocolTAP {
		return odp.TrustProtocol{}, errors.New("trust descriptor is invalid")
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
	raw, present := object["items"]
	if !present || string(raw) == "null" {
		return nil, errors.New("suggestions are invalid")
	}
	var items []string
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, errors.New("suggestions are invalid")
	}
	result := make([]string, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		value, err := requireText(item, "suggestions", 1, 128)
		if err != nil {
			return nil, err
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
		if len(result) == 25 {
			break
		}
	}
	return result, nil
}

func requiredText(data []byte, name string, minimum, maximum int) (string, error) {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return "", fmt.Errorf("%s is invalid", name)
	}
	return requireText(value, name, minimum, maximum)
}

func optionalText(data []byte, name string, maximum int) (string, error) {
	if data == nil || string(data) == "null" {
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

// facetCount reads a count that must have arrived as a JSON number rather than as a string
// spelling of one, which encoding/json would otherwise accept into a json.Number.
func facetCount(raw json.RawMessage) (int64, error) {
	text := strings.TrimSpace(string(raw))
	if text == "" || text[0] == '"' {
		return 0, errors.New("count must be a number")
	}
	return strconvInt64(text)
}

func strconvInt64(value string) (int64, error) {
	if number, err := strconv.ParseInt(value, 10, 64); err == nil {
		return number, nil
	}
	// JSON has no integer type, so an exponent is a legitimate spelling of a whole number.
	number, err := strconv.ParseFloat(value, 64)
	if err != nil || number != math.Trunc(number) || math.Abs(number) > 9_007_199_254_740_991 {
		return 0, errors.New("value is not an integer")
	}
	return int64(number), nil
}
