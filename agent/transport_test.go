package agent

import (
	"strings"
	"testing"
)

func TestCacheKeyHashesRequestBody(t *testing.T) {
	body := []byte(`{"query":"private catalog search"}`)
	key := cacheKey("principal-1", "POST", "https://service.example/odp/offerings/search", "en", body)
	if strings.Contains(key, "private catalog search") || !strings.Contains(key, "body_hash") {
		t.Fatalf("cache key = %q", key)
	}
	other := cacheKey("principal-2", "POST", "https://service.example/odp/offerings/search", "en", body)
	if key == other {
		t.Fatal("cache partitions produced the same key")
	}
}
