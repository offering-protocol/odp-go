package odp

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/dlclark/regexp2"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"golang.org/x/text/language"
	"golang.org/x/text/message"
)

const schemaOrigin = "https://offeringprotocol.org/schemas/"

//go:embed schemas/*.schema.json
var schemaFiles embed.FS

type ValidationIssue struct {
	Keyword string         `json:"keyword"`
	Message string         `json:"message"`
	Params  map[string]any `json:"params"`
	Path    string         `json:"path"`
}

type ValidationError struct {
	DocumentType string            `json:"document_type"`
	Issues       []ValidationIssue `json:"issues"`
}

func (err *ValidationError) Error() string {
	return "invalid ODP " + err.DocumentType
}

var (
	compiledSchemas map[string]*jsonschema.Schema
	compileOnce     sync.Once
	compileError    error
	issuePrinter    = message.NewPrinter(language.English)
)

func loadSchemas() (map[string]*jsonschema.Schema, error) {
	compileOnce.Do(func() {
		compiler := jsonschema.NewCompiler()
		compiler.DefaultDraft(jsonschema.Draft2020)
		compiler.AssertFormat()
		compiler.UseRegexpEngine(compileECMAScriptRegexp)
		entries, err := fs.ReadDir(schemaFiles, "schemas")
		if err != nil {
			compileError = err
			return
		}
		ids := make([]string, 0, len(entries))
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".schema.json") {
				continue
			}
			data, readErr := schemaFiles.ReadFile("schemas/" + entry.Name())
			if readErr != nil {
				compileError = readErr
				return
			}
			document, decodeErr := jsonschema.UnmarshalJSON(bytes.NewReader(data))
			if decodeErr != nil {
				compileError = fmt.Errorf("decode %s: %w", entry.Name(), decodeErr)
				return
			}
			id := schemaOrigin + entry.Name()
			if addErr := compiler.AddResource(id, document); addErr != nil {
				compileError = fmt.Errorf("register %s: %w", entry.Name(), addErr)
				return
			}
			ids = append(ids, id)
		}
		compiledSchemas = make(map[string]*jsonschema.Schema, len(ids))
		for _, id := range ids {
			schema, err := compiler.Compile(id)
			if err != nil {
				compileError = fmt.Errorf("compile %s: %w", id, err)
				return
			}
			compiledSchemas[id] = schema
		}
	})
	return compiledSchemas, compileError
}

type ecmaScriptRegexp regexp2.Regexp

func (expression *ecmaScriptRegexp) MatchString(value string) bool {
	matched, err := (*regexp2.Regexp)(expression).MatchString(value)
	return err == nil && matched
}

func (expression *ecmaScriptRegexp) String() string {
	return (*regexp2.Regexp)(expression).String()
}

func compileECMAScriptRegexp(pattern string) (jsonschema.Regexp, error) {
	expression, err := regexp2.Compile(pattern, regexp2.ECMAScript)
	if err != nil {
		return nil, err
	}
	return (*ecmaScriptRegexp)(expression), nil
}

func parseJSON[Value any](data []byte, schemaName, documentType string, refine func(Value) []ValidationIssue) (Value, error) {
	var zero Value
	schemas, err := loadSchemas()
	if err != nil {
		return zero, fmt.Errorf("load ODP schemas: %w", err)
	}
	schema := schemas[schemaOrigin+schemaName]
	if schema == nil {
		return zero, fmt.Errorf("missing bundled ODP schema %s", schemaName)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var raw any
	if err := decoder.Decode(&raw); err != nil {
		return zero, &ValidationError{DocumentType: documentType, Issues: []ValidationIssue{{
			Keyword: "json", Message: err.Error(), Params: map[string]any{}, Path: "",
		}}}
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			err = errors.New("multiple JSON values")
		}
		return zero, &ValidationError{DocumentType: documentType, Issues: []ValidationIssue{{
			Keyword: "json", Message: err.Error(), Params: map[string]any{}, Path: "",
		}}}
	}
	if err := schema.Validate(raw); err != nil {
		var validation *jsonschema.ValidationError
		if errors.As(err, &validation) {
			return zero, &ValidationError{DocumentType: documentType, Issues: schemaIssues(validation)}
		}
		return zero, fmt.Errorf("validate ODP %s: %w", documentType, err)
	}
	var value Value
	if err := json.Unmarshal(data, &value); err != nil {
		return zero, fmt.Errorf("decode validated ODP %s: %w", documentType, err)
	}
	if refine != nil {
		if issues := refine(value); len(issues) > 0 {
			return zero, &ValidationError{DocumentType: documentType, Issues: issues}
		}
	}
	return value, nil
}

