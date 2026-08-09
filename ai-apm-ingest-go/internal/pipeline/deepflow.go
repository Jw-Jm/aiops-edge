package pipeline

import (
	"log"
	"net/http"
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

// handleJSON 不再接受 DeepFlow 原生协议 JSON 数据。
//
// 注意：真实 DeepFlow 数据是通过 deepflow_sync.go 的 DeepFlowSyncer（从 deepflow-clickhouse
// 拉取 application_map / l7_flow_log 转写）接入 observability 的。此原生接收端此前是一个
// "解析后不落库、只 count++ 却返回 200"的假实现，会导致发送方误以为成功而数据全部丢失。
// 在实现真正的 JSON/Protobuf 写入之前，返回 501 Not Implemented，避免静默丢数据。
func (dr *DeepFlowReceiver) handleJSON(w http.ResponseWriter, r *http.Request, tenantID string) {
	_ = r.Body
	log.Printf("DeepFlow native JSON endpoint rejected: use DeepFlowSyncer (deepflow-clickhouse pull) instead")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotImplemented)
	_, _ = w.Write([]byte(`{"status":"error","error":"not implemented: use DeepFlowSyncer pull mode instead"}`))
}

// handleProtobuf 同样返回 501：protobuf 解码未实现，拒绝假成功。
func (dr *DeepFlowReceiver) handleProtobuf(w http.ResponseWriter, r *http.Request, tenantID string) {
	_ = r.Body
	log.Printf("DeepFlow native protobuf endpoint rejected: use DeepFlowSyncer (deepflow-clickhouse pull) instead")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotImplemented)
	_, _ = w.Write([]byte(`{"status":"error","error":"not implemented: use DeepFlowSyncer pull mode instead"}`))
}
