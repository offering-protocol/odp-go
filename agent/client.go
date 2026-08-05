package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"net/http"
	"strconv"
	"time"

	odp "github.com/offering-protocol/odp-go"
	"github.com/offering-protocol/odp-go/internal/jsonvalue"
)

const (
	maximumDocumentDepth = 8
	maximumRequestBytes  = maximumDocumentBytes
	maximumResourceDepth = 16
)

type ServiceClient struct {
	acceptLanguage   string
	cache            Cache
	client           *http.Client
	fallbacks        CacheFallbacks
	initialPageSize  int
	inspectionGate   chan struct{}
	maxRedirects     int
	partition        string
	resourceCache    Cache
	serviceOrigin    string
	supportingClient *http.Client
	supportingCache  Cache
}

func NewServiceClient(options ServiceClientOptions) (*ServiceClient, error) {
	serviceOrigin, err := odp.DeriveServiceOrigin(options.ServiceURL)
	if err != nil {
		return nil, err
	}
	hasCachePartition := options.CachePartition != ""
	if !hasCachePartition {
		options.CachePartition = "public"
	}
	if options.InitialPageSize == 0 {
		options.InitialPageSize = 50
	}
	if options.InitialPageSize < 1 || options.InitialPageSize > 100 {
		return nil, errors.New("initial page size must be from 1 through 100")
	}
	if options.MaxRedirects == 0 {
		options.MaxRedirects = 5
	}
	if options.MaxRedirects < 0 || options.MaxRedirects > 5 {
		return nil, errors.New("maximum redirects must be from 1 through 5")
	}
	fallbacks := options.CacheFallbacks
	if fallbacks.ServiceDocument == 0 {
		fallbacks.ServiceDocument = ServiceDocumentFallback
	}
	if fallbacks.Collection == 0 {
		fallbacks.Collection = CollectionFallback
	}
	if fallbacks.Offering == 0 {
		fallbacks.Offering = OfferingFallback
	}
	if fallbacks.ServiceDocument < 0 || fallbacks.Collection < 0 || fallbacks.Offering < 0 {
		return nil, errors.New("cache fallbacks cannot be negative")
	}
	base := options.HTTPClient
	if base == nil {
		base = http.DefaultClient
	}
	client := *base
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	supportingBase := options.SupportingHTTPClient
	if supportingBase == nil {
		supportingBase = http.DefaultClient
	}
	supportingClient := *supportingBase
	supportingClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	cache := options.Cache
	if cache == nil {
		cache = NewMemoryCache()
	}
	resourceCache := cache
	if options.HTTPClient != nil && !hasCachePartition {
		resourceCache = nil
	}
	return &ServiceClient{
		acceptLanguage: options.AcceptLanguage, cache: cache, client: &client, fallbacks: fallbacks,
		initialPageSize: options.InitialPageSize, inspectionGate: make(chan struct{}, 1), maxRedirects: options.MaxRedirects,
		partition: options.CachePartition, resourceCache: resourceCache, serviceOrigin: serviceOrigin, supportingCache: cache, supportingClient: &supportingClient,
	}, nil
}

func (client *ServiceClient) Inspect(ctx context.Context) (Inspection, error) {
	select {
	case client.inspectionGate <- struct{}{}:
		defer func() { <-client.inspectionGate }()
	case <-ctx.Done():
		return Inspection{}, ctx.Err()
	}
	target := client.serviceOrigin + "/.well-known/odp"
	validate := func(data []byte) error {
		if jsonvalue.Depth(data) > maximumDocumentDepth {
			return errors.New("ODP Service Document exceeds its nesting-depth limit")
		}
		_, err := odp.ParseServiceDocument(data)
		return err
	}
	result, err := request(ctx, client.client, http.MethodGet, target, nil, client.acceptLanguage, client.maxRedirects, maximumDocumentBytes, client.cache, cacheKey(client.partition, http.MethodGet, target, client.acceptLanguage, nil), client.fallbacks.ServiceDocument, validate)
	if err != nil {
		return Inspection{}, err
	}
	document, err := odp.ParseServiceDocument(result.body)
	if err != nil {
		return Inspection{}, err
	}
	capabilities := Capabilities{Operations: append([]odp.OperationDescriptor(nil), document.Operations...)}
	if document.Protocols != nil {
		capabilities.Enrollment = append([]odp.EnrollmentProtocol(nil), document.Protocols.Enrollment...)
		capabilities.Payments = append([]odp.PaymentProtocol(nil), document.Protocols.Payments...)
	}
	return Inspection{
		Capabilities: capabilities, Document: document, FinalURL: result.finalURL,
		Freshness: result.freshness, RequestedURL: target, ServiceOrigin: client.serviceOrigin,
	}, nil
}