func schemaIssues(root *jsonschema.ValidationError) []ValidationIssue {
	if len(root.Causes) != 0 {
		issues := make([]ValidationIssue, 0, len(root.Causes))
		for _, cause := range root.Causes {
			issues = append(issues, schemaIssues(cause)...)
		}
		return issues
	}
	keyword := "schema"
	if path := root.ErrorKind.KeywordPath(); len(path) > 0 {
		keyword = path[len(path)-1]
	}
	return []ValidationIssue{{
		Keyword: keyword,
		Message: root.ErrorKind.LocalizedString(issuePrinter),
		Params:  map[string]any{},
		Path:    jsonPointer(root.InstanceLocation),
	}}
}

func jsonPointer(parts []string) string {
	if len(parts) == 0 {
		return ""
	}
	escaped := make([]string, len(parts))
	for index, part := range parts {
		escaped[index] = strings.ReplaceAll(strings.ReplaceAll(part, "~", "~0"), "/", "~1")
	}
	return "/" + strings.Join(escaped, "/")
}

func ParseServiceDocument(data []byte) (ServiceDocument, error) {
	return parseJSON(data, "service-document.schema.json", "Service Document", serviceDocumentIssues)
}

func ParseAgentServiceDocument(data []byte) (ServiceDocument, error) {
	filtered, err := NormalizeAgentResponse(data, "service-document")
	if err != nil {
		return ParseServiceDocument(data)
	}
	return ParseServiceDocument(filtered)
}

func NormalizeAgentResponse(data []byte, kind string) ([]byte, error) {
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		return nil, err
	}
	normalizeAgentDocument(document, kind)
	filtered, err := json.Marshal(document)
	if err != nil {
		return nil, fmt.Errorf("encode Agent response: %w", err)
	}
	return filtered, nil
}

func normalizeAgentDocument(document map[string]any, kind string) {
	switch kind {
	case "service-document":
		filterAgentProtocols(document)
		if protocols, ok := document["protocols"].(map[string]any); ok {
			filterUnknownAuthentication(protocols, "payments")
		}
		filterNamedList(document, "operations", map[string]bool{
			"get-collection": true, "get-offering": true, "list-collection-offerings": true,
			"list-collections": true, "list-offerings": true, "search-collections": true,
			"search-offerings": true,
		})
		filterUnknownAuthentication(document, "operations")
		filterTypedList(document, "mcp", map[string]bool{"streamable-http": true})
		filterClosedObjectList(document, "operations", map[string]bool{"authentication": true, "name": true})
		filterClosedObjectList(document, "mcp", map[string]bool{"description": true, "name": true, "type": true, "url": true})
		filterPaymentOptions(document)
		normalizeBranding(document)
		normalizeSearchCapabilities(document)
	case "collection", "offering":
		filterTypedList(document, "images", map[string]bool{
			"image/avif": true, "image/jpeg": true, "image/png": true,
			"image/svg+xml": true, "image/webp": true,
		})
		stripObjectList(document, "images", map[string]bool{"alt": true, "height": true, "src": true, "type": true, "width": true})
		normalizeSearchCapabilities(document)
		if kind == "offering" {
			normalizeOffering(document)
		}
	case "collection-page", "offering-page":
		items, ok := document["items"].([]any)
		if !ok {
			return
		}
		itemKind := "collection"
		if kind == "offering-page" {
			itemKind = "offering"
		}
		for _, item := range items {
			if object, itemOK := item.(map[string]any); itemOK {
				normalizeAgentDocument(object, itemKind)
			}
		}
	case "filter-page":
		filterDefinitions(document, knownFilter)
	case "sort-page":
		filterDefinitions(document, knownSort)
	case "problem":
		filterProblemParameters(document)
	}
}

