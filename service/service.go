// Package service provides framework-neutral HTTP integration for ODP Services.
package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	odp "github.com/offering-protocol/odp-go"
	"github.com/offering-protocol/odp-go/internal/jsonvalue"
)

const (
	MediaType               = "application/odp+json"
	ProblemMediaType        = "application/problem+json"
	MaximumRequestBodyBytes = 65_536
	MaximumResourceBytes    = 524_288
)

const (
	maximumDocumentDepth = 8
	maximumResourceDepth = 16
)

type CatalogRequest struct {
	Cursor         string
	Language       string
	Limit          int
	Representation odp.Representation
	Request        *http.Request
}

type Catalog struct {
	GetCollection           func(context.Context, string, CatalogRequest) (*odp.Collection, error)
	GetOffering             func(context.Context, string, CatalogRequest) (*odp.Offering, error)
	ListCollectionOfferings func(context.Context, string, CatalogRequest) (odp.Page[odp.Offering], error)
	ListCollections         func(context.Context, CatalogRequest) (odp.Page[odp.Collection], error)
	ListOfferings           func(context.Context, CatalogRequest) (odp.Page[odp.Offering], error)
	SearchCollections       func(context.Context, *odp.CollectionSearchRequest, CatalogRequest) (odp.Page[odp.Collection], error)
	SearchOfferings         func(context.Context, *odp.OfferingSearchRequest, CatalogRequest) (odp.OfferingPage[odp.Offering], error)
}

type Options struct {
	Catalog                 Catalog
	Document                odp.ServiceDocument
	OperationAuthentication map[odp.Operation]odp.AuthenticationRequirement
}

type Error struct {
	Code    string
	Header  http.Header
	Message string
	Status  int
}

func (err *Error) Error() string {
	return err.Message
}

type Service struct {
	catalog      Catalog
	document     odp.ServiceDocument
	endpointBase string
}

func New(options Options) (*Service, error) {
	if options.Catalog.ListOfferings == nil || options.Catalog.GetOffering == nil {
		return nil, errors.New("ODP catalog requires ListOfferings and GetOffering handlers")
	}
	operations := []odp.Operation{odp.OperationGetOffering, odp.OperationListOfferings}
	optional := []struct {
		operation odp.Operation
		enabled   bool
	}{
		{odp.OperationGetCollection, options.Catalog.GetCollection != nil},
		{odp.OperationListCollectionOfferings, options.Catalog.ListCollectionOfferings != nil},
		{odp.OperationListCollections, options.Catalog.ListCollections != nil},
		{odp.OperationSearchCollections, options.Catalog.SearchCollections != nil},
		{odp.OperationSearchOfferings, options.Catalog.SearchOfferings != nil},
	}
	for _, capability := range optional {
		if capability.enabled {
			operations = append(operations, capability.operation)
		}
	}
	sort.Slice(operations, func(left, right int) bool { return operations[left] < operations[right] })
	for operation := range options.OperationAuthentication {
		if !slices.Contains(operations, operation) {
			return nil, fmt.Errorf("authentication configured for unadvertised ODP operation %s", operation)
		}
	}
	descriptors := make([]odp.OperationDescriptor, len(operations))
	for index, operation := range operations {
		authentication := options.OperationAuthentication[operation]
		if authentication == "" {
			authentication = odp.AuthenticationNotRequired
		}
		descriptors[index] = odp.OperationDescriptor{Authentication: authentication, Name: operation}
	}
	document := options.Document
	document.ODPVersion = odp.Version
	document.Operations = descriptors
	encoded, err := json.Marshal(document)
	if err != nil {
		return nil, fmt.Errorf("encode ODP Service Document: %w", err)
	}
	document, err = odp.ParseServiceDocument(encoded)
	if err != nil {
		return nil, err
	}
	if len(encoded) > MaximumRequestBodyBytes || jsonvalue.Depth(encoded) > maximumDocumentDepth {
		return nil, errors.New("ODP Service Document exceeds its resource limits")
	}
	return &Service{
		catalog:      options.Catalog,
		document:     document,
		endpointBase: strings.TrimSuffix(document.HTTP.EndpointBase, "/"),
	}, nil
}

