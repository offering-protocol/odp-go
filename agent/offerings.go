package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"

	odp "github.com/offering-protocol/odp-go"
)

func (client *ServiceClient) GetOfferingDetails(ctx context.Context, id string) (OfferingDetails, error) {
	offering, inspection, err := client.getOffering(ctx, id, odp.RepresentationFull)
	if err != nil {
		return OfferingDetails{}, err
	}
	serviceOpenAPIURL := ""
	if inspection.Document.HTTP.OpenAPI != nil {
		serviceOpenAPIURL = inspection.Document.HTTP.OpenAPI.URL
	}
	actions, issues := normalizeActions(offering.Actions, client.serviceOrigin, serviceOpenAPIURL)
	details := OfferingDetails{Actions: actions, Issues: issues, Offering: offering}
	if offering.Schema == nil {
		return details, nil
	}
	reference, err := resolveHTTPSReference(offering.Schema.URL, client.serviceOrigin)
	if err != nil {
		details.Offering.Attributes = nil
		details.Issues = append(details.Issues, OfferingIssue{Message: err.Error(), Scope: OfferingIssueAttributeSchema})
		return details, nil
	}
	schema, validator, err := client.resolveSchema(ctx, reference)
	if err != nil {
		details.Offering.Attributes = nil
		details.Issues = append(details.Issues, OfferingIssue{Message: err.Error(), Scope: OfferingIssueAttributeSchema})
		return details, nil
	}
	details.AttributeSchema = schema
	if offering.Attributes != nil {
		encoded, _ := json.Marshal(offering.Attributes)
		var attributes any
		if json.Unmarshal(encoded, &attributes) != nil || validator.Validate(attributes) != nil {
			details.Offering.Attributes = nil
			details.Issues = append(details.Issues, OfferingIssue{Message: "Offering attributes do not match their Attribute Schema", Scope: OfferingIssueAttributes})
		}
	}
	return details, nil
}

func (client *ServiceClient) ResolveAction(ctx context.Context, offeringID, actionID string) (ResolvedAction, error) {
	details, err := client.GetOfferingDetails(ctx, offeringID)
	if err != nil {
		return ResolvedAction{}, err
	}
	var action *DiscoveredAction
	for index := range details.Actions {
		if details.Actions[index].ID == actionID {
			action = &details.Actions[index]
			break
		}
	}
	if action == nil {
		return ResolvedAction{}, fmt.Errorf("ODP Offering does not expose usable Action %s", actionID)
	}
	result := ResolvedAction{Action: *action}
	if action.HTTP != nil {
		if action.HTTP.Request == nil || action.HTTP.Request.Schema == nil {
			return result, nil
		}
		reference, err := resolveHTTPSReference(action.HTTP.Request.Schema.URL, client.serviceOrigin)
		if err != nil {
			return ResolvedAction{}, err
		}
		result.RequestSchema, _, err = client.resolveSchema(ctx, reference)
		return result, err
	}
	if action.OpenAPI == nil {
		return ResolvedAction{}, errors.New("ODP Action has no usable target")
	}
	result.OpenAPIDocument, result.Operation, err = client.resolveOpenAPI(ctx, action.OpenAPI.URL, action.OpenAPI.OperationID)
	return result, err
}

func normalizeActions(actions []odp.Action, serviceOrigin, serviceOpenAPIURL string) ([]DiscoveredAction, []OfferingIssue) {
	counts := map[string]int{}
	for _, action := range actions {
		counts[action.ID]++
	}
	result := []DiscoveredAction{}
	issues := []OfferingIssue{}
	reported := map[string]bool{}
	for _, action := range actions {
		if counts[action.ID] > 1 {
			if !reported[action.ID] {
				issues = append(issues, OfferingIssue{ActionID: action.ID, Message: fmt.Sprintf("Duplicate Action identifier %s", action.ID), Scope: OfferingIssueAction})
				reported[action.ID] = true
			}
			continue
		}
		discovered := DiscoveredAction{Authentication: action.Authentication, Description: action.Description, ID: action.ID, Rel: action.Rel}
		if action.HTTP != nil {
			target, err := resolveHTTPReference(action.HTTP.Href, serviceOrigin)
			if err != nil {
				issues = append(issues, OfferingIssue{ActionID: action.ID, Message: err.Error(), Scope: OfferingIssueAction})
				continue
			}
			discovered.HTTP = &DiscoveredHTTPAction{Method: action.HTTP.Method, Request: action.HTTP.Request, ResponseContentTypes: append([]string(nil), action.HTTP.ResponseContentTypes...), URL: target}
		} else if action.OpenAPI != nil {
			openAPIURL := action.OpenAPI.URL
			if openAPIURL == "" {
				openAPIURL = serviceOpenAPIURL
			}
			if openAPIURL == "" {
				issues = append(issues, OfferingIssue{ActionID: action.ID, Message: "OpenAPI Action has no OpenAPI document URL", Scope: OfferingIssueAction})
				continue
			}
			target, err := resolveHTTPSReference(openAPIURL, serviceOrigin)
			if err != nil {
				issues = append(issues, OfferingIssue{ActionID: action.ID, Message: err.Error(), Scope: OfferingIssueAction})
				continue
			}
			discovered.OpenAPI = &DiscoveredOpenAPIAction{OperationID: action.OpenAPI.OperationID, URL: target}
		} else {
			continue
		}
		result = append(result, discovered)
	}
	return result, issues
}

func resolveHTTPReference(reference, base string) (string, error) {
	baseURL, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	target, err := baseURL.Parse(reference)
	if err != nil || (target.Scheme != "https" && target.Scheme != "http") || target.Host == "" {
		return "", errors.New("ODP Action target must use HTTP or HTTPS")
	}
	return target.String(), nil
}

func resolveHTTPSReference(reference, base string) (string, error) {
	target, err := resolveHTTPReference(reference, base)
	if err != nil {
		return "", err
	}
	parsed, _ := url.Parse(target)
	if parsed.Scheme != "https" {
		return "", errors.New("ODP supporting document URL must use HTTPS")
	}
	return target, nil
}
