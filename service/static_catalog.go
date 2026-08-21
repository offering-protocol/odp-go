package service

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	odp "github.com/offering-protocol/odp-go"
)

const (
	defaultPageLimit     = 50
	continuationLifetime = time.Hour
)

type StaticCatalogOptions struct {
	Collections []odp.Collection
	Offerings   []odp.Offering
}

type staticContinuation struct {
	ExpiresAt      int64  `json:"expires_at"`
	Language       string `json:"language"`
	Limit          int    `json:"limit"`
	Offset         int    `json:"offset"`
	Representation string `json:"representation"`
	Target         string `json:"target"`
}

func NewStaticCatalog(options StaticCatalogOptions) (Catalog, error) {
	offerings, err := cloneSlice(options.Offerings)
	if err != nil {
		return Catalog{}, err
	}
	collections, err := cloneSlice(options.Collections)
	if err != nil {
		return Catalog{}, err
	}
	offeringByID := make(map[string]odp.Offering, len(offerings))
	for index, offering := range offerings {
		validated, err := validateOffering(offering, odp.RepresentationFull)
		if err != nil {
			return Catalog{}, fmt.Errorf("Offering %d: %w", index, err)
		}
		if _, duplicate := offeringByID[validated.ID]; duplicate {
			return Catalog{}, errors.New("Offering identifiers must be unique")
		}
		offerings[index] = validated
		offeringByID[validated.ID] = validated
	}
	collectionByID := make(map[string]odp.Collection, len(collections))
	for index, collection := range collections {
		validated, err := validateCollection(collection, odp.RepresentationFull)
		if err != nil {
			return Catalog{}, fmt.Errorf("Collection %d: %w", index, err)
		}
		if _, duplicate := collectionByID[validated.ID]; duplicate {
			return Catalog{}, errors.New("Collection identifiers must be unique")
		}
		collections[index] = validated
		collectionByID[validated.ID] = validated
	}
	if err := validateRelationships(offerings, collections, collectionByID); err != nil {
		return Catalog{}, err
	}
	continuationKey := make([]byte, 32)
	if _, err := rand.Read(continuationKey); err != nil {
		return Catalog{}, fmt.Errorf("create continuation key: %w", err)
	}
	catalog := Catalog{
		ListOfferings: func(_ context.Context, request CatalogRequest) (odp.Page[odp.Offering], error) {
			return staticPage(offerings, request, terseOffering, continuationKey)
		},
		GetOffering: func(_ context.Context, id string, request CatalogRequest) (*odp.Offering, error) {
			value, ok := offeringByID[id]
			if !ok {
				return nil, nil
			}
			if request.Representation == odp.RepresentationTerse {
				value = terseOffering(value)
			}
			copy, err := clone(value)
			return &copy, err
		},
	}
	if len(collections) != 0 {
		catalog.ListCollections = func(_ context.Context, request CatalogRequest) (odp.Page[odp.Collection], error) {
			return staticPage(collections, request, terseCollection, continuationKey)
		}
		catalog.GetCollection = func(_ context.Context, id string, request CatalogRequest) (*odp.Collection, error) {
			value, ok := collectionByID[id]
			if !ok {
				return nil, nil
			}
			if request.Representation == odp.RepresentationTerse {
				value = terseCollection(value)
			}
			copy, err := clone(value)
			return &copy, err
		}
		catalog.ListCollectionOfferings = func(_ context.Context, id string, request CatalogRequest) (odp.Page[odp.Offering], error) {
			if _, ok := collectionByID[id]; !ok {
				return odp.Page[odp.Offering]{}, requestError(404, "NOT_FOUND", "Collection not found")
			}
			members := make([]odp.Offering, 0)
			for _, offering := range offerings {
				if contains(offering.CollectionIDs, id) {
					members = append(members, offering)
				}
			}
			return staticPage(members, request, terseOffering, continuationKey)
		}
	}
	return catalog, nil
}

func staticPage[Value any](values []Value, request CatalogRequest, terse func(Value) Value, key []byte) (odp.Page[Value], error) {
	limit := request.Limit
	if limit == 0 {
		limit = defaultPageLimit
	}
	offset, err := consumeCursor(request, limit, key)
	if err != nil {
		return odp.Page[Value]{}, err
	}
	end := min(offset+limit, len(values))
	items := make([]Value, 0, end-offset)
	for _, stored := range values[offset:end] {
		value := stored
		if request.Representation == odp.RepresentationTerse {
			value = terse(value)
		}
		copy, err := clone(value)
		if err != nil {
			return odp.Page[Value]{}, err
		}
		items = append(items, copy)
	}
	page := odp.Page[Value]{Items: items, ODPVersion: odp.Version}
	if end < len(values) {
		page.Next, err = continuation(end, request, limit, key)
		if err != nil {
			return odp.Page[Value]{}, err
		}
	}
	return page, nil
}

