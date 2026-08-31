package bootstrap

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
)

// internalMTLS wraps privileged service routes.  The public browser API may
// use the same listener, therefore client certificates are requested at the
// TLS handshake and enforced only for /internal/* paths.
func internalMTLS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/internal/") {
			if r.TLS == nil || len(r.TLS.VerifiedChains) == 0 {
				http.Error(w, "client certificate required", http.StatusUnauthorized)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func configureMTLSServer(s *http.Server) error {
	certFile := strings.TrimSpace(os.Getenv("AIOPS_TLS_CERT_FILE"))
	keyFile := strings.TrimSpace(os.Getenv("AIOPS_TLS_KEY_FILE"))
	caFile := strings.TrimSpace(os.Getenv("AIOPS_TLS_CLIENT_CA_FILE"))
	required := strings.EqualFold(strings.TrimSpace(os.Getenv("AIOPS_MTLS_REQUIRED")), "true")
	if certFile == "" || keyFile == "" || caFile == "" {
		if required {
			return errors.New("AIOPS_MTLS_REQUIRED=true requires AIOPS_TLS_CERT_FILE, AIOPS_TLS_KEY_FILE and AIOPS_TLS_CLIENT_CA_FILE")
		}
		return nil
	}
	if required && strings.TrimSpace(os.Getenv("AIOPS_TLS_CLIENT_SAN")) == "" {
		return errors.New("AIOPS_MTLS_REQUIRED=true requires AIOPS_TLS_CLIENT_SAN")
	}
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return fmt.Errorf("load mTLS server certificate: %w", err)
	}
	caPEM, err := os.ReadFile(caFile)
	if err != nil {
		return fmt.Errorf("read mTLS client CA: %w", err)
	}
	clientCAs := x509.NewCertPool()
	if !clientCAs.AppendCertsFromPEM(caPEM) {
		return errors.New("mTLS client CA contains no certificate")
	}
	s.TLSConfig = &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{cert},
		ClientCAs:    clientCAs,
		// Public browser routes share this listener.  Internal middleware above
		// requires a verified chain for privileged paths.
		ClientAuth: tls.VerifyClientCertIfGiven,
		VerifyConnection: func(state tls.ConnectionState) error {
			return verifyClientSAN(state)
		},
	}
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

func listenHTTP(s *http.Server) error {
	if s.TLSConfig == nil {
		return s.ListenAndServe()
	}
	return s.ListenAndServeTLS("", "")
}
