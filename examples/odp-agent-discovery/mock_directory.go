package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	odp "github.com/offering-protocol/odp-go"
	"github.com/offering-protocol/odp-go/agent"
	"github.com/offering-protocol/odp-go/directory"
)

type mockDirectory struct {
	client         *directory.Client
	serviceClients map[string]*agent.ServiceClient
	services       []directory.Service
}

type directoryServiceJSON struct {
	ServiceID     string                    `json:"service_id"`
	Description   string                    `json:"description"`
	IndexedAt     string                    `json:"indexed_at"`
	Keywords      []string                  `json:"keywords,omitempty"`
	Language      string                    `json:"language"`
	Localizations []string                  `json:"localizations"`
	Name          string                    `json:"name"`
	Operations    []odp.OperationDescriptor `json:"operations"`
	Protocols     *odp.ServiceProtocols     `json:"protocols,omitempty"`
	ServiceOrigin string                    `json:"service_origin"`
}

func createMockDirectory(ctx context.Context, candidates []string) (*mockDirectory, error) {
	serviceClients := make(map[string]*agent.ServiceClient)
	services := make([]directory.Service, 0, len(candidates))
	wireServices := make([]directoryServiceJSON, 0, len(candidates))
	wireResults := make([]any, 0, len(candidates))
	for index, candidate := range candidates {
		client, err := agent.NewServiceClient(agent.ServiceClientOptions{AllowLocalNetwork: true, ServiceURL: candidate})
		if err != nil {
			continue
		}
		inspection, err := client.Inspect(ctx)
		if err != nil {
			continue
		}
		document := inspection.Document
		origin := fmt.Sprintf("https://service-%d.mock-directory.example", index+1)
		service := directory.Service{
			Description: document.Description, IndexedAt: time.Now().UTC(), Keywords: document.Keywords,
			Language: document.Language, Localizations: document.Localizations, Name: document.Name,
			Operations: document.Operations, Protocols: document.Protocols, ServiceOrigin: origin,
		}
		services = append(services, service)
		serviceClients[origin] = client
		wireService := directoryServiceJSON{
			ServiceID:   fmt.Sprintf("mock-%d", index+1),
			Description: document.Description, IndexedAt: service.IndexedAt.Format(time.RFC3339), Keywords: document.Keywords,
			Language: document.Language, Localizations: document.Localizations, Name: document.Name,
			Operations: document.Operations, Protocols: document.Protocols, ServiceOrigin: origin,
		}
		wireServices = append(wireServices, wireService)
		wireResults = append(wireResults, map[string]any{"type": "service", "service": wireService, "indexed_at": wireService.IndexedAt})
		anonymous := map[odp.Operation]bool{}
		for _, operation := range document.Operations {
			anonymous[operation.Name] = operation.Authentication != odp.AuthenticationRequired
		}
		if anonymous[odp.OperationListCollections] && anonymous[odp.OperationGetCollection] {
			for collection, err := range client.ListCollections(ctx, agent.ListOptions{MaxItems: 2, MaxPages: 1}) {
				if err != nil {
					return nil, fmt.Errorf("sample Collections from %s: %w", candidate, err)
				}
				wireResults = append(wireResults, map[string]any{
					"type": "collection", "service": wireService, "indexed_at": wireService.IndexedAt,
					"collection": map[string]any{"id": collection.ID, "name": collection.Name, "description": collection.Description},
				})
			}
		}
	}
	if len(services) == 0 {
		return nil, errors.New("no configured ODP Services are reachable")
	}
	body, err := json.Marshal(struct {
		Items []directoryServiceJSON `json:"items"`
	}{Items: wireServices})
	if err != nil {
		return nil, err
	}
	mixedBody, err := json.Marshal(map[string]any{"items": wireResults})
	if err != nil {
		return nil, err
	}
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodPost || request.URL.Host != "api.inflowpay.ai" || request.URL.Scheme != "https" {
			return nil, errors.New("mock directory received an unsupported request")
		}
		responseBody := body
		switch request.URL.Path {
		case "/v1/services/search":
		case "/v1/directory/search":
			responseBody = mixedBody
		default:
			return nil, errors.New("mock directory received an unsupported path")
		}
		return &http.Response{
			Body: io.NopCloser(bytes.NewReader(responseBody)), Header: http.Header{"Content-Type": []string{"application/json"}}, StatusCode: http.StatusOK,
		}, nil
	})
	directoryClient, err := directory.New(directory.Options{HTTPClient: &http.Client{Transport: transport}})
	if err != nil {
		return nil, err
	}
	return &mockDirectory{client: directoryClient, serviceClients: serviceClients, services: services}, nil
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}
