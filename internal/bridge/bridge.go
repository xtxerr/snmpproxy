// Package bridge converts between handler types and protobuf messages.
package bridge

import (
	"github.com/xtxerr/snmpproxy/internal/handler"
	"github.com/xtxerr/snmpproxy/internal/store"
)

// ============================================================================
// Namespace Conversions
// ============================================================================

// NamespaceToProto converts store.Namespace to proto Namespace fields.
func NamespaceToProto(ns *store.Namespace) map[string]interface{} {
	if ns == nil {
		return nil
	}
	return map[string]interface{}{
		"name":          ns.Name,
		"description":   ns.Description,
		"created_at_ms": ns.CreatedAt.UnixMilli(),
		"updated_at_ms": ns.UpdatedAt.UnixMilli(),
		"version":       ns.Version,
	}
}

// NamespaceInfoToProto converts handler.NamespaceInfo to proto.
func NamespaceInfoToProto(info *handler.NamespaceInfo) map[string]interface{} {
	if info == nil {
		return nil
	}
	return map[string]interface{}{
		"name":        info.Name,
		"description": info.Description,
		"stats": map[string]interface{}{
			"target_count":     info.TargetCount,
			"poller_count":     info.PollerCount,
			"pollers_running":  info.PollersRunning,
			"pollers_up":       info.PollersUp,
			"pollers_down":     info.PollersDown,
			"tree_node_count":  info.TreeNodeCount,
		},
		"created_at_ms": info.CreatedAt.UnixMilli(),
		"updated_at_ms": info.UpdatedAt.UnixMilli(),
		"version":       info.Version,
	}
}

// ============================================================================
// Target Conversions
// ============================================================================

// TargetToProto converts store.Target to proto Target fields.
func TargetToProto(t *store.Target) map[string]interface{} {
	if t == nil {
		return nil
	}
	return map[string]interface{}{
		"namespace":     t.Namespace,
		"name":          t.Name,
		"description":   t.Description,
		"labels":        t.Labels,
		"created_at_ms": t.CreatedAt.UnixMilli(),
		"updated_at_ms": t.UpdatedAt.UnixMilli(),
		"version":       t.Version,
	}
}

// TargetInfoToProto converts handler.TargetInfo to proto.
func TargetInfoToProto(info *handler.TargetInfo) map[string]interface{} {
	if info == nil {
		return nil
	}
	return map[string]interface{}{
		"namespace":   info.Namespace,
		"name":        info.Name,
		"description": info.Description,
		"labels":      info.Labels,
		"stats": map[string]interface{}{
			"poller_count":    info.PollerCount,
			"pollers_running": info.PollersRunning,
			"pollers_up":      info.PollersUp,
			"pollers_down":    info.PollersDown,
			"polls_total":     info.PollsTotal,
			"polls_success":   info.PollsSuccess,
			"polls_failed":    info.PollsFailed,
		},
		"created_at_ms": info.CreatedAt.UnixMilli(),
		"updated_at_ms": info.UpdatedAt.UnixMilli(),
		"version":       info.Version,
	}
}

// ============================================================================
// Poller Conversions
// ============================================================================

// PollerToProto converts store.Poller to proto Poller fields.
func PollerToProto(p *store.Poller) map[string]interface{} {
	if p == nil {
		return nil
	}
	return map[string]interface{}{
		"namespace":       p.Namespace,
		"target":          p.Target,
		"name":            p.Name,
		"description":     p.Description,
		"protocol":        p.Protocol,
		"admin_state":     p.AdminState,
		"created_at_ms":   p.CreatedAt.UnixMilli(),
		"updated_at_ms":   p.UpdatedAt.UnixMilli(),
		"version":         p.Version,
	}
}

// PollerInfoToProto converts handler.PollerInfo to proto.
func PollerInfoToProto(info *handler.PollerInfo) map[string]interface{} {
	if info == nil {
		return nil
	}
	result := map[string]interface{}{
		"namespace":            info.Namespace,
		"target":               info.Target,
		"name":                 info.Name,
		"description":          info.Description,
		"protocol":             info.Protocol,
		"admin_state":          info.AdminState,
		"oper_state":           info.OperState,
		"health_state":         info.HealthState,
		"last_error":           info.LastError,
		"consecutive_failures": info.ConsecutiveFailures,
		"stats": map[string]interface{}{
			"polls_total":     info.PollsTotal,
			"polls_success":   info.PollsSuccess,
			"polls_failed":    info.PollsFailed,
			"avg_poll_ms":     info.AvgPollMs,
			"min_poll_ms":     info.MinPollMs,
			"max_poll_ms":     info.MaxPollMs,
			"last_value":      info.LastValue,
			"samples_buffered": info.SamplesBuffered,
		},
		"polling": map[string]interface{}{
			"interval_ms": info.IntervalMs,
			"timeout_ms":  info.TimeoutMs,
			"retries":     info.Retries,
			"buffer_size": info.BufferSize,
		},
		"created_at_ms":    info.CreatedAt.UnixMilli(),
		"updated_at_ms":    info.UpdatedAt.UnixMilli(),
		"version":          info.Version,
	}

	if info.LastPollAt != nil {
		result["last_poll_ms"] = info.LastPollAt.UnixMilli()
	}
	if info.LastSuccessAt != nil {
		result["last_success_ms"] = info.LastSuccessAt.UnixMilli()
	}
	if info.LastFailureAt != nil {
		result["last_failure_ms"] = info.LastFailureAt.UnixMilli()
	}

	return result
}

