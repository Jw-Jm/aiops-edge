package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	graphpkg "github.com/observability-platform/ai-apm-query-go/internal/graph"
	"github.com/observability-platform/ai-apm-query-go/internal/query"
)

// PanoramaService is the service-level view model.  Identity and ownership
// come from the graph projection when available; RED metrics come from the
// ClickHouse trace source of truth.  The browser never joins these sources.
type PanoramaService struct {
	EntityUID       string  `json:"entity_uid,omitempty"`
	ServiceName     string  `json:"service_name"`
	Namespace       string  `json:"namespace,omitempty"`
	ApplicationUID  string  `json:"application_uid,omitempty"`
	ApplicationName string  `json:"application_name,omitempty"`
	Health          string  `json:"health"`
	Calls           int64   `json:"calls"`
	Errors          int64   `json:"errors"`
	ErrorRate       float64 `json:"error_rate"`
	AvgLatencyMS    float64 `json:"avg_latency_ms"`
}

type PanoramaEdge struct {
	SourceService     string  `json:"source_service"`
	TargetService     string  `json:"target_service"`
	SourceNamespace   string  `json:"source_namespace,omitempty"`
	TargetNamespace   string  `json:"target_namespace,omitempty"`
	SourceApplication string  `json:"source_application,omitempty"`
	TargetApplication string  `json:"target_application,omitempty"`
	Calls             int64   `json:"calls"`
	Errors            int64   `json:"errors"`
	ErrorRate         float64 `json:"error_rate"`
	LatencyMS         float64 `json:"latency_ms"`
	CrossNamespace    bool    `json:"cross_namespace"`
}

type PanoramaGroup struct {
	GroupUID     string            `json:"group_uid"`
	Name         string            `json:"name"`
	GroupBy      string            `json:"group_by"`
	ServiceCount int               `json:"service_count"`
	Healthy      int               `json:"healthy"`
	Degraded     int               `json:"degraded"`
	Critical     int               `json:"critical"`
	Calls        int64             `json:"calls"`
	Errors       int64             `json:"errors"`
	ErrorRate    float64           `json:"error_rate"`
	Services     []PanoramaService `json:"services"`
}

type PanoramaGroupEdge struct {
	SourceGroupUID string  `json:"source_group_uid"`
	TargetGroupUID string  `json:"target_group_uid"`
	SourceName     string  `json:"source_name"`
	TargetName     string  `json:"target_name"`
	Routes         int     `json:"routes"`
	Calls          int64   `json:"calls"`
	Errors         int64   `json:"errors"`
	ErrorRate      float64 `json:"error_rate"`
	LatencyMS      float64 `json:"latency_ms"`
}

type servicePanoramaData struct {
	Services []PanoramaService
	Edges    []PanoramaEdge
	Revision string
	Warnings []string
}

func panoramaNoData(err error) bool {
	var qe *query.QueryError
	return errors.As(err, &qe) && qe.Code == query.NoDataCode
}

func panoramaMinutes(r *http.Request) (int, error) {
	minutes := 60
	if raw := strings.TrimSpace(r.URL.Query().Get("minutes")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 7*24*60 {
			return 0, fmt.Errorf("minutes must be between 1 and 10080")
		}
		minutes = value
	}
	return minutes, nil
}

func panoramaMatrixLimit(r *http.Request) (int, error) {
	limit := 200
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 200 {
			return 0, fmt.Errorf("matrix limit must be between 1 and 200")
		}
		limit = value
	}
	return limit, nil
}

func panoramaScope(r *http.Request) query.TopologyScope {
	tenantID := extractTenantID(r)
	if authorization, ok := requestAuthorizationContext(r); ok && authorization.TenantID != "" {
		tenantID = authorization.TenantID
	}
	return query.TopologyScope{TenantID: tenantID, ClusterID: extractClusterIDIfSpecific(r), Services: currentScope(r).Services}
}

func panoramaEntityScope(scope query.TopologyScope) graphpkg.GraphScope {
	out := graphpkg.GraphScope{TenantID: scope.TenantID, ClusterIDs: map[string]struct{}{}}
	if scope.ClusterID != "" {
		out.ClusterIDs[scope.ClusterID] = struct{}{}
	}
	return out
}