func continuation(offset int, request CatalogRequest, limit int, key []byte) (string, error) {
	state := staticContinuation{
		ExpiresAt: time.Now().Add(continuationLifetime).Unix(), Language: request.Language, Limit: limit, Offset: offset,
		Representation: string(request.Representation), Target: request.Request.URL.Path,
	}
	data, err := json.Marshal(state)
	if err != nil {
		return "", err
	}
	payload := base64.RawURLEncoding.EncodeToString(data)
	signature := sign(payload, key)
	query := url.Values{
		"cursor":         []string{payload + "." + signature},
		"limit":          []string{strconv.Itoa(limit)},
		"representation": []string{string(request.Representation)},
	}
	return request.Request.URL.Path + "?" + query.Encode(), nil
}

func consumeCursor(request CatalogRequest, limit int, key []byte) (int, error) {
	if request.Cursor == "" {
		return 0, nil
	}
	state, ok := decodeContinuation(request.Cursor, key)
	if !ok || state.ExpiresAt < time.Now().Unix() {
		return 0, requestError(410, "CONTINUATION_EXPIRED", "Continuation is unavailable")
	}
	if state.Language != request.Language || state.Limit != limit || state.Representation != string(request.Representation) || state.Target != request.Request.URL.Path {
		return 0, requestError(400, "INVALID_REQUEST", "Continuation context changed")
	}
	return state.Offset, nil
}

func decodeContinuation(cursor string, key []byte) (staticContinuation, bool) {
	parts := strings.Split(cursor, ".")
	if len(parts) != 2 {
		return staticContinuation{}, false
	}
	expected, err := base64.RawURLEncoding.DecodeString(sign(parts[0], key))
	if err != nil {
		return staticContinuation{}, false
	}
	actual, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || !hmac.Equal(actual, expected) {
		return staticContinuation{}, false
	}
	data, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return staticContinuation{}, false
	}
	var state staticContinuation
	if err := json.Unmarshal(data, &state); err != nil || state.ExpiresAt <= 0 || state.Limit < 1 || state.Offset < 0 || state.Target == "" {
		return staticContinuation{}, false
	}
	if state.Representation != string(odp.RepresentationTerse) && state.Representation != string(odp.RepresentationFull) {
		return staticContinuation{}, false
	}
	return state, true
}

func sign(payload string, key []byte) string {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func terseOffering(offering odp.Offering) odp.Offering {
	return odp.Offering{
		AuthExpands:   offering.AuthExpands,
		CollectionIDs: offering.CollectionIDs,
		Description:   offering.Description,
		ID:            offering.ID,
		Images:        primaryImage(offering.Images),
		Language:      offering.Language,
		Localizations: offering.Localizations,
		Name:          offering.Name,
		Price:         offering.Price,
		WebURL:        offering.WebURL,
	}
}

func terseCollection(collection odp.Collection) odp.Collection {
	return odp.Collection{
		AuthExpands:   collection.AuthExpands,
		Description:   collection.Description,
		ID:            collection.ID,
		Images:        primaryImage(collection.Images),
		Language:      collection.Language,
		Localizations: collection.Localizations,
		Name:          collection.Name,
		ParentIDs:     collection.ParentIDs,
		WebURL:        collection.WebURL,
	}
}

func primaryImage(images []odp.ResourceImage) []odp.ResourceImage {
	if len(images) == 0 {
		return nil
	}
	return images[:1]
}

func validateRelationships(offerings []odp.Offering, collections []odp.Collection, collectionByID map[string]odp.Collection) error {
	for _, offering := range offerings {
		for _, id := range offering.CollectionIDs {
			if _, ok := collectionByID[id]; !ok {
				return fmt.Errorf("Offering %s references unknown Collection %s", offering.ID, id)
			}
		}
	}
	depths := make(map[string]int, len(collections))
	visiting := make(map[string]bool, len(collections))
	var depth func(odp.Collection) (int, error)
	depth = func(collection odp.Collection) (int, error) {
		if known, ok := depths[collection.ID]; ok {
			return known, nil
		}
		if visiting[collection.ID] {
			return 0, errors.New("Collection hierarchy must be acyclic")
		}
		visiting[collection.ID] = true
		maximum := 0
		for _, id := range collection.ParentIDs {
			parent, ok := collectionByID[id]
			if !ok {
				return 0, fmt.Errorf("Collection %s references unknown parent %s", collection.ID, id)
			}
			parentDepth, err := depth(parent)
			if err != nil {
				return 0, err
			}
			maximum = max(maximum, parentDepth+1)
		}
		visiting[collection.ID] = false
		if maximum > 32 {
			return 0, errors.New("Collection hierarchy exceeds 32 edges")
		}
		depths[collection.ID] = maximum
		return maximum, nil
	}
	for _, collection := range collections {
		if _, err := depth(collection); err != nil {
			return err
		}
	}
	return nil
}

func clone[Value any](value Value) (Value, error) {
	var copy Value
	data, err := json.Marshal(value)
	if err != nil {
		return copy, err
	}
	if err := json.Unmarshal(data, &copy); err != nil {
		return copy, err
	}
	return copy, nil
}

func cloneSlice[Value any](values []Value) ([]Value, error) {
	return clone(values)
}

func contains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