func (client *ServiceClient) ListCollections(ctx context.Context, options ListOptions) iter.Seq2[odp.Collection, error] {
	return client.collectionItems(ctx, odp.OperationListCollections, "", options, nil)
}

func (client *ServiceClient) ListCollectionPages(ctx context.Context, options ListOptions) iter.Seq2[odp.Page[odp.Collection], error] {
	return client.collectionPages(ctx, odp.OperationListCollections, "", options, nil)
}

func (client *ServiceClient) SearchCollections(ctx context.Context, options CollectionSearchOptions) iter.Seq2[odp.Collection, error] {
	list := ListOptions{Limit: options.Limit, MaxItems: options.MaxItems, MaxPages: options.MaxPages, Representation: options.Representation}
	body := odp.CollectionSearchRequest{Limit: requestLimit(options.Limit, client.initialPageSize), ODPVersion: odp.Version, ParentID: options.ParentID, Query: options.Query}
	return client.collectionItems(ctx, odp.OperationSearchCollections, "", list, &body)
}

func (client *ServiceClient) SearchCollectionPages(ctx context.Context, options CollectionSearchOptions) iter.Seq2[odp.Page[odp.Collection], error] {
	list := ListOptions{Limit: options.Limit, MaxItems: options.MaxItems, MaxPages: options.MaxPages, Representation: options.Representation}
	body := odp.CollectionSearchRequest{Limit: requestLimit(options.Limit, client.initialPageSize), ODPVersion: odp.Version, ParentID: options.ParentID, Query: options.Query}
	return client.collectionPages(ctx, odp.OperationSearchCollections, "", list, &body)
}

func (client *ServiceClient) ContinueListCollections(ctx context.Context, next string, options ContinuationOptions) iter.Seq2[odp.Collection, error] {
	return client.continueCollectionItems(ctx, next, options, false)
}

func (client *ServiceClient) ContinueListCollectionPages(ctx context.Context, next string, options ContinuationOptions) iter.Seq2[odp.Page[odp.Collection], error] {
	return client.continueCollectionPages(ctx, next, options, false)
}

func (client *ServiceClient) ContinueSearchCollections(ctx context.Context, next string, options ContinuationOptions) iter.Seq2[odp.Collection, error] {
	return client.continueCollectionItems(ctx, next, options, true)
}

func (client *ServiceClient) ContinueSearchCollectionPages(ctx context.Context, next string, options ContinuationOptions) iter.Seq2[odp.Page[odp.Collection], error] {
	return client.continueCollectionPages(ctx, next, options, true)
}

func (client *ServiceClient) GetCollection(ctx context.Context, id string, representation odp.Representation) (odp.Collection, error) {
	data, err := client.get(ctx, odp.OperationGetCollection, id, representation, client.fallbacks.Collection)
	if err != nil {
		return odp.Collection{}, err
	}
	return odp.ParseCollection(data)
}

func (client *ServiceClient) ListOfferings(ctx context.Context, options ListOptions) iter.Seq2[odp.Offering, error] {
	return client.offeringItems(ctx, odp.OperationListOfferings, "", options, nil)
}

func (client *ServiceClient) ListOfferingPages(ctx context.Context, options ListOptions) iter.Seq2[odp.OfferingPage[odp.Offering], error] {
	return client.offeringPages(ctx, odp.OperationListOfferings, "", options, nil)
}

func (client *ServiceClient) ListCollectionOfferings(ctx context.Context, collectionID string, options ListOptions) iter.Seq2[odp.Offering, error] {
	return client.offeringItems(ctx, odp.OperationListCollectionOfferings, collectionID, options, nil)
}

