package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	odp "github.com/offering-protocol/odp-go"
	"github.com/offering-protocol/odp-go/internal/jsonvalue"
)

const (
	maximumDocumentBytes = 65_536
	maximumProblemBytes  = 16_384
	maximumResourceBytes = 524_288
	mediaTypeODP         = "application/odp+json"
	mediaTypeProblem     = "application/problem+json"
)

type responseData struct {
	body      []byte
	finalURL  string
	freshness Freshness
	header    http.Header
	status    int
}

func request(ctx context.Context, client *http.Client, method, target string, body []byte, acceptLanguage string, maxRedirects int, maximumBytes int64, cache Cache, cacheKey string, fallback time.Duration, validate func([]byte) error) (responseData, error) {
	record, cached, err := cachedRecord(ctx, cache, cacheKey)
	if err != nil {
		return responseData{}, err
	}
	currentTarget := target
	if cached && record.FinalURL != "" {
		requestedOrigin, requestedErr := odp.DeriveServiceOrigin(target)
		finalOrigin, finalErr := odp.DeriveServiceOrigin(record.FinalURL)
		if requestedErr == nil && finalErr == nil && requestedOrigin == finalOrigin {
			currentTarget = record.FinalURL
		} else {
			_ = cache.Delete(ctx, cacheKey)
			cached = false
			record = CacheRecord{}
		}
	}
	if cached && time.Now().Before(record.ExpiresAt) {
		if validate != nil {
			if err := validate(record.Body); err != nil {
				_ = cache.Delete(ctx, cacheKey)
				return responseData{}, err
			}
		}
		return responseData{body: record.Body, finalURL: currentTarget, freshness: FreshnessFresh, status: record.Status}, nil
	}
	current, err := url.Parse(currentTarget)
	if err != nil {
		return responseData{}, err
	}
	origin, err := odp.DeriveServiceOrigin(target)
	if err != nil {
		return responseData{}, err
	}
	for redirects := 0; ; redirects++ {
		var input io.Reader
		if body != nil {
			input = bytes.NewReader(body)
		}
		req, err := http.NewRequestWithContext(ctx, method, current.String(), input)
		if err != nil {
			return responseData{}, err
		}
		req.Header.Set("Accept", mediaTypeODP)
		if acceptLanguage != "" {
			req.Header.Set("Accept-Language", acceptLanguage)
		}
		if body != nil {
			req.Header.Set("Content-Type", mediaTypeODP)
		}
		if cached {
			if record.ETag != "" {
				req.Header.Set("If-None-Match", record.ETag)
			}
			if record.LastModified != "" {
				req.Header.Set("If-Modified-Since", record.LastModified)
			}
		}
		response, err := client.Do(req)
		if err != nil {
			return responseData{}, err
		}
		if redirectStatus(response.StatusCode) {
			_ = response.Body.Close()
			if redirects >= maxRedirects {
				return responseData{}, errors.New("ODP response exceeded its redirect limit")
			}
			location := response.Header.Get("Location")
			if location == "" {
				return responseData{}, errors.New("ODP redirect omitted Location")
			}
			next, err := current.Parse(location)
			if err != nil {
				return responseData{}, fmt.Errorf("parse ODP redirect: %w", err)
			}
			nextOrigin, err := odp.DeriveServiceOrigin(next.String())
			if err != nil || nextOrigin != origin {
				return responseData{}, errors.New("ODP redirect changed Service origin")
			}
			if response.StatusCode == http.StatusSeeOther || ((response.StatusCode == http.StatusMovedPermanently || response.StatusCode == http.StatusFound) && method == http.MethodPost) {
				method = http.MethodGet
				body = nil
			}
			current = next
			continue
		}
		responseLimit := maximumBytes
		if response.StatusCode != http.StatusNotModified && (response.StatusCode < 200 || response.StatusCode > 299) {
			responseLimit = min(responseLimit, maximumProblemBytes)
		}
		result, err := consumeResponse(response, responseLimit)
		if err != nil {
			return responseData{}, err
		}
		result.finalURL = current.String()
		if result.status == http.StatusNotModified {
			if !cached {
				return responseData{}, errors.New("ODP response returned 304 without a cached representation")
			}
			if validate != nil {
				if err := validate(record.Body); err != nil {
					_ = cache.Delete(ctx, cacheKey)
					return responseData{}, err
				}
			}
			now := time.Now()
			if hasFreshnessDirective(response.Header) {
				record.ExpiresAt = expiry(response.Header, fallback, now)
			} else {
				lifetime := time.Duration(0)
				if !record.StoredAt.IsZero() {
					lifetime = record.ExpiresAt.Sub(record.StoredAt)
				}
				if lifetime < 0 {
					lifetime = 0
				}
				record.ExpiresAt = now.Add(lifetime)
			}
			if value := response.Header.Get("ETag"); value != "" {
				record.ETag = value
			}
			if value := response.Header.Get("Last-Modified"); value != "" {
				record.LastModified = value
			}
			record.FinalURL = current.String()
			record.StoredAt = now
			if _, noStore := cacheDirectives(response.Header.Get("Cache-Control"))["no-store"]; noStore {
				if err := cache.Delete(ctx, cacheKey); err != nil {
					return responseData{}, err
				}
				return responseData{body: record.Body, finalURL: record.FinalURL, freshness: FreshnessRevalidated, status: record.Status}, nil
			}
			if err := cache.Set(ctx, cacheKey, record); err != nil {
				return responseData{}, err
			}
			return responseData{body: record.Body, finalURL: record.FinalURL, freshness: FreshnessRevalidated, status: record.Status}, nil
		}
		if result.status < 200 || result.status > 299 {
			return responseData{}, responseError(result)
		}
		if validate != nil {
			if err := validate(result.body); err != nil {
				return responseData{}, err
			}
		}
		if cache != nil && cacheKey != "" {
			if cacheable(method, response.Header, fallback) {
				now := time.Now()
				record = CacheRecord{
					Body: result.body, ETag: response.Header.Get("ETag"), ExpiresAt: expiry(response.Header, fallback, now),
					FinalURL: current.String(), LastModified: response.Header.Get("Last-Modified"), Status: result.status, StoredAt: now,
				}
				if err := cache.Set(ctx, cacheKey, record); err != nil {
					return responseData{}, err
				}
			} else if err := cache.Delete(ctx, cacheKey); err != nil {
				return responseData{}, err
			}
		}
		result.freshness = FreshnessFetched
		return result, nil
	}
}

