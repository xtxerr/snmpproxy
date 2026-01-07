package server

import (
	"fmt"
	"sort"
	"strings"
	"time"

	pb "github.com/xtxerr/snmpproxy/internal/proto"
)

// DefaultBrowseLimit is the default number of items returned.
const DefaultBrowseLimit = 100

// MaxBrowseLimit is the maximum number of items that can be returned.
const MaxBrowseLimit = 1000

// handleBrowse dispatches browse requests to the appropriate handler.
func (srv *Server) handleBrowse(sess *Session, id uint64, req *pb.BrowseRequest) {
	// Normalize path
	p := strings.Trim(req.Path, "/")
	parts := splitPath(p)

	// Set default limit
	limit := int(req.Limit)
	if limit <= 0 {
		limit = DefaultBrowseLimit
	}
	if limit > MaxBrowseLimit {
		limit = MaxBrowseLimit
	}

	var resp *pb.BrowseResponse
	var err error

	if len(parts) == 0 || p == "" {
		resp = srv.browseRoot()
	} else {
		switch parts[0] {
		case "targets":
			resp, err = srv.browseTargets(sess, parts[1:], req, limit)
		case "tags":
			resp, err = srv.browseTags(sess, parts[1:], req, limit)
		case "server":
			resp, err = srv.browseServer(sess, parts[1:], req)
		case "session":
			resp, err = srv.browseSession(sess, parts[1:], req)
		default:
			err = fmt.Errorf("unknown path: /%s", parts[0])
		}
	}

	if err != nil {
		sess.Send(newError(id, ErrInvalidRequest, err.Error()))
		return
	}

	sess.Send(&pb.Envelope{
		Id:      id,
		Payload: &pb.Envelope_BrowseResp{BrowseResp: resp},
	})
}

// ============================================================================
// Root
// ============================================================================

func (srv *Server) browseRoot() *pb.BrowseResponse {
	srv.mu.RLock()
	targetCount := int32(len(srv.targets))
	srv.mu.RUnlock()

	return &pb.BrowseResponse{
		Path: "/",
		Nodes: []*pb.BrowseNode{
			{Name: "targets", Type: pb.NodeType_NODE_DIRECTORY, TargetCount: targetCount, Description: "All monitored targets"},
			{Name: "tags", Type: pb.NodeType_NODE_DIRECTORY, Description: "Browse by tags"},
			{Name: "server", Type: pb.NodeType_NODE_DIRECTORY, Description: "Server status and config"},
			{Name: "session", Type: pb.NodeType_NODE_DIRECTORY, Description: "Current session info"},
		},
	}
}

// ============================================================================
// Targets
// ============================================================================

func (srv *Server) browseTargets(sess *Session, parts []string, req *pb.BrowseRequest, limit int) (*pb.BrowseResponse, error) {
	// /targets - list all targets with optional filters
	if len(parts) == 0 {
		return srv.browseTargetList(req, limit)
	}

	// /targets/<id> - target details or sub-resource
	return srv.browseTargetByID(parts, req)
}

