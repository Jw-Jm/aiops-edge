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
	ResolveTarget(ctx context.Context, clusterID, resourceType, namespace, name string) (KubeObjectIdentity, error)
}

// queryActionTargetResolver adapts the existing K8s query repository without
// introducing a second Kubernetes client or credential path.
type queryActionTargetResolver struct {
	repo *query.KubernetesRepository
}

func (r queryActionTargetResolver) ResolveTarget(ctx context.Context, clusterID, resourceType, namespace, name string) (KubeObjectIdentity, error) {
	identity, err := r.repo.GetObjectIdentity(ctx, query.KubernetesScope{ClusterID: clusterID}, clusterID, resourceType, namespace, name)
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
	input.ClusterID = strings.TrimSpace(input.ClusterID)
	input.ResourceType = strings.ToLower(strings.TrimSpace(input.ResourceType))
	input.Namespace = strings.TrimSpace(input.Namespace)
	input.TargetName = strings.TrimSpace(input.TargetName)
	input.Operation = strings.ToLower(strings.TrimSpace(input.Operation))
	if strings.TrimSpace(input.ClusterID) == "" || strings.TrimSpace(input.TargetName) == "" {
		return ActionPreflightResult{}, errors.New("cluster_id, namespace and target_name are required")
	}
	if input.ResourceType != "node" && strings.TrimSpace(input.Namespace) == "" {
		return ActionPreflightResult{}, errors.New("namespace is required for namespaced targets")
	}
	allowedKinds, ok := canonicalK8sActionKinds[input.Operation]
	if !ok || !allowedKinds[input.ResourceType] {
		return ActionPreflightResult{}, fmt.Errorf("unsupported operation %q for target resource type %q", input.Operation, input.ResourceType)
	}
	params, err := canonicalizeActionParams(input.Operation, input.Params)
	if err != nil {
		return ActionPreflightResult{}, err
	}
	identity, err := s.resolver.ResolveTarget(ctx, input.ClusterID, input.ResourceType, input.Namespace, input.TargetName)
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
