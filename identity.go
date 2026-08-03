package odp

import (
	"errors"
	"strings"
)

func NewResourceIdentity(serviceDocumentURL string, resourceType ResourceType, id string) (ResourceIdentity, error) {
	if !IsLocalResourceIdentifier(id) {
		return ResourceIdentity{}, errors.New("invalid ODP local resource identifier")
	}
	if resourceType != ResourceCollection && resourceType != ResourceOffering {
		return ResourceIdentity{}, errors.New("invalid ODP resource type")
	}
	origin, err := DeriveServiceOrigin(serviceDocumentURL)
	if err != nil {
		return ResourceIdentity{}, err
	}
	return ResourceIdentity{ID: id, Service: origin, Type: resourceType}, nil
}

func (identity ResourceIdentity) Key() string {
	return strings.Join([]string{identity.Service, string(identity.Type), identity.ID}, "\x00")
}

func (identity ResourceIdentity) Equal(other ResourceIdentity) bool {
	return identity == other
}
