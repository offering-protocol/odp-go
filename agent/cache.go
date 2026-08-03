package agent

import (
	"context"
	"sync"
)

type MemoryCache struct {
	mutex   sync.RWMutex
	records map[string]CacheRecord
}

func NewMemoryCache() *MemoryCache {
	return &MemoryCache{records: make(map[string]CacheRecord)}
}

func (cache *MemoryCache) Delete(_ context.Context, key string) error {
	cache.mutex.Lock()
	defer cache.mutex.Unlock()
	delete(cache.records, key)
	return nil
}

func (cache *MemoryCache) Get(_ context.Context, key string) (CacheRecord, bool, error) {
	cache.mutex.RLock()
	defer cache.mutex.RUnlock()
	record, ok := cache.records[key]
	if !ok {
		return CacheRecord{}, false, nil
	}
	record.Body = append([]byte(nil), record.Body...)
	return record, true, nil
}

func (cache *MemoryCache) Set(_ context.Context, key string, record CacheRecord) error {
	cache.mutex.Lock()
	defer cache.mutex.Unlock()
	record.Body = append([]byte(nil), record.Body...)
	cache.records[key] = record
	return nil
}
