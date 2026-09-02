package api

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/observability-platform/ai-apm-query-go/internal/contract"
	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

// chatOperationByCapability is intentionally server-owned.  The caller may
// name the logical tool for audit readability, but it cannot choose an
// operation different from the signed route capability.
var chatOperationByCapability = map[string]string{
	"observability.metrics.read":  "metrics",
	"observability.logs.read":     "logs",
	"observability.traces.read":   "traces",
	"observability.alerts.read":   "alerts",
	"kubernetes.events.read":      "events",
	"observability.topology.read": "topology",
	"kubernetes.resources.read":   "kubernetes",
	"changes.read":                "changes",
	"knowledge.search":            "knowledge",
	"knowledge.graph.read":        "graph",
	"kubevirt.resources.read":     "kubevirt",
	"hardware.inventory.read":     "hardware_inventory",
	"hardware.health.read":        "hardware_health",
	"catalog.read":                "catalog",
	"network.topology.read":       "network_topology",
}

func (h *Handler) beginChatToolRun(req *internalQueryRequest, tenantID, clusterID string) (*toolRunContext, bool, error) {
	if h.chatToolDAO == nil {
		return nil, false, &internalQueryError{Code: contract.ErrorCodeToolUnavailable, Message: "ChatTool audit persistence unavailable"}
	}
	if req.PrincipalType != "user" || req.PrincipalID == "" || req.SessionID == "" {
		return nil, false, &internalQueryError{Code: contract.ErrorCodeValidationFailed, Message: "chat tool authenticated user session required"}
	}
	if !validUUID(req.PrincipalID) || !validUUID(req.SessionID) || !validUUID(req.ChatSessionID) ||
		!validUUID(req.ChatTurnID) || !validUUID(req.ChatToolCallID) {
		return nil, false, &internalQueryError{Code: contract.ErrorCodeValidationFailed, Message: "chat tool canonical identity required"}
	}
	operation := chatOperationByCapability[req.RouteCapability]
	if operation == "" {
		return nil, false, &internalQueryError{Code: contract.ErrorCodeValidationFailed, Message: "chat tool capability is not read-only"}
	}
	toolName := strings.TrimSpace(req.ChatToolName)
	if toolName == "" || len(toolName) > 128 {
		return nil, false, &internalQueryError{Code: contract.ErrorCodeValidationFailed, Message: "chat tool name required"}
	}
	if req.ChatToolRunID == "" {
		req.ChatToolRunID = newUUID()
	}
	if !validUUID(req.ChatToolRunID) {
		return nil, false, &internalQueryError{Code: contract.ErrorCodeValidationFailed, Message: "chat tool run id must be canonical UUID"}
	}
	audit := store.AIChatToolRun{
		ChatToolRunID: req.ChatToolRunID, PrincipalID: req.PrincipalID, SessionID: req.SessionID,
		ChatSessionID: req.ChatSessionID, TurnID: req.ChatTurnID, ToolCallID: req.ChatToolCallID,
		TenantID: tenantID, ClusterID: clusterID, ToolName: toolName, Operation: operation,
		Capability: req.RouteCapability, ArgsHash: toolArgsHash(req), Status: "running",
	}
	created, existing, err := h.chatToolDAO.Start(audit)
	if err != nil {
		if errors.Is(err, store.ErrChatToolIdempotencyConflict) {
			return nil, false, &internalQueryError{Code: contract.ErrorCodeRunStateConflict, Message: err.Error()}
		}
		if errors.Is(err, store.ErrChatToolAuditOwnership) {
			return nil, false, &internalQueryError{Code: contract.ErrorCodeTenantAccessDenied, Message: err.Error()}
		}
		return nil, false, &internalQueryError{Code: contract.ErrorCodeToolUnavailable, Message: "ChatTool audit persistence unavailable"}
	}
	if created {
		return &toolRunContext{ToolRunID: audit.ChatToolRunID, ChatAudit: &audit}, false, nil
	}
	if existing == nil {
		return nil, false, &internalQueryError{Code: contract.ErrorCodeToolUnavailable, Message: "ChatTool audit replay unavailable"}
	}
	return &toolRunContext{ToolRunID: existing.ChatToolRunID, ChatAudit: existing}, true, nil
}

func chatAuditStatus(quality string, errMsg string) string {
	if errMsg != "" {
		return "failed"
	}
	switch quality {
	case "complete":
		return "success"
	case "partial":
		return "partial"
	default:
		return "unavailable"
	}
}

func (h *Handler) finishChatToolRun(trc *toolRunContext, quality string, data []byte, errMsg string) error {
	if trc == nil || trc.ChatAudit == nil || h.chatToolDAO == nil {
		return errors.New("ChatTool audit context unavailable")
	}
	digest := ""
	if data != nil {
		digest = sha256Digest(data)
	}
	count := chatResultCount(data)
	status := chatAuditStatus(quality, errMsg)
	if status == "success" && count == 0 {
		status = "no_data"
	}
	if err := h.chatToolDAO.Finish(trc.ChatAudit.ChatToolRunID, trc.ChatAudit.PrincipalID,
		trc.ChatAudit.SessionID, trc.ChatAudit.TenantID, trc.ChatAudit.ClusterID,
		status, digest, count, stableChatAuditError(errMsg)); err != nil {
		return fmt.Errorf("finish ChatTool audit: %w", err)
	}
	return nil
}

func chatResultCount(data []byte) int64 {
	if len(data) == 0 {
		return 0
	}
	var value interface{}
	if jsonErr := json.Unmarshal(data, &value); jsonErr != nil {
		return 0
	}
	switch typed := value.(type) {
	case []interface{}:
		return int64(len(typed))
	case map[string]interface{}:
		for _, key := range []string{"total", "count", "result_count"} {
			if n, ok := typed[key].(float64); ok && n >= 0 {
				return int64(n)
			}
		}
		for _, key := range []string{"points", "logs", "traces", "alerts", "events", "items", "results", "pods", "nodes"} {
			if rows, ok := typed[key].([]interface{}); ok {
				return int64(len(rows))
			}
		}
	}
	return 0
}

func stableChatAuditError(message string) string {
	if message == "" {
		return ""
	}
	// Never persist provider/SQL/transport text in the ChatTool audit table.
	// The audit is queryable by operators, so even a bounded prefix could leak a
	// URL, credential fragment or internal host.  Detailed diagnostics stay in
	// server-side structured logs keyed by request/tool IDs.
	return "CHAT_TOOL_FAILED"
}

func sha256Digest(data []byte) string {
	// Keep the digest implementation local to the Chat boundary so it does not
	// accidentally inherit Investigation evidence semantics.
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum[:])
}