func (srv *Server) browseTargetList(req *pb.BrowseRequest, limit int) (*pb.BrowseResponse, error) {
	srv.mu.RLock()
	defer srv.mu.RUnlock()

	// Build filter
	filter := &Filter{
		Tags:     req.FilterTags,
		State:    req.FilterState,
		Protocol: req.FilterProtocol,
		Host:     req.FilterHost,
	}

	// Get candidate IDs
	var targetIDs []string
	if filter.IsEmpty() {
		// All targets
		targetIDs = make([]string, 0, len(srv.targets))
		for id := range srv.targets {
			targetIDs = append(targetIDs, id)
		}
	} else {
		// Filtered
		candidates := srv.indices.Query(filter)
		if candidates == nil {
			// Filter returned nil (no results)
			return &pb.BrowseResponse{
				Path:       "/targets",
				Nodes:      []*pb.BrowseNode{},
				TotalCount: 0,
			}, nil
		}
		targetIDs = candidates.ToSlice()
	}

	sort.Strings(targetIDs)
	totalCount := len(targetIDs)

	// Apply cursor (cursor = last ID from previous page)
	if req.Cursor != "" {
		idx := sort.SearchStrings(targetIDs, req.Cursor)
		if idx < len(targetIDs) && targetIDs[idx] == req.Cursor {
			idx++ // Skip the cursor item itself
		}
		if idx >= len(targetIDs) {
			return &pb.BrowseResponse{
				Path:       "/targets",
				Nodes:      []*pb.BrowseNode{},
				TotalCount: int32(totalCount),
				HasMore:    false,
			}, nil
		}
		targetIDs = targetIDs[idx:]
	}

	// Apply limit
	hasMore := len(targetIDs) > limit
	if hasMore {
		targetIDs = targetIDs[:limit]
	}

	// Build nodes
	nodes := make([]*pb.BrowseNode, 0, len(targetIDs))
	var nextCursor string

	for _, id := range targetIDs {
		t, ok := srv.targets[id]
		if !ok {
			continue
		}

		node := &pb.BrowseNode{
			Name:        id,
			Type:        pb.NodeType_NODE_TARGET,
			Description: t.Description,
		}

		if req.LongFormat {
			node.Target = srv.targetToProto(t, true)
		}

		nodes = append(nodes, node)
		nextCursor = id
	}

	resp := &pb.BrowseResponse{
		Path:       "/targets",
		Nodes:      nodes,
		TotalCount: int32(totalCount),
		HasMore:    hasMore,
	}
	if hasMore {
		resp.NextCursor = nextCursor
	}

	return resp, nil
}

func (srv *Server) browseTargetByID(parts []string, req *pb.BrowseRequest) (*pb.BrowseResponse, error) {
	targetID := parts[0]

	srv.mu.RLock()
	t, ok := srv.targets[targetID]
	srv.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("target not found: %s", targetID)
	}

	basePath := "/targets/" + targetID

	// /targets/<id>
	if len(parts) == 1 {
		return &pb.BrowseResponse{
			Path: basePath,
			Nodes: []*pb.BrowseNode{
				{Name: "config", Type: pb.NodeType_NODE_INFO, Description: "Protocol configuration"},
				{Name: "stats", Type: pb.NodeType_NODE_INFO, Description: "Polling statistics"},
				{Name: "history", Type: pb.NodeType_NODE_DIRECTORY, Description: "Sample history"},
				{Name: targetID, Type: pb.NodeType_NODE_TARGET, Target: srv.targetToProto(t, req.LongFormat)},
			},
		}, nil
	}

	// /targets/<id>/<sub>
	switch parts[1] {
	case "config":
		return srv.browseTargetConfig(t, basePath)
	case "stats":
		return srv.browseTargetStats(t, basePath)
	case "history":
		return srv.browseTargetHistory(t, basePath, req)
	default:
		return nil, fmt.Errorf("unknown target sub-path: %s", parts[1])
	}
}

func (srv *Server) browseTargetConfig(t *Target, basePath string) (*pb.BrowseResponse, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	info := map[string]string{
		"protocol":    t.Protocol,
		"interval_ms": fmt.Sprintf("%d", t.IntervalMs),
		"buffer_size": fmt.Sprintf("%d", t.bufSize),
		"persistent":  fmt.Sprintf("%v", t.Persistent),
	}

	if t.SNMP != nil {
		info["host"] = t.Host
		info["port"] = fmt.Sprintf("%d", t.Port)
		info["oid"] = t.OID

		if t.SNMP.TimeoutMs > 0 {
			info["timeout_ms"] = fmt.Sprintf("%d", t.SNMP.TimeoutMs)
		}
		if t.SNMP.Retries > 0 {
			info["retries"] = fmt.Sprintf("%d", t.SNMP.Retries)
		}

		if v2c := t.SNMP.GetV2C(); v2c != nil {
			info["version"] = "2c"
			info["community"] = maskString(v2c.Community)
		} else if v3 := t.SNMP.GetV3(); v3 != nil {
			info["version"] = "3"
			info["security_name"] = v3.SecurityName
			info["security_level"] = v3.SecurityLevel
			if v3.AuthProtocol != "" {
				info["auth_protocol"] = v3.AuthProtocol
				info["auth_password"] = "****"
			}
			if v3.PrivProtocol != "" {
				info["priv_protocol"] = v3.PrivProtocol
				info["priv_password"] = "****"
			}
			if v3.ContextName != "" {
				info["context_name"] = v3.ContextName
			}
		}
	}

	return &pb.BrowseResponse{
		Path: basePath + "/config",
		Nodes: []*pb.BrowseNode{
			{Name: "config", Type: pb.NodeType_NODE_INFO, Info: info},
		},
	}, nil
}

