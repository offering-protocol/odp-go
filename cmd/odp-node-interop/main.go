package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"

	odp "github.com/offering-protocol/odp-go"
	"github.com/offering-protocol/odp-go/agent"
)

func main() {
	if len(os.Args) != 2 {
		fail(errors.New("usage: odp-node-interop SERVICE_URL"))
	}
	if err := run(context.Background(), os.Args[1]); err != nil {
		fail(err)
	}
	_, _ = fmt.Fprintln(os.Stdout, "Go Agent interoperates with the Node.js example Service")
}

func run(ctx context.Context, serviceURL string) error {
	client, err := agent.NewServiceClient(agent.ServiceClientOptions{ServiceURL: serviceURL})
	if err != nil {
		return err
	}
	inspection, err := client.Inspect(ctx)
	if err != nil {
		return err
	}
	if inspection.Document.Name != "Small Example Store" {
		return fmt.Errorf("unexpected Service %q", inspection.Document.Name)
	}
	identifiers := []string{}
	for offering, err := range client.ListOfferings(ctx, agent.ListOptions{}) {
		if err != nil {
			return err
		}
		identifiers = append(identifiers, offering.ID)
	}
	for _, expected := range []string{"architecture-review", "incident-plan"} {
		if !slices.Contains(identifiers, expected) {
			return fmt.Errorf("Offering list omitted %s", expected)
		}
	}
	details, err := client.GetOfferingDetails(ctx, "incident-plan")
	if err != nil {
		return err
	}
	if details.Name != "Incident Response Plan" || details.Price == nil || details.Price.Type != odp.PriceFree {
		return errors.New("full Offering did not match the Node.js example")
	}
	resolved, err := client.ResolveAction(ctx, "incident-plan", "download")
	if err != nil {
		return err
	}
	if resolved.Action.HTTP == nil || resolved.Action.HTTP.URL != serviceURL+"/downloads/incident-plan.txt" {
		return errors.New("download Action did not resolve to the Node.js Service")
	}
	return nil
}

func fail(err error) {
	_, _ = fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