func (client *ServiceClient) ListCollectionOfferingPages(ctx context.Context, collectionID string, options ListOptions) iter.Seq2[odp.OfferingPage[odp.Offering], error] {
	return client.offeringPages(ctx, odp.OperationListCollectionOfferings, collectionID, options, nil)
}

func (client *ServiceClient) SearchOfferings(ctx context.Context, options OfferingSearchOptions) iter.Seq2[odp.Offering, error] {
	list := ListOptions{Limit: options.Limit, MaxItems: options.MaxItems, MaxPages: options.MaxPages, Representation: options.Representation}
	body := odp.OfferingSearchRequest{
		CollectionID: options.CollectionID, Filters: options.Filters, IncludeDescendants: options.IncludeDescendants,
		Limit: requestLimit(options.Limit, client.initialPageSize), ODPVersion: odp.Version, Query: options.Query,
		Refinements: options.Refinements, Sort: options.Sort,
	}
	return client.offeringItems(ctx, odp.OperationSearchOfferings, "", list, &body)
}

func (client *ServiceClient) SearchOfferingPages(ctx context.Context, options OfferingSearchOptions) iter.Seq2[odp.OfferingPage[odp.Offering], error] {
	list := ListOptions{Limit: options.Limit, MaxItems: options.MaxItems, MaxPages: options.MaxPages, Representation: options.Representation}
	body := odp.OfferingSearchRequest{
		CollectionID: options.CollectionID, Filters: options.Filters, IncludeDescendants: options.IncludeDescendants,
		Limit: requestLimit(options.Limit, client.initialPageSize), ODPVersion: odp.Version, Query: options.Query,
		Refinements: options.Refinements, Sort: options.Sort,
	}
	return client.offeringPages(ctx, odp.OperationSearchOfferings, "", list, &body)
}

func (client *ServiceClient) ContinueListOfferings(ctx context.Context, next string, options ContinuationOptions) iter.Seq2[odp.Offering, error] {
	return client.continueOfferingItems(ctx, next, options, false)
}

func (client *ServiceClient) ContinueListOfferingPages(ctx context.Context, next string, options ContinuationOptions) iter.Seq2[odp.OfferingPage[odp.Offering], error] {
	return client.continueOfferingPages(ctx, next, options, false)
}

func (client *ServiceClient) ContinueSearchOfferings(ctx context.Context, next string, options ContinuationOptions) iter.Seq2[odp.Offering, error] {
	return client.continueOfferingItems(ctx, next, options, true)
}

func (client *ServiceClient) ContinueSearchOfferingPages(ctx context.Context, next string, options ContinuationOptions) iter.Seq2[odp.OfferingPage[odp.Offering], error] {
	return client.continueOfferingPages(ctx, next, options, true)
}

func (client *ServiceClient) GetOffering(ctx context.Context, id string, representation odp.Representation) (odp.Offering, error) {
	offering, _, err := client.getOffering(ctx, id, representation)
	return offering, err
}

func (client *ServiceClient) getOffering(ctx context.Context, id string, representation odp.Representation) (odp.Offering, Inspection, error) {
	inspection, err := client.Inspect(ctx)
	if err != nil {
		return odp.Offering{}, Inspection{}, err
	}
	data, err := client.getWithInspection(ctx, inspection, odp.OperationGetOffering, id, representation, client.fallbacks.Offering)
	if err != nil {
		return odp.Offering{}, Inspection{}, err
	}
	offering, err := odp.ParseOffering(data)
	return offering, inspection, err
}

func (client *ServiceClient) get(ctx context.Context, operation odp.Operation, id string, representation odp.Representation, fallback time.Duration) ([]byte, error) {
	inspection, err := client.Inspect(ctx)
	if err != nil {
		return nil, err
	}
	return client.getWithInspection(ctx, inspection, operation, id, representation, fallback)
}

