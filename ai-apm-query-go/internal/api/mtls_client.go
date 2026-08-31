package api

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// newInternalServiceClient creates the client used for Query API → internal
// service calls. In production, missing mTLS material is an explicit error;
// tests and local profiles may omit it and use the normal transport.
func newInternalServiceClient(timeout time.Duration) (*http.Client, error) {
	certFile := strings.TrimSpace(os.Getenv("AIOPS_TLS_CERT_FILE"))
	keyFile := strings.TrimSpace(os.Getenv("AIOPS_TLS_KEY_FILE"))
	caFile := strings.TrimSpace(os.Getenv("AIOPS_TLS_CLIENT_CA_FILE"))
	required := strings.EqualFold(strings.TrimSpace(os.Getenv("AIOPS_MTLS_REQUIRED")), "true")
	if certFile == "" || keyFile == "" || caFile == "" {
		if required {
			return nil, errors.New("mTLS is required but client certificate/key/CA are not configured")
		}
		return &http.Client{Timeout: timeout}, nil
	}
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("load mTLS client certificate: %w", err)
	}
	pem, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("read mTLS client CA: %w", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(pem) {
		return nil, errors.New("mTLS client CA contains no certificate")
	}
	return &http.Client{Timeout: timeout, Transport: &http.Transport{
		TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots, Certificates: []tls.Certificate{cert}},
	}}, nil
}