func cachedRecord(ctx context.Context, cache Cache, key string) (CacheRecord, bool, error) {
	if cache == nil || key == "" {
		return CacheRecord{}, false, nil
	}
	return cache.Get(ctx, key)
}

func consumeResponse(response *http.Response, limit int64) (responseData, error) {
	defer response.Body.Close()
	if response.ContentLength > limit {
		return responseData{}, errors.New("ODP response exceeds its byte limit")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return responseData{}, err
	}
	if int64(len(body)) > limit {
		return responseData{}, errors.New("ODP response exceeds its byte limit")
	}
	if !utf8.Valid(body) {
		return responseData{}, errors.New("ODP response must use UTF-8")
	}
	if response.StatusCode >= 200 && response.StatusCode <= 299 {
		mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
		if err != nil || mediaType != mediaTypeODP {
			return responseData{}, errors.New("ODP response has an unsupported media type")
		}
	}
	return responseData{body: body, header: response.Header.Clone(), status: response.StatusCode}, nil
}

func responseError(response responseData) error {
	var problem *odp.ProblemDetails
	if mediaType, _, _ := mime.ParseMediaType(response.header.Get("Content-Type")); mediaType == mediaTypeProblem && jsonvalue.Depth(response.body) <= maximumResourceDepth {
		filtered, err := odp.NormalizeAgentResponse(response.body, "problem")
		if err != nil {
			filtered = response.body
		}
		parsed, err := odp.ParseProblemResponse(filtered, response.status)
		if err == nil {
			problem = &parsed
		}
	}
	code := "HTTP_ERROR"
	if problem != nil {
		code = problem.Code
	}
	return &RequestError{
		Code: code, Header: response.header.Clone(), Problem: problem,
		Retryable: response.status == http.StatusTooManyRequests || response.status >= 500, Status: response.status,
	}
}