func (srv *Server) browseTargetStats(t *Target, basePath string) (*pb.BrowseResponse, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	var avgPollMs int32
	if t.PollsTotal > 0 {
		avgPollMs = int32(t.PollMsTotal / t.PollsTotal)
	}

	minPollMs := t.PollMsMin
	if minPollMs < 0 {
		minPollMs = 0
	}

	var successRate string
	if t.PollsTotal > 0 {
		rate := float64(t.PollsOK) / float64(t.PollsTotal) * 100
		successRate = fmt.Sprintf("%.2f%%", rate)
	} else {
		successRate = "N/A"
	}

	info := map[string]string{
		"state":          t.State,
		"error_count":    fmt.Sprintf("%d", t.ErrCount),
		"last_error":     t.LastError,
		"polls_total":    fmt.Sprintf("%d", t.PollsTotal),
		"polls_success":  fmt.Sprintf("%d", t.PollsOK),
		"polls_failed":   fmt.Sprintf("%d", t.PollsFailed),
		"success_rate":   successRate,
		"avg_poll_ms":    fmt.Sprintf("%d", avgPollMs),
		"min_poll_ms":    fmt.Sprintf("%d", minPollMs),
		"max_poll_ms":    fmt.Sprintf("%d", t.PollMsMax),
		"buffer_used":    fmt.Sprintf("%d/%d", t.count, t.bufSize),
		"created_at":     t.CreatedAt.Format(time.RFC3339),
		"owner_count":    fmt.Sprintf("%d", len(t.Owners)),
	}

	if t.LastPollMs > 0 {
		info["last_poll"] = time.UnixMilli(t.LastPollMs).Format(time.RFC3339)
	}

	return &pb.BrowseResponse{
		Path: basePath + "/stats",
		Nodes: []*pb.BrowseNode{
			{Name: "stats", Type: pb.NodeType_NODE_INFO, Info: info},
		},
	}, nil
}

func (srv *Server) browseTargetHistory(t *Target, basePath string, req *pb.BrowseRequest) (*pb.BrowseResponse, error) {
	limit := int(req.Limit)
	if limit <= 0 {
		limit = 10
	}
	if limit > 1000 {
		limit = 1000
	}

	samples := t.ReadLastN(limit)

	nodes := make([]*pb.BrowseNode, len(samples))
	for i, s := range samples {
		ts := time.UnixMilli(s.TimestampMs).Format("15:04:05.000")
		var desc string
		if s.Valid {
			if s.Text != "" {
				desc = s.Text
			} else {
				desc = fmt.Sprintf("%d", s.Counter)
			}
		} else {
			desc = "ERROR: " + s.Error
		}

		nodes[i] = &pb.BrowseNode{
			Name:        ts,
			Type:        pb.NodeType_NODE_INFO,
			Description: desc,
			Info: map[string]string{
				"timestamp_ms": fmt.Sprintf("%d", s.TimestampMs),
				"counter":      fmt.Sprintf("%d", s.Counter),
				"text":         s.Text,
				"valid":        fmt.Sprintf("%v", s.Valid),
				"error":        s.Error,
				"poll_ms":      fmt.Sprintf("%d", s.PollMs),
			},
		}
	}

	t.mu.RLock()
	totalBuffered := t.count
	t.mu.RUnlock()

	return &pb.BrowseResponse{
		Path:       basePath + "/history",
		Nodes:      nodes,
		TotalCount: int32(totalBuffered),
	}, nil
}

// ============================================================================
// Tags
// ============================================================================