func normalizeBranding(document map[string]any) {
	branding, ok := document["branding"].(map[string]any)
	if !ok {
		return
	}
	for key := range branding {
		if key != "icon" && key != "logo" {
			delete(branding, key)
		}
	}
	for _, member := range []string{"icon", "logo"} {
		image, imageOK := branding[member].(map[string]any)
		imageType, typeOK := image["type"].(string)
		if imageOK && typeOK && imageType != "image/png" && imageType != "image/svg+xml" && imageType != "image/webp" {
			delete(branding, member)
		} else if imageOK {
			for key := range image {
				if key != "src" && key != "type" {
					delete(image, key)
				}
			}
		}
	}
	if len(branding) == 0 {
		delete(document, "branding")
	}
}

func filterClosedObjectList(document map[string]any, member string, allowed map[string]bool) {
	items, ok := document[member].([]any)
	if !ok {
		return
	}
	filtered := items[:0]
	for _, item := range items {
		object, objectOK := item.(map[string]any)
		valid := true
		if objectOK {
			for key := range object {
				if !allowed[key] {
					valid = false
				}
			}
		}
		if valid {
			filtered = append(filtered, item)
		}
	}
	if len(filtered) == 0 {
		delete(document, member)
	} else {
		document[member] = filtered
	}
}

func filterUnknownAuthentication(document map[string]any, member string) {
	items, ok := document[member].([]any)
	if !ok {
		return
	}
	filtered := items[:0]
	for _, item := range items {
		object, objectOK := item.(map[string]any)
		if !objectOK || !hasUnknownAuthentication(object) {
			filtered = append(filtered, item)
		}
	}
	if len(filtered) == 0 {
		delete(document, member)
	} else {
		document[member] = filtered
	}
}

func hasUnknownAuthentication(value map[string]any) bool {
	authentication, ok := value["authentication"].(string)
	return ok && authentication != "not-required" && authentication != "optional" && authentication != "required"
}

func stripObjectList(document map[string]any, member string, allowed map[string]bool) {
	items, ok := document[member].([]any)
	if !ok {
		return
	}
	for _, item := range items {
		if object, objectOK := item.(map[string]any); objectOK {
			for key := range object {
				if !allowed[key] {
					delete(object, key)
				}
			}
		}
	}
}

func normalizeSearchCapabilities(document map[string]any) {
	capabilities, ok := document["search_capabilities"].(map[string]any)
	if !ok {
		return
	}
	for member, recognized := range map[string]func(map[string]any) bool{
		"filters": knownFilter,
		"sorts":   knownSort,
	} {
		source, sourceOK := capabilities[member].(map[string]any)
		items, itemsOK := source["inline"].([]any)
		if !sourceOK || !itemsOK {
			continue
		}
		filtered := items[:0]
		for _, item := range items {
			definition, definitionOK := item.(map[string]any)
			if !definitionOK || recognized(definition) {
				filtered = append(filtered, item)
			}
		}
		if len(filtered) == 0 {
			delete(capabilities, member)
		} else {
			source["inline"] = filtered
		}
	}
	if len(capabilities) == 0 {
		delete(document, "search_capabilities")
	}
}

func filterNamedList(document map[string]any, member string, recognized map[string]bool) {
	filterAgentList(document, member, "name", recognized)
}

func filterTypedList(document map[string]any, member string, recognized map[string]bool) {
	filterAgentList(document, member, "type", recognized)
}

