package service_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	odp "github.com/offering-protocol/odp-go"
	"github.com/offering-protocol/odp-go/service"
)

func newService(t *testing.T, catalog service.Catalog) *service.Service {
	t.Helper()
	runtime, err := service.New(service.Options{
		Catalog: catalog,
		Document: odp.ServiceDocument{
			Description:   "Example catalog",
			HTTP:          odp.HTTPConfiguration{EndpointBase: "/odp"},
			Language:      "en",
			Localizations: []string{"en"},
			Name:          "Example",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return runtime
}

func staticService(t *testing.T) *service.Service {
	t.Helper()
	catalog, err := service.NewStaticCatalog(service.StaticCatalogOptions{
		Collections: []odp.Collection{{ID: "compute", Name: "Compute", ODPVersion: odp.Version}},
		Offerings: []odp.Offering{
			{
				Actions:       []odp.Action{{HTTP: &odp.HTTPActionTarget{Href: "/rent", Method: http.MethodPost}, ID: "rent", Rel: odp.ActionPurchase}},
				Attributes:    map[string]json.RawMessage{"memory": json.RawMessage("80")},
				CollectionIDs: []string{"compute"},
				Description:   "Dedicated accelerator",
				ID:            "gpu-h100",
				Name:          "H100 GPU",
				ODPVersion:    odp.Version,
				Schema:        &odp.SchemaReference{URL: "/schemas/gpu.json"},
			},
			{ID: "storage", Name: "Storage", ODPVersion: odp.Version},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return newService(t, catalog)
}

func request(t *testing.T, handler http.Handler, method, target string, body io.Reader, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, body)
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	return response
}

func decodeObject(t *testing.T, response *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func TestServiceDocumentAndRepresentationDefaults(t *testing.T) {
	runtime := staticService(t)
	document := runtime.Document()
	wantOperations := []odp.Operation{
		odp.OperationGetCollection,
		odp.OperationGetOffering,
		odp.OperationListCollectionOfferings,
		odp.OperationListCollections,
		odp.OperationListOfferings,
	}
	if strings.Join(operations(document), ",") != strings.Join(operationStrings(wantOperations), ",") {
		t.Fatalf("operations = %v, want %v", document.Operations.Supported, wantOperations)
	}
	wellKnown := request(t, runtime, http.MethodGet, "https://service.example/.well-known/odp", nil, nil)
	if wellKnown.Code != http.StatusOK || wellKnown.Header().Get("Content-Type") != service.MediaType {
		t.Fatalf("well-known response = %d %q", wellKnown.Code, wellKnown.Header().Get("Content-Type"))
	}

	list := request(t, runtime, http.MethodGet, "https://service.example/odp/offerings", nil, nil)
	items := decodeObject(t, list)["items"].([]any)
	first := items[0].(map[string]any)
	if _, exists := first["actions"]; exists {
		t.Fatal("terse Offering contains actions")
	}
	if _, exists := first["attributes"]; exists {
		t.Fatal("terse Offering contains attributes")
	}

	detail := request(t, runtime, http.MethodGet, "https://service.example/odp/offerings/gpu-h100", nil, nil)
	offering := decodeObject(t, detail)
	if _, exists := offering["actions"]; !exists {
		t.Fatal("full Offering omits actions")
	}
	if _, exists := offering["attributes"]; !exists {
		t.Fatal("full Offering omits attributes")
	}
}

func TestStaticCatalogContinuation(t *testing.T) {
	runtime := staticService(t)
	first := request(t, runtime, http.MethodGet, "https://service.example/odp/offerings?limit=1", nil, nil)
	next := decodeObject(t, first)["next"].(string)
	parsed, err := url.Parse(next)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Path != "/odp/offerings" || parsed.Query().Get("cursor") == "" {
		t.Fatalf("next = %q", next)
	}
	second := request(t, runtime, http.MethodGet, "https://service.example"+next, nil, nil)
	item := decodeObject(t, second)["items"].([]any)[0].(map[string]any)
	if item["id"] != "storage" {
		t.Fatalf("second page id = %v", item["id"])
	}
	changedLanguage := request(t, runtime, http.MethodGet, "https://service.example"+next, nil, map[string]string{"Accept-Language": "ja"})
	if changedLanguage.Code != http.StatusBadRequest {
		t.Fatalf("changed-language continuation status = %d", changedLanguage.Code)
	}
	query := parsed.Query()
	query.Set("cursor", "x"+query.Get("cursor"))
	parsed.RawQuery = query.Encode()
	tampered := request(t, runtime, http.MethodGet, "https://service.example"+parsed.String(), nil, nil)
	if tampered.Code != http.StatusGone {
		t.Fatalf("tampered continuation status = %d", tampered.Code)
	}
}

func TestCollectionOperations(t *testing.T) {
	runtime := staticService(t)
	collection := request(t, runtime, http.MethodGet, "https://service.example/odp/collections/compute", nil, nil)
	if got := decodeObject(t, collection)["id"]; got != "compute" {
		t.Fatalf("Collection id = %v", got)
	}
	members := request(t, runtime, http.MethodGet, "https://service.example/odp/collections/compute/offerings", nil, nil)
	items := decodeObject(t, members)["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["id"] != "gpu-h100" {
		t.Fatalf("Collection members = %v", items)
	}
}

func TestSearchRequestBodyLimit(t *testing.T) {
	catalog := service.Catalog{
		ListOfferings: func(context.Context, service.CatalogRequest) (odp.Page[odp.Offering], error) {
			return odp.Page[odp.Offering]{}, nil
		},
		GetOffering: func(context.Context, string, service.CatalogRequest) (*odp.Offering, error) {
			return nil, nil
		},
		SearchOfferings: func(context.Context, *odp.OfferingSearchRequest, service.CatalogRequest) (odp.OfferingPage[odp.Offering], error) {
			return odp.OfferingPage[odp.Offering]{}, nil
		},
	}
	runtime := newService(t, catalog)
	base := `{"odp_version":"1.0","query":"gpu"}`
	exact := base + strings.Repeat(" ", service.MaximumRequestBodyBytes-len(base))
	headers := map[string]string{"Content-Type": service.MediaType}
	valid := request(t, runtime, http.MethodPost, "https://service.example/odp/offerings/search", strings.NewReader(exact), headers)
	if valid.Code != http.StatusOK {
		t.Fatalf("boundary status = %d; body = %s", valid.Code, valid.Body.String())
	}
	over := request(t, runtime, http.MethodPost, "https://service.example/odp/offerings/search", strings.NewReader(exact+" "), headers)
	if over.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("over-limit status = %d", over.Code)
	}
}

func TestSearchRejectsInvalidUTF8AndExcessiveDepth(t *testing.T) {
	catalog := service.Catalog{
		ListOfferings: func(context.Context, service.CatalogRequest) (odp.Page[odp.Offering], error) {
			return odp.Page[odp.Offering]{}, nil
		},
		GetOffering: func(context.Context, string, service.CatalogRequest) (*odp.Offering, error) {
			return nil, nil
		},
		SearchOfferings: func(context.Context, *odp.OfferingSearchRequest, service.CatalogRequest) (odp.OfferingPage[odp.Offering], error) {
			return odp.OfferingPage[odp.Offering]{}, nil
		},
	}
	runtime := newService(t, catalog)
	headers := map[string]string{"Content-Type": service.MediaType}
	invalidUTF8 := request(t, runtime, http.MethodPost, "https://service.example/odp/offerings/search", strings.NewReader("{\"query\":\"\xff\"}"), headers)
	if invalidUTF8.Code != http.StatusBadRequest {
		t.Fatalf("invalid UTF-8 status = %d", invalidUTF8.Code)
	}
	deep := strings.Repeat("[", 17) + "0" + strings.Repeat("]", 17)
	excessiveDepth := request(t, runtime, http.MethodPost, "https://service.example/odp/offerings/search", strings.NewReader(deep), headers)
	if excessiveDepth.Code != http.StatusBadRequest {
		t.Fatalf("excessive-depth status = %d", excessiveDepth.Code)
	}
}

func TestNegotiationAndProblemDetails(t *testing.T) {
	runtime := staticService(t)
	unacceptable := request(t, runtime, http.MethodGet, "https://service.example/odp/offerings", nil, map[string]string{
		"Accept": service.MediaType + ";q=0, */*;q=1",
	})
	if unacceptable.Code != http.StatusNotAcceptable {
		t.Fatalf("unacceptable status = %d", unacceptable.Code)
	}
	method := request(t, runtime, http.MethodPost, "https://service.example/odp/offerings", nil, nil)
	if method.Code != http.StatusMethodNotAllowed || method.Header().Get("Allow") != http.MethodGet {
		t.Fatalf("method response = %d, Allow %q", method.Code, method.Header().Get("Allow"))
	}
	problem := decodeObject(t, method)
	if problem["code"] != "METHOD_NOT_ALLOWED" || problem["status"] != float64(http.StatusMethodNotAllowed) {
		t.Fatalf("problem = %v", problem)
	}
}

func TestUnexpectedCatalogFailureIsNotExposed(t *testing.T) {
	runtime := newService(t, service.Catalog{
		ListOfferings: func(context.Context, service.CatalogRequest) (odp.Page[odp.Offering], error) {
			return odp.Page[odp.Offering]{}, errors.New("database password leaked")
		},
		GetOffering: func(context.Context, string, service.CatalogRequest) (*odp.Offering, error) { return nil, nil },
	})
	response := request(t, runtime, http.MethodGet, "https://service.example/odp/offerings", nil, nil)
	if response.Code != http.StatusInternalServerError || strings.Contains(response.Body.String(), "password") {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
}

func TestStaticCatalogRejectsInvalidRelationships(t *testing.T) {
	_, err := service.NewStaticCatalog(service.StaticCatalogOptions{
		Collections: []odp.Collection{
			{ID: "a", Name: "A", ODPVersion: odp.Version, ParentIDs: []string{"b"}},
			{ID: "b", Name: "B", ODPVersion: odp.Version, ParentIDs: []string{"a"}},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "acyclic") {
		t.Fatalf("cycle error = %v", err)
	}
	_, err = service.NewStaticCatalog(service.StaticCatalogOptions{
		Offerings: []odp.Offering{{CollectionIDs: []string{"missing"}, ID: "orphan", Name: "Orphan", ODPVersion: odp.Version}},
	})
	if err == nil || !strings.Contains(err.Error(), "unknown Collection") {
		t.Fatalf("membership error = %v", err)
	}
}

func operations(document odp.ServiceDocument) []string {
	return operationStrings(document.Operations.Supported)
}

func operationStrings(values []odp.Operation) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = string(value)
	}
	return result
}