func (srv *Server) browseTags(sess *Session, parts []string, req *pb.BrowseRequest, limit int) (*pb.BrowseResponse, error) {
	tagPath := strings.Join(parts, "/")
	basePath := "/tags"
	if tagPath != "" {
		basePath = "/tags/" + tagPath
	}

	// Get sub-tags and direct targets at this level
	subTags, directTargetIDs := srv.indices.ListTagChildren(tagPath)

	// Check if last part is a target ID
	if len(parts) > 0 {
		possibleTargetID := parts[len(parts)-1]
		srv.mu.RLock()
		_, isTarget := srv.targets[possibleTargetID]
		srv.mu.RUnlock()

		if isTarget {
			// Check if parent path is a valid tag for this target
			parentPath := ""
			if len(parts) > 1 {
				parentPath = strings.Join(parts[:len(parts)-1], "/")
			}
			if parentPath == "" || srv.indices.HasTag(possibleTargetID, parentPath) {
				// It's a target - delegate to target browsing
				return srv.browseTargetByID([]string{possibleTargetID}, req)
			}
		}
	}

	// Check for target sub-paths under tags (e.g., /tags/location/dc1/abc123/config)
	if len(parts) >= 2 {
		// Try to find where the target ID starts
		for i := len(parts) - 1; i >= 1; i-- {
			possibleTargetID := parts[i]
			srv.mu.RLock()
			_, isTarget := srv.targets[possibleTargetID]
			srv.mu.RUnlock()

			if isTarget {
				tagPrefix := strings.Join(parts[:i], "/")
				if srv.indices.HasTag(possibleTargetID, tagPrefix) {
					// Delegate to target browsing with remaining path
					return srv.browseTargetByID(parts[i:], req)
				}
			}
		}
	}

	// Apply filters to targets if any filters are set
	var filteredTargetIDs []string
	if req.FilterState != "" || req.FilterProtocol != "" || req.FilterHost != "" {
		srv.mu.RLock()
		filter := &Filter{
			State:    req.FilterState,
			Protocol: req.FilterProtocol,
			Host:     req.FilterHost,
		}
		candidates := srv.indices.Query(filter)
		srv.mu.RUnlock()

		if candidates != nil {
			// Intersect with direct targets at this tag level
			directSet := NewStringSet()
			for _, id := range directTargetIDs {
				directSet.Add(id)
			}
			intersection := candidates.Intersect(directSet)
			filteredTargetIDs = intersection.ToSlice()
		}
	} else {
		filteredTargetIDs = directTargetIDs
	}

	// Build nodes: first subdirectories, then targets
	var nodes []*pb.BrowseNode

	// Add sub-tags as directories
	for _, subTag := range subTags {
		fullTag := subTag
		if tagPath != "" {
			fullTag = tagPath + "/" + subTag
		}
		count := srv.indices.CountByTag(fullTag)

		nodes = append(nodes, &pb.BrowseNode{
			Name:        subTag,
			Type:        pb.NodeType_NODE_DIRECTORY,
			TargetCount: int32(count),
		})
	}

	// Add targets
	srv.mu.RLock()
	for _, targetID := range filteredTargetIDs {
		t, ok := srv.targets[targetID]
		if !ok {
			continue
		}

		node := &pb.BrowseNode{
			Name:        targetID,
			Type:        pb.NodeType_NODE_TARGET,
			Description: t.Description,
		}

		if req.LongFormat {
			node.Target = srv.targetToProto(t, true)
		}

		nodes = append(nodes, node)
	}
	srv.mu.RUnlock()

	// Total count: directories + targets
	totalTagTargets := srv.indices.CountByTag(tagPath)
	if tagPath == "" {
		srv.mu.RLock()
		totalTagTargets = len(srv.targets)
		srv.mu.RUnlock()
	}

	return &pb.BrowseResponse{
		Path:       basePath,
		Nodes:      nodes,
		TotalCount: int32(totalTagTargets),
	}, nil
}

// ============================================================================
// Server
// ============================================================================

func (srv *Server) browseServer(sess *Session, parts []string, req *pb.BrowseRequest) (*pb.BrowseResponse, error) {
	if len(parts) == 0 {
		return &pb.BrowseResponse{
			Path: "/server",
			Nodes: []*pb.BrowseNode{
				{Name: "status", Type: pb.NodeType_NODE_INFO, Description: "Server status"},
				{Name: "config", Type: pb.NodeType_NODE_INFO, Description: "Runtime configuration"},
				{Name: "sessions", Type: pb.NodeType_NODE_DIRECTORY, Description: "Connected sessions"},
			},
		}, nil
	}

	switch parts[0] {
	case "status":
		return srv.browseServerStatus()
	case "config":
		return srv.browseServerConfig()
	case "sessions":
		return srv.browseServerSessions(parts[1:], req)
	default:
		return nil, fmt.Errorf("unknown server path: %s", parts[0])
	}
}