func (service *Service) Document() odp.ServiceDocument {
	encoded, _ := json.Marshal(service.document)
	var document odp.ServiceDocument
	_ = json.Unmarshal(encoded, &document)
	return document
}

func (service *Service) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if err := service.serve(writer, request); err != nil {
		var serviceError *Error
		if errors.As(err, &serviceError) {
			writeProblem(writer, serviceError)
			return
		}
		writeProblem(writer, &Error{
			Code: "INTERNAL_ERROR", Message: "The ODP Service could not complete the request", Status: http.StatusInternalServerError,
		})
	}
}

func (service *Service) serve(writer http.ResponseWriter, request *http.Request) error {
	if !acceptsODP(request.Header.Get("Accept")) {
		return requestError(http.StatusNotAcceptable, "NOT_ACCEPTABLE", "Accept must allow "+MediaType)
	}
	path := request.URL.Path
	if path == "/.well-known/odp" {
		if err := requireMethod(request, http.MethodGet); err != nil {
			return err
		}
		return writeJSON(writer, service.document, service.document.Language, MaximumRequestBodyBytes, maximumDocumentDepth)
	}
	if !strings.HasPrefix(path, service.endpointBase+"/") {
		return requestError(http.StatusNotFound, "NOT_FOUND", "ODP resource not found")
	}
	operationPath := strings.TrimPrefix(path, service.endpointBase)
	switch operationPath {
	case "/offerings":
		if err := requireMethod(request, http.MethodGet); err != nil {
			return err
		}
		input, err := catalogRequest(request, odp.RepresentationTerse)
		if err != nil {
			return err
		}
		page, err := service.catalog.ListOfferings(request.Context(), input)
		if err != nil {
			return err
		}
		page, err = validateOfferingPage(page, input.Representation)
		if err != nil {
			return err
		}
		return writeJSON(writer, page, service.document.Language, MaximumResourceBytes, maximumResourceDepth)
	case "/offerings/search":
		return service.searchOfferings(writer, request)
	case "/collections":
		if err := requireMethod(request, http.MethodGet); err != nil {
			return err
		}
		if service.catalog.ListCollections == nil {
			return unsupported(odp.OperationListCollections)
		}
		input, err := catalogRequest(request, odp.RepresentationTerse)
		if err != nil {
			return err
		}
		page, err := service.catalog.ListCollections(request.Context(), input)
		if err != nil {
			return err
		}
		page, err = validateCollectionPage(page, input.Representation)
		if err != nil {
			return err
		}
		return writeJSON(writer, page, service.document.Language, MaximumResourceBytes, maximumResourceDepth)
	case "/collections/search":
		return service.searchCollections(writer, request)
	}
	if id, ok, err := resourceID(operationPath, "/offerings/"); err != nil {
		return err
	} else if ok {
		return service.getOffering(writer, request, id)
	}
	if id, ok, err := collectionOfferingID(operationPath); err != nil {
		return err
	} else if ok {
		return service.listCollectionOfferings(writer, request, id)
	}
	if id, ok, err := resourceID(operationPath, "/collections/"); err != nil {
		return err
	} else if ok {
		return service.getCollection(writer, request, id)
	}
	return requestError(http.StatusNotFound, "NOT_FOUND", "ODP resource not found")
}

func (service *Service) getOffering(writer http.ResponseWriter, request *http.Request, id string) error {
	if err := requireMethod(request, http.MethodGet); err != nil {
		return err
	}
	input, err := catalogRequest(request, odp.RepresentationFull)
	if err != nil {
		return err
	}
	offering, err := service.catalog.GetOffering(request.Context(), id, input)
	if err != nil {
		return err
	}
	if offering == nil {
		return requestError(http.StatusNotFound, "NOT_FOUND", "Offering not found")
	}
	validated, err := validateOffering(*offering, input.Representation)
	if err != nil {
		return err
	}
	if validated.ID != id {
		return errors.New("Offering identifier does not match its request path")
	}
	return writeJSON(writer, validated, representationLanguage(validated.Language, service.document.Language), MaximumResourceBytes, maximumResourceDepth)
}

