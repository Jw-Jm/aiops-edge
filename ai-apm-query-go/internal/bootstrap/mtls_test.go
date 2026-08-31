package bootstrap

import (
	"crypto/tls"
	"crypto/x509"
	"net/url"
	"testing"
)

func TestVerifyClientSANAcceptsConfiguredDNSOrURI(t *testing.T) {
	t.Setenv("AIOPS_TLS_CLIENT_SAN", "caller.observability.svc.cluster.local,spiffe://observability/query-api")

	for name, cert := range map[string]*x509.Certificate{
		"dns": {DNSNames: []string{"caller.observability.svc.cluster.local"}},
		"uri": {URIs: mustParseURIs(t, "spiffe://observability/query-api")},
	} {
		t.Run(name, func(t *testing.T) {
			if err := verifyClientSAN(tlsState(cert)); err != nil {
				t.Fatalf("expected configured SAN to be accepted: %v", err)
			}
		})
	}
}

func TestVerifyClientSANRejectsUnconfiguredIdentity(t *testing.T) {
	t.Setenv("AIOPS_TLS_CLIENT_SAN", "caller.observability.svc.cluster.local")
	if err := verifyClientSAN(tlsState(&x509.Certificate{DNSNames: []string{"other.observability.svc.cluster.local"}})); err == nil {
		t.Fatal("expected unconfigured client SAN to be rejected")
	}
}

func TestVerifyClientSANAllowsMissingAllowlistOnlyForOptionalTLS(t *testing.T) {
	t.Setenv("AIOPS_TLS_CLIENT_SAN", "")
	if err := verifyClientSAN(tlsState(&x509.Certificate{DNSNames: []string{"caller.observability.svc.cluster.local"}})); err != nil {
		t.Fatalf("optional TLS without an allowlist should not reject the connection: %v", err)
	}
}

func tlsState(cert *x509.Certificate) tls.ConnectionState {
	return tls.ConnectionState{PeerCertificates: []*x509.Certificate{cert}}
}

func mustParseURIs(t *testing.T, raw ...string) []*url.URL {
	t.Helper()
	uris := make([]*url.URL, 0, len(raw))
	for _, value := range raw {
		u, err := url.Parse(value)
		if err != nil {
			t.Fatalf("parse URI SAN: %v", err)
		}
		uris = append(uris, u)
	}
	return uris
}