func (client *ServiceClient) getWithInspection(ctx context.Context, inspection Inspection, operation odp.Operation, id string, representation odp.Representation, fallback time.Duration) ([]byte, error) {
	if !supports(inspection.Document, operation) {
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedOperation, operation)
	}
	target, err := odp.BuildOperationURL(inspection.Document.HTTP.EndpointBase, operation, client.serviceOrigin, id)
	if err != nil {
		return nil, err
	}
	query := target.Query()
	query.Set("representation", string(defaultRepresentation(representation, odp.RepresentationFull)))
	target.RawQuery = query.Encode()
	key := cacheKey(client.partition, http.MethodGet, target.String(), client.acceptLanguage, nil)
	validate := func(data []byte) error {
		if jsonvalue.Depth(data) > maximumResourceDepth {
			return errors.New("ODP response exceeds its nesting-depth limit")
		}
		if operation == odp.OperationGetCollection {
			_, err := odp.ParseCollection(data)
			return err
		}
		_, err := odp.ParseOffering(data)
		return err
	}
	result, err := request(ctx, client.client, http.MethodGet, target.String(), nil, client.acceptLanguage, client.maxRedirects, maximumResourceBytes, client.resourceCache, key, fallback, validate)
	if err != nil {
		return nil, err
	}
	return result.body, nil
}

func (client *ServiceClient) collectionItems(ctx context.Context, operation odp.Operation, id string, options ListOptions, body *odp.CollectionSearchRequest) iter.Seq2[odp.Collection, error] {
	return func(yield func(odp.Collection, error) bool) {
		count := 0
		for page, err := range client.collectionPages(ctx, operation, id, options, body) {
			if err != nil {
				yield(odp.Collection{}, err)
				return
			}
			for _, item := range page.Items {
				if options.MaxItems != 0 && count >= options.MaxItems {
					return
				}
				count++
				if !yield(item, nil) {
					return
				}
			}
		}
	}
}

func (client *ServiceClient) continueCollectionItems(ctx context.Context, next string, options ContinuationOptions, search bool) iter.Seq2[odp.Collection, error] {
	return func(yield func(odp.Collection, error) bool) {
		count := 0
		for page, err := range client.continueCollectionPages(ctx, next, options, search) {
			if err != nil {
				yield(odp.Collection{}, err)
				return
			}
			for _, item := range page.Items {
				if options.MaxItems != 0 && count >= options.MaxItems {
					return
				}
				count++
				if !yield(item, nil) {
					return
				}
			}
		}
	}
}

func (client *ServiceClient) continueCollectionPages(ctx context.Context, next string, options ContinuationOptions, search bool) iter.Seq2[odp.Page[odp.Collection], error] {
	return func(yield func(odp.Page[odp.Collection], error) bool) {
		list := ListOptions{MaxItems: options.MaxItems, MaxPages: options.MaxPages, Representation: options.Representation}
		if err := validateListOptions(list); err != nil {
			yield(odp.Page[odp.Collection]{}, err)
			return
		}
		validate := func(data []byte) error {
			if jsonvalue.Depth(data) > maximumResourceDepth {
				return errors.New("ODP response exceeds its nesting-depth limit")
			}
			_, err := odp.ParsePage[odp.Collection](data)
			return err
		}
		fallback := client.fallbacks.Collection
		if search {
			fallback = 0
		}
		data, err := client.continueRequest(ctx, next, fallback, validate)
		if err != nil {
			yield(odp.Page[odp.Collection]{}, err)
			return
		}
		page, err := odp.ParsePage[odp.Collection](data)
		if err != nil {
			yield(odp.Page[odp.Collection]{}, err)
			return
		}
		client.yieldCollectionPages(ctx, page, options.MaxPages, fallback, next, yield)
	}
}

func (client *ServiceClient) collectionPages(ctx context.Context, operation odp.Operation, id string, options ListOptions, search *odp.CollectionSearchRequest) iter.Seq2[odp.Page[odp.Collection], error] {
	return func(yield func(odp.Page[odp.Collection], error) bool) {
		if err := validateListOptions(options); err != nil {
			yield(odp.Page[odp.Collection]{}, err)
			return
		}
		initial, err := client.initialRequest(ctx, operation, id, options, search)
		if err != nil {
			yield(odp.Page[odp.Collection]{}, err)
			return
		}
		page, err := odp.ParsePage[odp.Collection](initial)
		if err != nil {
			yield(odp.Page[odp.Collection]{}, err)
			return
		}
		fallback := client.fallbacks.Collection
		if operation == odp.OperationSearchCollections {
			fallback = 0
		}
		client.yieldCollectionPages(ctx, page, options.MaxPages, fallback, "", yield)
	}
}