func (service *Service) getCollection(writer http.ResponseWriter, request *http.Request, id string) error {
	if err := requireMethod(request, http.MethodGet); err != nil {
		return err
	}
	if service.catalog.GetCollection == nil {
		return unsupported(odp.OperationGetCollection)
	}
	input, err := catalogRequest(request, odp.RepresentationFull)
	if err != nil {
		return err
	}
	collection, err := service.catalog.GetCollection(request.Context(), id, input)
	if err != nil {
		return err
	}
	if collection == nil {
		return requestError(http.StatusNotFound, "NOT_FOUND", "Collection not found")
	}
	validated, err := validateCollection(*collection, input.Representation)
	if err != nil {
		return err
	}
	if validated.ID != id {
		return errors.New("Collection identifier does not match its request path")
	}
	return writeJSON(writer, validated, representationLanguage(validated.Language, service.document.Language), MaximumResourceBytes, maximumResourceDepth)
}

func (service *Service) listCollectionOfferings(writer http.ResponseWriter, request *http.Request, id string) error {
	if err := requireMethod(request, http.MethodGet); err != nil {
		return err
	}
	if service.catalog.ListCollectionOfferings == nil {
		return unsupported(odp.OperationListCollectionOfferings)
	}
	input, err := catalogRequest(request, odp.RepresentationTerse)
	if err != nil {
		return err
	}
	page, err := service.catalog.ListCollectionOfferings(request.Context(), id, input)
	if err != nil {
		return err
	}
	page, err = validateOfferingPage(page, input.Representation)
	if err != nil {
		return err
	}
	return writeJSON(writer, page, service.document.Language, MaximumResourceBytes, maximumResourceDepth)
}

func (service *Service) searchOfferings(writer http.ResponseWriter, request *http.Request) error {
	if service.catalog.SearchOfferings == nil {
		return unsupported(odp.OperationSearchOfferings)
	}
	input, err := catalogRequest(request, odp.RepresentationTerse)
	if err != nil {
		return err
	}
	var query *odp.OfferingSearchRequest
	if request.Method == http.MethodGet {
		if input.Cursor == "" {
			return requestError(http.StatusBadRequest, "INVALID_REQUEST", "Search continuation requires a cursor")
		}
	} else {
		if err := requireMethod(request, http.MethodPost); err != nil {
			return err
		}
		body, err := readRequestBody(request)
		if err != nil {
			return err
		}
		parsed, err := odp.ParseOfferingSearchRequest(body)
		if err != nil {
			return invalidRequest(err)
		}
		query = &parsed
		if parsed.Limit != 0 {
			input.Limit = parsed.Limit
		}
	}
	page, err := service.catalog.SearchOfferings(request.Context(), query, input)
	if err != nil {
		return err
	}
	if len(page.Items) > 100 {
		return errors.New("ODP page contains more than 100 items")
	}
	page.ODPVersion = odp.Version
	for index, offering := range page.Items {
		page.Items[index], err = validateOffering(offering, input.Representation)
		if err != nil {
			return err
		}
	}
	return writeJSON(writer, page, service.document.Language, MaximumResourceBytes, maximumResourceDepth)
}

