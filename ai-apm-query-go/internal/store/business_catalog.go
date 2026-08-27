package store

import (
	"errors"
	"time"
)

type BusinessSystem struct {
	BusinessUUID string
	TenantID     string
	Name         string
	NameKey      string
	Owner        string
	Criticality  string
	Status       string
	Version      int64
}

type Application struct {
	ApplicationUUID string
	TenantID        string
	BusinessUUID    string
	Name            string
	NameKey         string
	Owner           string
	Criticality     string
	Status          string
	Version         int64
}

type ApplicationService struct {
	ServiceUUID          string
	TenantID             string
	ApplicationUUID      string
	Name                 string
	NameKey              string
	ClusterID            string
	Namespace            string
	K8sServiceUID        string
	TelemetryServiceName string
	Status               string
	Version              int64
}

type BusinessCatalogDAO struct{}

func (d *BusinessCatalogDAO) UpsertBusiness(item BusinessSystem) error {
	conn := GetDB()
	if conn == nil {
		return errors.New("mysql unavailable")
	}
	if item.Status == "" {
		item.Status = "active"
	}
	if item.Criticality == "" {
		item.Criticality = "normal"
	}
	if item.Version <= 0 {
		item.Version = 1
	}
	_, err := conn.Exec(`INSERT INTO business_systems
    (business_uuid, tenant_id, name, name_key, owner, criticality, status, version)
    VALUES (?, ?, ?, ?, ?, ?, ?, ?)
    ON DUPLICATE KEY UPDATE name=VALUES(name), name_key=VALUES(name_key), owner=VALUES(owner),
      criticality=VALUES(criticality), status=VALUES(status), version=VALUES(version), updated_at=NOW()`,
		item.BusinessUUID, item.TenantID, item.Name, item.NameKey, item.Owner, item.Criticality, item.Status, item.Version)
	return err
}

func (d *BusinessCatalogDAO) UpsertApplication(item Application) error {
	conn := GetDB()
	if conn == nil {
		return errors.New("mysql unavailable")
	}
	if item.Status == "" {
		item.Status = "active"
	}
	if item.Criticality == "" {
		item.Criticality = "normal"
	}
	if item.Version <= 0 {
		item.Version = 1
	}
	_, err := conn.Exec(`INSERT INTO applications
    (application_uuid, tenant_id, business_uuid, name, name_key, owner, criticality, status, version)
    VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
    ON DUPLICATE KEY UPDATE business_uuid=VALUES(business_uuid), name=VALUES(name), name_key=VALUES(name_key),
      owner=VALUES(owner), criticality=VALUES(criticality), status=VALUES(status), version=VALUES(version), updated_at=NOW()`,
		item.ApplicationUUID, item.TenantID, item.BusinessUUID, item.Name, item.NameKey, item.Owner, item.Criticality,
		item.Status, item.Version)
	return err
}

func (d *BusinessCatalogDAO) UpsertService(item ApplicationService) error {
	conn := GetDB()
	if conn == nil {
		return errors.New("mysql unavailable")
	}
	if item.Status == "" {
		item.Status = "active"
	}
	if item.Version <= 0 {
		item.Version = 1
	}
	_, err := conn.Exec(`INSERT INTO application_services
    (service_uuid, tenant_id, application_uuid, name, name_key, cluster_id, namespace, k8s_service_uid,
     telemetry_service_name, status, version)
    VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
    ON DUPLICATE KEY UPDATE application_uuid=VALUES(application_uuid), name=VALUES(name), name_key=VALUES(name_key),
      cluster_id=VALUES(cluster_id), namespace=VALUES(namespace), k8s_service_uid=VALUES(k8s_service_uid),
      telemetry_service_name=VALUES(telemetry_service_name), status=VALUES(status), version=VALUES(version), updated_at=NOW()`,
		item.ServiceUUID, item.TenantID, item.ApplicationUUID, item.Name, item.NameKey, nullableStr(item.ClusterID), item.Namespace,
		item.K8sServiceUID, item.TelemetryServiceName, item.Status, item.Version)
	return err
}

func nowUTC() time.Time { return time.Now().UTC() }
