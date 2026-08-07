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

func ParseCollection(data []byte) (Collection, error) {
	return parseJSON(data, "collection.schema.json", "Collection", localizedIssues[Collection])
}

func ParseOffering(data []byte) (Offering, error) {
	return parseJSON(data, "offering.schema.json", "Offering", localizedIssues[Offering])
}

func ParseProblemDetails(data []byte) (ProblemDetails, error) {
	return parseJSON[ProblemDetails](data, "problem-details.schema.json", "Problem Details", nil)
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

type localizedRepresentation interface {
	Collection | Offering
}

func localizedIssues[Value localizedRepresentation](value Value) []ValidationIssue {
	var representationLanguage string
	var localizations []string
	switch typed := any(value).(type) {
	case Collection:
		representationLanguage = typed.Language
		localizations = typed.Localizations
	case Offering:
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
	return issues
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