func (srv *Server) browseServerStatus() (*pb.BrowseResponse, error) {
	srv.mu.RLock()
	var sessActive, sessLost int
	for _, s := range srv.sessions {
		if s.IsLost() {
			sessLost++
		} else {
			sessActive++
		}
	}
	targetCount := len(srv.targets)
	srv.mu.RUnlock()

	// Get state counts from index
	stats := srv.indices.Stats()
	polling := stats["state_polling"]
	unreachable := stats["state_unreachable"]
	errored := stats["state_error"]

	heapSize, queueUsed := srv.poller.Stats()

	info := map[string]string{
		"version":             Version,
		"uptime":              formatDuration(time.Since(srv.startedAt)),
		"started_at":          srv.startedAt.Format(time.RFC3339),
		"sessions_active":     fmt.Sprintf("%d", sessActive),
		"sessions_lost":       fmt.Sprintf("%d", sessLost),
		"targets_total":       fmt.Sprintf("%d", targetCount),
		"targets_polling":     fmt.Sprintf("%d", polling),
		"targets_unreachable": fmt.Sprintf("%d", unreachable),
		"targets_error":       fmt.Sprintf("%d", errored),
		"poller_workers":      fmt.Sprintf("%d", srv.cfg.Poller.Workers),
		"poller_queue":        fmt.Sprintf("%d/%d", queueUsed, srv.cfg.Poller.QueueSize),
		"poller_heap":         fmt.Sprintf("%d", heapSize),
		"polls_total":         fmt.Sprintf("%d", srv.pollsTotal.Load()),
		"polls_success":       fmt.Sprintf("%d", srv.pollsOK.Load()),
		"polls_failed":        fmt.Sprintf("%d", srv.pollsFailed.Load()),
	}

	return &pb.BrowseResponse{
		Path: "/server/status",
		Nodes: []*pb.BrowseNode{
			{Name: "status", Type: pb.NodeType_NODE_INFO, Info: info},
		},
	}, nil
}

func (srv *Server) browseServerConfig() (*pb.BrowseResponse, error) {
	srv.runtimeMu.RLock()
	info := map[string]string{
		"default_timeout_ms":  fmt.Sprintf("%d", srv.defaultTimeoutMs),
		"default_retries":     fmt.Sprintf("%d", srv.defaultRetries),
		"default_buffer_size": fmt.Sprintf("%d", srv.defaultBufferSize),
		"min_interval_ms":     fmt.Sprintf("%d", srv.minIntervalMs),
		"poller_workers":      fmt.Sprintf("%d", srv.cfg.Poller.Workers),
		"poller_queue_size":   fmt.Sprintf("%d", srv.cfg.Poller.QueueSize),
		"reconnect_window":    fmt.Sprintf("%ds", srv.cfg.Session.ReconnectWindowSec),
	}
	srv.runtimeMu.RUnlock()

	return &pb.BrowseResponse{
		Path: "/server/config",
		Nodes: []*pb.BrowseNode{
			{Name: "config", Type: pb.NodeType_NODE_INFO, Info: info},
		},
	}, nil
}

func (srv *Server) browseServerSessions(parts []string, req *pb.BrowseRequest) (*pb.BrowseResponse, error) {
	srv.mu.RLock()
	defer srv.mu.RUnlock()

	if len(parts) == 0 {
		nodes := make([]*pb.BrowseNode, 0, len(srv.sessions))
		for id, sess := range srv.sessions {
			status := "connected"
			if sess.IsLost() {
				status = "lost"
			}
			nodes = append(nodes, &pb.BrowseNode{
				Name:        id,
				Type:        pb.NodeType_NODE_INFO,
				Description: status,
				Info: map[string]string{
					"token_id": sess.TokenID,
					"status":   status,
					"created":  sess.CreatedAt.Format(time.RFC3339),
				},
			})
		}

		// Sort by ID
		sort.Slice(nodes, func(i, j int) bool {
			return nodes[i].Name < nodes[j].Name
		})

		return &pb.BrowseResponse{
			Path:       "/server/sessions",
			Nodes:      nodes,
			TotalCount: int32(len(nodes)),
		}, nil
	}

	// /server/sessions/<id>
	sess, ok := srv.sessions[parts[0]]
	if !ok {
		return nil, fmt.Errorf("session not found: %s", parts[0])
	}

	status := "connected"
	if sess.IsLost() {
		status = "lost"
	}

	subs := sess.GetSubscriptions()
	var owned []string
	for tid, t := range srv.targets {
		t.mu.RLock()
		if t.Owners[sess.ID] {
			owned = append(owned, tid)
		}
		t.mu.RUnlock()
	}
	sort.Strings(owned)

	info := map[string]string{
		"session_id":    sess.ID,
		"token_id":      sess.TokenID,
		"status":        status,
		"created":       sess.CreatedAt.Format(time.RFC3339),
		"owned_count":   fmt.Sprintf("%d", len(owned)),
		"owned":         strings.Join(owned, ", "),
		"subscribed":    fmt.Sprintf("%d", len(subs)),
		"subscriptions": strings.Join(subs, ", "),
	}

	return &pb.BrowseResponse{
		Path: "/server/sessions/" + parts[0],
		Nodes: []*pb.BrowseNode{
			{Name: parts[0], Type: pb.NodeType_NODE_INFO, Info: info},
		},
	}, nil
}

