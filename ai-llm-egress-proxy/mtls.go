package main

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
)

func configureMTLSServer(s *http.Server) error {
	certFile, keyFile, caFile := strings.TrimSpace(os.Getenv("AIOPS_TLS_CERT_FILE")), strings.TrimSpace(os.Getenv("AIOPS_TLS_KEY_FILE")), strings.TrimSpace(os.Getenv("AIOPS_TLS_CLIENT_CA_FILE"))
	required := strings.EqualFold(strings.TrimSpace(os.Getenv("AIOPS_MTLS_REQUIRED")), "true")
	if certFile == "" || keyFile == "" || caFile == "" {
		if required {
			return errors.New("mTLS is required but certificate/key/client CA are not configured")
		}
		return nil
	}
	if required && strings.TrimSpace(os.Getenv("AIOPS_TLS_CLIENT_SAN")) == "" {
		return errors.New("AIOPS_MTLS_REQUIRED=true requires AIOPS_TLS_CLIENT_SAN")
	}
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return fmt.Errorf("load mTLS certificate: %w", err)
	}
	pem, err := os.ReadFile(caFile)
	if err != nil {
		return fmt.Errorf("read mTLS client CA: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return errors.New("mTLS client CA contains no certificate")
	}
	s.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{cert}, ClientAuth: tls.VerifyClientCertIfGiven, ClientCAs: pool, VerifyConnection: verifyClientSAN}
	return nil
}

func verifyClientSAN(state tls.ConnectionState) error {
	expected := strings.TrimSpace(os.Getenv("AIOPS_TLS_CLIENT_SAN"))
	if expected == "" || len(state.PeerCertificates) == 0 {
		return nil
	}
	leaf := state.PeerCertificates[0]
	for _, allowed := range strings.Split(expected, ",") {
		allowed = strings.TrimSpace(allowed)
		for _, name := range leaf.DNSNames {
			if allowed != "" && name == allowed {
				return nil
			}
		}
		for _, uri := range leaf.URIs {
			if allowed != "" && uri.String() == allowed {
				return nil
			}
		}
	}
	return fmt.Errorf("client certificate SAN %q is not allowed", expected)
}

func requireMTLS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/v1/") && (r.TLS == nil || len(r.TLS.VerifiedChains) == 0) {
			http.Error(w, "client certificate required", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func listenHTTP(s *http.Server) error {
	if s.TLSConfig == nil {
		return s.ListenAndServe()
	}
	return s.ListenAndServeTLS("", "")
}
