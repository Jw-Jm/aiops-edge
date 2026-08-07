package api

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

var jwtSecret = func() []byte {
	if s := os.Getenv("JWT_SECRET"); s != "" { return []byte(s) }
	return []byte("observability-platform-secret")
}()

func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" { http.Error(w, "method not allowed", 405); return }
	body, _ := io.ReadAll(r.Body)
	var creds struct{ Username, Password string }
	json.Unmarshal(body, &creds)
	if creds.Username != "admin" || creds.Password != "admin123" {
		respondJSON(w, 401, map[string]interface{}{"error": "invalid credentials"})
		return
	}
	token := generateJWT(creds.Username)
	respondJSON(w, 200, map[string]interface{}{"token": token, "username": creds.Username})
}

func generateJWT(username string) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	payload := map[string]interface{}{"username": username, "exp": time.Now().Add(24 * time.Hour).Unix()}
	payloadBytes, _ := json.Marshal(payload)
	payloadStr := base64.RawURLEncoding.EncodeToString(payloadBytes)
	sig := base64.RawURLEncoding.EncodeToString(append(jwtSecret, payloadBytes...))
	return header + "." + payloadStr + "." + sig[:32]
}

func validateJWT(tokenStr string) (string, bool) {
	parts := strings.Split(tokenStr, ".")
	if len(parts) != 3 { return "", false }
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil { return "", false }
	var claims struct{ Username string; Exp float64 }
	if json.Unmarshal(payloadBytes, &claims) != nil { return "", false }
	if time.Now().Unix() > int64(claims.Exp) { return "", false }
	return claims.Username, true
}

// isInternalRequest checks if request comes from trusted internal service
func isInternalRequest(r *http.Request) bool {
	// Trust requests with X-Internal-Token header (shared secret between services)
	if r.Header.Get("X-Internal-Token") == os.Getenv("INTERNAL_TOKEN") {
		return true
	}
	// Trust localhost requests (port-forward / LoadBalancer from Docker Desktop)
	addr := r.RemoteAddr
	if strings.HasPrefix(addr, "127.0.0.1") || strings.HasPrefix(addr, "::1") || strings.HasPrefix(addr, "localhost") {
		return true
	}
	// Trust Docker bridge network (LoadBalancer NAT from host)
	if strings.HasPrefix(addr, "192.168.") || strings.HasPrefix(addr, "10.") || strings.HasPrefix(addr, "172.1") {
		return true
	}
	// Trust Vite proxy (X-Forwarded-For: localhost)
	fwd := r.Header.Get("X-Forwarded-For")
	if fwd != "" && (strings.Contains(fwd, "127.0.0.1") || strings.Contains(fwd, "::1")) {
		return true
	}
	return false
}

func AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		// Public endpoints: health, login, OPTIONS, settings setup
		if path == "/health" || path == "/api/v1/health" ||
			path == "/api/v1/auth/login" ||
			strings.HasPrefix(path, "/api/v1/settings/llm") ||
			r.Method == "OPTIONS" {
			next.ServeHTTP(w, r)
			return
		}

		// Trust internal requests (from ai-orchestrator, port-forward, LoadBalancer local traffic)
		if isInternalRequest(r) {
			next.ServeHTTP(w, r)
			return
		}

		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if _, ok := validateJWT(token); !ok {
			respondJSON(w, 401, map[string]interface{}{"error": "unauthorized"})
			return
		}
		next.ServeHTTP(w, r)
	})
}