// ============================================================================
// Session (current)
// ============================================================================

func (srv *Server) browseSession(sess *Session, parts []string, req *pb.BrowseRequest) (*pb.BrowseResponse, error) {
	if len(parts) == 0 {
		return &pb.BrowseResponse{
			Path: "/session",
			Nodes: []*pb.BrowseNode{
				{Name: "info", Type: pb.NodeType_NODE_INFO, Description: "Session details"},
				{Name: "owned", Type: pb.NodeType_NODE_DIRECTORY, Description: "Targets you own"},
				{Name: "subscribed", Type: pb.NodeType_NODE_DIRECTORY, Description: "Your subscriptions"},
			},
		}, nil
	}

	switch parts[0] {
	case "info":
		return srv.browseSessionInfo(sess)
	case "owned":
		return srv.browseSessionOwned(sess, req)
	case "subscribed":
		return srv.browseSessionSubscribed(sess, req)
	default:
		return nil, fmt.Errorf("unknown session path: %s", parts[0])
	}
}

func (srv *Server) browseSessionInfo(sess *Session) (*pb.BrowseResponse, error) {
	subs := sess.GetSubscriptions()

	srv.mu.RLock()
	var owned []string
	for tid, t := range srv.targets {
		t.mu.RLock()
		if t.Owners[sess.ID] {
			owned = append(owned, tid)
		}
		t.mu.RUnlock()
	}
	srv.mu.RUnlock()

	info := map[string]string{
		"session_id":   sess.ID,
		"token_id":     sess.TokenID,
		"created":      sess.CreatedAt.Format(time.RFC3339),
		"owned_count":  fmt.Sprintf("%d", len(owned)),
		"subscribed":   fmt.Sprintf("%d", len(subs)),
	}

	return &pb.BrowseResponse{
		Path: "/session/info",
		Nodes: []*pb.BrowseNode{
			{Name: "info", Type: pb.NodeType_NODE_INFO, Info: info},
		},
	}, nil
}

func (srv *Server) browseSessionOwned(sess *Session, req *pb.BrowseRequest) (*pb.BrowseResponse, error) {
	srv.mu.RLock()
	defer srv.mu.RUnlock()

	var nodes []*pb.BrowseNode
	for tid, t := range srv.targets {
		t.mu.RLock()
		isOwner := t.Owners[sess.ID]
		t.mu.RUnlock()

		if !isOwner {
			continue
		}

		node := &pb.BrowseNode{
			Name:        tid,
			Type:        pb.NodeType_NODE_TARGET,
			Description: t.Description,
		}

		if req.LongFormat {
			node.Target = srv.targetToProto(t, true)
		}

		nodes = append(nodes, node)
	}

	// Sort by ID
	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i].Name < nodes[j].Name
	})

	return &pb.BrowseResponse{
		Path:       "/session/owned",
		Nodes:      nodes,
		TotalCount: int32(len(nodes)),
	}, nil
}

