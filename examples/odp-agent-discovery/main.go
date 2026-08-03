package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os/signal"
	"syscall"

	"github.com/offering-protocol/odp-go/agent"
	"github.com/offering-protocol/odp-go/directory"
)

type serviceURLs []string

func (values *serviceURLs) String() string {
	return fmt.Sprint([]string(*values))
}

func (values *serviceURLs) Set(value string) error {
	*values = append(*values, value)
	return nil
}

func main() {
	var candidates serviceURLs
	flag.Var(&candidates, "service", "ODP Service origin; repeat for multiple Services")
	flag.Parse()
	if len(candidates) == 0 {
		candidates = append(candidates, "http://localhost:4101")
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	mock, err := createMockDirectory(ctx, candidates)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Mock directory contains %d reachable ODP Service(s).\n", len(mock.services))
	for _, service := range mock.services {
		fmt.Printf("\nService: %s (%s)\n", service.Name, service.ServiceOrigin)
		inspection, err := mock.serviceClients[service.ServiceOrigin].Inspect(ctx)
		if err != nil {
			log.Fatal(err)
		}
		printJSON("ODP Service Document", inspection.Document)
	}

	odpAgent, err := agent.New(agent.AgentOptions{
		Directory: mock.client,
		ServiceClient: func(_ context.Context, service directory.Service) (*agent.ServiceClient, error) {
			return mock.serviceClients[service.ServiceOrigin], nil
		},
	})
	if err != nil {
		log.Fatal(err)
	}
	for event, err := range odpAgent.SearchOfferingsAcrossServices(ctx, agent.FederatedSearchRequest{}) {
		if err != nil {
			log.Fatal(err)
		}
		if event.Type == agent.DiscoveryIssue {
			log.Printf("%s: %v", event.Service.Name, event.Err)
			continue
		}
		printJSON("Terse Offering from "+event.Service.Name, event.Offering)
		full, err := mock.serviceClients[event.Service.ServiceOrigin].GetOffering(ctx, event.Offering.ID, "full")
		if err != nil {
			log.Printf("%s Offering %s: %v", event.Service.Name, event.Offering.ID, err)
			continue
		}
		printJSON("Full Offering from "+event.Service.Name, full)
	}
}

func printJSON(label string, value any) {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		log.Printf("%s: %v", label, err)
		return
	}
	fmt.Printf("\n%s:\n%s\n", label, encoded)
}
