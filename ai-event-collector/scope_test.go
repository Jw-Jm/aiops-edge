package main

import "testing"

func TestEventScopeValidate(t *testing.T) {
	cases := []struct {
		name      string
		tenant    string
		cluster   string
		wantValid bool
	}{
		{"valid canonical uuid", "3f3c3b3a-0000-4000-8000-000000000001", "3f3c3b3a-0000-4000-8000-000000000002", true},
		{"empty tenant", "", "3f3c3b3a-0000-4000-8000-000000000002", false},
		{"default tenant", "default", "3f3c3b3a-0000-4000-8000-000000000002", false},
		{"slug cluster", "3f3c3b3a-0000-4000-8000-000000000001", "orbstack", false},
		{"numeric cluster", "3f3c3b3a-0000-4000-8000-000000000001", "1", false},
		{"empty cluster", "3f3c3b3a-0000-4000-8000-000000000001", "", false},
	}
	for _, c := range cases {
		sc := EventScope{TenantID: c.tenant, ClusterID: c.cluster}
		if err := sc.Validate(); (err == nil) != c.wantValid {
			t.Errorf("%s: Validate() err=%v, wantValid=%v", c.name, err, c.wantValid)
		}
	}
}

func TestValidateCanonicalUUID(t *testing.T) {
	if !validateCanonicalUUID("3f3c3b3a-0000-4000-8000-000000000001") {
		t.Error("expected valid UUID accepted")
	}
	if validateCanonicalUUID("default") || validateCanonicalUUID("orbstack") || validateCanonicalUUID("1") || validateCanonicalUUID("") {
		t.Error("expected invalid refs rejected")
	}
}
