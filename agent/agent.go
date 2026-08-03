package agent

import (
	"context"
	"errors"
	"iter"

	"github.com/offering-protocol/odp-go/directory"
)

func New(options AgentOptions) (*Agent, error) {
	directoryClient := options.Directory
	if directoryClient == nil {
		var err error
		directoryClient, err = directory.New(directory.Options{Environment: options.Environment, HTTPClient: options.DirectoryHTTPClient})
		if err != nil {
			return nil, err
		}
	}
	factory := options.ServiceClient
	if factory == nil {
		factory = func(_ context.Context, service directory.Service) (*ServiceClient, error) {
			return NewServiceClient(ServiceClientOptions{ServiceURL: service.ServiceOrigin})
		}
	}
	return &Agent{directory: directoryClient, environment: directoryClient.Environment(), serviceClient: factory}, nil
}

func (agent *Agent) searchOfferingsAcrossServices(ctx context.Context, request FederatedSearchRequest) iter.Seq2[DiscoveryEvent, error] {
	return func(yield func(DiscoveryEvent, error) bool) {
		maxServices, err := bounded(request.MaxServices, 10, 1, 100, "maximum Services")
		if err != nil {
			yield(DiscoveryEvent{}, err)
			return
		}
		maxOfferings, err := bounded(request.MaxOfferingsPerService, 10, 1, 100, "maximum Offerings per Service")
		if err != nil {
			yield(DiscoveryEvent{}, err)
			return
		}
		concurrency, err := bounded(request.Concurrency, 4, 1, 16, "concurrency")
		if err != nil {
			yield(DiscoveryEvent{}, err)
			return
		}
		services := make([]directory.Service, 0, maxServices)
		for service, err := range agent.directory.SearchServices(ctx, request.Services, directory.IterationOptions{MaxItems: maxServices}) {
			if err != nil {
				yield(DiscoveryEvent{}, err)
				return
			}
			services = append(services, service)
		}
		workContext, cancel := context.WithCancel(ctx)
		defer cancel()
		results := make([]chan []DiscoveryEvent, len(services))
		semaphore := make(chan struct{}, concurrency)
		for index, service := range services {
			results[index] = make(chan []DiscoveryEvent, 1)
			go func() {
				var events []DiscoveryEvent
				select {
				case semaphore <- struct{}{}:
					defer func() { <-semaphore }()
					events = agent.searchService(workContext, service, request.Offerings, maxOfferings)
				case <-workContext.Done():
					events = []DiscoveryEvent{{Err: workContext.Err(), Service: service, Type: DiscoveryIssue}}
				}
				select {
				case results[index] <- events:
				case <-workContext.Done():
				}
			}()
		}
		for _, result := range results {
			var serviceResults []DiscoveryEvent
			select {
			case serviceResults = <-result:
			case <-ctx.Done():
				yield(DiscoveryEvent{}, ctx.Err())
				return
			}
			for _, event := range serviceResults {
				if !yield(event, nil) {
					return
				}
			}
		}
	}
}

func (agent *Agent) searchService(ctx context.Context, service directory.Service, options OfferingSearchOptions, maximum int) []DiscoveryEvent {
	client, err := agent.serviceClient(ctx, service)
	if err != nil {
		return []DiscoveryEvent{{Err: err, Service: service, Type: DiscoveryIssue}}
	}
	options.MaxItems = maximum
	options.Representation = "terse"
	results := make([]DiscoveryEvent, 0, maximum)
	sequence := client.SearchOfferings(ctx, options)
	if !hasOfferingSearch(options) {
		list := ListOptions{MaxItems: maximum, Representation: "terse"}
		if options.CollectionID != "" {
			sequence = client.ListCollectionOfferings(ctx, options.CollectionID, list)
		} else {
			sequence = client.ListOfferings(ctx, list)
		}
	}
	for offering, err := range sequence {
		if err != nil {
			return []DiscoveryEvent{{Err: err, Service: service, Type: DiscoveryIssue}}
		}
		value := offering
		results = append(results, DiscoveryEvent{Offering: &value, Service: service, Type: DiscoveryOffering})
	}
	return results
}

func hasOfferingSearch(options OfferingSearchOptions) bool {
	return options.Query != "" || options.Filters != nil || options.IncludeDescendants || options.Sort != "" || options.Refinements != nil
}

func bounded(value, fallback, minimum, maximum int, name string) (int, error) {
	if value == 0 {
		value = fallback
	}
	if value < minimum || value > maximum {
		return 0, errors.New(name + " is outside its supported bounds")
	}
	return value, nil
}
