package graph

type GraphScope struct {
	TenantID   string
	ClusterIDs map[string]struct{}
	IsAdmin    bool
}

func (s GraphScope) Allows(entity Entity) bool {
	if s.IsAdmin {
		return true
	}
	if s.TenantID == "" || entity.TenantID != s.TenantID {
		return false
	}
	if entity.ClusterID == "" {
		return true
	}
	if len(s.ClusterIDs) == 0 {
		return false
	}
	_, ok := s.ClusterIDs[entity.ClusterID]
	return ok
}

func (s GraphScope) AllowsEdge(source, target Entity) bool {
	return s.Allows(source) && s.Allows(target)
}
