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
		for _, candidate := range schemaReferences(document) {
			resolved, err := resolveSchemaReference(candidate, documentURL)
			if err != nil {
				return err
			}
			resolved.Fragment = ""
			if resolved.String() != documentURL {
				if err := load(resolved.String(), depth+1); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := load(rootURL.String(), 0); err != nil {
		return nil, nil, err
	}
	keys := map[string]string{}
	externalURLs := make([]string, 0, len(documents)-1)
	for documentURL := range documents {
		if documentURL != rootURL.String() {
			externalURLs = append(externalURLs, documentURL)
		}
	}
	slices.Sort(externalURLs)
	for index, documentURL := range externalURLs {
		keys[documentURL] = fmt.Sprintf("odp_external_%d", index)
	}
	root := cloneMap(documents[rootURL.String()])
	rewriteSchemaReferences(root, rootURL.String(), rootURL.String(), keys)
	if len(keys) != 0 {
		definitions, _ := root["$defs"].(map[string]any)
		if definitions == nil {
			definitions = map[string]any{}
		}
		for documentURL, key := range keys {
			external := cloneMap(documents[documentURL])
			rewriteSchemaReferences(external, documentURL, rootURL.String(), keys)
			delete(external, "$id")
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

func schemaReferences(value any) []string {
	result := []string{}
	switch typed := value.(type) {
	case []any:
		for _, child := range typed {
			result = append(result, schemaReferences(child)...)
		}
	case map[string]any:
		if reference, ok := typed["$ref"].(string); ok {
			result = append(result, reference)
		}
		for key, child := range typed {
			if key != "$ref" {
				result = append(result, schemaReferences(child)...)
			}
		}
	}
	return result
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

func rewriteSchemaReferences(value any, documentURL, rootURL string, keys map[string]string) {
	switch typed := value.(type) {
	case []any:
		for _, child := range typed {
			rewriteSchemaReferences(child, documentURL, rootURL, keys)
		}
	case map[string]any:
		if reference, ok := typed["$ref"].(string); ok {
			resolved, err := resolveSchemaReference(reference, documentURL)
			if err == nil {
				fragment := resolved.Fragment
				resolved.Fragment = ""
				prefix := ""
				if resolved.String() != rootURL {
					prefix = "/$defs/" + keys[resolved.String()]
				}
				if fragment != "" {
					prefix += "/" + strings.TrimPrefix(fragment, "/")
				}
				typed["$ref"] = "#" + prefix
			}
		}
		for key, child := range typed {
			if key != "$ref" {
				rewriteSchemaReferences(child, documentURL, rootURL, keys)
			}
		}
	}
}

func cloneMap(value map[string]any) map[string]any {
	encoded, _ := json.Marshal(value)
	var result map[string]any
	_ = json.Unmarshal(encoded, &result)
	return result
}
