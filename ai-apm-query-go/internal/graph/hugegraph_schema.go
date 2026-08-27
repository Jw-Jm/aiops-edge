package graph

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
)

//go:embed schema_manifest_v2.json
var schemaManifestFS embed.FS

func SchemaManifestV2() (string, string) {
	raw, err := schemaManifestFS.ReadFile("schema_manifest_v2.json")
	if err != nil {
		panic(err)
	}
	var value interface{}
	if err := json.Unmarshal(raw, &value); err != nil {
		panic(err)
	}
	normalized, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	sum := sha256.Sum256(normalized)
	return string(normalized), hex.EncodeToString(sum[:])
}
