package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/offering-protocol/odp-go/agent"
	"github.com/offering-protocol/odp-go/directory"
)

type mockDirectory struct {
	client         *directory.Client
	serviceClients map[string]*agent.ServiceClient
	services       []directory.Service
}

type directoryServiceJSON struct {
	Description   string              `json:"description"`
	IndexedAt     string              `json:"indexed_at"`
	Keywords      []string            `json:"keywords,omitempty"`
	Language      string              `json:"language"`
	Localizations []string            `json:"localizations"`
	Name          string              `json:"name"`
	Operations    []string            `json:"operations"`
	Protocols     *directoryProtocols `json:"protocols,omitempty"`
	ServiceOrigin string              `json:"service_origin"`
}

type directoryProtocols struct {
	Onboarding []string `json:"onboarding,omitempty"`
	Payments   []string `json:"payments,omitempty"`
}

func createMockDirectory(ctx context.Context, candidates []string) (*mockDirectory, error) {
	serviceClients := make(map[string]*agent.ServiceClient)
	services := make([]directory.Service, 0, len(candidates))
	wireServices := make([]directoryServiceJSON, 0, len(candidates))
	for _, candidate := range candidates {
		client, err := agent.NewServiceClient(agent.ServiceClientOptions{ServiceURL: candidate})
		if err != nil {
			continue
		}
		inspection, err := client.Inspect(ctx)
		if err != nil {
			continue
		}
		document := inspection.Document
		service := directory.Service{
			Description: document.Description, IndexedAt: time.Now().UTC(), Keywords: document.Keywords,
			Language: document.Language, Localizations: document.Localizations, Name: document.Name,
			Operations: document.Operations.Supported, Protocols: document.Protocols, ServiceOrigin: inspection.ServiceOrigin,
		}
		services = append(services, service)
		serviceClients[inspection.ServiceOrigin] = client
		operations := make([]string, len(document.Operations.Supported))
		for index, operation := range document.Operations.Supported {
			operations[index] = string(operation)
		}
		var protocols *directoryProtocols
		if document.Protocols != nil {
			protocols = &directoryProtocols{}
			for _, protocol := range document.Protocols.Onboarding {
				protocols.Onboarding = append(protocols.Onboarding, string(protocol))
			}
			for _, protocol := range document.Protocols.Payments {
				protocols.Payments = append(protocols.Payments, string(protocol))
			}
		}
		wireServices = append(wireServices, directoryServiceJSON{
			Description: document.Description, IndexedAt: service.IndexedAt.Format(time.RFC3339), Keywords: document.Keywords,
			Language: document.Language, Localizations: document.Localizations, Name: document.Name,
			Operations: operations, Protocols: protocols, ServiceOrigin: inspection.ServiceOrigin,
		})
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
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodPost || request.URL.Path != "/v1/services/search" {
			return nil, errors.New("mock directory received an unsupported request")
		}
		return &http.Response{
			Body: io.NopCloser(bytes.NewReader(body)), Header: http.Header{"Content-Type": []string{"application/json"}}, StatusCode: http.StatusOK,
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
