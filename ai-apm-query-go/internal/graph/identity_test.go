package graph

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

type identityFixture struct {
	Vectors []struct {
		Name     string   `json:"name"`
		Parts    []string `json:"parts"`
		Expected string   `json:"expected"`
	} `json:"vectors"`
	Entities []struct {
		Name     string   `json:"name"`
		Kind     string   `json:"kind"`
		Parts    []string `json:"parts"`
		Expected string   `json:"expected"`
	} `json:"entities"`
}

func loadIdentityFixture(t *testing.T) identityFixture {
	t.Helper()
	path := filepath.Join("..", "..", "..", "tests", "fixtures", "graph", "graph_identity_v1.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read identity fixture: %v", err)
	}
	var fixture identityFixture
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatalf("decode identity fixture: %v", err)
	}
	return fixture
}

func TestSHA256PartsMatchesSharedFixture(t *testing.T) {
	fixture := loadIdentityFixture(t)
	for _, vector := range fixture.Vectors {
		t.Run(vector.Name, func(t *testing.T) {
			if got := SHA256Parts(vector.Parts...); got != vector.Expected {
				t.Fatalf("SHA256Parts(%q) = %q, want %q", vector.Parts, got, vector.Expected)
			}
		})
	}
}

func TestEntityUIDMatchesSharedFixture(t *testing.T) {
	fixture := loadIdentityFixture(t)
	for _, vector := range fixture.Entities {
		t.Run(vector.Name, func(t *testing.T) {
			if got := EntityUID(vector.Kind, vector.Parts...); got != vector.Expected {
				t.Fatalf("EntityUID(%q, %q) = %q, want %q", vector.Kind, vector.Parts, got, vector.Expected)
			}
		})
	}
}

func TestNameKeyV1NormalizesUnicodeWhitespace(t *testing.T) {
	if got := NameKeyV1("  Order\u2003Service\n API "); got != "order service api" {
		t.Fatalf("NameKeyV1() = %q, want %q", got, "order service api")
	}
}

func TestEdgeUIDMatchesSharedFixture(t *testing.T) {
	fixture := loadIdentityFixture(t)
	for _, vector := range fixture.Vectors {
		if vector.Name != "runs_on_edge" {
			continue
		}
		if got := EdgeUID(vector.Parts[0], vector.Parts[1], vector.Parts[2], vector.Parts[3]); got != "edge:v1:"+vector.Expected {
			t.Fatalf("EdgeUID() = %q, want %q", got, "edge:v1:"+vector.Expected)
		}
	}
}