func (client *ServiceClient) yieldCollectionPages(ctx context.Context, first odp.Page[odp.Collection], maxPages int, fallback time.Duration, initialReference string, yield func(odp.Page[odp.Collection], error) bool) {
	pages := maxPages
	if pages == 0 {
		pages = odp.MaxTraversalPages
	}
	page := first
	visited := make(map[string]struct{})
	if initialReference != "" {
		visited[initialReference] = struct{}{}
	}
	for count := 0; count < pages; count++ {
		if !yield(page, nil) || page.Next == "" {
			return
		}
		if count+1 >= pages {
			return
		}
		if _, exists := visited[page.Next]; exists {
			yield(odp.Page[odp.Collection]{}, odp.ErrPaginationLoop)
			return
		}
		visited[page.Next] = struct{}{}
		validate := func(data []byte) error {
			if jsonvalue.Depth(data) > maximumResourceDepth {
				return errors.New("ODP response exceeds its nesting-depth limit")
			}
			_, err := odp.ParsePage[odp.Collection](data)
			return err
		}
		data, err := client.continueRequest(ctx, page.Next, fallback, validate)
		if err != nil {
			yield(odp.Page[odp.Collection]{}, err)
			return
		}
		page, err = odp.ParsePage[odp.Collection](data)
		if err != nil {
			yield(odp.Page[odp.Collection]{}, err)
			return
		}
	}
}

func (client *ServiceClient) offeringItems(ctx context.Context, operation odp.Operation, id string, options ListOptions, body *odp.OfferingSearchRequest) iter.Seq2[odp.Offering, error] {
	return func(yield func(odp.Offering, error) bool) {
		count := 0
		for page, err := range client.offeringPages(ctx, operation, id, options, body) {
			if err != nil {
				yield(odp.Offering{}, err)
				return
			}
			for _, item := range page.Items {
				if options.MaxItems != 0 && count >= options.MaxItems {
					return
				}
				count++
				if !yield(item, nil) {
					return
				}
			}
		}
	}
}

func (client *ServiceClient) continueOfferingItems(ctx context.Context, next string, options ContinuationOptions, search bool) iter.Seq2[odp.Offering, error] {
	return func(yield func(odp.Offering, error) bool) {
		count := 0
		for page, err := range client.continueOfferingPages(ctx, next, options, search) {
			if err != nil {
				yield(odp.Offering{}, err)
				return
			}
			for _, item := range page.Items {
				if options.MaxItems != 0 && count >= options.MaxItems {
					return
				}
				count++
				if !yield(item, nil) {
					return
				}
			}
		}
	}
}

func (client *ServiceClient) continueOfferingPages(ctx context.Context, next string, options ContinuationOptions, search bool) iter.Seq2[odp.OfferingPage[odp.Offering], error] {
	return func(yield func(odp.OfferingPage[odp.Offering], error) bool) {
		list := ListOptions{MaxItems: options.MaxItems, MaxPages: options.MaxPages, Representation: options.Representation}
		if err := validateListOptions(list); err != nil {
			yield(odp.OfferingPage[odp.Offering]{}, err)
			return
		}
		validate := func(data []byte) error {
			if jsonvalue.Depth(data) > maximumResourceDepth {
				return errors.New("ODP response exceeds its nesting-depth limit")
			}
			_, err := parseOfferingPage(data, search)
			return err
		}
		fallback := client.fallbacks.Offering
		if search {
			fallback = 0
		}
		data, err := client.continueRequest(ctx, next, fallback, validate)
		if err != nil {
			yield(odp.OfferingPage[odp.Offering]{}, err)
			return
		}
		page, err := parseOfferingPage(data, search)
		if err != nil {
			yield(odp.OfferingPage[odp.Offering]{}, err)
			return
		}
		if search && len(page.Refinements) != 0 {
			yield(odp.OfferingPage[odp.Offering]{}, errors.New("ODP Offering search continuation cannot contain refinements"))
			return
		}
		pages := options.MaxPages
		if pages == 0 {
			pages = odp.MaxTraversalPages
		}
		load := func(ctx context.Context, reference string) ([]byte, error) {
			return client.continueRequest(ctx, reference, fallback, validate)
		}
		for current, err := range iterateOfferingPages(ctx, page, search, pages, next, load) {
			if err != nil {
				yield(odp.OfferingPage[odp.Offering]{}, err)
				return
			}
			if !yield(current, nil) {
				return
			}
		}
	}
}

