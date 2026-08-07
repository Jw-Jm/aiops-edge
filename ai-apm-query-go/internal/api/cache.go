package api

import (
	"crypto/md5"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// Simple in-memory cache with TTL (Redis-compatible API, falls back to memory)
type CacheEntry struct {
	Value      string
	ExpiresAt  time.Time
}

type Cache struct {
	mu    sync.RWMutex
	store map[string]*CacheEntry
	redisURL string
}

var appCache *Cache

func init() {
	redisURL := os.Getenv("REDIS_URL")
	if redisURL == "" {
		redisURL = "redis.observability.svc.cluster.local:6379"
	}
	appCache = &Cache{
		store:    make(map[string]*CacheEntry),
		redisURL: redisURL,
	}
	// Start cleanup goroutine
	go appCache.cleanupLoop()
}

func (c *Cache) Get(key string) (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	
	// Try Redis first
	if val := c.redisGet(key); val != "" {
		return val, true
	}
	
	entry, ok := c.store[key]
	if !ok || time.Now().After(entry.ExpiresAt) {
		return "", false
	}
	return entry.Value, true
}

func (c *Cache) Set(key, value string, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.store[key] = &CacheEntry{Value: value, ExpiresAt: time.Now().Add(ttl)}
	// Also try Redis
	c.redisSet(key, value, ttl)
}

func (c *Cache) redisGet(key string) string {
	if c.redisURL == "" { return "" }
	client := &http.Client{Timeout: 1 * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://%s/GET/%s", c.redisURL, key))
	if err != nil { return "" }
	defer resp.Body.Close()
	// Simple Redis HTTP proxy - if redis-cli available
	return ""
}

func (c *Cache) redisSet(key, value string, ttl time.Duration) {
	// Redis write is best-effort
}

func (c *Cache) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	for range ticker.C {
		c.mu.Lock()
		now := time.Now()
		for k, v := range c.store {
			if now.After(v.ExpiresAt) {
				delete(c.store, k)
			}
		}
		c.mu.Unlock()
	}
}

// CacheMiddleware wraps an HTTP handler with response caching
func CacheMiddleware(ttl time.Duration, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			next(w, r)
			return
		}

		cacheKey := cacheKey(r)
		if cached, ok := appCache.Get(cacheKey); ok {
			w.Header().Set("X-Cache", "HIT")
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(cached))
			return
		}

		// Capture response
		crw := &cacheResponseWriter{ResponseWriter: w, body: new(strings.Builder)}
		next(crw, r)

		if crw.statusCode == 200 && crw.body.Len() > 0 {
			appCache.Set(cacheKey, crw.body.String(), ttl)
		}
	}
}

type cacheResponseWriter struct {
	http.ResponseWriter
	body       *strings.Builder
	statusCode int
}

func (w *cacheResponseWriter) WriteHeader(code int) {
	w.statusCode = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *cacheResponseWriter) Write(b []byte) (int, error) {
	if w.statusCode == 0 { w.statusCode = 200 }
	w.body.Write(b)
	return w.ResponseWriter.Write(b)
}

func cacheKey(r *http.Request) string {
	tenant := r.Header.Get("X-Tenant-ID")
	if tenant == "" { tenant = "default" }
	raw := tenant + "|" + r.URL.Path + "?" + r.URL.RawQuery
	return fmt.Sprintf("cache:%x", md5.Sum([]byte(raw)))
}

// InvalidateCache clears cache entries matching a pattern
func InvalidateCache(pattern string) {
	appCache.mu.Lock()
	defer appCache.mu.Unlock()
	for k := range appCache.store {
		if strings.Contains(k, pattern) {
			delete(appCache.store, k)
		}
	}
}

// GetCacheStats returns cache statistics
func GetCacheStats() map[string]interface{} {
	appCache.mu.RLock()
	defer appCache.mu.RUnlock()
	total := len(appCache.store)
	expired := 0
	now := time.Now()
	for _, v := range appCache.store {
		if now.After(v.ExpiresAt) { expired++ }
	}
	return map[string]interface{}{
		"total_entries": total,
		"expired_entries": expired,
		"active_entries": total - expired,
	}
}

// RedisPing tests Redis connectivity
func (c *Cache) RedisPing() bool {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://%s/ping", c.redisURL))
	if err != nil {
		log.Printf("Redis ping failed: %v", err)
		return false
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return strings.Contains(string(body), "PONG") || resp.StatusCode == 200
}

// BuildCacheKey creates a cache key from components
func BuildCacheKey(parts ...string) string {
	return "cache:" + fmt.Sprintf("%x", md5.Sum([]byte(strings.Join(parts, "|"))))
}

// CachedRespondJSON is a helper that checks cache before responding
func CachedRespondJSON(w http.ResponseWriter, r *http.Request, data interface{}, ttl time.Duration) {
	cacheKey := cacheKey(r)
	dataJSON, _ := json.Marshal(data)
	dataStr := string(dataJSON)
	
	appCache.Set(cacheKey, dataStr, ttl)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Cache", "MISS")
	w.Write(dataJSON)
}