func (service *Service) searchCollections(writer http.ResponseWriter, request *http.Request) error {
	if service.catalog.SearchCollections == nil {
		return unsupported(odp.OperationSearchCollections)
	}
	input, err := catalogRequest(request, odp.RepresentationTerse)
	if err != nil {
		return err
	}
	var query *odp.CollectionSearchRequest
	if request.Method == http.MethodGet {
		if input.Cursor == "" {
			return requestError(http.StatusBadRequest, "INVALID_REQUEST", "Search continuation requires a cursor")
		}
	} else {
		if err := requireMethod(request, http.MethodPost); err != nil {
			return err
		}
		body, err := readRequestBody(request)
		if err != nil {
			return err
		}
		parsed, err := odp.ParseCollectionSearchRequest(body)
		if err != nil {
			return invalidRequest(err)
		}
		query = &parsed
		if parsed.Limit != 0 {
			input.Limit = parsed.Limit
		}
	}
	page, err := service.catalog.SearchCollections(request.Context(), query, input)
	if err != nil {
		return err
	}
	page, err = validateCollectionPage(page, input.Representation)
	if err != nil {
		return err
	}
	return writeJSON(writer, page, service.document.Language, MaximumResourceBytes, maximumResourceDepth)
}

func catalogRequest(request *http.Request, defaultRepresentation odp.Representation) (CatalogRequest, error) {
	query := request.URL.Query()
	representationValues := query["representation"]
	if len(representationValues) > 1 {
		return CatalogRequest{}, requestError(http.StatusBadRequest, "INVALID_REQUEST", "representation must not be repeated")
	}
	representation := defaultRepresentation
	if len(representationValues) == 1 {
		representation = odp.Representation(representationValues[0])
	}
	if representation != odp.RepresentationTerse && representation != odp.RepresentationFull {
		return CatalogRequest{}, requestError(http.StatusBadRequest, "INVALID_REQUEST", "representation must be terse or full")
	}
	limitValues := query["limit"]
	if len(limitValues) > 1 {
		return CatalogRequest{}, requestError(http.StatusBadRequest, "INVALID_REQUEST", "limit must not be repeated")
	}
	limit := 0
	if len(limitValues) == 1 {
		parsed, err := strconv.Atoi(limitValues[0])
		if err != nil || parsed < 1 || parsed > 100 {
			return CatalogRequest{}, requestError(http.StatusBadRequest, "INVALID_REQUEST", "limit must be an integer from 1 through 100")
		}
		limit = parsed
	}
	cursorValues := query["cursor"]
	if len(cursorValues) > 1 {
		return CatalogRequest{}, requestError(http.StatusBadRequest, "INVALID_REQUEST", "cursor must not be repeated")
	}
	cursor := ""
	if len(cursorValues) == 1 {
		cursor = cursorValues[0]
	}
	return CatalogRequest{
		Cursor: cursor, Language: request.Header.Get("Accept-Language"), Limit: limit,
		Representation: representation, Request: request,
	}, nil
}

func readRequestBody(request *http.Request) ([]byte, error) {
	contentType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || !strings.EqualFold(contentType, MediaType) {
		return nil, requestError(http.StatusUnsupportedMediaType, "UNSUPPORTED_MEDIA_TYPE", "Content-Type must be "+MediaType)
	}
	if request.ContentLength > MaximumRequestBodyBytes {
		return nil, requestError(http.StatusRequestEntityTooLarge, "REQUEST_TOO_LARGE", "ODP request body exceeds its byte limit")
	}
	data, err := io.ReadAll(io.LimitReader(request.Body, MaximumRequestBodyBytes+1))
	if err != nil {
		return nil, requestError(http.StatusBadRequest, "INVALID_REQUEST", "ODP request body is invalid")
	}
	if len(data) > MaximumRequestBodyBytes {
		return nil, requestError(http.StatusRequestEntityTooLarge, "REQUEST_TOO_LARGE", "ODP request body exceeds its byte limit")
	}
	if !utf8.Valid(data) {
		return nil, requestError(http.StatusBadRequest, "INVALID_REQUEST", "ODP request body must use UTF-8")
	}
	if depth := jsonvalue.Depth(data); depth > maximumResourceDepth {
		return nil, requestError(http.StatusBadRequest, "INVALID_REQUEST", "ODP request body exceeds its JSON depth limit")
	}
	return data, nil
}

