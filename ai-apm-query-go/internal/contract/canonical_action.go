package contract

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
)

// CanonicalActionPayloadV2 is the immutable semantic payload approved by a
// human and consumed by the Action Executor. Every field that can change the
// side effect is included in the hash input.
type CanonicalActionPayloadV2 struct {
	Version         int64           `json:"version"`
	ActionType      string          `json:"action_type"`
	ResourceType    string          `json:"resource_type"`
	Namespace       string          `json:"namespace"`
	TargetName      string          `json:"target_name"`
	TargetUID       string          `json:"target_uid"`
	ResourceVersion string          `json:"resource_version"`
	Operation       string          `json:"operation"`
	Params          json.RawMessage `json:"params"`
	PolicyVersion   string          `json:"policy_version"`
}

// NormalizeJSON decodes and re-encodes JSON with UseNumber. encoding/json
// sorts object keys during Marshal, while UseNumber prevents a large integer
// in an action parameter from silently becoming a float64.
func NormalizeJSON(value any) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err == nil {
		return nil, errors.New("canonical JSON contains trailing values")
	} else if err != io.EOF {
		return nil, err
	}
	return json.Marshal(decoded)
}

// CanonicalActionHash computes the SHA-256 identity of an Action V2 payload.
func CanonicalActionHash(action CanonicalActionPayloadV2) (string, error) {
	canonical, err := NormalizeJSON(action)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), nil
}