func filterAgentList(document map[string]any, member, discriminator string, recognized map[string]bool) {
	items, ok := document[member].([]any)
	if !ok {
		return
	}
	filtered := items[:0]
	for _, item := range items {
		object, objectOK := item.(map[string]any)
		value, valueOK := object[discriminator].(string)
		if !objectOK || !valueOK || recognized[value] {
			filtered = append(filtered, item)
		}
	}
	if len(filtered) == 0 {
		delete(document, member)
	} else {
		document[member] = filtered
	}
}

func filterPaymentOptions(document map[string]any) {
	protocols, ok := document["protocols"].(map[string]any)
	if !ok {
		return
	}
	payments, ok := protocols["payments"].([]any)
	if !ok {
		return
	}
	recognized := map[string]bool{
		"algorand": true, "aptos": true, "arbitrum": true, "avalanche": true,
		"base": true, "card": true, "ethereum": true, "hedera": true, "inflow": true,
		"lightning": true, "polygon": true, "solana": true, "stellar": true,
		"stripe": true, "tempo": true, "ton": true,
	}
	for _, payment := range payments {
		object, objectOK := payment.(map[string]any)
		options, optionsOK := object["options"].([]any)
		if !objectOK || !optionsOK {
			continue
		}
		filtered := options[:0]
		for _, option := range options {
			name, nameOK := option.(string)
			if !nameOK || recognized[name] {
				filtered = append(filtered, option)
			}
		}
		if len(filtered) == 0 {
			delete(object, "options")
		} else {
			object["options"] = filtered
		}
	}
}

func normalizeOffering(document map[string]any) {
	if schema, ok := document["schema"].(map[string]any); ok && hasUnknownKeys(schema, map[string]bool{"url": true}) {
		delete(document, "schema")
	}
	knownPrices := map[string]bool{
		"fixed": true, "free": true, "metered": true, "quote": true,
		"range": true, "starting_at": true,
	}
	if price, ok := document["price"].(map[string]any); ok {
		if priceType, typeOK := price["type"].(string); typeOK && !knownPrices[priceType] {
			delete(document, "price")
		}
	}
	actions, ok := document["actions"].([]any)
	if !ok {
		return
	}
	filtered := actions[:0]
	for _, action := range actions {
		object, objectOK := action.(map[string]any)
		if objectOK && hasUnknownAuthentication(object) {
			continue
		}
		if objectOK && hasUnknownKeys(object, map[string]bool{"authentication": true, "description": true, "http": true, "id": true, "openapi": true, "rel": true}) {
			continue
		}
		http, httpOK := object["http"].(map[string]any)
		if httpOK && hasUnknownKeys(http, map[string]bool{"href": true, "method": true, "request": true, "response_content_types": true}) {
			continue
		}
		request, requestOK := http["request"].(map[string]any)
		if requestOK && hasUnknownKeys(request, map[string]bool{"content_type": true, "schema": true}) {
			continue
		}
		schema, schemaOK := request["schema"].(map[string]any)
		if schemaOK && hasUnknownKeys(schema, map[string]bool{"url": true}) {
			continue
		}
		openAPI, openAPIOK := object["openapi"].(map[string]any)
		if openAPIOK && hasUnknownKeys(openAPI, map[string]bool{"operation_id": true, "url": true}) {
			continue
		}
		method, methodOK := http["method"].(string)
		if !objectOK || !httpOK || !methodOK || method == "GET" || method == "POST" {
			filtered = append(filtered, action)
		}
	}
	if len(filtered) == 0 {
		delete(document, "actions")
	} else {
		document["actions"] = filtered
	}
}

func hasUnknownKeys(object map[string]any, allowed map[string]bool) bool {
	for key := range object {
		if !allowed[key] {
			return true
		}
	}
	return false
}

