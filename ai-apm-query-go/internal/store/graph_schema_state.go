package store

import (
	"errors"
)

type GraphSchemaState struct {
	SchemaName           string
	SchemaVersion        int64
	SchemaChecksumSHA256 string
	Graphspace           string
	GraphName            string
	AppliedBy            string
}

type GraphSchemaStateDAO struct{}

func (d *GraphSchemaStateDAO) Upsert(state GraphSchemaState) error {
	conn := GetDB()
	if conn == nil {
		return errors.New("mysql unavailable")
	}
	_, err := conn.Exec(`INSERT INTO graph_schema_state
    (schema_name, schema_version, schema_checksum_sha256, graphspace, graph_name, applied_by)
    VALUES (?, ?, ?, ?, ?, ?)
    ON DUPLICATE KEY UPDATE schema_version=VALUES(schema_version), schema_checksum_sha256=VALUES(schema_checksum_sha256),
      graphspace=VALUES(graphspace), graph_name=VALUES(graph_name), applied_by=VALUES(applied_by), applied_at=NOW()`,
		state.SchemaName, state.SchemaVersion, state.SchemaChecksumSHA256, state.Graphspace, state.GraphName, state.AppliedBy)
	return err
}

func (d *GraphSchemaStateDAO) Get(schemaName string) (*GraphSchemaState, error) {
	conn := GetDB()
	if conn == nil {
		return nil, errors.New("mysql unavailable")
	}
	var state GraphSchemaState
	err := conn.QueryRow(`SELECT schema_name, schema_version, schema_checksum_sha256, graphspace, graph_name, applied_by
    FROM graph_schema_state WHERE schema_name=?`, schemaName).Scan(&state.SchemaName, &state.SchemaVersion,
		&state.SchemaChecksumSHA256, &state.Graphspace, &state.GraphName, &state.AppliedBy)
	if err != nil {
		return nil, err
	}
	return &state, nil
}