func validateOfferingPage(page odp.Page[odp.Offering], representation odp.Representation) (odp.Page[odp.Offering], error) {
	if len(page.Items) > 100 {
		return odp.Page[odp.Offering]{}, errors.New("ODP page contains more than 100 items")
	}
	page.ODPVersion = odp.Version
	for index, offering := range page.Items {
		validated, err := validateOffering(offering, representation)
		if err != nil {
			return odp.Page[odp.Offering]{}, err
		}
		page.Items[index] = validated
	}
	return page, nil
}

func validateCollectionPage(page odp.Page[odp.Collection], representation odp.Representation) (odp.Page[odp.Collection], error) {
	if len(page.Items) > 100 {
		return odp.Page[odp.Collection]{}, errors.New("ODP page contains more than 100 items")
	}
	page.ODPVersion = odp.Version
	for index, collection := range page.Items {
		validated, err := validateCollection(collection, representation)
		if err != nil {
			return odp.Page[odp.Collection]{}, err
		}
		page.Items[index] = validated
	}
	return page, nil
}

func validateOffering(offering odp.Offering, representation odp.Representation) (odp.Offering, error) {
	forValidation := offering
	forValidation.ODPVersion = odp.Version
	encoded, err := json.Marshal(forValidation)
	if err != nil {
		return odp.Offering{}, err
	}
	validated, err := odp.ParseOffering(encoded)
	if err != nil {
		return odp.Offering{}, err
	}
	if representation == odp.RepresentationTerse && len(validated.Actions) != 0 {
		return odp.Offering{}, errors.New("ODP Terse Offering cannot contain Actions")
	}
	actionIDs := make(map[string]struct{}, len(validated.Actions))
	for _, action := range validated.Actions {
		if _, duplicate := actionIDs[action.ID]; duplicate {
			return odp.Offering{}, errors.New("ODP Offering Action identifiers must be unique")
		}
		actionIDs[action.ID] = struct{}{}
	}
	if representation == odp.RepresentationFull && len(validated.DetailFields) != 0 {
		return odp.Offering{}, errors.New("ODP Full Offering cannot contain detail_fields")
	}
	if representation == odp.RepresentationTerse {
		return offering, nil
	}
	return validated, nil
}

func validateCollection(collection odp.Collection, representation odp.Representation) (odp.Collection, error) {
	forValidation := collection
	forValidation.ODPVersion = odp.Version
	encoded, err := json.Marshal(forValidation)
	if err != nil {
		return odp.Collection{}, err
	}
	validated, err := odp.ParseCollection(encoded)
	if err != nil {
		return odp.Collection{}, err
	}
	if representation == odp.RepresentationFull && len(validated.DetailFields) != 0 {
		return odp.Collection{}, errors.New("ODP Full Collection cannot contain detail_fields")
	}
	if representation == odp.RepresentationTerse {
		return collection, nil
	}
	return validated, nil
}

func writeJSON(writer http.ResponseWriter, value any, language string, maximumBytes, maximumDepth int) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(encoded) > maximumBytes || jsonvalue.Depth(encoded) > maximumDepth {
		return errors.New("ODP response exceeds its resource limits")
	}
	writer.Header().Set("Content-Language", language)
	writer.Header().Set("Content-Type", MediaType)
	writer.Header().Set("Vary", "Accept-Language")
	_, err = writer.Write(encoded)
	return err
}

