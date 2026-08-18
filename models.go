package odp

import "encoding/json"

const Version = "1.0"

type Representation string

const (
	RepresentationTerse Representation = "terse"
	RepresentationFull  Representation = "full"
)

type PriceType string

const (
	PriceFixed      PriceType = "fixed"
	PriceFree       PriceType = "free"
	PriceMetered    PriceType = "metered"
	PriceQuote      PriceType = "quote"
	PriceRange      PriceType = "range"
	PriceStartingAt PriceType = "starting_at"
)

type ActionRelation string

const (
	ActionDownload ActionRelation = "download"
	ActionInvoke   ActionRelation = "invoke"
	ActionPurchase ActionRelation = "purchase"
	ActionQuote    ActionRelation = "quote"
	ActionReserve  ActionRelation = "reserve"
)

type ResourceType string

const (
	ResourceCollection ResourceType = "collection"
	ResourceOffering   ResourceType = "offering"
)

type Operation string

const (
	OperationGetCollection           Operation = "get-collection"
	OperationGetOffering             Operation = "get-offering"
	OperationListCollectionOfferings Operation = "list-collection-offerings"
	OperationListCollections         Operation = "list-collections"
	OperationListOfferings           Operation = "list-offerings"
	OperationSearchCollections       Operation = "search-collections"
	OperationSearchOfferings         Operation = "search-offerings"
)

type AuthenticationRequirement string

const (
	AuthenticationNotRequired AuthenticationRequirement = "not-required"
	AuthenticationOptional    AuthenticationRequirement = "optional"
	AuthenticationRequired    AuthenticationRequirement = "required"
)

type OperationDescriptor struct {
	Authentication AuthenticationRequirement `json:"authentication"`
	Name           Operation                 `json:"name"`
}

type AdditionalMembers map[string]json.RawMessage

type Protocol string

const (
	ProtocolAEP  Protocol = "aep"
	ProtocolMPP  Protocol = "mpp"
	ProtocolX402 Protocol = "x402"
)

type ServiceProtocols struct {
	Enrollment []EnrollmentProtocol `json:"enrollment,omitempty"`
	Payments   []PaymentProtocol    `json:"payments,omitempty"`
}

type EnrollmentProtocol struct {
	Name Protocol `json:"name"`
}

type PaymentOption string

const (
	PaymentOptionAlgorand  PaymentOption = "algorand"
	PaymentOptionAptos     PaymentOption = "aptos"
	PaymentOptionArbitrum  PaymentOption = "arbitrum"
	PaymentOptionAvalanche PaymentOption = "avalanche"
	PaymentOptionBase      PaymentOption = "base"
	PaymentOptionCard      PaymentOption = "card"
	PaymentOptionEthereum  PaymentOption = "ethereum"
	PaymentOptionHedera    PaymentOption = "hedera"
	PaymentOptionInflow    PaymentOption = "inflow"
	PaymentOptionLightning PaymentOption = "lightning"
	PaymentOptionPolygon   PaymentOption = "polygon"
	PaymentOptionSolana    PaymentOption = "solana"
	PaymentOptionStellar   PaymentOption = "stellar"
	PaymentOptionStripe    PaymentOption = "stripe"
	PaymentOptionTempo     PaymentOption = "tempo"
	PaymentOptionTON       PaymentOption = "ton"
)

func IsPaymentOption(value PaymentOption) bool {
	switch value {
	case PaymentOptionAlgorand, PaymentOptionAptos, PaymentOptionArbitrum, PaymentOptionAvalanche,
		PaymentOptionBase, PaymentOptionCard, PaymentOptionEthereum, PaymentOptionHedera,
		PaymentOptionInflow, PaymentOptionLightning, PaymentOptionPolygon, PaymentOptionSolana,
		PaymentOptionStellar, PaymentOptionStripe, PaymentOptionTempo, PaymentOptionTON:
		return true
	default:
		return false
	}
}

type PaymentProtocol struct {
	Authentication AuthenticationRequirement `json:"authentication"`
	Name           Protocol                  `json:"name"`
	Options        []PaymentOption           `json:"options,omitempty"`
}

type FilterType string

const (
	FilterBoolean  FilterType = "boolean"
	FilterDate     FilterType = "date"
	FilterDateTime FilterType = "date-time"
	FilterDecimal  FilterType = "decimal"
	FilterInteger  FilterType = "integer"
	FilterNumber   FilterType = "number"
	FilterString   FilterType = "string"
)

type FilterOperator string

const (
	OperatorEqual              FilterOperator = "eq"
	OperatorExists             FilterOperator = "exists"
	OperatorGreaterThan        FilterOperator = "gt"
	OperatorGreaterThanOrEqual FilterOperator = "gte"
	OperatorIn                 FilterOperator = "in"
	OperatorLessThan           FilterOperator = "lt"
	OperatorLessThanOrEqual    FilterOperator = "lte"
)

