package graph

import "fmt"

const (
	ErrUnknownEntityType       = "GRAPH_UNKNOWN_ENTITY_TYPE"
	ErrOntologyViolation       = "GRAPH_ONTOLOGY_VIOLATION"
	ErrGraphUnavailable        = "GRAPH_UNAVAILABLE"
	ErrGraphSchemaMismatch     = "GRAPH_SCHEMA_MISMATCH"
	ErrGraphFeatureUnavailable = "GRAPH_FEATURE_UNAVAILABLE_LEGACY"
	ErrGraphScopeViolation     = "GRAPH_SCOPE_VIOLATION"
	ErrGraphVersionConflict    = "GRAPH_VERSION_CONFLICT"
	ErrGraphEmpty              = "GRAPH_EMPTY"
	ErrGraphEntityNotFound     = "GRAPH_ENTITY_NOT_FOUND"
	ErrGraphQueryLimitExceeded = "GRAPH_QUERY_LIMIT_EXCEEDED"
)

type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string {
	if e.Message == "" {
		return e.Code
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func graphError(code, message string) error {
	return &Error{Code: code, Message: message}
}

// NewError lets boundary packages preserve the graph error contract without
// exposing the internal helper used by graph implementations.
func NewError(code, message string) *Error { return &Error{Code: code, Message: message} }