func (client *ServiceClient) offeringPages(ctx context.Context, operation odp.Operation, id string, options ListOptions, search *odp.OfferingSearchRequest) iter.Seq2[odp.OfferingPage[odp.Offering], error] {
	return func(yield func(odp.OfferingPage[odp.Offering], error) bool) {
		if err := validateListOptions(options); err != nil {
			yield(odp.OfferingPage[odp.Offering]{}, err)
			return
		}
		initial, err := client.initialRequest(ctx, operation, id, options, search)
		if err != nil {
			yield(odp.OfferingPage[odp.Offering]{}, err)
			return
		}
		page, err := parseOfferingPage(initial, operation == odp.OperationSearchOfferings)
		if err != nil {
			yield(odp.OfferingPage[odp.Offering]{}, err)
			return
		}
		pages := options.MaxPages
		if pages == 0 {
			pages = odp.MaxTraversalPages
		}
		fallback := client.fallbacks.Offering
		if operation == odp.OperationSearchOfferings {
			fallback = 0
		}
		load := func(ctx context.Context, next string) ([]byte, error) {
			validate := func(data []byte) error {
				if jsonvalue.Depth(data) > maximumResourceDepth {
					return errors.New("ODP response exceeds its nesting-depth limit")
				}
				_, err := parseOfferingPage(data, operation == odp.OperationSearchOfferings)
				return err
			}
			return client.continueRequest(ctx, next, fallback, validate)
		}
		for current, err := range iterateOfferingPages(ctx, page, operation == odp.OperationSearchOfferings, pages, "", load) {
			if err != nil {
				yield(odp.OfferingPage[odp.Offering]{}, err)
				return
			}
			if !yield(current, nil) {
				return
			}
		}
	}
}

func (client *ServiceClient) initialRequest(ctx context.Context, operation odp.Operation, id string, options ListOptions, body any) ([]byte, error) {
	inspection, err := client.Inspect(ctx)
	if err != nil {
		return nil, err
	}
	if !supports(inspection.Document, operation) {
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedOperation, operation)
	}
	target, err := odp.BuildOperationURL(inspection.Document.HTTP.EndpointBase, operation, client.serviceOrigin, id)
	if err != nil {
		return nil, err
	}
	query := target.Query()
	query.Set("representation", string(defaultRepresentation(options.Representation, odp.RepresentationTerse)))
	method := odpMethod(operation)
	var encoded []byte
	if body != nil {
		encoded, err = json.Marshal(body)
		if err != nil {
			return nil, err
		}
		if len(encoded) > maximumRequestBytes || jsonvalue.Depth(encoded) > maximumResourceDepth {
			return nil, errors.New("ODP search request exceeds its resource limits")
		}
		switch operation {
		case odp.OperationSearchCollections:
			if _, err := odp.ParseCollectionSearchRequest(encoded); err != nil {
				return nil, err
			}
		case odp.OperationSearchOfferings:
			if _, err := odp.ParseOfferingSearchRequest(encoded); err != nil {
				return nil, err
			}
		}
	} else {
		query.Set("limit", strconv.Itoa(requestLimit(options.Limit, client.initialPageSize)))
	}
	target.RawQuery = query.Encode()
	fallback := client.fallbacks.Offering
	if operation == odp.OperationListCollections {
		fallback = client.fallbacks.Collection
	}
	if operation == odp.OperationSearchCollections || operation == odp.OperationSearchOfferings {
		fallback = 0
	}
	key := cacheKey(client.partition, method, target.String(), client.acceptLanguage, encoded)
	validate := func(data []byte) error {
		if jsonvalue.Depth(data) > maximumResourceDepth {
			return errors.New("ODP response exceeds its nesting-depth limit")
		}
		if operation == odp.OperationListCollections || operation == odp.OperationSearchCollections {
			_, err := odp.ParsePage[odp.Collection](data)
			return err
		}
		_, err := parseOfferingPage(data, operation == odp.OperationSearchOfferings)
		return err
	}
	result, err := request(ctx, client.client, method, target.String(), encoded, client.acceptLanguage, client.maxRedirects, maximumResourceBytes, client.resourceCache, key, fallback, validate)
	if err != nil {
		return nil, err
	}
	return result.body, nil
}

