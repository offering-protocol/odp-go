// Package directory provides the canonical ODP Service directory client.
package directory

import (
	"encoding/json"
	"net/http"
	"time"

	odp "github.com/offering-protocol/odp-go"
)

type Environment string

const (
	Production Environment = "production"
	Sandbox    Environment = "sandbox"
)

const (
	productionOrigin = "https://directory.offeringprotocol.org"
	sandboxOrigin    = "https://sandbox.offeringprotocol.org"
)

type Options struct {
	Environment Environment
	HTTPClient  *http.Client
}

type ServiceFilters struct {
	Keywords   []string        `json:"keywords,omitempty"`
	Onboarding []odp.Protocol  `json:"onboarding,omitempty"`
	Operations []odp.Operation `json:"operations,omitempty"`
	Payments   []odp.Protocol  `json:"payments,omitempty"`
}

type SearchRequest struct {
	Filters *ServiceFilters `json:"filters,omitempty"`
	Limit   int             `json:"limit,omitempty"`
	Query   string          `json:"query,omitempty"`
}

type IterationOptions struct {
	MaxItems int
	MaxPages int
}

type Service struct {
	Additional    odp.AdditionalMembers
	Description   string
	IndexedAt     time.Time
	Keywords      []string
	Language      string
	Localizations []string
	Name          string
	Operations    []odp.Operation
	Protocols     *odp.ServiceProtocols
	ServiceOrigin string
}

type Facet[Value ~string] struct {
	Count int64
	Value Value
}

type Facets struct {
	Keywords   []Facet[string]
	Onboarding []Facet[odp.Protocol]
	Operations []Facet[odp.Operation]
	Payments   []Facet[odp.Protocol]
}

type SearchPage struct {
	Additional odp.AdditionalMembers
	Facets     *Facets
	Items      []Service
	Next       string
}

type SuggestionRequest struct {
	Limit  int
	Prefix string
}

type RequestError struct {
	Header  http.Header
	Message string
	Status  int
}

func (err *RequestError) Error() string {
	return err.Message
}

type Client struct {
	environment Environment
	httpClient  *http.Client
	origin      string
}

func (client *Client) Environment() Environment {
	return client.environment
}

func cloneAdditional(object map[string]json.RawMessage, known ...string) odp.AdditionalMembers {
	for _, name := range known {
		delete(object, name)
	}
	if len(object) == 0 {
		return nil
	}
	return object
}
