package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// graphSourceRows is the small typed-query adapter used by canonical graph
// source views.  SQL remains owned by query-api/store; orchestrator receives
// only the bounded JSON view and never a database connection.
func graphSourceRows(query string, args ...interface{}) ([]map[string]interface{}, error) {
	conn := GetDB()
	if conn == nil {
		return nil, errors.New("mysql unavailable")
	}
	rows, err := conn.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	result := make([]map[string]interface{}, 0)
	for rows.Next() {
		values := make([]interface{}, len(columns))
		pointers := make([]interface{}, len(columns))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			return nil, err
		}
		item := make(map[string]interface{}, len(columns))
		for i, column := range columns {
			item[column] = graphSourceValue(values[i])
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func graphSourceValue(value interface{}) interface{} {
	switch typed := value.(type) {
	case []byte:
		return string(typed)
	case sql.NullString:
		if typed.Valid {
			return typed.String
		}
		return ""
	default:
		return value
	}
}

// ListGraphCatalog returns MySQL-authoritative business/application/service
// records.  The response keys intentionally match the graph builder contract.
func (d *BusinessCatalogDAO) ListGraphCatalog(tenantID, clusterID string) (map[string]interface{}, error) {
	if strings.TrimSpace(tenantID) == "" {
		return nil, errors.New("tenant id is required")
	}
	businesses, err := graphSourceRows(`SELECT business_uuid, tenant_id, name, name_key, owner, criticality, status, version
    FROM business_systems WHERE tenant_id=? ORDER BY business_uuid LIMIT 5000`, tenantID)
	if err != nil {
		return nil, err
	}
	applications, err := graphSourceRows(`SELECT application_uuid, tenant_id, business_uuid, name, name_key, owner, criticality, status, version
    FROM applications WHERE tenant_id=? ORDER BY application_uuid LIMIT 5000`, tenantID)
	if err != nil {
		return nil, err
	}
	services, err := graphSourceRows(`SELECT service_uuid, tenant_id, application_uuid, name, name_key, cluster_id, namespace,
    k8s_service_uid, telemetry_service_name, status, version FROM application_services
    WHERE tenant_id=? AND (cluster_id=? OR cluster_id IS NULL OR cluster_id='') ORDER BY service_uuid LIMIT 5000`, tenantID, clusterID)
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{"businesses": businesses, "applications": applications, "services": services}, nil
}

// ListGraphHardware returns the canonical hardware inventory identity view.
// Components are grouped below their physical server to match HardwareBuilder.
func (d *HardwareInventoryDAO) ListGraphHardware(tenantID, clusterID string) (map[string]interface{}, error) {
	if strings.TrimSpace(tenantID) == "" {
		return nil, errors.New("tenant id is required")
	}
	assets, err := graphSourceRows(`SELECT asset_uuid, tenant_id, cluster_id, system_uuid, vendor, product_name,
    serial_number, hostname, bmc_identifier, inventory_hash, status FROM hardware_assets
    WHERE tenant_id=? AND (cluster_id=? OR cluster_id IS NULL OR cluster_id='') ORDER BY asset_uuid LIMIT 5000`, tenantID, clusterID)
	if err != nil {
		return nil, err
	}
	components, err := graphSourceRows(`SELECT component_uid, asset_uuid, tenant_id, component_type, stable_locator,
    stable_locator_sha256, vendor, model, serial_number, capacity_bytes, pci_bdf, permanent_mac, wwn,
    resolution, inventory_json, status FROM hardware_components WHERE tenant_id=? ORDER BY component_uid LIMIT 20000`, tenantID)
	if err != nil {
		return nil, err
	}
	byAsset := map[string][]map[string]interface{}{}
	for _, component := range components {
		asset := strings.TrimSpace(toGraphString(component["asset_uuid"]))
		if asset != "" {
			byAsset[asset] = append(byAsset[asset], component)
		}
	}
	for _, asset := range assets {
		asset["components"] = byAsset[toGraphString(asset["asset_uuid"])]
		// HardwareBuilder accepts the canonical identity names below.
		asset["system_uuid"] = asset["system_uuid"]
		asset["serial"] = asset["serial_number"]
		asset["product"] = asset["product_name"]
	}
	return map[string]interface{}{"servers": assets}, nil
}

func toGraphString(value interface{}) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(strings.Trim(stringValueBytes(value), "\x00"))
}

func stringValueBytes(value interface{}) string {
	if data, ok := value.([]byte); ok {
		return string(data)
	}
	return strings.TrimSpace(fmt.Sprint(value))
}