func panoramaAttr(attrs map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		if value, ok := attrs[key]; ok && value != nil {
			if text := strings.TrimSpace(fmt.Sprint(value)); text != "" && text != "<nil>" {
				return text
			}
		}
	}
	return ""
}

func panoramaHealth(calls, errors int64) string {
	if calls <= 0 {
		return "unknown"
	}
	rate := float64(errors) / float64(calls)
	if rate > .10 {
		return "critical"
	}
	if rate > .03 {
		return "degraded"
	}
	return "healthy"
}

func panoramaRevision(services []PanoramaService, edges []PanoramaEdge) string {
	parts := make([]string, 0, len(services)+len(edges))
	for _, service := range services {
		parts = append(parts, fmt.Sprintf("s:%s:%s:%s", service.EntityUID, service.ServiceName, service.Namespace))
	}
	for _, edge := range edges {
		// A topology revision identifies structure only.  RED counters are
		// intentionally excluded so the browser can refresh the summary without
		// rebuilding the grouped map on every metrics tick.
		parts = append(parts, fmt.Sprintf("e:%s:%s", edge.SourceService, edge.TargetService))
	}
	sort.Strings(parts)
	hash := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(hash[:])[:16]
}

func (h *Handler) collectServicePanorama(ctx context.Context, r *http.Request, minutes int) (servicePanoramaData, error) {
	if h.topoRepo == nil {
		return servicePanoramaData{}, query.Unavailable("service panorama repository is not configured")
	}
	scope := panoramaScope(r)
	nodes, err := h.topoRepo.GlobalNodes(ctx, scope, minutes)
	if err != nil && !panoramaNoData(err) {
		return servicePanoramaData{}, err
	}
	edges, err := h.topoRepo.GlobalEdgesWithTraceFallback(ctx, scope, minutes)
	if err != nil && !panoramaNoData(err) {
		return servicePanoramaData{}, err
	}
	nsMap, nsErr := h.topoRepo.GlobalServiceNS(ctx, scope, minutes)
	if nsErr != nil && !panoramaNoData(nsErr) {
		nsMap = map[string]string{}
	}

	entityByName := map[string]graphpkg.Entity{}
	warnings := []string{}
	if h.graphRepo != nil && h.graphInitErr == nil {
		entities, searchErr := h.graphRepo.SearchEntities(ctx, panoramaEntityScope(scope), graphpkg.EntitySearchQuery{EntityType: "service", Limit: 300})
		if searchErr != nil {
			warnings = append(warnings, "GRAPH_IDENTITY_UNAVAILABLE")
		} else {
			for _, entity := range entities {
				if entity.Name != "" {
					entityByName[entity.Name] = entity
				}
			}
		}
	} else {
		warnings = append(warnings, "GRAPH_IDENTITY_UNAVAILABLE")
	}

	byName := map[string]PanoramaService{}
	for _, node := range nodes {
		if node.Service == "" || isDeletedService(node.Service) {
			continue
		}
		entity := entityByName[node.Service]
		service := PanoramaService{EntityUID: entity.EntityUID, ServiceName: node.Service, Namespace: nsMap[node.Service], Calls: node.Calls, Errors: node.Errors,
			AvgLatencyMS: node.AvgNs / 1e6}
		if entity.Namespace != "" {
			service.Namespace = entity.Namespace
		}
		service.ApplicationUID = panoramaAttr(entity.Attrs, "application_uid", "app_uid")
		service.ApplicationName = panoramaAttr(entity.Attrs, "application_name", "application")
		service.ErrorRate = 0
		if service.Calls > 0 {
			service.ErrorRate = float64(service.Errors) / float64(service.Calls)
		}
		service.Health = panoramaHealth(service.Calls, service.Errors)
		byName[service.ServiceName] = service
	}
	for _, edge := range edges {
		if edge.Source == "" || edge.Target == "" || edge.Source == edge.Target || isDeletedService(edge.Source) || isDeletedService(edge.Target) {
			continue
		}
		for _, name := range []string{edge.Source, edge.Target} {
			if _, ok := byName[name]; !ok {
				entity := entityByName[name]
				service := PanoramaService{EntityUID: entity.EntityUID, ServiceName: name, Namespace: nsMap[name], Health: "unknown"}
				service.ApplicationUID = panoramaAttr(entity.Attrs, "application_uid", "app_uid")
				service.ApplicationName = panoramaAttr(entity.Attrs, "application_name", "application")
				byName[name] = service
			}
		}
	}
	services := make([]PanoramaService, 0, len(byName))
	for _, service := range byName {
		services = append(services, service)
	}
	sort.Slice(services, func(i, j int) bool {
		ki := services[i].ApplicationName + "\x00" + services[i].Namespace + "\x00" + services[i].ServiceName
		kj := services[j].ApplicationName + "\x00" + services[j].Namespace + "\x00" + services[j].ServiceName
		return ki < kj
	})
	serviceMap := map[string]PanoramaService{}
	for _, service := range services {
		serviceMap[service.ServiceName] = service
	}
	panoramaEdges := make([]PanoramaEdge, 0, len(edges))
	for _, edge := range edges {
		if edge.Source == "" || edge.Target == "" || edge.Source == edge.Target || isDeletedService(edge.Source) || isDeletedService(edge.Target) {
			continue
		}
		calls, errors := edge.Calls, edge.Errors
		rate := 0.0
		if calls > 0 {
			rate = float64(errors) / float64(calls)
		}
		sourceService, targetService := serviceMap[edge.Source], serviceMap[edge.Target]
		panoramaEdges = append(panoramaEdges, PanoramaEdge{SourceService: edge.Source, TargetService: edge.Target,
			SourceNamespace: sourceService.Namespace, TargetNamespace: targetService.Namespace,
			SourceApplication: sourceService.ApplicationName, TargetApplication: targetService.ApplicationName,
			Calls: calls, Errors: errors, ErrorRate: rate, LatencyMS: edge.AvgNs / 1e6,
			CrossNamespace: sourceService.Namespace != "" && targetService.Namespace != "" && sourceService.Namespace != targetService.Namespace})
	}
	sort.Slice(panoramaEdges, func(i, j int) bool {
		if panoramaEdges[i].Calls != panoramaEdges[j].Calls {
			return panoramaEdges[i].Calls > panoramaEdges[j].Calls
		}
		return panoramaEdges[i].SourceService+"\x00"+panoramaEdges[i].TargetService < panoramaEdges[j].SourceService+"\x00"+panoramaEdges[j].TargetService
	})
	return servicePanoramaData{Services: services, Edges: panoramaEdges, Revision: panoramaRevision(services, panoramaEdges), Warnings: warnings}, nil
}