type HTTPConfiguration struct {
	Additional   AdditionalMembers `json:"-"`
	EndpointBase string            `json:"endpoint_base"`
	OpenAPI      *ServiceOpenAPI   `json:"openapi,omitempty"`
}

type CapabilityLink struct {
	Additional AdditionalMembers `json:"-"`
	Href       string            `json:"href"`
}

type FilterUnit struct {
	Additional AdditionalMembers `json:"-"`
	Code       string            `json:"code"`
	System     string            `json:"system"`
	Title      string            `json:"title,omitempty"`
}

type FilterDefinition struct {
	Additional  AdditionalMembers `json:"-"`
	Description string            `json:"description"`
	ID          string            `json:"id"`
	Operators   []FilterOperator  `json:"operators"`
	Refinable   bool              `json:"refinable,omitempty"`
	Title       string            `json:"title"`
	Type        FilterType        `json:"type"`
	Unit        *FilterUnit       `json:"unit,omitempty"`
}

type SortKey struct {
	Additional AdditionalMembers `json:"-"`
	Direction  SortDirection     `json:"direction"`
	FilterID   string            `json:"filter_id"`
	Missing    MissingPlacement  `json:"missing"`
}

type SortDirection string

const (
	SortAscending  SortDirection = "ascending"
	SortDescending SortDirection = "descending"
)

type MissingPlacement string

const (
	MissingFirst MissingPlacement = "first"
	MissingLast  MissingPlacement = "last"
)

type SortDefinition struct {
	Additional  AdditionalMembers `json:"-"`
	Description string            `json:"description"`
	ID          string            `json:"id"`
	Keys        []SortKey         `json:"keys"`
	Title       string            `json:"title"`
}

type FilterCapabilitySource struct {
	Additional AdditionalMembers  `json:"-"`
	Inline     []FilterDefinition `json:"inline,omitempty"`
	Linked     *CapabilityLink    `json:"linked,omitempty"`
}

type SortCapabilitySource struct {
	Additional AdditionalMembers `json:"-"`
	Inline     []SortDefinition  `json:"inline,omitempty"`
	Linked     *CapabilityLink   `json:"linked,omitempty"`
}

type SearchCapabilities struct {
	Additional AdditionalMembers       `json:"-"`
	Filters    *FilterCapabilitySource `json:"filters,omitempty"`
	Sorts      *SortCapabilitySource   `json:"sorts,omitempty"`
}

type ServiceBrandingImageType string

const (
	ServiceBrandingPNG  ServiceBrandingImageType = "image/png"
	ServiceBrandingSVG  ServiceBrandingImageType = "image/svg+xml"
	ServiceBrandingWebP ServiceBrandingImageType = "image/webp"
)

type ServiceBrandingImage struct {
	Source string                   `json:"src"`
	Type   ServiceBrandingImageType `json:"type"`
}

type ServiceBranding struct {
	Icon ServiceBrandingImage `json:"icon"`
	Logo ServiceBrandingImage `json:"logo"`
}

type ServiceOpenAPI struct {
	URL string `json:"url"`
}

type ServiceDocument struct {
	Additional         AdditionalMembers     `json:"-"`
	Branding           *ServiceBranding      `json:"branding,omitempty"`
	Description        string                `json:"description"`
	DocumentationURL   string                `json:"documentation_url,omitempty"`
	HTTP               HTTPConfiguration     `json:"http"`
	Keywords           []string              `json:"keywords,omitempty"`
	Language           string                `json:"language"`
	Localizations      []string              `json:"localizations"`
	Name               string                `json:"name"`
	ODPVersion         string                `json:"odp_version"`
	Operations         []OperationDescriptor `json:"operations"`
	Protocols          *ServiceProtocols     `json:"protocols,omitempty"`
	SearchCapabilities *SearchCapabilities   `json:"search_capabilities,omitempty"`
	StatusURL          string                `json:"status_url,omitempty"`
	SupportURL         string                `json:"support_url,omitempty"`
	WebsiteURL         string                `json:"website_url,omitempty"`
}

type Collection struct {
	Additional         AdditionalMembers   `json:"-"`
	AuthExpands        bool                `json:"auth_expands,omitempty"`
	Description        string              `json:"description,omitempty"`
	DetailFields       []string            `json:"detail_fields,omitempty"`
	ID                 string              `json:"id"`
	Language           string              `json:"language,omitempty"`
	Localizations      []string            `json:"localizations,omitempty"`
	Name               string              `json:"name"`
	ODPVersion         string              `json:"odp_version,omitempty"`
	ParentIDs          []string            `json:"parent_ids,omitempty"`
	SearchCapabilities *SearchCapabilities `json:"search_capabilities,omitempty"`
	WebURL             string              `json:"web_url,omitempty"`
}

type SchemaReference struct {
	URL string `json:"url"`
}

type PricePreview struct {
	Additional AdditionalMembers `json:"-"`
	Amount     string            `json:"amount,omitempty"`
	Currency   string            `json:"currency,omitempty"`
	Maximum    string            `json:"maximum,omitempty"`
	Minimum    string            `json:"minimum,omitempty"`
	Type       PriceType         `json:"type"`
	Unit       string            `json:"unit,omitempty"`
}

