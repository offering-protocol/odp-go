package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"

	odp "github.com/offering-protocol/odp-go"
	"github.com/offering-protocol/odp-go/service"
)

type request struct {
	Case     map[string]json.RawMessage `json:"case"`
	Role     string                     `json:"role"`
	Sequence int                        `json:"sequence"`
	Vector   struct {
		Subject string `json:"subject"`
	} `json:"vector"`
}

type response struct {
	Message         string `json:"message,omitempty"`
	ProtocolVersion string `json:"protocol_version"`
	Sequence        int    `json:"sequence"`
	Status          string `json:"status"`
}

func main() {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)
	encoder := json.NewEncoder(os.Stdout)
	for scanner.Scan() {
		if strings.TrimSpace(scanner.Text()) == "" {
			continue
		}
		var input request
		if err := json.Unmarshal(scanner.Bytes(), &input); err != nil {
			failProcess(err)
		}
		result := evaluate(input)
		if err := encoder.Encode(result); err != nil {
			failProcess(err)
		}
	}
	if err := scanner.Err(); err != nil {
		failProcess(err)
	}
}

func evaluate(input request) response {
	result := response{ProtocolVersion: "1", Sequence: input.Sequence}
	actual, mapped, err := evaluateCase(input.Vector.Subject, input.Case, input.Role)
	if err != nil {
		result.Status = "failed"
		result.Message = truncate(err.Error(), 1024)
		return result
	}
	if !mapped {
		result.Status = "skipped"
		result.Message = "No public Go operation maps this vector case"
		return result
	}
	if actual {
		result.Status = "passed"
	} else {
		result.Status = "failed"
		result.Message = "Public API result did not match the vector"
	}
	return result
}

func evaluateCase(subject string, test map[string]json.RawMessage, role string) (bool, bool, error) {
	valid, _ := field[bool](test, "valid")
	switch subject {
	case "local-identifier":
		value, err := requiredField[string](test, "value")
		return odp.IsLocalResourceIdentifier(value) == valid, true, err
	case "identity-comparison":
		left, leftErr := requiredField[odp.ResourceIdentity](test, "left")
		right, rightErr := requiredField[odp.ResourceIdentity](test, "right")
		expected, expectedErr := requiredField[bool](test, "same_identity")
		return left.Equal(right) == expected, true, errors.Join(leftErr, rightErr, expectedErr)
	case "service-origin":
		value, err := requiredField[string](test, "value")
		origin, originErr := odp.DeriveServiceOrigin(value)
		return (originErr == nil && origin == value) == valid, true, err
	case "resource-reference":
		value, err := requiredField[string](test, "value")
		_, resolveErr := odp.ResolveResourceReference(value, "https://service.example")
		return (resolveErr == nil) == valid, true, err
	case "service-document":
		return parseResult(test, "document", odp.ParseServiceDocument)
	case "collection-envelope":
		return parseResult(test, "document", odp.ParseCollection)
	case "offering-contract":
		representation, _ := field[string](test, "representation")
		if representation != "full" {
			return false, false, nil
		}
		return parseResult(test, "document", odp.ParseOffering)
	case "collection-search-contract":
		if operation(test) != "validate-request" {
			return false, false, nil
		}
		return parseResult(test, "request", odp.ParseCollectionSearchRequest)
	case "offering-search-contract":
		if operation(test) != "validate-request" {
			return false, false, nil
		}
		return parseResult(test, "request", odp.ParseOfferingSearchRequest)
	case "filter-sort-contract":
		switch operation(test) {
		case "validate-definition":
			return parseResult(test, "definition", odp.ParseFilterDefinition)
		case "validate-sort":
			if _, found := test["definitions"]; found {
				return false, false, nil
			}
			return parseResult(test, "sort", odp.ParseSortDefinition)
		default:
			return false, false, nil
		}
	case "pagination-contract":
		return evaluatePagination(test)
	case "errors-limits-contract":
		return evaluateErrorsAndLimits(test)
	case "role-baseline":
		return evaluateBaseline(test, role)
	default:
		return false, false, nil
	}
}

func evaluatePagination(test map[string]json.RawMessage) (bool, bool, error) {
	valid, _ := field[bool](test, "valid")
	switch operation(test) {
	case "validate-page":
		return parseResult(test, "page", odp.ParsePage[json.RawMessage])
	case "validate-limit":
		limit, err := requiredField[int](test, "limit")
		return (limit >= 1 && limit <= 100) == valid, true, err
	case "validate-next":
		next, nextErr := requiredField[string](test, "next")
		origin, originErr := requiredField[string](test, "service_origin")
		_, err := odp.ResolveContinuation(next, origin)
		return (err == nil) == valid, true, errors.Join(nextErr, originErr)
	default:
		return false, false, nil
	}
}