func filterPanoramaData(data servicePanoramaData, r *http.Request) servicePanoramaData {
	namespace := strings.TrimSpace(r.URL.Query().Get("namespace"))
	applicationUID := strings.TrimSpace(r.URL.Query().Get("application_uid"))
	if namespace == "" && applicationUID == "" {
		return data
	}
	kept := map[string]struct{}{}
	services := make([]PanoramaService, 0, len(data.Services))
	for _, service := range data.Services {
		if namespace != "" && service.Namespace != namespace {
			continue
		}
		if applicationUID != "" && service.ApplicationUID != applicationUID {
			continue
		}
		services = append(services, service)
		kept[service.ServiceName] = struct{}{}
	}
	edges := make([]PanoramaEdge, 0, len(data.Edges))
	for _, edge := range data.Edges {
		if _, ok := kept[edge.SourceService]; !ok {
			continue
		}
		if _, ok := kept[edge.TargetService]; !ok {
			continue
		}
		edges = append(edges, edge)
	}
	data.Services, data.Edges = services, edges
	data.Revision = panoramaRevision(services, edges)
	return data
}

func (h *Handler) ServicePanoramaOverview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	minutes, err := panoramaMinutes(r)
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	data, err := h.collectServicePanorama(ctx, r, minutes)
	if err != nil {
		respondQueryError(w, err)
		return
	}
	data = filterPanoramaData(data, r)
	overview := map[string]interface{}{"total": len(data.Services), "healthy": 0, "degraded": 0, "critical": 0, "calls": int64(0), "errors": int64(0),
		"error_rate": 0.0, "avg_latency_ms": 0.0, "p95_latency_ms": 0.0, "cross_namespace_edges": 0, "cycle_count": countPanoramaCycles(data.Edges),
		"top_abnormal_services": topPanoramaServices(data.Services), "top_error_edges": topPanoramaErrorEdges(data.Edges), "top_latency_edges": topPanoramaLatencyEdges(data.Edges), "topology_revision": data.Revision, "warnings": data.Warnings}
	var latencyWeighted float64
	for _, service := range data.Services {
		switch service.Health {
		case "healthy":
			overview["healthy"] = overview["healthy"].(int) + 1
		case "degraded":
			overview["degraded"] = overview["degraded"].(int) + 1
		case "critical":
			overview["critical"] = overview["critical"].(int) + 1
		}
		overview["calls"] = overview["calls"].(int64) + service.Calls
		overview["errors"] = overview["errors"].(int64) + service.Errors
		latencyWeighted += service.AvgLatencyMS * float64(service.Calls)
	}
	if calls := overview["calls"].(int64); calls > 0 {
		overview["error_rate"] = float64(overview["errors"].(int64)) / float64(calls)
		overview["avg_latency_ms"] = latencyWeighted / float64(calls)
	}
	if p95, p95Err := h.topoRepo.P95Latency(ctx, panoramaScope(r)); p95Err == nil {
		overview["p95_latency_ms"] = p95
	} else if !panoramaNoData(p95Err) {
		data.Warnings = append(data.Warnings, "P95_LATENCY_UNAVAILABLE")
		overview["warnings"] = data.Warnings
	}
	for _, edge := range data.Edges {
		if edge.CrossNamespace {
			overview["cross_namespace_edges"] = overview["cross_namespace_edges"].(int) + 1
		}
	}
	respondJSON(w, http.StatusOK, overview)
}

