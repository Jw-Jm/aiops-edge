package graph

import (
	"strings"
	"testing"
)

func TestSchemaManifestV2ContainsSingleEntityAndFrozenLabels(t *testing.T) {
	manifest, checksum := SchemaManifestV2()
	if manifest == "" || len(checksum) != 64 {
		t.Fatalf("manifest/checksum = %q/%q", manifest, checksum)
	}
	for _, required := range []string{"CUSTOMIZE_STRING", "Entity", "HAS_COMPONENT", "DEPENDS_ON", "entity_uid", "last_seen_ms"} {
		if !strings.Contains(manifest, required) {
			t.Fatalf("manifest missing %q", required)
		}
	}
}
