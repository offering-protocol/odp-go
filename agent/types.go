// Package agent provides Agent-side ODP Service inspection, catalog navigation, and discovery.
package agent

import (
	"context"
	"errors"
	"iter"
	"net/http"
	"time"

	odp "github.com/offering-protocol/odp-go"
	"github.com/offering-protocol/odp-go/directory"
)

const (
	ServiceDocumentFallback = 4 * time.Hour
	CollectionFallback      = time.Hour
	OfferingFallback        = 5 * time.Minute
)

type Freshness string

const (
	FreshnessFetched     Freshness = "fetched"
	FreshnessFresh       Freshness = "fresh"
	FreshnessRevalidated Freshness = "revalidated"
)

type Capabilities struct {
	Enrollment []odp.EnrollmentProtocol
	Operations []odp.OperationDescriptor
	Payments   []odp.PaymentProtocol
}

type Inspection struct {
	Capabilities  Capabilities
	Document      odp.ServiceDocument
	FinalURL      string
	Freshness     Freshness
	RequestedURL  string
	ServiceOrigin string
}

type CacheFallbacks struct {
	Collection      time.Duration
	Offering        time.Duration
	ServiceDocument time.Duration
}

type CacheRecord struct {
	Body         []byte
	ETag         string
	ExpiresAt    time.Time
	FinalURL     string
	LastModified string
	Status       int
	StoredAt     time.Time
}

type Cache interface {
	Delete(context.Context, string) error
	Get(context.Context, string) (CacheRecord, bool, error)
	Set(context.Context, string, CacheRecord) error
}

type ServiceClientOptions struct {
	AcceptLanguage       string
	Cache                Cache
	CacheFallbacks       CacheFallbacks
	CachePartition       string
	HTTPClient           *http.Client
	SupportingHTTPClient *http.Client
	InitialPageSize      int
	MaxRedirects         int
	ServiceURL           string
}

type CapabilityScope string

const (
	CapabilityScopeCollection CapabilityScope = "collection"
	CapabilityScopeService    CapabilityScope = "service"
)

type CapabilityKind string

const (
	CapabilityKindFilters CapabilityKind = "filters"
	CapabilityKindSorts   CapabilityKind = "sorts"
)

type CapabilityIssue struct {
	Kind    CapabilityKind
	Message string
	Scope   CapabilityScope
}

type ResolvedSortDefinition struct {
	odp.SortDefinition
	Filters []odp.FilterDefinition
}

type SearchCapabilityCatalog struct {
	Filters map[string]odp.FilterDefinition
	Issues  []CapabilityIssue
	Sorts   map[string]ResolvedSortDefinition
}

type OfferingIssue struct {
	ActionID string
	Message  string
	Scope    OfferingIssueScope
}

type OfferingIssueScope string

const (
	OfferingIssueAction          OfferingIssueScope = "action"
	OfferingIssueAttributeSchema OfferingIssueScope = "attribute_schema"
	OfferingIssueAttributes      OfferingIssueScope = "attributes"
)

type DiscoveredHTTPAction struct {
	Method               string
	Request              *odp.ActionRequest
	ResponseContentTypes []string
	URL                  string
}

type DiscoveredOpenAPIAction struct {
	OperationID string
	URL         string
}

type DiscoveredAction struct {
	Authentication odp.AuthenticationRequirement
	Description    string
	HTTP           *DiscoveredHTTPAction
	ID             string
	OpenAPI        *DiscoveredOpenAPIAction
	Rel            odp.ActionRelation
}

type OfferingDetails struct {
	odp.Offering
	Actions         []DiscoveredAction
	AttributeSchema map[string]any
	Issues          []OfferingIssue
}

type ResolvedAction struct {
	Action          DiscoveredAction
	OpenAPIDocument map[string]any
	Operation       map[string]any
	RequestSchema   map[string]any
}

type ListOptions struct {
	Limit          int
	MaxItems       int
	MaxPages       int
	Representation odp.Representation
}

type ContinuationOptions struct {
	MaxItems       int
	MaxPages       int
	Representation odp.Representation
}

type CollectionSearchOptions struct {
	Limit          int
	MaxItems       int
	MaxPages       int
	ParentID       odp.Optional[string]
	Query          string
	Representation odp.Representation
}

type OfferingSearchOptions struct {
	CollectionID       string
	Filters            []odp.FilterExpression
	IncludeDescendants bool
	Limit              int
	MaxItems           int
	MaxPages           int
	Query              string
	Refinements        []string
	Representation     odp.Representation
	Sort               string
}

type RequestError struct {
	Code      string
	Header    http.Header
	Problem   *odp.ProblemDetails
	Retryable bool
	Status    int
}

func (err *RequestError) Error() string {
	if err.Problem != nil && err.Problem.Detail != "" {
		return err.Problem.Detail
	}
	if err.Problem != nil && err.Problem.Title != "" {
		return err.Problem.Title
	}
	return http.StatusText(err.Status)
}

type ServiceClientFactory func(context.Context, directory.Service) (*ServiceClient, error)

type AgentOptions struct {
	Directory           *directory.Client
	DirectoryHTTPClient *http.Client
	Environment         directory.Environment
	ServiceClient       ServiceClientFactory
}

type FederatedSearchRequest struct {
	Concurrency            int
	MaxOfferingsPerService int
	MaxServices            int
	Offerings              OfferingSearchOptions
	Services               directory.SearchRequest
}

type DiscoveryEventType string

const (
	DiscoveryOffering DiscoveryEventType = "offering"
	DiscoveryIssue    DiscoveryEventType = "issue"
)

type DiscoveryEvent struct {
	Err      error
	Offering *odp.Offering
	Service  directory.Service
	Type     DiscoveryEventType
}

type Agent struct {
	directory     *directory.Client
	environment   directory.Environment
	serviceClient ServiceClientFactory
}

func (agent *Agent) Environment() directory.Environment {
	return agent.environment
}

func (agent *Agent) SearchOfferingsAcrossServices(ctx context.Context, request FederatedSearchRequest) iter.Seq2[DiscoveryEvent, error] {
	return agent.searchOfferingsAcrossServices(ctx, request)
}

var ErrUnsupportedOperation = errors.New("ODP Service does not advertise the requested operation")