func serviceGroup(service PanoramaService, requested string) (string, string, string) {
	if requested == "application" && service.ApplicationUID != "" {
		name := service.ApplicationName
		if name == "" {
			name = service.ApplicationUID
		}
		return "application:" + service.ApplicationUID, name, "application"
	}
	if requested == "application" && service.ApplicationName != "" {
		return "application:name:" + service.ApplicationName, service.ApplicationName, "application"
	}
	ns := service.Namespace
	if ns == "" {
		ns = "(未分组)"
	}
	return "namespace:" + ns, ns, "namespace"
}

func (h *Handler) ServicePanoramaMap(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	minutes, err := panoramaMinutes(r)
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	requested := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("group_by")))
	if requested == "" {
		requested = "application"
	}
	if requested != "application" && requested != "namespace" {
		respondError(w, http.StatusBadRequest, "group_by must be application or namespace")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	data, err := h.collectServicePanorama(ctx, r, minutes)
	if err != nil {
		respondQueryError(w, err)
		return
	}
	data = filterPanoramaData(data, r)
	groups := map[string]*PanoramaGroup{}
	serviceGroups := map[string]string{}
	for _, service := range data.Services {
		uid, name, effective := serviceGroup(service, requested)
		serviceGroups[service.ServiceName] = uid
		group := groups[uid]
		if group == nil {
			group = &PanoramaGroup{GroupUID: uid, Name: name, GroupBy: effective, Services: []PanoramaService{}}
			groups[uid] = group
		}
		group.Services = append(group.Services, service)
		group.ServiceCount++
		group.Calls += service.Calls
		group.Errors += service.Errors
		switch service.Health {
		case "healthy":
			group.Healthy++
		case "degraded":
			group.Degraded++
		case "critical":
			group.Critical++
		}
	}
	groupList := make([]PanoramaGroup, 0, len(groups))
	for _, group := range groups {
		if group.Calls > 0 {
			group.ErrorRate = float64(group.Errors) / float64(group.Calls)
		}
		sort.Slice(group.Services, func(i, j int) bool { return group.Services[i].ServiceName < group.Services[j].ServiceName })
		groupList = append(groupList, *group)
	}
	sort.Slice(groupList, func(i, j int) bool { return groupList[i].Name < groupList[j].Name })
	type aggregate struct {
		edge   PanoramaGroupEdge
		routes map[string]struct{}
	}
	aggregates := map[string]*aggregate{}
	for _, edge := range data.Edges {
		src, dst := serviceGroups[edge.SourceService], serviceGroups[edge.TargetService]
		if src == "" || dst == "" || src == dst {
			continue
		}
		key := src + "\x00" + dst
		item := aggregates[key]
		if item == nil {
			item = &aggregate{edge: PanoramaGroupEdge{SourceGroupUID: src, TargetGroupUID: dst}, routes: map[string]struct{}{}}
			aggregates[key] = item
		}
		item.edge.Routes++
		item.routes[edge.SourceService+"\x00"+edge.TargetService] = struct{}{}
		item.edge.Calls += edge.Calls
		item.edge.Errors += edge.Errors
		item.edge.LatencyMS += edge.LatencyMS * float64(edge.Calls)
	}
	groupEdges := make([]PanoramaGroupEdge, 0, len(aggregates))
	groupName := map[string]string{}
	for _, group := range groupList {
		groupName[group.GroupUID] = group.Name
	}
	for _, item := range aggregates {
		item.edge.SourceName = groupName[item.edge.SourceGroupUID]
		item.edge.TargetName = groupName[item.edge.TargetGroupUID]
		if item.edge.Calls > 0 {
			item.edge.ErrorRate = float64(item.edge.Errors) / float64(item.edge.Calls)
			item.edge.LatencyMS /= float64(item.edge.Calls)
		}
		groupEdges = append(groupEdges, item.edge)
	}
	sort.Slice(groupEdges, func(i, j int) bool {
		if groupEdges[i].Calls != groupEdges[j].Calls {
			return groupEdges[i].Calls > groupEdges[j].Calls
		}
		return groupEdges[i].SourceName+"\x00"+groupEdges[i].TargetName < groupEdges[j].SourceName+"\x00"+groupEdges[j].TargetName
	})
	respondJSON(w, http.StatusOK, map[string]interface{}{"group_by": requested, "groups": groupList, "services": data.Services, "aggregated_edges": groupEdges, "topology_revision": data.Revision, "warnings": data.Warnings})
}

