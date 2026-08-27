package agent

import (
	"encoding/json"
	"strings"
	"testing"

	odp "github.com/offering-protocol/odp-go"
)

func TestParseOfferingPageValidatesItemsAndRepresentation(t *testing.T) {
	if _, err := parseOfferingPage([]byte(`{"odp_version":"1.0","items":[{"id":"gpu"}]}`), false, odp.RepresentationTerse); err == nil {
		t.Fatal("invalid Offering item passed validation")
	}
	if _, err := parseOfferingPage([]byte(`{"odp_version":"1.0","items":[{"detail_fields":["memory"],"id":"gpu","name":"GPU"}]}`), false, odp.RepresentationFull); err == nil || !strings.Contains(err.Error(), "detail_fields") {
		t.Fatalf("full representation error = %v", err)
	}
	items := make([]odp.Offering, 101)
	for index := range items {
		items[index] = odp.Offering{ID: "gpu", Name: "GPU"}
	}
	data, err := json.Marshal(odp.Page[odp.Offering]{Items: items, ODPVersion: odp.Version})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parseOfferingPage(data, false, odp.RepresentationTerse); err == nil {
		t.Fatalf("page-size error = %v", err)
	}
}

func TestParseCollectionPageValidatesItemsAndRepresentation(t *testing.T) {
	if _, err := parseCollectionPage([]byte(`{"odp_version":"1.0","items":[{"id":"compute"}]}`), odp.RepresentationTerse); err == nil {
		t.Fatal("invalid Collection item passed validation")
	}
	if _, err := parseCollectionPage([]byte(`{"odp_version":"1.0","items":[{"detail_fields":["region"],"id":"compute","name":"Compute"}]}`), odp.RepresentationFull); err == nil || !strings.Contains(err.Error(), "detail_fields") {
		t.Fatalf("full representation error = %v", err)
	}
}