func writeProblem(writer http.ResponseWriter, problem *Error) {
	details := odp.ProblemDetails{
		Code: problem.Code, Status: problem.Status, Title: problem.Message,
		Type: "https://offeringprotocol.org/problems/" + strings.ToLower(strings.ReplaceAll(problem.Code, "_", "-")),
	}
	encoded, err := json.Marshal(details)
	if err != nil {
		encoded = nil
	}
	if _, err := odp.ParseProblemResponse(encoded, problem.Status); err != nil || len(encoded) > 16_384 || jsonvalue.Depth(encoded) > maximumResourceDepth {
		problem = &Error{
			Code: "INTERNAL_ERROR", Message: "The ODP Service could not complete the request", Status: http.StatusInternalServerError,
		}
		details = odp.ProblemDetails{
			Code: problem.Code, Status: problem.Status, Title: problem.Message,
			Type: "https://offeringprotocol.org/problems/internal-error",
		}
		encoded, _ = json.Marshal(details)
	}
	for name, values := range problem.Header {
		for _, value := range values {
			writer.Header().Add(name, value)
		}
	}
	writer.Header().Set("Content-Type", ProblemMediaType)
	writer.WriteHeader(problem.Status)
	_, _ = writer.Write(encoded)
}

func acceptsODP(accept string) bool {
	if strings.TrimSpace(accept) == "" {
		return true
	}
	bestSpecificity := -1
	bestQuality := 0.0
	for _, entry := range strings.Split(accept, ",") {
		mediaType, parameters, err := mime.ParseMediaType(strings.TrimSpace(entry))
		if err != nil {
			continue
		}
		specificity := -1
		switch strings.ToLower(mediaType) {
		case MediaType:
			specificity = 2
		case "application/*":
			specificity = 1
		case "*/*":
			specificity = 0
		}
		if specificity < 0 {
			continue
		}
		quality := 1.0
		if raw, ok := parameters["q"]; ok {
			parsed, err := strconv.ParseFloat(raw, 64)
			if err != nil || parsed < 0 || parsed > 1 {
				continue
			}
			quality = parsed
		}
		if specificity > bestSpecificity || (specificity == bestSpecificity && quality > bestQuality) {
			bestSpecificity, bestQuality = specificity, quality
		}
	}
	return bestSpecificity >= 0 && bestQuality > 0
}

func requireMethod(request *http.Request, expected string) error {
	if request.Method == expected {
		return nil
	}
	return &Error{
		Code: "METHOD_NOT_ALLOWED", Header: http.Header{"Allow": []string{expected}},
		Message: "ODP operation requires " + expected, Status: http.StatusMethodNotAllowed,
	}
}

func resourceID(path, prefix string) (string, bool, error) {
	if !strings.HasPrefix(path, prefix) {
		return "", false, nil
	}
	suffix := strings.TrimPrefix(path, prefix)
	if suffix == "" || strings.Contains(suffix, "/") {
		return "", false, nil
	}
	return decodeIdentifier(suffix)
}

func collectionOfferingID(path string) (string, bool, error) {
	const prefix = "/collections/"
	const suffix = "/offerings"
	if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, suffix) {
		return "", false, nil
	}
	id := strings.TrimSuffix(strings.TrimPrefix(path, prefix), suffix)
	if id == "" || strings.Contains(id, "/") {
		return "", false, nil
	}
	return decodeIdentifier(id)
}

func decodeIdentifier(value string) (string, bool, error) {
	decoded, err := url.PathUnescape(value)
	if err != nil || !odp.IsLocalResourceIdentifier(decoded) {
		return "", false, requestError(http.StatusBadRequest, "INVALID_REQUEST", "Resource identifier is malformed")
	}
	return decoded, true, nil
}

func requestError(status int, code, message string) *Error {
	return &Error{Code: code, Message: message, Status: status}
}

func unsupported(operation odp.Operation) *Error {
	return requestError(http.StatusNotFound, "NOT_FOUND", string(operation)+" is not supported")
}

func invalidRequest(err error) *Error {
	var validation *odp.ValidationError
	if errors.As(err, &validation) {
		return requestError(http.StatusBadRequest, "INVALID_REQUEST", validation.Error())
	}
	return requestError(http.StatusBadRequest, "INVALID_REQUEST", "ODP request body is invalid")
}

func representationLanguage(language, fallback string) string {
	if language != "" {
		return language
	}
	return fallback
}