type PanoramaMatrixCell struct {
	SourceUID         string  `json:"source_uid"`
	TargetUID         string  `json:"target_uid"`
	SourceService     string  `json:"source_service"`
	TargetService     string  `json:"target_service"`
	SourceNamespace   string  `json:"source_namespace,omitempty"`
	TargetNamespace   string  `json:"target_namespace,omitempty"`
	SourceApplication string  `json:"source_application,omitempty"`
	TargetApplication string  `json:"target_application,omitempty"`
	Calls             int64   `json:"calls"`
	Errors            int64   `json:"errors"`
	ErrorRate         float64 `json:"error_rate"`
	LatencyMS         float64 `json:"latency_ms"`
	CrossNamespace    bool    `json:"cross_namespace"`
}

func (h *Handler) ServiceDependencyMatrix(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	minutes, err := panoramaMinutes(r)
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	limit, err := panoramaMatrixLimit(r)
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	data, err := h.collectServicePanorama(ctx, r, minutes)
	if err != nil {
		respondQueryError(w, err)
		return
	}
	data = filterPanoramaData(data, r)
	totalServices := len(data.Services)
	truncated := totalServices > limit
	if truncated {
		data.Services = append([]PanoramaService(nil), data.Services[:limit]...)
		kept := map[string]struct{}{}
		for _, service := range data.Services {
			kept[service.ServiceName] = struct{}{}
		}
		filteredEdges := make([]PanoramaEdge, 0, len(data.Edges))
		for _, edge := range data.Edges {
			if _, ok := kept[edge.SourceService]; !ok {
				continue
			}
			if _, ok := kept[edge.TargetService]; !ok {
				continue
			}
			filteredEdges = append(filteredEdges, edge)
		}
		data.Edges = filteredEdges
		data.Revision = panoramaRevision(data.Services, data.Edges)
	}
	uidByName := map[string]string{}
	order := make([]string, 0, len(data.Services))
	for _, service := range data.Services {
		uid := service.EntityUID
		if uid == "" {
			uid = "service:name:" + service.ServiceName
		}
		uidByName[service.ServiceName] = uid
		order = append(order, uid)
	}
	cells := make([]PanoramaMatrixCell, 0, len(data.Edges))
	for _, edge := range data.Edges {
		cells = append(cells, PanoramaMatrixCell{SourceUID: uidByName[edge.SourceService], TargetUID: uidByName[edge.TargetService], SourceService: edge.SourceService, TargetService: edge.TargetService, SourceNamespace: edge.SourceNamespace, TargetNamespace: edge.TargetNamespace, SourceApplication: edge.SourceApplication, TargetApplication: edge.TargetApplication, Calls: edge.Calls, Errors: edge.Errors, ErrorRate: edge.ErrorRate, LatencyMS: edge.LatencyMS, CrossNamespace: edge.CrossNamespace})
	}
	if truncated {
		data.Warnings = append(data.Warnings, "MATRIX_SERVICE_LIMIT_REACHED")
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{"services": data.Services, "row_order": order, "column_order": order, "cells": cells, "topology_revision": data.Revision, "warnings": data.Warnings, "truncated": truncated, "total_services": totalServices, "limit": limit})
}

