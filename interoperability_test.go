package odp_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	odp "github.com/offering-protocol/odp-go"
	"github.com/offering-protocol/odp-go/agent"
	"github.com/offering-protocol/odp-go/service"
)

func TestLocalAgentAndServiceInteroperate(t *testing.T) {
	t.Parallel()

	offering := odp.Offering{
		Actions: []odp.Action{{
			Authentication: odp.AuthenticationNotRequired,
			HTTP:           &odp.HTTPActionTarget{Href: "/download", Method: http.MethodGet},
			ID:             "download",
			Rel:            odp.ActionDownload,
		}},
		ID:         "guide",
		Name:       "ODP Guide",
		ODPVersion: odp.Version,
		Price:      &odp.PricePreview{Type: odp.PriceFree},
	}
	catalog, err := service.NewStaticCatalog(service.StaticCatalogOptions{
		Offerings: []odp.Offering{offering},
	})
	if err != nil {
		t.Fatalf("create catalog: %v", err)
	}
	localService, err := service.New(service.Options{
		Catalog: catalog,
		Document: odp.ServiceDocument{
			Description:   "Local Go interoperability Service",
			HTTP:          odp.HTTPConfiguration{EndpointBase: "/odp"},
			Language:      "en",
			Localizations: []string{"en"},
			Name:          "Local Go Service",
		},
	})
	if err != nil {
		t.Fatalf("create Service: %v", err)
	}
	server := httptest.NewServer(localService)
	t.Cleanup(server.Close)

	client, err := agent.NewServiceClient(agent.ServiceClientOptions{ServiceURL: server.URL})
	if err != nil {
		t.Fatalf("create Agent: %v", err)
	}
	inspection, err := client.Inspect(context.Background())
	if err != nil {
		t.Fatalf("inspect Service: %v", err)
	}
	if inspection.Document.Name != "Local Go Service" {
		t.Fatalf("inspect Service name = %q", inspection.Document.Name)
	}

	var listed []odp.Offering
	for item, listErr := range client.ListOfferings(context.Background(), agent.ListOptions{}) {
		if listErr != nil {
			t.Fatalf("list Offerings: %v", listErr)
		}
		listed = append(listed, item)
	}
	if len(listed) != 1 || listed[0].ID != offering.ID {
		t.Fatalf("listed Offerings = %#v", listed)
	}

	details, err := client.GetOfferingDetails(context.Background(), offering.ID)
	if err != nil {
		t.Fatalf("get Offering: %v", err)
	}
	if details.Name != offering.Name || details.Price == nil || details.Price.Type != odp.PriceFree {
		t.Fatalf("Offering details = %#v", details)
	}
	resolved, err := client.ResolveAction(context.Background(), offering.ID, "download")
	if err != nil {
		t.Fatalf("resolve Action: %v", err)
	}
	if resolved.Action.HTTP == nil || resolved.Action.HTTP.URL != server.URL+"/download" {
		t.Fatalf("resolved Action = %#v", resolved.Action)
	}
}
