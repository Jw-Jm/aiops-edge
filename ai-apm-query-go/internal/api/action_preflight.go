package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/observability-platform/ai-apm-query-go/internal/contract"
	"github.com/observability-platform/ai-apm-query-go/internal/query"
)

// KubeObjectIdentity is the target snapshot captured before approval. It is
// deliberately limited to fields needed by the executor TOCTOU check.
type KubeObjectIdentity struct {
	UID             string
	ResourceVersion string
	Namespace       string
	Name            string
	Observed        []byte
}

// ActionTargetResolver is the narrow read-only boundary used by preflight.
// Implementations must resolve the canonical cluster identity before reading.
type ActionTargetResolver interface {
	ResolveDeployment(ctx context.Context, clusterID, namespace, name string) (KubeObjectIdentity, error)
}

// queryActionTargetResolver adapts the existing K8s query repository without
// introducing a second Kubernetes client or credential path.
type queryActionTargetResolver struct {
	repo *query.KubernetesRepository
}

func (r queryActionTargetResolver) ResolveDeployment(ctx context.Context, clusterID, namespace, name string) (KubeObjectIdentity, error) {
	identity, err := r.repo.GetDeploymentIdentity(ctx, query.KubernetesScope{ClusterID: clusterID}, clusterID, namespace, name)
	if err != nil {
		return KubeObjectIdentity{}, err
	}
	return KubeObjectIdentity{
		UID: identity.UID, ResourceVersion: identity.ResourceVersion,
		Namespace: identity.Namespace, Name: identity.Name, Observed: identity.Observed,
	}, nil
}

// PreflightInput is the semantic candidate emitted by the investigation
// worker. It contains no caller-supplied UID/RV; those are read from the
// target boundary below.
type PreflightInput struct {
	ClusterID    string
	ResourceType string
	Namespace    string
	TargetName   string
	Operation    string
	Params       json.RawMessage
}

// ActionPreflightResult is the immutable candidate handed to the Action DAO.
type ActionPreflightResult struct {
	HashSchemaVersion int
	ActionVersion     int64
	ActionHash        string
	PolicyVersion     string
	PreflightStatus   string
	DryRun            bool
	ResourceType      string
	Namespace         string
	TargetName        string
	TargetUID         string
	ResourceVersion   string
	Operation         string
	Params            json.RawMessage
}

type ActionPreflightService struct {
	resolver      ActionTargetResolver
	policyVersion string
}

func NewActionPreflightService(resolver ActionTargetResolver) *ActionPreflightService {
	return &ActionPreflightService{resolver: resolver, policyVersion: "action-policy-v1"}
}

func (s *ActionPreflightService) Resolve(ctx context.Context, input PreflightInput) (ActionPreflightResult, error) {
	if s == nil || s.resolver == nil {
		return ActionPreflightResult{}, errors.New("action preflight resolver unavailable")
	}
	if strings.TrimSpace(input.ClusterID) == "" || strings.TrimSpace(input.Namespace) == "" || strings.TrimSpace(input.TargetName) == "" {
		return ActionPreflightResult{}, errors.New("cluster_id, namespace and target_name are required")
	}
	if input.ResourceType != "deployment" {
		return ActionPreflightResult{}, fmt.Errorf("unsupported target resource type %q", input.ResourceType)
	}
	if input.Operation != "patch" && input.Operation != "scale" {
		return ActionPreflightResult{}, fmt.Errorf("unsupported executable operation %q", input.Operation)
	}
	params := input.Params
	if len(params) == 0 {
		params = json.RawMessage(`{}`)
	}
	var object map[string]any
	if err := json.Unmarshal(params, &object); err != nil || object == nil {
		return ActionPreflightResult{}, errors.New("params must be a JSON object")
	}
	if input.Operation == "scale" {
		replicas, ok := object["replicas"]
		if !ok {
			return ActionPreflightResult{}, errors.New("scale requires params.replicas")
		}
		if number, ok := replicas.(float64); !ok || number < 0 || number > 10000 || number != float64(int(number)) {
			return ActionPreflightResult{}, errors.New("params.replicas must be an integer between 0 and 10000")
		}
	} else {
		// Patch is intentionally limited to metadata annotations. The same
		// structured payload is sent to the executor and used by reconciliation;
		// arbitrary JSON patches would make the approval hash an incomplete
		// description of the mutation surface.
		metadata, ok := object["metadata"].(map[string]any)
		annotations, annotationsOK := metadata["annotations"].(map[string]any)
		if !ok || !annotationsOK || len(annotations) == 0 {
			return ActionPreflightResult{}, errors.New("patch requires params.metadata.annotations")
		}
		for key, value := range annotations {
			if !strings.HasPrefix(key, "aiops.observability.io/") && !strings.HasPrefix(key, "aio-action-executor/") {
				return ActionPreflightResult{}, fmt.Errorf("patch annotation %q is outside the allowlist", key)
			}
			if text, ok := value.(string); !ok || len(text) > 256 {
				return ActionPreflightResult{}, fmt.Errorf("patch annotation %q must be a string of at most 256 bytes", key)
			}
		}
	}
	identity, err := s.resolver.ResolveDeployment(ctx, input.ClusterID, input.Namespace, input.TargetName)
	if err != nil {
		return ActionPreflightResult{}, fmt.Errorf("resolve target: %w", err)
	}
	if identity.UID == "" || identity.ResourceVersion == "" {
		return ActionPreflightResult{}, errors.New("target identity is incomplete")
	}
	payload := contract.CanonicalActionPayloadV2{
		Version: 1, ActionType: "kubernetes", ResourceType: input.ResourceType,
		Namespace: identity.Namespace, TargetName: identity.Name, TargetUID: identity.UID,
		ResourceVersion: identity.ResourceVersion, Operation: input.Operation,
		Params: params, PolicyVersion: s.policyVersion,
	}
	hash, err := contract.CanonicalActionHash(payload)
	if err != nil {
		return ActionPreflightResult{}, fmt.Errorf("hash action payload: %w", err)
	}
	return ActionPreflightResult{
		HashSchemaVersion: 2, ActionVersion: 1, ActionHash: hash,
		PolicyVersion: s.policyVersion, PreflightStatus: "passed", DryRun: false,
		ResourceType: input.ResourceType, Namespace: identity.Namespace,
		TargetName: identity.Name, TargetUID: identity.UID,
		ResourceVersion: identity.ResourceVersion, Operation: input.Operation, Params: params,
	}, nil
}