func (h *Handler) ServiceDependencies(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	uid := strings.TrimPrefix(strings.TrimSuffix(r.URL.Path, "/"), "/api/v1/services/")
	uid = strings.TrimSuffix(uid, "/dependencies")
	if decoded, decodeErr := url.PathUnescape(uid); decodeErr == nil {
		uid = decoded
	}
	if uid == "" || h.graphRepo == nil || h.graphInitErr != nil {
		respondGraphError(w, graphpkg.ErrGraphUnavailable, "service dependency graph is unavailable")
		return
	}
	scope, err := h.graphScope(r)
	if err != nil {
		respondGraphAuthorizationError(w, err)
		return
	}
	upstreamDepth, err := graphIntQuery(r, "upstream_depth", 1, 1, graphpkg.DefaultPublicMaxDepth)
	if err != nil {
		respondGraphParamError(w, err)
		return
	}
	downstreamDepth, err := graphIntQuery(r, "downstream_depth", 1, 1, graphpkg.DefaultPublicMaxDepth)
	if err != nil {
		respondGraphParamError(w, err)
		return
	}
	depth := upstreamDepth
	if downstreamDepth > depth {
		depth = downstreamDepth
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	result, err := h.graphRepo.Neighbors(ctx, scope, graphpkg.NeighborQuery{CenterEntityUID: uid, Direction: "BOTH", MaxDepth: depth, MaxVertices: graphpkg.DefaultPublicMaxVertices, MaxEdges: graphpkg.DefaultPublicMaxEdges})
	if err != nil {
		respondGraphErrorFromGo(w, err)
		return
	}
	byUID := map[string]graphpkg.Entity{}
	for _, vertex := range result.Vertices {
		byUID[vertex.EntityUID] = vertex
	}
	center, ok := byUID[uid]
	if !ok {
		center, err = h.graphRepo.GetEntity(ctx, scope, uid)
		if err != nil {
			respondGraphErrorFromGo(w, err)
			return
		}
	}
	upstream, downstream, middleware := []graphpkg.Entity{}, []graphpkg.Entity{}, []graphpkg.Entity{}
	includeMiddleware := !strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("include_middleware")), "false")
	seenUp, seenDown, seenMid := map[string]bool{}, map[string]bool{}, map[string]bool{}
	isMiddleware := func(entity graphpkg.Entity) bool {
		return entity.EntityType == "middleware" || entity.EntityType == "database" || entity.EntityType == "cache" || entity.EntityType == "mq"
	}
	for _, edge := range result.Edges {
		if edge.SourceUID == uid {
			if v, ok := byUID[edge.TargetUID]; ok && !seenDown[v.EntityUID] {
				downstream = append(downstream, v)
				seenDown[v.EntityUID] = true
			}
		}
		if edge.TargetUID == uid {
			if v, ok := byUID[edge.SourceUID]; ok && !seenUp[v.EntityUID] {
				upstream = append(upstream, v)
				seenUp[v.EntityUID] = true
			}
		}
		for _, endpoint := range []string{edge.SourceUID, edge.TargetUID} {
			if !includeMiddleware {
				break
			}
			if endpoint == uid {
				continue
			}
			if v, ok := byUID[endpoint]; ok && isMiddleware(v) && !seenMid[v.EntityUID] {
				middleware = append(middleware, v)
				seenMid[v.EntityUID] = true
			}
		}
	}
	sort.Slice(upstream, func(i, j int) bool { return upstream[i].Name < upstream[j].Name })
	sort.Slice(downstream, func(i, j int) bool { return downstream[i].Name < downstream[j].Name })
	sort.Slice(middleware, func(i, j int) bool { return middleware[i].Name < middleware[j].Name })
	respondJSON(w, http.StatusOK, map[string]interface{}{"center": center, "upstream": upstream, "downstream": downstream, "middleware": middleware, "edges": result.Edges, "cycles": panoramaGraphCycles(result), "topology_revision": panoramaSubgraphRevision(result), "meta": result.Meta})
}

