package store

import "testing"

func TestCatalogDAO(t *testing.T) {
	if GetDB() == nil {
		t.Skip("MySQL not available")
	}
	d := &CatalogDAO{}
	id, err := d.Create(&ServiceCatalog{ServiceName: "svc-c", Owner: "ops", Status: "active"})
	if err != nil {
		t.Fatalf("create err: %v", err)
	}
	if id <= 0 {
		t.Fatal("create returned non-positive id")
	}
}

func TestDeviceDAO(t *testing.T) {
	if GetDB() == nil {
		t.Skip("MySQL not available")
	}
	d := &DeviceDAO{}
	id, err := d.Create(&Device{Hostname: "node-c", IP: "10.0.0.1", Status: "online", Role: "node"})
	if err != nil {
		t.Fatalf("create device err: %v", err)
	}
	if id <= 0 {
		t.Fatal("device create returned non-positive id")
	}
}

func TestClusterDAO(t *testing.T) {
	if GetDB() == nil {
		t.Skip("MySQL not available")
	}
	d := &ClusterDAO{}
	id, err := d.Upsert(&Cluster{Name: "cluster-c", Provider: "orbstack", Version: "v1.30", NodeCount: 3, Status: "active"})
	if err != nil {
		t.Fatalf("upsert cluster err: %v", err)
	}
	if id <= 0 {
		t.Fatal("cluster upsert returned non-positive id")
	}
}
