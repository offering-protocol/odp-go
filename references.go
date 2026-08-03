package odp

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"

	"golang.org/x/net/idna"
)

var localIdentifier = regexp.MustCompile(`^[A-Za-z0-9._~-]{1,128}$`)

var operationMethods = map[Operation]string{
	OperationGetCollection:           "GET",
	OperationGetOffering:             "GET",
	OperationListCollectionOfferings: "GET",
	OperationListCollections:         "GET",
	OperationListOfferings:           "GET",
	OperationSearchCollections:       "POST",
	OperationSearchOfferings:         "POST",
}

func IsLocalResourceIdentifier(value string) bool {
	return value != "." && value != ".." && localIdentifier.MatchString(value)
}

func OperationMethod(operation Operation) (string, bool) {
	method, ok := operationMethods[operation]
	return method, ok
}

func DeriveServiceOrigin(serviceDocumentURL string) (string, error) {
	parsed, err := url.Parse(serviceDocumentURL)
	if err != nil {
		return "", fmt.Errorf("parse Service URL: %w", err)
	}
	if parsed.User != nil {
		return "", errors.New("Service URL cannot contain user information")
	}
	if err := requireSecureURL(parsed); err != nil {
		return "", err
	}
	return canonicalOrigin(parsed)
}

func ResolveResourceReference(reference, serviceOrigin string) (*url.URL, error) {
	if !strings.HasPrefix(reference, "/") && !strings.HasPrefix(reference, "https://") &&
		!strings.HasPrefix(reference, "http://localhost") &&
		!strings.HasPrefix(reference, "http://127.0.0.1") &&
		!strings.HasPrefix(reference, "http://[::1]") {
		return nil, errors.New("ODP resource reference must be an origin-relative absolute path or secure absolute URL")
	}
	if strings.HasPrefix(reference, "//") {
		return nil, errors.New("ODP resource reference cannot be scheme-relative")
	}
	origin, err := url.Parse(serviceOrigin)
	if err != nil {
		return nil, fmt.Errorf("parse Service origin: %w", err)
	}
	resolved, err := origin.Parse(reference)
	if err != nil {
		return nil, fmt.Errorf("parse ODP resource reference: %w", err)
	}
	if resolved.Fragment != "" {
		return nil, errors.New("ODP resource reference cannot contain a fragment")
	}
	if resolved.User != nil {
		return nil, errors.New("ODP resource reference cannot contain user information")
	}
	if err := requireSecureURL(resolved); err != nil {
		return nil, err
	}
	return resolved, nil
}

func ResolveContinuation(reference, serviceOrigin string) (*url.URL, error) {
	origin, err := url.Parse(serviceOrigin)
	if err != nil {
		return nil, fmt.Errorf("parse Service origin: %w", err)
	}
	resolved, err := ResolveResourceReference(reference, serviceOrigin)
	if err != nil {
		return nil, err
	}
	resolvedOrigin, err := canonicalOrigin(resolved)
	if err != nil {
		return nil, err
	}
	expectedOrigin, err := canonicalOrigin(origin)
	if err != nil {
		return nil, err
	}
	if resolvedOrigin != expectedOrigin {
		return nil, errors.New("ODP continuation reference must remain on the Service origin")
	}
	return resolved, nil
}

func canonicalOrigin(value *url.URL) (string, error) {
	host, err := idna.Lookup.ToASCII(value.Hostname())
	if err != nil {
		return "", fmt.Errorf("normalize ODP host: %w", err)
	}
	host = strings.ToLower(host)
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	port := value.Port()
	if (value.Scheme == "https" && port == "443") || (value.Scheme == "http" && port == "80") {
		port = ""
	}
	if port != "" {
		host += ":" + port
	}
	return value.Scheme + "://" + host, nil
}

func BuildOperationURL(endpointBase string, operation Operation, serviceOrigin, id string) (*url.URL, error) {
	if !strings.HasPrefix(endpointBase, "/") || strings.HasPrefix(endpointBase, "//") {
		return nil, errors.New("ODP endpoint base must be an origin-relative absolute path")
	}
	path, err := operationPath(operation, id)
	if err != nil {
		return nil, err
	}
	return ResolveResourceReference(strings.TrimSuffix(endpointBase, "/")+path, serviceOrigin)
}

func operationPath(operation Operation, id string) (string, error) {
	resource := operation == OperationGetCollection || operation == OperationGetOffering ||
		operation == OperationListCollectionOfferings
	if resource && !IsLocalResourceIdentifier(id) {
		return "", fmt.Errorf("%s requires a valid local resource identifier", operation)
	}
	if !resource && id != "" {
		return "", fmt.Errorf("%s does not accept a resource identifier", operation)
	}
	switch operation {
	case OperationListCollections:
		return "/collections", nil
	case OperationSearchCollections:
		return "/collections/search", nil
	case OperationGetCollection:
		return "/collections/" + id, nil
	case OperationListCollectionOfferings:
		return "/collections/" + id + "/offerings", nil
	case OperationListOfferings:
		return "/offerings", nil
	case OperationSearchOfferings:
		return "/offerings/search", nil
	case OperationGetOffering:
		return "/offerings/" + id, nil
	default:
		return "", fmt.Errorf("unsupported ODP operation %q", operation)
	}
}

func requireSecureURL(value *url.URL) error {
	host := value.Hostname()
	if host == "" {
		return errors.New("ODP URL must include a host")
	}
	loopback := host == "localhost" || net.ParseIP(host).IsLoopback()
	if value.Scheme != "https" && !(value.Scheme == "http" && loopback) {
		return errors.New("ODP URL must use HTTPS except on loopback hosts")
	}
	return nil
}