func filterDefinitions(document map[string]any, recognized func(map[string]any) bool) {
	items, ok := document["items"].([]any)
	if !ok {
		return
	}
	filtered := items[:0]
	for _, item := range items {
		object, objectOK := item.(map[string]any)
		if !objectOK || recognized(object) {
			filtered = append(filtered, item)
		}
	}
	document["items"] = filtered
}

func knownFilter(definition map[string]any) bool {
	types := map[string]bool{"boolean": true, "date": true, "date-time": true, "decimal": true, "integer": true, "number": true, "string": true}
	if value, ok := definition["type"].(string); ok && !types[value] {
		return false
	}
	operators := map[string]bool{"eq": true, "exists": true, "gt": true, "gte": true, "in": true, "lt": true, "lte": true}
	if values, ok := definition["operators"].([]any); ok {
		for _, value := range values {
			if operator, operatorOK := value.(string); operatorOK && !operators[operator] {
				return false
			}
		}
	}
	if unit, ok := definition["unit"].(map[string]any); ok {
		if system, systemOK := unit["system"].(string); systemOK && system != "service" && system != "ucum" {
			return false
		}
	}
	return true
}

func knownSort(definition map[string]any) bool {
	keys, ok := definition["keys"].([]any)
	if !ok {
		return true
	}
	for _, value := range keys {
		key, keyOK := value.(map[string]any)
		if !keyOK {
			continue
		}
		direction, directionOK := key["direction"].(string)
		missing, missingOK := key["missing"].(string)
		if directionOK && direction != "ascending" && direction != "descending" ||
			missingOK && missing != "first" && missing != "last" {
			return false
		}
	}
	return true
}

func filterProblemParameters(document map[string]any) {
	parameters, ok := document["invalid_params"].([]any)
	if !ok {
		return
	}
	recognized := map[string]bool{"body": true, "header": true, "path": true, "query": true}
	filtered := parameters[:0]
	for _, parameter := range parameters {
		object, objectOK := parameter.(map[string]any)
		location, locationOK := object["in"].(string)
		if !objectOK || !locationOK || recognized[location] {
			filtered = append(filtered, parameter)
		}
	}
	document["invalid_params"] = filtered
}

func filterAgentProtocols(document map[string]any) {
	protocols, ok := document["protocols"].(map[string]any)
	if !ok {
		return
	}
	filterAgentProtocolCategory(protocols, "enrollment", map[string]bool{"aep": true})
	filterAgentProtocolCategory(protocols, "payments", map[string]bool{"mpp": true, "x402": true})
	filterAgentProtocolCategory(protocols, "trust", map[string]bool{"tap": true})
	if len(protocols) == 0 {
		delete(document, "protocols")
	}
}

func filterAgentProtocolCategory(protocols map[string]any, category string, recognized map[string]bool) {
	descriptors, ok := protocols[category].([]any)
	if !ok {
		return
	}
	filtered := descriptors[:0]
	for _, descriptor := range descriptors {
		object, objectOK := descriptor.(map[string]any)
		name, nameOK := object["name"].(string)
		if !objectOK || !nameOK || recognized[name] {
			filtered = append(filtered, descriptor)
		}
	}
	if len(filtered) == 0 && len(descriptors) != 0 {
		delete(protocols, category)
		return
	}
	protocols[category] = filtered
}

func ParseCollection(data []byte) (Collection, error) {
	return parseJSON(data, "collection.schema.json", "Collection", representationIssues[Collection])
}

func ParseOffering(data []byte) (Offering, error) {
	return parseJSON(data, "offering.schema.json", "Offering", representationIssues[Offering])
}

func ParseProblemDetails(data []byte) (ProblemDetails, error) {
	return parseJSON(data, "problem-details.schema.json", "Problem Details", problemDetailsIssues)
}

func ParseProblemResponse(data []byte, status int) (ProblemDetails, error) {
	problem, err := ParseProblemDetails(data)
	if err != nil {
		return ProblemDetails{}, err
	}
	if problem.Status != status {
		return ProblemDetails{}, &ValidationError{DocumentType: "Problem Details", Issues: []ValidationIssue{{
			Keyword: "http-status", Message: "must match the HTTP response status",
			Params: map[string]any{"httpStatus": status}, Path: "/status",
		}}}
	}
	return problem, nil
}

