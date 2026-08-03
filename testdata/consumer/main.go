package main

import (
	"fmt"

	odp "github.com/offering-protocol/odp-go"
	"github.com/offering-protocol/odp-go/agent"
	"github.com/offering-protocol/odp-go/directory"
	"github.com/offering-protocol/odp-go/service"
)

func main() {
	_, _ = agent.NewServiceClient(agent.ServiceClientOptions{ServiceURL: "https://service.example"})
	_, _ = directory.New(directory.Options{})
	_, _ = service.NewStaticCatalog(service.StaticCatalogOptions{})
	fmt.Println(odp.Version)
}
