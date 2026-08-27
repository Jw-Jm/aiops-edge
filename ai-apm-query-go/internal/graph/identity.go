package graph

import (
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"unicode"
)

const (
	IdentityContractVersion  = "graph-identity-v1"
	OntologyContractVersion  = "graph-ontology-v2"
	GraphDTOContractVersion  = "graph-dto-v1"
	PropagationPolicyVersion = "propagation-v1"
	GraphSchemaVersion       = 2
	AssetNamespace           = "0b8607dd-6b92-5e95-b007-d32874ffefab"
	MutationNamespace        = "7af0bc4b-dba0-56b1-ac7c-0fe13db2ef5b"
	GlobalClusterScopeID     = "00000000-0000-0000-0000-000000000000"
)

const unitSeparator = "\x1f"

// NameKeyV1 is the cross-language display-name lookup normalizer.
func NameKeyV1(value string) string {
	return strings.Join(strings.FieldsFunc(strings.ToLower(strings.TrimSpace(value)), unicode.IsSpace), " ")
}

// SHA256Parts joins UTF-8 parts with the contract's Unit Separator and returns
// a lower-case hexadecimal SHA-256 digest.
func SHA256Parts(parts ...string) string {
	h := sha256.New()
	_, _ = h.Write([]byte(strings.Join(parts, unitSeparator)))
	return hex.EncodeToString(h.Sum(nil))
}

// EntityUID builds the public UID format used by all graph vertices. Callers
// must pass the already normalized, stable identity parts.
func EntityUID(kind string, parts ...string) string {
	kind = strings.TrimSpace(kind)
	return fmt.Sprintf("%s:v1:%s", kind, strings.Join(parts, ":"))
}

// EdgeUID is stable across source changes. Source provenance belongs in attrs,
// while tenant/relation/endpoints identify one logical current edge.
func EdgeUID(tenantID, relationType, sourceUID, targetUID string) string {
	return "edge:v1:" + SHA256Parts(tenantID, relationType, sourceUID, targetUID)
}

func K8sEntityUID(kind, clusterID, objectUID string) string {
	return EntityUID("k8s-"+strings.TrimPrefix(strings.ToLower(kind), "k8s-"), clusterID, objectUID)
}

func KubeVirtEntityUID(kind, clusterID, objectUID string) string {
	return EntityUID("kubevirt-"+strings.TrimPrefix(strings.ToLower(kind), "kubevirt-"), clusterID, objectUID)
}

func PhysicalServerEntityUID(assetUUID string) string {
	return EntityUID("physical-server", assetUUID)
}

func HardwareComponentEntityUID(assetUUID, componentType, stableLocator string) string {
	return EntityUID("component", assetUUID, strings.ToLower(strings.TrimSpace(componentType)), SHA256Parts(stableLocator))
}

func ProvisionalServiceEntityUID(tenantID, clusterID, serviceName string) string {
	return EntityUID("service-provisional", tenantID, clusterID, SHA256Parts(NameKeyV1(serviceName)))
}

func CanonicalServiceEntityUID(tenantID, serviceUUID string) string {
	return EntityUID("service", tenantID, serviceUUID)
}

// AssetUUIDV5 returns the deterministic asset UUID required by the physical
// inventory contract. The boolean is false when no stable inventory identity
// is available; hostname is intentionally not accepted as a fallback.
func AssetUUIDV5(tenantID, systemUUID, vendor, serial string) (string, bool) {
	nameParts := []string{tenantID}
	switch {
	case strings.TrimSpace(systemUUID) != "":
		nameParts = append(nameParts, systemUUID)
	case strings.TrimSpace(vendor) != "" && strings.TrimSpace(serial) != "":
		nameParts = append(nameParts, vendor, serial)
	default:
		return "", false
	}
	ns, err := parseUUID(AssetNamespace)
	if err != nil {
		return "", false
	}
	name := []byte(strings.Join(nameParts, unitSeparator))
	h := sha1.New()
	_, _ = h.Write(ns[:])
	_, _ = h.Write(name)
	digest := h.Sum(nil)
	digest[6] = (digest[6] & 0x0f) | 0x50
	digest[8] = (digest[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		uint32(digest[0])<<24|uint32(digest[1])<<16|uint32(digest[2])<<8|uint32(digest[3]),
		uint16(digest[4])<<8|uint16(digest[5]),
		uint16(digest[6])<<8|uint16(digest[7]),
		uint16(digest[8])<<8|uint16(digest[9]),
		uint64(digest[10])<<40|uint64(digest[11])<<32|uint64(digest[12])<<24|uint64(digest[13])<<16|uint64(digest[14])<<8|uint64(digest[15])), true
}

func parseUUID(value string) ([16]byte, error) {
	var out [16]byte
	clean := strings.ReplaceAll(strings.TrimSpace(value), "-", "")
	if len(clean) != 32 {
		return out, fmt.Errorf("invalid UUID %q", value)
	}
	decoded, err := hex.DecodeString(clean)
	if err != nil {
		return out, err
	}
	copy(out[:], decoded)
	return out, nil
}