// ServiceDependenciesOrDetail keeps the legacy service detail redirect while
// reserving the canonical /services/{entity_uid}/dependencies contract for the
// service panorama.  ServeMux routes the shared /services/ prefix here.
func (h *Handler) ServiceDependenciesOrDetail(w http.ResponseWriter, r *http.Request) {
	if strings.HasSuffix(strings.TrimRight(r.URL.Path, "/"), "/dependencies") {
		h.ServiceDependencies(w, r)
		return
	}
	h.ServiceDetail(w, r)
}

func topPanoramaServices(services []PanoramaService) []PanoramaService {
	out := append([]PanoramaService(nil), services...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].ErrorRate != out[j].ErrorRate {
			return out[i].ErrorRate > out[j].ErrorRate
		}
		return out[i].ServiceName < out[j].ServiceName
	})
	if len(out) > 5 {
		out = out[:5]
	}
	return out
}
func topPanoramaErrorEdges(edges []PanoramaEdge) []PanoramaEdge {
	out := append([]PanoramaEdge(nil), edges...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].ErrorRate != out[j].ErrorRate {
			return out[i].ErrorRate > out[j].ErrorRate
		}
		return out[i].SourceService+out[i].TargetService < out[j].SourceService+out[j].TargetService
	})
	if len(out) > 5 {
		out = out[:5]
	}
	return out
}
func topPanoramaLatencyEdges(edges []PanoramaEdge) []PanoramaEdge {
	out := append([]PanoramaEdge(nil), edges...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].LatencyMS != out[j].LatencyMS {
			return out[i].LatencyMS > out[j].LatencyMS
		}
		return out[i].SourceService+out[i].TargetService < out[j].SourceService+out[j].TargetService
	})
	if len(out) > 5 {
		out = out[:5]
	}
	return out
}

func countPanoramaCycles(edges []PanoramaEdge) int {
	adjacency := map[string][]string{}
	for _, edge := range edges {
		adjacency[edge.SourceService] = append(adjacency[edge.SourceService], edge.TargetService)
	}
	color := map[string]int{}
	count := 0
	var visit func(string)
	visit = func(node string) {
		color[node] = 1
		for _, next := range adjacency[node] {
			if color[next] == 1 {
				count++
			} else if color[next] == 0 {
				visit(next)
			}
		}
		color[node] = 2
	}
	for node := range adjacency {
		if color[node] == 0 {
			visit(node)
		}
	}
	return count
}

func panoramaGraphCycles(subgraph graphpkg.Subgraph) [][]string {
	adjacency := map[string][]string{}
	for _, edge := range subgraph.Edges {
		adjacency[edge.SourceUID] = append(adjacency[edge.SourceUID], edge.TargetUID)
	}
	cycles := [][]string{}
	path := []string{}
	onPath := map[string]int{}
	var visit func(string)
	visit = func(node string) {
		if len(cycles) >= 20 {
			return
		}
		if index, ok := onPath[node]; ok {
			cycle := append([]string(nil), path[index:]...)
			cycle = append(cycle, node)
			cycles = append(cycles, cycle)
			return
		}
		onPath[node] = len(path)
		path = append(path, node)
		for _, next := range adjacency[node] {
			visit(next)
		}
		delete(onPath, node)
		path = path[:len(path)-1]
	}
	for node := range adjacency {
		visit(node)
		if len(cycles) >= 20 {
			break
		}
	}
	return cycles
}

func panoramaSubgraphRevision(subgraph graphpkg.Subgraph) string {
	parts := make([]string, 0, len(subgraph.Vertices)+len(subgraph.Edges))
	for _, vertex := range subgraph.Vertices {
		parts = append(parts, "v:"+vertex.EntityUID+":"+vertex.Status)
	}
	for _, edge := range subgraph.Edges {
		parts = append(parts, "e:"+edge.EdgeUID+":"+edge.SourceUID+":"+edge.TargetUID+":"+edge.RelationType+":"+edge.Status)
	}
	sort.Strings(parts)
	hash := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(hash[:])[:16]
}