func expiry(header http.Header, fallback time.Duration, now time.Time) time.Time {
	control := cacheDirectives(header.Get("Cache-Control"))
	if _, found := control["no-store"]; found {
		return now
	}
	if _, found := control["no-cache"]; found {
		return now
	}
	if value, found := control["max-age"]; found {
		seconds, err := strconv.ParseInt(value, 10, 64)
		if err == nil && seconds >= 0 {
			age, ageErr := strconv.ParseInt(strings.TrimSpace(header.Get("Age")), 10, 64)
			if ageErr == nil && age > 0 {
				seconds = max(0, seconds-age)
			}
			return now.Add(time.Duration(seconds) * time.Second)
		}
	}
	if value, err := http.ParseTime(header.Get("Expires")); err == nil {
		return value
	}
	return now.Add(fallback)
}

func cacheDirectives(value string) map[string]string {
	result := make(map[string]string)
	for _, directive := range strings.Split(value, ",") {
		name, raw, found := strings.Cut(strings.TrimSpace(directive), "=")
		name = strings.ToLower(name)
		if name == "" {
			continue
		}
		if !found {
			result[name] = ""
			continue
		}
		result[name] = strings.Trim(strings.TrimSpace(raw), `"`)
	}
	return result
}

func hasFreshnessDirective(header http.Header) bool {
	control := cacheDirectives(header.Get("Cache-Control"))
	for _, name := range []string{"max-age", "no-cache", "no-store"} {
		if _, found := control[name]; found {
			return true
		}
	}
	return header.Get("Expires") != ""
}

func cacheable(method string, header http.Header, fallback time.Duration) bool {
	if fallback < 0 || (method != http.MethodGet && method != http.MethodPost) {
		return false
	}
	if !supportedVary(header.Values("Vary")) {
		return false
	}
	control := cacheDirectives(header.Get("Cache-Control"))
	if _, found := control["no-store"]; found {
		return false
	}
	_, noCache := control["no-cache"]
	return (method == http.MethodGet && (fallback > 0 || noCache)) || explicitFreshness(header)
}

func supportedVary(headers []string) bool {
	for _, header := range headers {
		for _, name := range strings.Split(header, ",") {
			switch strings.ToLower(strings.TrimSpace(name)) {
			case "", "accept", "accept-language", "content-type":
			default:
				return false
			}
		}
	}
	return true
}

func explicitFreshness(header http.Header) bool {
	_, maximumAge := cacheDirectives(header.Get("Cache-Control"))["max-age"]
	return maximumAge || header.Get("Expires") != ""
}

func cacheKey(partition, method, target, language string, body []byte) string {
	value := struct {
		BodyHash  string `json:"body_hash,omitempty"`
		Language  string `json:"language,omitempty"`
		Method    string `json:"method"`
		Partition string `json:"partition"`
		URL       string `json:"url"`
	}{Language: language, Method: method, Partition: partition, URL: target}
	if body != nil {
		digest := sha256.Sum256(body)
		value.BodyHash = fmt.Sprintf("%x", digest)
	}
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func redirectStatus(status int) bool {
	return status == http.StatusMovedPermanently || status == http.StatusFound || status == http.StatusSeeOther || status == http.StatusTemporaryRedirect || status == http.StatusPermanentRedirect
}