// ============================================================================
// Sample Conversions
// ============================================================================

// SampleToProto converts store.Sample to proto Sample fields.
func SampleToProto(s *store.Sample) map[string]interface{} {
	if s == nil {
		return nil
	}
	result := map[string]interface{}{
		"namespace":    s.Namespace,
		"target":       s.Target,
		"poller":       s.Poller,
		"timestamp_ms": s.TimestampMs,
		"valid":        s.Valid,
		"error":        s.Error,
		"poll_ms":      s.PollMs,
	}

	if s.ValueCounter != nil {
		result["counter"] = *s.ValueCounter
	}
	if s.ValueText != nil {
		result["text"] = *s.ValueText
	}
	if s.ValueGauge != nil {
		result["gauge"] = *s.ValueGauge
	}

	return result
}

// SamplesToProto converts a slice of samples.
func SamplesToProto(samples []store.Sample) []map[string]interface{} {
	result := make([]map[string]interface{}, len(samples))
	for i, s := range samples {
		result[i] = SampleToProto(&s)
	}
	return result
}

// ============================================================================
// Browse Entry Conversions
// ============================================================================

// BrowseEntryToProto converts handler.BrowseEntry to proto.
func BrowseEntryToProto(e *handler.BrowseEntry) map[string]interface{} {
	if e == nil {
		return nil
	}
	result := map[string]interface{}{
		"name":        e.Name,
		"type":        e.Type,
		"description": e.Description,
	}

	if e.Target != "" {
		result["target"] = e.Target
	}
	if e.Poller != "" {
		result["poller"] = e.Poller
	}
	if e.ChildCount > 0 {
		result["child_count"] = e.ChildCount
	}
	if e.PollerCount > 0 {
		result["poller_count"] = e.PollerCount
	}

	return result
}

// BrowseEntriesToProto converts a slice of browse entries.
func BrowseEntriesToProto(entries []handler.BrowseEntry) []map[string]interface{} {
	result := make([]map[string]interface{}, len(entries))
	for i, e := range entries {
		result[i] = BrowseEntryToProto(&e)
	}
	return result
}

// ============================================================================
// Secret Conversions
// ============================================================================

// SecretInfoToProto converts handler.SecretInfo to proto.
func SecretInfoToProto(s *handler.SecretInfo) map[string]interface{} {
	if s == nil {
		return nil
	}
	return map[string]interface{}{
		"namespace":     s.Namespace,
		"name":          s.Name,
		"type":          s.Type,
		"reference_count": s.ReferenceCount,
		"created_at_ms": s.CreatedAt.UnixMilli(),
		"updated_at_ms": s.UpdatedAt.UnixMilli(),
	}
}

// ============================================================================
// Server Status Conversions
// ============================================================================

// ServerStatusToProto converts handler.ServerStatusResponse to proto.
func ServerStatusToProto(s *handler.ServerStatusResponse) map[string]interface{} {
	if s == nil {
		return nil
	}
	return map[string]interface{}{
		"version":         s.Version,
		"uptime_ms":       s.UptimeMs,
		"started_at_ms":   s.StartedAtMs,
		"namespace_count": s.NamespaceCount,
		"target_count":    s.TargetCount,
		"poller_count":    s.PollerCount,
		"pollers_running": s.PollersRunning,
		"pollers_up":      s.PollersUp,
		"pollers_down":    s.PollersDown,
		"sessions_active": s.SessionsActive,
		"sessions_lost":   s.SessionsLost,
		"polls_total":     s.PollsTotal,
		"polls_success":   s.PollsSuccess,
		"polls_failed":    s.PollsFailed,
		"scheduler": map[string]interface{}{
			"heap_size":  s.SchedulerHeapSize,
			"queue_used": s.SchedulerQueueUsed,
		},
	}
}

// SessionInfoToProto converts handler.SessionInfoResponse to proto.
func SessionInfoToProto(s *handler.SessionInfoResponse) map[string]interface{} {
	if s == nil {
		return nil
	}
	return map[string]interface{}{
		"session_id":    s.SessionID,
		"token_id":      s.TokenID,
		"namespace":     s.Namespace,
		"created_at_ms": s.CreatedAtMs,
		"subscriptions": s.Subscriptions,
	}
}
