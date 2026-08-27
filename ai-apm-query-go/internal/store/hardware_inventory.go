package store

import (
	"errors"
	"time"
)

type HardwareAsset struct {
	AssetUUID       string
	TenantID        string
	ClusterID       string
	SystemUUID      string
	Vendor          string
	ProductName     string
	SerialNumber    string
	Hostname        string
	BMCIdentifier   string
	InventoryHash   string
	Status          string
	LastInventoryAt *time.Time
}

type HardwareComponent struct {
	ComponentUID        string
	AssetUUID           string
	TenantID            string
	ComponentType       string
	StableLocator       string
	StableLocatorSHA256 string
	Vendor              string
	Model               string
	SerialNumber        string
	CapacityBytes       *int64
	PCIBDF              string
	PermanentMAC        string
	WWN                 string
	Resolution          string
	InventoryJSON       string
	Status              string
	LastInventoryAt     *time.Time
}

type HardwareInventoryDAO struct{}

func (d *HardwareInventoryDAO) UpsertAsset(asset HardwareAsset) error {
	conn := GetDB()
	if conn == nil {
		return errors.New("mysql unavailable")
	}
	if asset.Status == "" {
		asset.Status = "active"
	}
	_, err := conn.Exec(`INSERT INTO hardware_assets
    (asset_uuid, tenant_id, cluster_id, system_uuid, vendor, product_name, serial_number, hostname,
     bmc_identifier, inventory_hash, status, last_inventory_at)
    VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
    ON DUPLICATE KEY UPDATE cluster_id=VALUES(cluster_id), system_uuid=VALUES(system_uuid), vendor=VALUES(vendor),
      product_name=VALUES(product_name), serial_number=VALUES(serial_number), hostname=VALUES(hostname),
      bmc_identifier=VALUES(bmc_identifier), inventory_hash=VALUES(inventory_hash), status=VALUES(status),
      last_inventory_at=VALUES(last_inventory_at), updated_at=NOW()`,
		asset.AssetUUID, asset.TenantID, asset.ClusterID, nullableStr(asset.SystemUUID), asset.Vendor, asset.ProductName,
		asset.SerialNumber, asset.Hostname, asset.BMCIdentifier, asset.InventoryHash, asset.Status, nullableTime(asset.LastInventoryAt))
	return err
}

func (d *HardwareInventoryDAO) UpsertComponent(component HardwareComponent) error {
	conn := GetDB()
	if conn == nil {
		return errors.New("mysql unavailable")
	}
	if component.Resolution == "" {
		component.Resolution = "physical"
	}
	if component.Status == "" {
		component.Status = "active"
	}
	_, err := conn.Exec(`INSERT INTO hardware_components
    (component_uid, asset_uuid, tenant_id, component_type, stable_locator, stable_locator_sha256, vendor, model,
     serial_number, capacity_bytes, pci_bdf, permanent_mac, wwn, resolution, inventory_json, status, last_inventory_at)
    VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
    ON DUPLICATE KEY UPDATE vendor=VALUES(vendor), model=VALUES(model), serial_number=VALUES(serial_number),
      capacity_bytes=VALUES(capacity_bytes), pci_bdf=VALUES(pci_bdf), permanent_mac=VALUES(permanent_mac),
      wwn=VALUES(wwn), resolution=VALUES(resolution), inventory_json=VALUES(inventory_json), status=VALUES(status),
      last_inventory_at=VALUES(last_inventory_at), updated_at=NOW()`, component.ComponentUID, component.AssetUUID,
		component.TenantID, component.ComponentType, component.StableLocator, component.StableLocatorSHA256,
		component.Vendor, component.Model, component.SerialNumber, component.CapacityBytes, component.PCIBDF,
		component.PermanentMAC, component.WWN, component.Resolution, nullableJSON([]byte(component.InventoryJSON)), component.Status,
		nullableTime(component.LastInventoryAt))
	return err
}
