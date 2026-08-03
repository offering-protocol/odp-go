package agent_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/offering-protocol/odp-go/agent"
	"github.com/offering-protocol/odp-go/directory"
	"github.com/offering-protocol/odp-go/service"
)

const directoryResults = `{"items":[
  {"description":"Slow catalog","indexed_at":"2026-08-02T00:00:00Z","language":"en","localizations":["en"],"name":"Slow","operations":["get-offering","list-offerings"],"service_origin":"https://slow.example"},
  {"description":"Fast catalog","indexed_at":"2026-08-02T00:00:00Z","language":"en","localizations":["en"],"name":"Fast","operations":["get-offering","list-offerings"],"service_origin":"https://fast.example"},
  {"description":"Bad catalog","indexed_at":"2026-08-02T00:00:00Z","language":"en","localizations":["en"],"name":"Bad","operations":["get-offering","list-offerings"],"service_origin":"https://bad.example"}
]}`

func TestAgentSearchesServicesConcurrentlyInDirectoryOrder(t *testing.T) {
	directoryTransport := roundTripFunc(func(*http.Request) (*http.Response, error) {
		return response(http.StatusOK, directoryResults, nil, "application/json"), nil
	})
	directoryClient, err := directory.New(directory.Options{
		Environment: directory.Sandbox,
		HTTPClient:  &http.Client{Transport: directoryTransport},
	})
	if err != nil {
		t.Fatal(err)
	}
	requestsStarted := make(chan struct{}, 3)
	releaseRequests := make(chan struct{})
	fastComplete := make(chan struct{})
	var active atomic.Int64
	var maximum atomic.Int64
	serviceTransport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == "/.well-known/odp" {
			return response(http.StatusOK, documentJSON("get-offering", "list-offerings"), nil, service.MediaType), nil
		}
		current := active.Add(1)
		for {
			observed := maximum.Load()
			if current <= observed || maximum.CompareAndSwap(observed, current) {
				break
			}
		}
		defer active.Add(-1)
		requestsStarted <- struct{}{}
		<-releaseRequests
		switch request.URL.Host {
		case "fast.example":
			close(fastComplete)
		case "bad.example":
			return response(http.StatusServiceUnavailable, `{"code":"UNAVAILABLE","status":503,"title":"Unavailable","type":"about:blank"}`, nil, service.ProblemMediaType), nil
		}
		name := strings.TrimSuffix(request.URL.Host, ".example")
		return response(http.StatusOK, `{"items":[{"id":"`+name+`","name":"`+name+`"}],"odp_version":"1.0"}`, nil, service.MediaType), nil
	})
	agentClient, err := agent.New(agent.AgentOptions{
		Directory: directoryClient,
		ServiceClient: func(_ context.Context, service directory.Service) (*agent.ServiceClient, error) {
			return agent.NewServiceClient(agent.ServiceClientOptions{
				CachePartition: "test", HTTPClient: &http.Client{Transport: serviceTransport}, ServiceURL: service.ServiceOrigin,
			})
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if agentClient.Environment() != directory.Sandbox {
		t.Fatalf("environment = %q", agentClient.Environment())
	}
	type discoveryResult struct {
		err    error
		events []agent.DiscoveryEvent
	}
	done := make(chan discoveryResult, 1)
	go func() {
		var events []agent.DiscoveryEvent
		for event, err := range agentClient.SearchOfferingsAcrossServices(t.Context(), agent.FederatedSearchRequest{Concurrency: 2, MaxServices: 3, MaxOfferingsPerService: 1}) {
			if err != nil {
				done <- discoveryResult{err: err}
				return
			}
			events = append(events, event)
		}
		done <- discoveryResult{events: events}
	}()
	<-requestsStarted
	<-requestsStarted
	close(releaseRequests)
	<-fastComplete
	result := <-done
	if result.err != nil {
		t.Fatal(result.err)
	}
	events := result.events
	if maximum.Load() != 2 {
		t.Fatalf("maximum concurrency = %d", maximum.Load())
	}
	if len(events) != 3 || events[0].Service.Name != "Slow" || events[1].Service.Name != "Fast" || events[2].Service.Name != "Bad" {
		t.Fatalf("events = %#v", events)
	}
	if events[0].Type != agent.DiscoveryOffering || events[1].Type != agent.DiscoveryOffering || events[2].Type != agent.DiscoveryIssue || events[2].Err == nil {
		t.Fatalf("events = %#v", events)
	}
}

func TestAgentRejectsInvalidBoundsBeforeDirectoryRequest(t *testing.T) {
	var requests atomic.Int64
	directoryClient, err := directory.New(directory.Options{HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		requests.Add(1)
		return &http.Response{Body: io.NopCloser(strings.NewReader(`{"items":[]}`)), Header: http.Header{"Content-Type": []string{"application/json"}}, StatusCode: http.StatusOK}, nil
	})}})
	if err != nil {
		t.Fatal(err)
	}
	agentClient, err := agent.New(agent.AgentOptions{Directory: directoryClient})
	if err != nil {
		t.Fatal(err)
	}
	events := agentClient.SearchOfferingsAcrossServices(t.Context(), agent.FederatedSearchRequest{Concurrency: 17})
	_, sequenceError, ok := firstEvent(events)
	if !ok || sequenceError == nil || requests.Load() != 0 {
		t.Fatalf("error = %v, requests = %d", sequenceError, requests.Load())
	}
}

func TestAgentCancelsOutstandingServiceWorkWhenIterationStops(t *testing.T) {
	directoryClient, err := directory.New(directory.Options{HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return response(http.StatusOK, directoryResults, nil, "application/json"), nil
	})}})
	if err != nil {
		t.Fatal(err)
	}
	secondaryStarted := make(chan struct{}, 2)
	secondaryExited := make(chan struct{}, 2)
	firstTransport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == "/.well-known/odp" {
			return response(http.StatusOK, documentJSON("get-offering", "list-offerings"), nil, service.MediaType), nil
		}
		return response(http.StatusOK, `{"items":[{"id":"first","name":"First"}],"odp_version":"1.0"}`, nil, service.MediaType), nil
	})
	agentClient, err := agent.New(agent.AgentOptions{
		Directory: directoryClient,
		ServiceClient: func(ctx context.Context, service directory.Service) (*agent.ServiceClient, error) {
			if service.Name != "Slow" {
				secondaryStarted <- struct{}{}
				<-ctx.Done()
				secondaryExited <- struct{}{}
				return nil, ctx.Err()
			}
			<-secondaryStarted
			<-secondaryStarted
			return agent.NewServiceClient(agent.ServiceClientOptions{
				CachePartition: "test", HTTPClient: &http.Client{Transport: firstTransport}, ServiceURL: service.ServiceOrigin,
			})
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for event, err := range agentClient.SearchOfferingsAcrossServices(t.Context(), agent.FederatedSearchRequest{Concurrency: 3, MaxServices: 3}) {
		if err != nil {
			t.Fatal(err)
		}
		if event.Type != agent.DiscoveryOffering {
			t.Fatalf("event = %#v", event)
		}
		break
	}
	for range 2 {
		select {
		case <-secondaryExited:
		case <-t.Context().Done():
			t.Fatal(t.Context().Err())
		}
	}
}

func firstEvent(sequence func(func(agent.DiscoveryEvent, error) bool)) (agent.DiscoveryEvent, error, bool) {
	var event agent.DiscoveryEvent
	var sequenceError error
	found := false
	sequence(func(value agent.DiscoveryEvent, err error) bool {
		event = value
		sequenceError = err
		found = true
		return false
	})
	return event, sequenceError, found
}
