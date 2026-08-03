package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	odp "github.com/offering-protocol/odp-go"
	"github.com/offering-protocol/odp-go/service"
)

func main() {
	address := flag.String("addr", "localhost:4101", "HTTP listen address")
	flag.Parse()

	catalog, err := service.NewStaticCatalog(service.StaticCatalogOptions{
		Collections: []odp.Collection{
			{Description: "Guides and reference materials", ID: "resources", Name: "Resources", ODPVersion: odp.Version},
		},
		Offerings: []odp.Offering{
			{
				Actions: []odp.Action{
					{Description: "Download the guide", HTTP: &odp.HTTPActionTarget{Href: "/downloads/agent-guide.txt", Method: http.MethodGet}, ID: "download", Rel: odp.ActionDownload},
				},
				CollectionIDs: []string{"resources"},
				Description:   "A short guide for building an ODP Agent",
				ID:            "agent-guide",
				Name:          "ODP Agent Guide",
				ODPVersion:    odp.Version,
				Price:         &odp.PricePreview{Type: odp.PriceFree},
			},
		},
	})
	if err != nil {
		log.Fatal(err)
	}

	odpService, err := service.New(service.Options{
		Catalog: catalog,
		Document: odp.ServiceDocument{
			Description:   "Free resources for ODP integrators",
			HTTP:          odp.HTTPConfiguration{EndpointBase: "/odp"},
			Keywords:      []string{"agent", "developer", "documentation"},
			Language:      "en",
			Localizations: []string{"en"},
			Name:          "ODP Developer Resources",
		},
	})
	if err != nil {
		log.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.Handle("/.well-known/odp", odpService)
	mux.Handle("/odp/", odpService)
	mux.HandleFunc("GET /downloads/agent-guide.txt", func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = writer.Write([]byte("Build Agents against the ODP Service Document and advertised operations.\n"))
	})

	server := &http.Server{
		Addr:              *address,
		Handler:           logRequests(mux),
		ReadHeaderTimeout: 5 * time.Second,
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdown); err != nil {
			log.Printf("shutdown: %v", err)
		}
	}()

	log.Printf("ODP Service listening at http://%s", *address)
	log.Printf("Service Document: http://%s/.well-known/odp", *address)
	log.Printf("Offerings: http://%s/odp/offerings", *address)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		started := time.Now()
		next.ServeHTTP(writer, request)
		log.Printf("%s %s %s", request.Method, request.URL.RequestURI(), time.Since(started).Round(time.Millisecond))
	})
}