func ParseResourceIdentity(data []byte) (ResourceIdentity, error) {
	return parseJSON[ResourceIdentity](data, "resource-identity.schema.json", "resource identity", nil)
}

func ParsePage[Item any](data []byte) (Page[Item], error) {
	return parseJSON[Page[Item]](data, "page-envelope.schema.json", "page envelope", nil)
}

func ParseCollectionSearchRequest(data []byte) (CollectionSearchRequest, error) {
	return parseJSON[CollectionSearchRequest](data, "collection-search-request.schema.json", "Collection search request", nil)
}

func ParseOfferingSearchRequest(data []byte) (OfferingSearchRequest, error) {
	return parseJSON[OfferingSearchRequest](data, "offering-search-request.schema.json", "Offering search request", nil)
}

func ParseOfferingSearchResponse(data []byte) (OfferingPage[Offering], error) {
	return parseJSON[OfferingPage[Offering]](data, "offering-search-response.schema.json", "Offering search response", nil)
}

func ParseFilterDefinition(data []byte) (FilterDefinition, error) {
	return parseJSON(data, "filter-definition.schema.json", "Filter Definition", filterDefinitionIssues)
}

func ParseSortDefinition(data []byte) (SortDefinition, error) {
	return parseJSON[SortDefinition](data, "sort-definition.schema.json", "Sort Definition", nil)
}

func ParseFilterDefinitionPage(data []byte) (Page[FilterDefinition], error) {
	return parseJSON[Page[FilterDefinition]](data, "filter-definition-page.schema.json", "Filter Definition page", nil)
}

func ParseSortDefinitionPage(data []byte) (Page[SortDefinition], error) {
	return parseJSON[Page[SortDefinition]](data, "sort-definition-page.schema.json", "Sort Definition page", nil)
}

func issue(path, keyword, message string) ValidationIssue {
	return ValidationIssue{Keyword: keyword, Message: message, Params: map[string]any{}, Path: path}
}

func validLanguageTag(value string) bool {
	if _, err := language.Parse(value); err != nil {
		return false
	}

	subtags := strings.Split(strings.ToLower(value), "-")
	if subtags[0] == "x" {
		return true
	}

	variants := make(map[string]struct{})
	extensions := make(map[string]struct{})
	inExtension := false
	for _, subtag := range subtags[1:] {
		if len(subtag) == 1 {
			inExtension = true
			if subtag == "x" {
				break
			}
			if _, exists := extensions[subtag]; exists {
				return false
			}
			extensions[subtag] = struct{}{}
			continue
		}
		if inExtension || !((len(subtag) >= 5 && len(subtag) <= 8) || (len(subtag) == 4 && subtag[0] >= '0' && subtag[0] <= '9')) {
			continue
		}
		if _, exists := variants[subtag]; exists {
			return false
		}
		variants[subtag] = struct{}{}
	}
	return true
}