type ActionRequest struct {
	ContentType string           `json:"content_type,omitempty"`
	Schema      *SchemaReference `json:"schema,omitempty"`
}

type HTTPActionTarget struct {
	Href                 string         `json:"href"`
	Method               string         `json:"method"`
	Request              *ActionRequest `json:"request,omitempty"`
	ResponseContentTypes []string       `json:"response_content_types,omitempty"`
}

type OpenAPIActionTarget struct {
	OperationID string `json:"operation_id"`
	URL         string `json:"url,omitempty"`
}

type Action struct {
	Authentication AuthenticationRequirement `json:"authentication"`
	Description    string                    `json:"description,omitempty"`
	HTTP           *HTTPActionTarget         `json:"http,omitempty"`
	ID             string                    `json:"id"`
	OpenAPI        *OpenAPIActionTarget      `json:"openapi,omitempty"`
	Rel            ActionRelation            `json:"rel"`
}

type Offering struct {
	Actions       []Action                   `json:"actions,omitempty"`
	Additional    AdditionalMembers          `json:"-"`
	AuthExpands   bool                       `json:"auth_expands,omitempty"`
	Attributes    map[string]json.RawMessage `json:"attributes,omitempty"`
	CollectionIDs []string                   `json:"collection_ids,omitempty"`
	Description   string                     `json:"description,omitempty"`
	DetailFields  []string                   `json:"detail_fields,omitempty"`
	ID            string                     `json:"id"`
	Language      string                     `json:"language,omitempty"`
	Localizations []string                   `json:"localizations,omitempty"`
	Name          string                     `json:"name"`
	ODPVersion    string                     `json:"odp_version,omitempty"`
	Price         *PricePreview              `json:"price,omitempty"`
	Schema        *SchemaReference           `json:"schema,omitempty"`
	WebURL        string                     `json:"web_url,omitempty"`
}

type InvalidParameter struct {
	Additional AdditionalMembers `json:"-"`
	In         string            `json:"in"`
	Name       string            `json:"name"`
	Reason     string            `json:"reason"`
}

type ProblemDetails struct {
	Additional    AdditionalMembers  `json:"-"`
	Code          string             `json:"code"`
	Detail        string             `json:"detail,omitempty"`
	Instance      string             `json:"instance,omitempty"`
	InvalidParams []InvalidParameter `json:"invalid_params,omitempty"`
	Status        int                `json:"status"`
	Title         string             `json:"title"`
	Type          string             `json:"type"`
}

type ResourceIdentity struct {
	ID      string       `json:"id"`
	Service string       `json:"service"`
	Type    ResourceType `json:"type"`
}

type CollectionSearchRequest struct {
	Additional AdditionalMembers `json:"-"`
	Limit      int               `json:"limit,omitempty"`
	ODPVersion string            `json:"odp_version"`
	ParentID   Optional[string]  `json:"parent_id,omitzero"`
	Query      string            `json:"query,omitempty"`
}

type OfferingSearchRequest struct {
	Additional         AdditionalMembers  `json:"-"`
	CollectionID       string             `json:"collection_id,omitempty"`
	Filters            []FilterExpression `json:"filters,omitempty"`
	IncludeDescendants bool               `json:"include_descendants,omitempty"`
	Limit              int                `json:"limit,omitempty"`
	ODPVersion         string             `json:"odp_version"`
	Query              string             `json:"query,omitempty"`
	Refinements        []string           `json:"refinements,omitempty"`
	Sort               string             `json:"sort,omitempty"`
}

type FilterExpression struct {
	Additional AdditionalMembers `json:"-"`
	ID         string            `json:"id"`
	Operator   FilterOperator    `json:"operator"`
	Value      json.RawMessage   `json:"value"`
}

type RefinementBucket struct {
	Additional    AdditionalMembers `json:"-"`
	Count         int               `json:"count"`
	CountRelation string            `json:"count_relation,omitempty"`
	Value         any               `json:"value"`
}

type RefinementGroup struct {
	Additional AdditionalMembers  `json:"-"`
	FilterID   string             `json:"filter_id"`
	Values     []RefinementBucket `json:"values"`
}

type Page[Item any] struct {
	Additional  AdditionalMembers `json:"-"`
	AuthExpands bool              `json:"auth_expands,omitempty"`
	Items       []Item            `json:"items"`
	Next        string            `json:"next,omitempty"`
	ODPVersion  string            `json:"odp_version"`
}

type OfferingPage[Item any] struct {
	Additional  AdditionalMembers `json:"-"`
	AuthExpands bool              `json:"auth_expands,omitempty"`
	Items       []Item            `json:"items"`
	Next        string            `json:"next,omitempty"`
	ODPVersion  string            `json:"odp_version"`
	Refinements []RefinementGroup `json:"refinements,omitempty"`
}