func evaluateErrorsAndLimits(test map[string]json.RawMessage) (bool, bool, error) {
	valid, _ := field[bool](test, "valid")
	if operation(test) == "validate-problem" {
		status, statusErr := requiredField[int](test, "http_status")
		problem, problemErr := requiredField[json.RawMessage](test, "problem")
		_, err := odp.ParseProblemResponse(problem, status)
		return (err == nil) == valid, true, errors.Join(statusErr, problemErr)
	}
	resource, _ := field[string](test, "resource")
	if operation(test) != "validate-limit" || resource != "request" {
		return false, false, nil
	}
	bytes, err := requiredField[int](test, "bytes")
	if err != nil {
		return false, true, err
	}
	status, err := serviceRequestStatus(bytes)
	return (status == http.StatusOK) == valid, true, err
}

func serviceRequestStatus(size int) (int, error) {
	catalog := service.Catalog{
		ListOfferings: func(context.Context, service.CatalogRequest) (odp.Page[odp.Offering], error) {
			return odp.Page[odp.Offering]{Items: []odp.Offering{}, ODPVersion: odp.Version}, nil
		},
		GetOffering: func(context.Context, string, service.CatalogRequest) (*odp.Offering, error) { return nil, nil },
		SearchOfferings: func(context.Context, *odp.OfferingSearchRequest, service.CatalogRequest) (odp.OfferingPage[odp.Offering], error) {
			return odp.OfferingPage[odp.Offering]{Items: []odp.Offering{}, ODPVersion: odp.Version}, nil
		},
	}
	runtime, err := service.New(service.Options{Catalog: catalog, Document: odp.ServiceDocument{Description: "Conformance Service", HTTP: odp.HTTPConfiguration{EndpointBase: "/odp"}, Language: "en", Localizations: []string{"en"}, Name: "Conformance Service"}})
	if err != nil {
		return 0, err
	}
	payload := `{"odp_version":"1.0","query":"gpu"}`
	body := payload + strings.Repeat(" ", size-len(payload))
	request := httptest.NewRequest(http.MethodPost, "https://service.example/odp/offerings/search", strings.NewReader(body))
	request.Header.Set("Accept", service.MediaType)
	request.Header.Set("Content-Type", service.MediaType)
	response := httptest.NewRecorder()
	runtime.ServeHTTP(response, request)
	return response.Code, nil
}

func evaluateBaseline(test map[string]json.RawMessage, role string) (bool, bool, error) {
	caseRole, err := requiredField[string](test, "role")
	if err != nil {
		return false, true, err
	}
	if caseRole != role {
		return false, false, nil
	}
	valid, _ := field[bool](test, "valid")
	if role == "agent" {
		behaviors, err := requiredField[[]string](test, "behaviors")
		if err != nil {
			return false, true, err
		}
		required := []string{"enforce-compatibility", "enforce-redirect-and-security", "follow-pagination", "get-offering", "handle-errors-and-limits", "honor-caching", "inspect-service", "list-offerings", "process-localization", "process-representations"}
		actual := !slices.ContainsFunc(required, func(value string) bool { return !slices.Contains(behaviors, value) })
		return actual == valid, true, nil
	}
	operations, operationsErr := requiredField[[]odp.Operation](test, "operations")
	listResponse, listErr := requiredField[json.RawMessage](test, "list_response")
	getResponse, getErr := requiredField[json.RawMessage](test, "get_response")
	descriptors := make([]odp.OperationDescriptor, len(operations))
	for index, operation := range operations {
		descriptors[index] = odp.OperationDescriptor{Authentication: odp.AuthenticationNotRequired, Name: operation}
	}
	document := odp.ServiceDocument{Description: "Conformance Service", HTTP: odp.HTTPConfiguration{EndpointBase: "/odp"}, Language: "en", Localizations: []string{"en"}, Name: "Conformance Service", ODPVersion: odp.Version, Operations: descriptors}
	encoded, marshalErr := json.Marshal(document)
	_, documentErr := odp.ParseServiceDocument(encoded)
	_, pageErr := odp.ParsePage[odp.Offering](listResponse)
	_, offeringErr := odp.ParseOffering(getResponse)
	actual := documentErr == nil && pageErr == nil && offeringErr == nil
	return actual == valid, true, errors.Join(operationsErr, listErr, getErr, marshalErr)
}

func parseResult[Value any](test map[string]json.RawMessage, key string, parse func([]byte) (Value, error)) (bool, bool, error) {
	value, err := requiredField[json.RawMessage](test, key)
	if err != nil {
		return false, true, err
	}
	valid, _ := field[bool](test, "valid")
	_, parseErr := parse(value)
	return (parseErr == nil) == valid, true, nil
}

func operation(test map[string]json.RawMessage) string {
	value, _ := field[string](test, "operation")
	return value
}

func requiredField[Value any](object map[string]json.RawMessage, name string) (Value, error) {
	value, found := field[Value](object, name)
	if !found {
		var zero Value
		return zero, fmt.Errorf("case omitted %s", name)
	}
	return value, nil
}

func field[Value any](object map[string]json.RawMessage, name string) (Value, bool) {
	var value Value
	raw, found := object[name]
	if !found || json.Unmarshal(raw, &value) != nil {
		return value, false
	}
	return value, true
}

func truncate(value string, maximum int) string {
	if len(value) <= maximum {
		return value
	}
	return value[:maximum]
}

func failProcess(err error) {
	_, _ = fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
