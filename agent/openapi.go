package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"

	"github.com/pb33f/libopenapi"
)

var openAPIVersion = regexp.MustCompile(`^3\.1\.\d+(?:[-+].*)?$`)

func (client *ServiceClient) resolveOpenAPI(ctx context.Context, target, operationID string) (map[string]any, map[string]any, error) {
	document, err := client.supportingJSON(ctx, target, "openapi", "application/vnd.oai.openapi+json;version=3.1, application/json;q=0.9", []string{"application/vnd.oai.openapi+json", "application/json"}, 1_048_576, 0)
	if err != nil {
		return nil, nil, err
	}
	version, ok := document["openapi"].(string)
	if !ok || !openAPIVersion.MatchString(version) {
		return nil, nil, errors.New("ODP Action requires an OpenAPI 3.1 document")
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		return nil, nil, err
	}
	parsed, err := libopenapi.NewDocument(encoded)
	if err != nil {
		return nil, nil, fmt.Errorf("parse ODP Action OpenAPI document: %w", err)
	}
	if _, validationError := parsed.BuildV3Model(); validationError != nil {
		return nil, nil, errors.New("ODP Action OpenAPI document is invalid")
	}
	matches := []map[string]any{}
	paths, ok := document["paths"].(map[string]any)
	if !ok {
		return nil, nil, errors.New("ODP OpenAPI document must contain paths")
	}
	for _, rawPath := range paths {
		path, ok := rawPath.(map[string]any)
		if !ok {
			continue
		}
		for _, method := range []string{"delete", "get", "head", "options", "patch", "post", "put", "trace"} {
			operation, ok := path[method].(map[string]any)
			if ok && operation["operationId"] == operationID {
				matches = append(matches, operation)
			}
		}
	}
	if len(matches) != 1 {
		return nil, nil, fmt.Errorf("ODP Action operation_id %s must resolve exactly once", operationID)
	}
	return cloneMap(document), cloneMap(matches[0]), nil
}
