package main

import (
	"context"
	"fmt"

	odp "github.com/offering-protocol/odp-go"
	"github.com/offering-protocol/odp-go/agent"
	"github.com/offering-protocol/odp-go/directory"
	"github.com/offering-protocol/odp-go/service"
)

func main() {
	_, _ = agent.NewServiceClient(agent.ServiceClientOptions{ServiceURL: "https://service.example"})
	directoryClient, _ := directory.New(directory.Options{})
	var mixed directory.SearchSequence[directory.Result] = directoryClient.Search(context.Background(), directory.DirectorySearchRequest{}, directory.IterationOptions{})
	var services directory.SearchSequence[directory.Service] = directoryClient.SearchServices(context.Background(), directory.SearchRequest{}, directory.IterationOptions{})
	if mixed.Items == nil || mixed.Responses == nil || services.Items == nil || services.Responses == nil {
		panic("missing search iterator")
	}
	_, _ = service.NewStaticCatalog(service.StaticCatalogOptions{})
	fmt.Println(odp.Version)
}
