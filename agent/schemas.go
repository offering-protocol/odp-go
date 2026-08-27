package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

const (
	attributeSchemaDialect       = "https://json-schema.org/draft/2020-12/schema"
	attributeSchemaMediaType     = "application/schema+json"
	maximumAttributeDocuments    = 16
	maximumAttributeDocumentSize = 262_144
	maximumAttributeGraphSize    = 1_048_576
	maximumAttributeDepth        = 8
)

func (client *ServiceClient) resolveSchema(ctx context.Context, reference string) (map[string]any, *jsonschema.Schema, error) {
	rootURL, err := url.Parse(reference)
	if err != nil || rootURL.Scheme != "https" || rootURL.Host == "" {
		return nil, nil, errors.New("ODP Attribute Schema URL must use HTTPS")
	}
	rootURL.Fragment = ""
	documents := map[string]map[string]any{}
	depths := map[string]int{rootURL.String(): 0}
	graphBytes := 0
	var load func(string, int) error
	load = func(documentURL string, depth int) error {
		if _, found := documents[documentURL]; found {
			return nil
		}
		if len(documents) >= maximumAttributeDocuments {
			return errors.New("ODP Attribute Schema graph exceeds 16 documents")
		}
		if depth > maximumAttributeDepth {
			return errors.New("ODP Attribute Schema graph exceeds eight reference levels")
		}
		document, err := client.supportingJSON(ctx, documentURL, "attribute-schema", attributeSchemaMediaType, []string{attributeSchemaMediaType}, maximumAttributeDocumentSize, 24*time.Hour)
		if err != nil {
			return err
		}
		if document["$schema"] != attributeSchemaDialect {
			return errors.New("ODP Attribute Schema must declare JSON Schema Draft 2020-12")
		}
		if err := requireFragmentDynamicReferences(document); err != nil {
			return err
		}
		encoded, err := json.Marshal(document)
		if err != nil {
			return err
		}
		graphBytes += len(encoded)
		if graphBytes > maximumAttributeGraphSize {
			return errors.New("ODP Attribute Schema graph exceeds its byte limit")
		}
		if err := requireSupportedVocabularies(document); err != nil {
			return err
		}
		documents[documentURL] = document
		depths[documentURL] = depth
		references, err := schemaReferences(document, documentURL)
		if err != nil {
			return err
		}
		for _, reference := range references {
			if err := load(reference, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	if err := load(rootURL.String(), 0); err != nil {
		return nil, nil, err
	}
	externalURLs := make([]string, 0, len(documents)-1)
	for documentURL := range documents {
		if documentURL != rootURL.String() {
			externalURLs = append(externalURLs, documentURL)
		}
	}
	slices.Sort(externalURLs)
	root := cloneMap(documents[rootURL.String()])
	if _, found := root["$id"]; !found {
		root["$id"] = rootURL.String()
	}
	if len(externalURLs) != 0 {
		definitions, _ := root["$defs"].(map[string]any)
		if definitions == nil {
			definitions = map[string]any{}
		}
		for index, documentURL := range externalURLs {
			key := fmt.Sprintf("odp_external_%d", index)
			for {
				if _, exists := definitions[key]; !exists {
					break
				}
				key += "_"
			}
			external := cloneMap(documents[documentURL])
			if _, found := external["$id"]; !found {
				external["$id"] = documentURL
			}
			definitions[key] = external
		}
		root["$defs"] = definitions
	}
	encoded, err := json.Marshal(root)
	if err != nil {
		return nil, nil, err
	}
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(encoded))
	if err != nil {
		return nil, nil, err
	}
	if err := compiler.AddResource(rootURL.String(), document); err != nil {
		return nil, nil, err
	}
	compiled, err := compiler.Compile(rootURL.String())
	if err != nil {
		return nil, nil, fmt.Errorf("compile ODP Attribute Schema: %w", err)
	}
	return root, compiled, nil
}

func requireSupportedVocabularies(value any) error {
	switch typed := value.(type) {
	case []any:
		for _, child := range typed {
			if err := requireSupportedVocabularies(child); err != nil {
				return err
			}
		}
	case map[string]any:
		if vocabulary, ok := typed["$vocabulary"].(map[string]any); ok {
			for uri, required := range vocabulary {
				if required == true && !strings.HasPrefix(uri, "https://json-schema.org/draft/2020-12/vocab/") {
					return fmt.Errorf("ODP Attribute Schema requires unsupported vocabulary %s", uri)
				}
			}
		}
		for _, child := range typed {
			if err := requireSupportedVocabularies(child); err != nil {
				return err
			}
		}
	}
	return nil
}

func requireFragmentDynamicReferences(value any) error {
	switch typed := value.(type) {
	case []any:
		for _, child := range typed {
			if err := requireFragmentDynamicReferences(child); err != nil {
				return err
			}
		}
	case map[string]any:
		if reference, found := typed["$dynamicRef"]; found {
			text, valid := reference.(string)
			if !valid || !strings.HasPrefix(text, "#") {
				return errors.New("ODP Attribute Schema $dynamicRef must be a fragment-only reference")
			}
		}
		for _, child := range typed {
			if err := requireFragmentDynamicReferences(child); err != nil {
				return err
			}
		}
	}
	return nil
}

func schemaReferences(value any, retrievalURL string) ([]string, error) {
	local := make(map[string]struct{})
	if err := schemaResourceURLs(value, retrievalURL, local); err != nil {
		return nil, err
	}
	references := make(map[string]struct{})
	if err := collectSchemaReferences(value, retrievalURL, references); err != nil {
		return nil, err
	}
	result := make([]string, 0, len(references))
	for reference := range references {
		if _, found := local[reference]; !found {
			result = append(result, reference)
		}
	}
	slices.Sort(result)
	return result, nil
}

func schemaResourceURLs(value any, base string, result map[string]struct{}) error {
	resolvedBase, err := resolveSchemaReference("", base)
	if err != nil {
		return err
	}
	resolvedBase.Fragment = ""
	result[resolvedBase.String()] = struct{}{}
	switch typed := value.(type) {
	case []any:
		for _, child := range typed {
			if err := schemaResourceURLs(child, base, result); err != nil {
				return err
			}
		}
	case map[string]any:
		if identifier, ok := typed["$id"].(string); ok {
			resolved, err := resolveSchemaReference(identifier, base)
			if err != nil {
				return err
			}
			resolved.Fragment = ""
			base = resolved.String()
			result[base] = struct{}{}
		}
		for _, child := range typed {
			if err := schemaResourceURLs(child, base, result); err != nil {
				return err
			}
		}
	}
	return nil
}

func collectSchemaReferences(value any, base string, result map[string]struct{}) error {
	switch typed := value.(type) {
	case []any:
		for _, child := range typed {
			if err := collectSchemaReferences(child, base, result); err != nil {
				return err
			}
		}
	case map[string]any:
		if identifier, ok := typed["$id"].(string); ok {
			resolved, err := resolveSchemaReference(identifier, base)
			if err != nil {
				return err
			}
			resolved.Fragment = ""
			base = resolved.String()
		}
		if reference, ok := typed["$ref"].(string); ok {
			resolved, err := resolveSchemaReference(reference, base)
			if err != nil {
				return err
			}
			resolved.Fragment = ""
			result[resolved.String()] = struct{}{}
		}
		for key, child := range typed {
			if key != "$ref" {
				if err := collectSchemaReferences(child, base, result); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func resolveSchemaReference(reference, base string) (*url.URL, error) {
	baseURL, err := url.Parse(base)
	if err != nil {
		return nil, err
	}
	resolved, err := baseURL.Parse(reference)
	if err != nil || resolved.Scheme != "https" || resolved.Host == "" {
		return nil, errors.New("ODP Attribute Schema references must use HTTPS")
	}
	return resolved, nil
}

func cloneMap(value map[string]any) map[string]any {
	encoded, _ := json.Marshal(value)
	var result map[string]any
	_ = json.Unmarshal(encoded, &result)
	return result
}