func serviceDocumentIssues(value ServiceDocument) []ValidationIssue {
	issues := make([]ValidationIssue, 0)
	if _, ok := value.Additional["id"]; ok {
		issues = append(issues, issue("/id", "prohibited", "must not appear in a Service Document"))
	}
	if _, ok := value.Additional["web_url"]; ok {
		issues = append(issues, issue("/web_url", "prohibited", "must not appear in a Service Document"))
	}
	if !validLanguageTag(value.Language) {
		issues = append(issues, issue("/language", "language-tag", "must be a language tag"))
	}
	folded := make(map[string]struct{}, len(value.Localizations))
	containsDefault := false
	for _, tag := range value.Localizations {
		if !validLanguageTag(tag) {
			issues = append(issues, issue("/localizations", "language-tag", "must contain only language tags"))
			break
		}
		key := strings.ToLower(tag)
		if _, exists := folded[key]; exists {
			issues = append(issues, issue("/localizations", "unique-language-tag", "must be unique without regard to case"))
			break
		}
		folded[key] = struct{}{}
		containsDefault = containsDefault || strings.EqualFold(tag, value.Language)
	}
	if !containsDefault {
		issues = append(issues, issue("/localizations", "contains-default-language", "must contain the default language"))
	}
	codePoints := 0
	for _, keyword := range value.Keywords {
		codePoints += utf8.RuneCountInString(keyword)
	}
	if codePoints > 1024 {
		issues = append(issues, issue("/keywords", "max-code-points", "must contain no more than 1024 code points in total"))
	}
	if value.SearchCapabilities != nil && !supports(value.Operations, OperationSearchOfferings) {
		issues = append(issues, issue("/search_capabilities", "operation-support", "requires the search-offerings operation"))
	}
	return issues
}

type resourceRepresentation interface {
	Collection | Offering
}

func representationIssues[Value resourceRepresentation](value Value) []ValidationIssue {
	var representationLanguage string
	var localizations []string
	var images []ResourceImage
	switch typed := any(value).(type) {
	case Collection:
		images = typed.Images
		representationLanguage = typed.Language
		localizations = typed.Localizations
	case Offering:
		images = typed.Images
		representationLanguage = typed.Language
		localizations = typed.Localizations
	}
	issues := make([]ValidationIssue, 0)
	if representationLanguage != "" && !validLanguageTag(representationLanguage) {
		issues = append(issues, issue("/language", "language-tag", "must be a language tag"))
	}
	folded := make(map[string]struct{}, len(localizations))
	for _, tag := range localizations {
		if !validLanguageTag(tag) {
			issues = append(issues, issue("/localizations", "language-tag", "must contain only language tags"))
			break
		}
		key := strings.ToLower(tag)
		if _, exists := folded[key]; exists {
			issues = append(issues, issue("/localizations", "unique-language-tag", "must be unique without regard to case"))
			break
		}
		folded[key] = struct{}{}
	}
	if representationLanguage != "" && len(localizations) > 0 {
		if _, exists := folded[strings.ToLower(representationLanguage)]; !exists {
			issues = append(issues, issue("/localizations", "contains-language", "must contain the representation language"))
		}
	}
	imageSources := make(map[string]struct{}, len(images))
	for _, image := range images {
		if _, exists := imageSources[image.Source]; exists {
			issues = append(issues, issue("/images", "unique-image-source", "must contain unique image sources"))
			break
		}
		imageSources[image.Source] = struct{}{}
	}
	return issues
}

func problemDetailsIssues(value ProblemDetails) []ValidationIssue {
	expectedType := "https://offeringprotocol.org/problems/" + strings.ToLower(strings.ReplaceAll(value.Code, "_", "-"))
	if value.Type == expectedType {
		return nil
	}
	return []ValidationIssue{{
		Keyword: "problem-type", Message: "must correspond to the problem code",
		Params: map[string]any{"expectedType": expectedType}, Path: "/type",
	}}
}

func filterDefinitionIssues(value FilterDefinition) []ValidationIssue {
	issues := make([]ValidationIssue, 0)
	if value.Type == FilterString || value.Type == FilterBoolean {
		for _, operator := range value.Operators {
			if operator == OperatorGreaterThan || operator == OperatorGreaterThanOrEqual || operator == OperatorLessThan || operator == OperatorLessThanOrEqual {
				issues = append(issues, issue("/operators", "operator-type", "contains an operator incompatible with the Filter type"))
				break
			}
		}
	}
	if value.Type == FilterBoolean && value.Unit != nil {
		issues = append(issues, issue("/unit", "unit-type", "must not appear on a boolean Filter"))
	}
	return issues
}

func supports(operations []OperationDescriptor, expected Operation) bool {
	for _, operation := range operations {
		if operation.Name == expected {
			return true
		}
	}
	return false
}
