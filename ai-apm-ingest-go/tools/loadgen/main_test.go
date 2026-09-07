package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestIngestRequestCarriesTenantScope(t *testing.T) {
	req, err := newIngestRequest(http.MethodPost, "https://ingest.test/v1/traces", []byte(`{}`), "api-key", "tenant-123")
	if err != nil {
		t.Fatalf("newIngestRequest() error = %v", err)
	}
	if got := req.Header.Get("X-Api-Key"); got != "api-key" {
		t.Fatalf("X-Api-Key = %q, want api-key", got)
	}
	if got := req.Header.Get("X-Tenant-ID"); got != "tenant-123" {
		t.Fatalf("X-Tenant-ID = %q, want tenant-123", got)
	}
	if got := req.Header.Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
}

func TestStrictTraceContainsPaymentsOrdersParentChildAndMarker(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	trace := strictTrace(7, "strict-run-7", now, true)
	if len(trace.ResourceSpans) != 2 {
		t.Fatalf("strictTrace resource spans = %d, want 2", len(trace.ResourceSpans))
	}

	var root, child *otlpSpan
	services := map[string]bool{}
	for _, resourceSpan := range trace.ResourceSpans {
		service := resourceStringAttribute(resourceSpan.Resource.Attributes, "service.name")
		services[service] = true
		for i := range resourceSpan.ScopeSpans[0].Spans {
			span := &resourceSpan.ScopeSpans[0].Spans[i]
			if span.ParentSpanID == "" {
				root = span
			} else {
				child = span
			}
			if resourceStringAttribute(span.Attributes, "aiops.test.marker") != "strict-run-7" {
				t.Fatalf("span marker = %q, want strict-run-7", resourceStringAttribute(span.Attributes, "aiops.test.marker"))
			}
		}
	}
	if !services["payments"] || !services["orders"] {
		t.Fatalf("services = %#v, want payments and orders", services)
	}
	if root == nil || child == nil || child.ParentSpanID != root.SpanID {
		t.Fatalf("parent/child relation is not explainable: root=%#v child=%#v", root, child)
	}
	if root.Status["code"] != float64(0) || child.Status["code"] != float64(2) {
		t.Fatalf("strict trace status = root %#v child %#v, want mixed success/error", root.Status, child.Status)
	}
}

func TestStrictMTLSClientLoadsCAAndClientCertificate(t *testing.T) {
	dir := t.TempDir()
	ca, cert, key := writeTestCertificate(t, dir)
	client, err := newMTLSClient(ca, cert, key)
	if err != nil {
		t.Fatalf("newMTLSClient() error = %v", err)
	}
	if client.Transport == nil {
		t.Fatal("newMTLSClient() transport is nil")
	}
}

func writeTestCertificate(t *testing.T, dir string) (string, string, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "strict-loadgen.test"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		IsCA:         true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	caPath := filepath.Join(dir, "ca.crt")
	certPath := filepath.Join(dir, "tls.crt")
	keyPath := filepath.Join(dir, "tls.key")
	for path, data := range map[string][]byte{caPath: certPEM, certPath: certPEM, keyPath: keyPEM} {
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return caPath, certPath, keyPath
}