func (srv *Server) browseSessionSubscribed(sess *Session, req *pb.BrowseRequest) (*pb.BrowseResponse, error) {
	subs := sess.GetSubscriptions()

	srv.mu.RLock()
	defer srv.mu.RUnlock()

	nodes := make([]*pb.BrowseNode, 0, len(subs))
	for _, tid := range subs {
		t, ok := srv.targets[tid]
		if !ok {
			continue
		}

		node := &pb.BrowseNode{
			Name:        tid,
			Type:        pb.NodeType_NODE_TARGET,
			Description: t.Description,
		}

		if req.LongFormat {
			node.Target = srv.targetToProto(t, true)
		}

		nodes = append(nodes, node)
	}

	// Sort by ID
	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i].Name < nodes[j].Name
	})

	return &pb.BrowseResponse{
		Path:       "/session/subscribed",
		Nodes:      nodes,
		TotalCount: int32(len(nodes)),
	}, nil
}

// ============================================================================
// Helpers
// ============================================================================

func splitPath(p string) []string {
	if p == "" {
		return nil
	}
	return strings.Split(p, "/")
}

func maskString(s string) string {
	if len(s) <= 3 {
		return "***"
	}
	return s[:3] + "***"
}

func formatDuration(d time.Duration) string {
	days := int(d.Hours() / 24)
	hours := int(d.Hours()) % 24
	mins := int(d.Minutes()) % 60

	if days > 0 {
		return fmt.Sprintf("%dd %dh %dm", days, hours, mins)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh %dm", hours, mins)
	}
	return fmt.Sprintf("%dm", mins)
}

// targetToProto converts a Target to protobuf.
// If includeSensitive is true, includes owners/subscribers lists.
func (srv *Server) targetToProto(t *Target, includeSensitive bool) *pb.Target {
	t.mu.RLock()
	defer t.mu.RUnlock()

	var avgPollMs int32
	if t.PollsTotal > 0 {
		avgPollMs = int32(t.PollMsTotal / t.PollsTotal)
	}

	minPollMs := t.PollMsMin
	if minPollMs < 0 {
		minPollMs = 0
	}

	// Count subscribers
	var subscriberCount int32
	srv.mu.RLock()
	for _, sess := range srv.sessions {
		if sess.IsSubscribed(t.ID) {
			subscriberCount++
		}
	}
	srv.mu.RUnlock()

	target := &pb.Target{
		Id:              t.ID,
		Description:     t.Description,
		Tags:            t.Tags,
		Persistent:      t.Persistent,
		Protocol:        t.Protocol,
		IntervalMs:      t.IntervalMs,
		BufferSize:      uint32(t.bufSize),
		State:           t.State,
		LastPollMs:      t.LastPollMs,
		LastError:       t.LastError,
		ErrorCount:      int32(t.ErrCount),
		SamplesBuffered: int32(t.count),
		OwnerCount:      int32(len(t.Owners)),
		SubscriberCount: subscriberCount,
		CreatedAtMs:     t.CreatedAt.UnixMilli(),
		PollsTotal:      t.PollsTotal,
		PollsSuccess:    t.PollsOK,
		PollsFailed:     t.PollsFailed,
		AvgPollMs:       avgPollMs,
		MinPollMs:       minPollMs,
		MaxPollMs:       t.PollMsMax,
	}

	if includeSensitive {
		target.Owners = make([]string, 0, len(t.Owners))
		for owner := range t.Owners {
			target.Owners = append(target.Owners, owner)
		}
		sort.Strings(target.Owners)

		// Subscribers are in sessions, not target
		srv.mu.RLock()
		for _, sess := range srv.sessions {
			if sess.IsSubscribed(t.ID) {
				target.Subscribers = append(target.Subscribers, sess.ID)
			}
		}
		srv.mu.RUnlock()
		sort.Strings(target.Subscribers)
	}

	// Protocol config
	if t.SNMP != nil {
		target.ProtocolConfig = &pb.Target_Snmp{Snmp: t.SNMP}
	}

	return target
}

// newError creates an error envelope.
func newError(id uint64, code int32, msg string) *pb.Envelope {
	return &pb.Envelope{
		Id: id,
		Payload: &pb.Envelope_Error{
			Error: &pb.Error{Code: code, Message: msg},
		},
	}
}

// Error codes
const (
	ErrUnknown          = 1
	ErrAuthFailed       = 2
	ErrNotAuthenticated = 3
	ErrInvalidRequest   = 4
	ErrTargetNotFound   = 5
	ErrInternal         = 6
)
