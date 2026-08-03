package odp_test

import (
	"encoding/json"
	"os"
	"testing"

	odp "github.com/offering-protocol/odp-go"
)

func readVector[Case any](t *testing.T, name string) []Case {
	t.Helper()
	data, err := os.ReadFile("testdata/vectors/" + name)
	if err != nil {
		t.Fatal(err)
	}
	var vector struct {
		Cases []Case `json:"cases"`
	}
	if err := json.Unmarshal(data, &vector); err != nil {
		t.Fatal(err)
	}
	return vector.Cases
}

func TestLocalIdentifierVectors(t *testing.T) {
	type testCase struct {
		Name  string `json:"name"`
		Valid bool   `json:"valid"`
		Value string `json:"value"`
	}
	for _, test := range readVector[testCase](t, "identity-local-identifier.json") {
		t.Run(test.Name, func(t *testing.T) {
			if got := odp.IsLocalResourceIdentifier(test.Value); got != test.Valid {
				t.Fatalf("IsLocalResourceIdentifier() = %v, want %v", got, test.Valid)
			}
		})
	}
}

func TestIdentityVectors(t *testing.T) {
	type testCase struct {
		Left         odp.ResourceIdentity `json:"left"`
		Name         string               `json:"name"`
		Right        odp.ResourceIdentity `json:"right"`
		SameIdentity bool                 `json:"same_identity"`
	}
	for _, test := range readVector[testCase](t, "identity-comparison.json") {
		t.Run(test.Name, func(t *testing.T) {
			if got := test.Left.Equal(test.Right); got != test.SameIdentity {
				t.Fatalf("Equal() = %v, want %v", got, test.SameIdentity)
			}
		})
	}
}

func TestServiceDocumentVectors(t *testing.T) {
	type testCase struct {
		Document json.RawMessage `json:"document"`
		Name     string          `json:"name"`
		Valid    bool            `json:"valid"`
	}
	for _, test := range readVector[testCase](t, "service-document-validation.json") {
		t.Run(test.Name, func(t *testing.T) {
			_, err := odp.ParseServiceDocument(test.Document)
			if got := err == nil; got != test.Valid {
				t.Fatalf("ParseServiceDocument() valid = %v, want %v; error = %v", got, test.Valid, err)
			}
		})
	}
}

func TestPageVectors(t *testing.T) {
	type testCase struct {
		Name      string          `json:"name"`
		Operation string          `json:"operation"`
		Page      json.RawMessage `json:"page"`
		Valid     bool            `json:"valid"`
	}
	for _, test := range readVector[testCase](t, "pagination-contract.json") {
		if test.Operation != "validate-page" {
			continue
		}
		t.Run(test.Name, func(t *testing.T) {
			_, err := odp.ParsePage[json.RawMessage](test.Page)
			if got := err == nil; got != test.Valid {
				t.Fatalf("ParsePage() valid = %v, want %v; error = %v", got, test.Valid, err)
			}
		})
	}
}

func TestProblemVectors(t *testing.T) {
	type testCase struct {
		HTTPStatus int             `json:"http_status"`
		Name       string          `json:"name"`
		Operation  string          `json:"operation"`
		Problem    json.RawMessage `json:"problem"`
		Valid      bool            `json:"valid"`
	}
	for _, test := range readVector[testCase](t, "errors-limits-contract.json") {
		if test.Operation != "validate-problem" {
			continue
		}
		t.Run(test.Name, func(t *testing.T) {
			_, err := odp.ParseProblemResponse(test.Problem, test.HTTPStatus)
			if got := err == nil; got != test.Valid {
				t.Fatalf("ParseProblemResponse() valid = %v, want %v; error = %v", got, test.Valid, err)
			}
		})
	}
}
