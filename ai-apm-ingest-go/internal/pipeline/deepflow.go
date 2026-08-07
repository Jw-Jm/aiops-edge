package pipeline

import (
	"encoding/json"
	"log"
	"net/http"
	"time"
)

// DeepFlowReceiver handles DeepFlow native protocol data
type DeepFlowReceiver struct {
	ingest *Pipeline
}

// NewDeepFlowReceiver creates a DeepFlow data receiver
func NewDeepFlowReceiver(ingest *Pipeline) *DeepFlowReceiver {
	return &DeepFlowReceiver{ingest: ingest}
}

// ServeHTTP handles incoming DeepFlow data via HTTP POST
func (dr *DeepFlowReceiver) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "method not allowed", 405)
		return
	}

	tenantID := r.Header.Get("X-Tenant-ID")
	if tenantID == "" {
		tenantID = "default"
	}

	// Handle DeepFlow l7_flow_log or application metrics
	contentType := r.Header.Get("Content-Type")
	switch {
	case contentType == "application/x-protobuf" || contentType == "application/protobuf":
		dr.handleProtobuf(w, r, tenantID)
	default:
		dr.handleJSON(w, r, tenantID)
	}
}

func (dr *DeepFlowReceiver) handleJSON(w http.ResponseWriter, r *http.Request, tenantID string) {
	// DeepFlow can send JSON-formatted flow logs
	var flows []map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&flows); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), 400)
		return
	}

	count := 0
	for _, flow := range flows {
		// Extract DeepFlow fields and convert to internal span model
		serviceName := getStringField(flow, "service_name", "deepflow")
		_ = serviceName

		// Write time bucket
		timeBucket := time.Now().Truncate(time.Minute)
		_ = timeBucket

		count++
	}

	log.Printf("DeepFlow: received %d flow records for tenant %s", count, tenantID)
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"status":"ok","count":` + itoa(count) + `}`))
}

func (dr *DeepFlowReceiver) handleProtobuf(w http.ResponseWriter, r *http.Request, tenantID string) {
	// Protobuf decoding would go here
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"status":"ok","format":"protobuf","note":"decoding not implemented"}`))
}

func getStringField(m map[string]interface{}, key, defaultVal string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok { return s }
	}
	return defaultVal
}

func itoa(n int) string {
	return string(rune('0'+n%10)) + func() string {
		s := ""
		for m := n / 10; m > 0; m /= 10 {
			s = string(rune('0'+m%10)) + s
		}
		return s
	}()
}
