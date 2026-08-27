package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/offering-protocol/odp-go/agent"
)

func main() {
	if len(os.Args) != 2 {
		fail(errors.New("usage: odp-interoperability-agent SERVICE_URL"))
	}
	if err := run(context.Background(), os.Args[1]); err != nil {
		fail(err)
	}
	_, _ = fmt.Fprintln(os.Stdout, "Go Agent interoperates with the ODP Service")
}

func run(ctx context.Context, serviceURL string) error {
	client, err := agent.NewServiceClient(agent.ServiceClientOptions{AllowLocalNetwork: true, ServiceURL: serviceURL})
	if err != nil {
		return err
	}
	inspection, err := client.Inspect(ctx)
	if err != nil {
		return err
	}
	if inspection.Document.Name == "" {
		return errors.New("Service name is empty")
	}
	var firstID string
	var firstName string
	for offering, listErr := range client.ListOfferings(ctx, agent.ListOptions{}) {
		if listErr != nil {
			return listErr
		}
		if firstID == "" {
			firstID = offering.ID
			firstName = offering.Name
		}
	}
	if firstID == "" {
		return errors.New("Service returned no Offerings")
	}
	details, err := client.GetOfferingDetails(ctx, firstID)
	if err != nil {
		return err
	}
	if details.ID != firstID || details.Name != firstName {
		return errors.New("full Offering does not match its listed summary")
	}
	if len(details.Actions) > 0 {
		resolved, resolveErr := client.ResolveAction(ctx, firstID, details.Actions[0].ID)
		if resolveErr != nil {
			return resolveErr
		}
		if resolved.Action.ID != details.Actions[0].ID {
			return errors.New("resolved Action identifier changed")
		}
	}
	return nil
}

func fail(err error) {
	_, _ = fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