func (client *ServiceClient) continueRequest(ctx context.Context, reference string, fallback time.Duration, validate func([]byte) error) ([]byte, error) {
	target, err := odp.ResolveContinuation(reference, client.serviceOrigin)
	if err != nil {
		return nil, err
	}
	key := cacheKey(client.partition, http.MethodGet, target.String(), client.acceptLanguage, nil)
	result, err := request(ctx, client.client, http.MethodGet, target.String(), nil, client.acceptLanguage, client.maxRedirects, maximumResourceBytes, client.resourceCache, key, fallback, validate)
	if err != nil {
		return nil, err
	}
	return result.body, nil
}

func parseOfferingPage(data []byte, search bool) (odp.OfferingPage[odp.Offering], error) {
	if search {
		return odp.ParseOfferingSearchResponse(data)
	}
	page, err := odp.ParsePage[odp.Offering](data)
	if err != nil {
		return odp.OfferingPage[odp.Offering]{}, err
	}
	return odp.OfferingPage[odp.Offering]{Additional: page.Additional, AuthExpands: page.AuthExpands, Items: page.Items, Next: page.Next, ODPVersion: page.ODPVersion}, nil
}

func iterateOfferingPages(ctx context.Context, first odp.OfferingPage[odp.Offering], search bool, maximumPages int, initialReference string, load func(context.Context, string) ([]byte, error)) iter.Seq2[odp.OfferingPage[odp.Offering], error] {
	return func(yield func(odp.OfferingPage[odp.Offering], error) bool) {
		page := first
		visited := make(map[string]struct{})
		if initialReference != "" {
			visited[initialReference] = struct{}{}
		}
		for count := 0; count < maximumPages; count++ {
			if !yield(page, nil) || page.Next == "" {
				return
			}
			if count+1 >= maximumPages {
				return
			}
			if _, exists := visited[page.Next]; exists {
				yield(odp.OfferingPage[odp.Offering]{}, odp.ErrPaginationLoop)
				return
			}
			visited[page.Next] = struct{}{}
			data, err := load(ctx, page.Next)
			if err != nil {
				yield(odp.OfferingPage[odp.Offering]{}, err)
				return
			}
			page, err = parseOfferingPage(data, search)
			if err != nil {
				yield(odp.OfferingPage[odp.Offering]{}, err)
				return
			}
		}
	}
}

func validateListOptions(options ListOptions) error {
	if options.Limit < 0 || options.Limit > 100 {
		return errors.New("limit must be from 1 through 100")
	}
	if options.MaxItems < 0 || options.MaxItems > 10_000 {
		return errors.New("maximum items must be from 1 through 10000")
	}
	if options.MaxPages < 0 || options.MaxPages > odp.MaxTraversalPages {
		return errors.New("maximum pages must be from 1 through 16")
	}
	if options.Representation != "" && options.Representation != odp.RepresentationTerse && options.Representation != odp.RepresentationFull {
		return errors.New("representation must be terse or full")
	}
	return nil
}

func supports(document odp.ServiceDocument, operation odp.Operation) bool {
	for _, candidate := range document.Operations {
		if candidate.Name == operation {
			return true
		}
	}
	return false
}

func defaultRepresentation(value, fallback odp.Representation) odp.Representation {
	if value == "" {
		return fallback
	}
	return value
}

func requestLimit(value, fallback int) int {
	if value == 0 {
		return fallback
	}
	return value
}

func odpMethod(operation odp.Operation) string {
	method, _ := odp.OperationMethod(operation)
	return method
}
