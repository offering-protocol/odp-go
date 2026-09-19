// Package directory searches indexed Services and Collections in the canonical Directory.
package directory

import (
	"encoding/json"
	"iter"
	"net/http"
	"net/url"
	"time"

	odp "github.com/offering-protocol/odp-go"
)

type Environment string

const (
	Production Environment = "production"
	Sandbox    Environment = "sandbox"
)

const (
	productionOrigin = "https://api.inflowpay.ai"
	sandboxOrigin    = "https://sandbox.inflowpay.ai"
)

type Options struct {
	Environment Environment
	HTTPClient  *http.Client
}

type ServiceFilters struct {
	Enrollment []odp.EnrollmentProtocol `json:"enrollment,omitempty"`
	Keywords   []string                 `json:"keywords,omitempty"`
	Operations []OperationFilter        `json:"operations,omitempty"`
	Payments   []PaymentFilter          `json:"payments,omitempty"`
	Trust      []odp.TrustProtocol      `json:"trust,omitempty"`
}

type OperationFilter struct {
	Authentication odp.AuthenticationRequirement `json:"authentication,omitempty"`
	Name           odp.Operation                 `json:"name"`
}

type PaymentFilter struct {
	Authentication odp.AuthenticationRequirement `json:"authentication,omitempty"`
	Name           odp.Protocol                  `json:"name"`
	Options        []odp.PaymentOption           `json:"options,omitempty"`
}

type SearchRequest struct {
	Filters *ServiceFilters `json:"filters,omitempty"`
	Limit   int             `json:"limit,omitempty"`
	Query   string          `json:"query,omitempty"`
}

type DirectorySearchRequest struct {
	SearchRequest
	Types []string `json:"types,omitempty"`
}

type IndexedService struct {
	Service
	ServiceID string
}

type ServiceReference struct {
	Additional    odp.AdditionalMembers
	ServiceID     string
	ServiceOrigin string
	Name          string
}

type CollectionSummary struct {
	Additional  odp.AdditionalMembers
	ID          string
	Name        string
	Description string
}

// Result retains unknown resource types in Raw without interpreting them as Services.
type Result struct {
	Additional       odp.AdditionalMembers
	Type             string
	Service          *IndexedService
	Collection       *CollectionSummary
	AvailableThrough *ServiceReference
	IndexedAt        time.Time
	Raw              json.RawMessage
}

type IterationOptions struct {
	MaxItems int
	MaxPages int
	// OnIssue, when set, is called for each record a page carried that could not be used. Without
	// it an item traversal cannot tell a page of unusable records from a page of no matches.
	OnIssue func(Issue)
}

type Service struct {
	Additional       odp.AdditionalMembers
	Description      string
	DocumentationURL string
	IndexedAt        time.Time
	Keywords         []string
	Language         string
	Localizations    []string
	Name             string
	Operations       []odp.OperationDescriptor
	Protocols        *odp.ServiceProtocols
	ServiceOrigin    string
	StatusURL        string
	SupportURL       string
	WebsiteURL       string
}

type Facet[Value any] struct {
	Count int64
	Value Value
}

type Facets struct {
	Enrollment     []Facet[odp.EnrollmentProtocol]
	Keywords       []Facet[string]
	Operations     []Facet[odp.OperationDescriptor]
	PaymentOptions []Facet[PaymentOptionFacetValue]
	Payments       []Facet[odp.PaymentProtocol]
	Trust          []Facet[odp.TrustProtocol]
}

type PaymentOptionFacetValue struct {
	Name   odp.Protocol      `json:"name"`
	Option odp.PaymentOption `json:"option"`
}

type SearchResponse[Item any] struct {
	Additional odp.AdditionalMembers
	Facets     *Facets
	// Issues reports records this page carried that could not be used. They are reported rather
	// than raised because a directory is not an ODP protocol role: one unusable record says
	// nothing about the rest of the page.
	Issues []Issue
	Items  []Item
	Next   string
}

// SearchSequence exposes independent, lazy traversals of the same search.
type SearchSequence[Item any] struct {
	Items     iter.Seq2[Item, error]
	Responses iter.Seq2[SearchResponse[Item], error]
}

type IssueScope string

const IssueService IssueScope = "service"
const IssueResult IssueScope = "result"

// Issue describes one Directory record this client discarded, and why.
type Issue struct {
	Index   int
	Message string
	Scope   IssueScope
}

type SuggestionRequest struct {
	Limit  int
	Prefix string
}

type RequestError struct {
	Header    http.Header
	Message   string
	Retryable bool
	Status    int
}

func (err *RequestError) Error() string {
	return err.Message
}

type Client struct {
	environment Environment
	httpClient  *http.Client
	originURL   *url.URL
}

func (client *Client) Environment() Environment {
	return client.environment
}

// cloneAdditional copies the members a caller may keep, leaving the source map untouched.
func cloneAdditional(object map[string]json.RawMessage, known ...string) odp.AdditionalMembers {
	excluded := make(map[string]struct{}, len(known))
	for _, name := range known {
		excluded[name] = struct{}{}
	}
	result := odp.AdditionalMembers{}
	for name, value := range object {
		if _, found := excluded[name]; found {
			continue
		}
		result[name] = value
	}
	if len(result) == 0 {
		return nil
	}
	return result
}
