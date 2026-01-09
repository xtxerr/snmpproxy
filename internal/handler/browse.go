package handler

import (
	"strings"

	"github.com/xtxerr/snmpproxy/internal/manager"
	"github.com/xtxerr/snmpproxy/internal/store"
)

// BrowseHandler handles browse/tree operations.
type BrowseHandler struct {
	*Handler
}

// NewBrowseHandler creates a browse handler.
func NewBrowseHandler(h *Handler) *BrowseHandler {
	return &BrowseHandler{Handler: h}
}

// BrowseEntry represents an entry in the browse result.
type BrowseEntry struct {
	Name        string
	Path        string
	Type        string // "directory", "target", "poller"
	Description string

	// For targets/pollers linked via tree
	LinkRef string

	// Target details (if type == "target")
	Target *store.Target
	Stats  *store.TargetStats

	// Poller details (if type == "poller")
	Poller      *store.Poller
	AdminState  string
	OperState   string
	HealthState string
}

// ============================================================================
// Browse (unified)
// ============================================================================

// BrowseRequest holds browse request data.
type BrowseRequest struct {
	Path string // "/", "/targets", "/targets/router", "/tree/dc1/network"
}

// BrowseResponse holds browse response data.
type BrowseResponse struct {
	Path    string
	Entries []*BrowseEntry
}

// Browse navigates the virtual filesystem.
func (h *BrowseHandler) Browse(ctx *RequestContext, req *BrowseRequest) (*BrowseResponse, error) {
	if err := RequireNamespace(ctx); err != nil {
		return nil, err
	}

	namespace := ctx.MustNamespace()
	path := normalizePath(req.Path)

	// Route based on path
	switch {
	case path == "/":
		return h.browseRoot(ctx, namespace)

	case path == "/targets":
		return h.browseTargets(ctx, namespace)

	case strings.HasPrefix(path, "/targets/"):
		parts := strings.Split(strings.TrimPrefix(path, "/targets/"), "/")
		if len(parts) == 1 {
			return h.browseTarget(ctx, namespace, parts[0])
		}
		if len(parts) == 2 {
			return h.browsePoller(ctx, namespace, parts[0], parts[1])
		}
		return nil, Errorf(ErrNotFound, "invalid path: %s", path)

	case path == "/tree":
		return h.browseTree(ctx, namespace, "/")

	case strings.HasPrefix(path, "/tree/"):
		treePath := strings.TrimPrefix(path, "/tree")
		return h.browseTree(ctx, namespace, treePath)

	default:
		return nil, Errorf(ErrNotFound, "invalid path: %s", path)
	}
}

// browseRoot returns root entries.
func (h *BrowseHandler) browseRoot(ctx *RequestContext, namespace string) (*BrowseResponse, error) {
	return &BrowseResponse{
		Path: "/",
		Entries: []*BrowseEntry{
			{Name: "targets", Path: "/targets", Type: "directory", Description: "All targets"},
			{Name: "tree", Path: "/tree", Type: "directory", Description: "Hierarchical tree view"},
		},
	}, nil
}

// browseTargets returns all targets.
func (h *BrowseHandler) browseTargets(ctx *RequestContext, namespace string) (*BrowseResponse, error) {
	targets := h.mgr.Targets.List(namespace)

	var entries []*BrowseEntry
	for _, t := range targets {
		stats, _ := h.mgr.Targets.GetStats(namespace, t.Name)
		entries = append(entries, &BrowseEntry{
			Name:        t.Name,
			Path:        "/targets/" + t.Name,
			Type:        "target",
			Description: t.Description,
			Target:      t,
			Stats:       stats,
		})
	}

	return &BrowseResponse{
		Path:    "/targets",
		Entries: entries,
	}, nil
}

// browseTarget returns pollers for a target.
func (h *BrowseHandler) browseTarget(ctx *RequestContext, namespace, targetName string) (*BrowseResponse, error) {
	target, err := h.mgr.Targets.Get(namespace, targetName)
	if err != nil {
		return nil, Errorf(ErrInternal, "get target: %v", err)
	}
	if target == nil {
		return nil, Errorf(ErrNotFound, "target not found: %s", targetName)
	}

	pollers := h.mgr.Pollers.List(namespace, targetName)

	var entries []*BrowseEntry
	for _, p := range pollers {
		state := h.mgr.States.Get(namespace, targetName, p.Name)
		admin, oper, health := state.GetState()

		entries = append(entries, &BrowseEntry{
			Name:        p.Name,
			Path:        "/targets/" + targetName + "/" + p.Name,
			Type:        "poller",
			Description: p.Description,
			Poller:      p,
			AdminState:  admin,
			OperState:   oper,
			HealthState: health,
		})
	}

	return &BrowseResponse{
		Path:    "/targets/" + targetName,
		Entries: entries,
	}, nil
}

// browsePoller returns poller details (leaf node).
func (h *BrowseHandler) browsePoller(ctx *RequestContext, namespace, targetName, pollerName string) (*BrowseResponse, error) {
	info, err := h.mgr.GetPollerFullInfo(namespace, targetName, pollerName)
	if err != nil {
		return nil, Errorf(ErrNotFound, err.Error())
	}

	entry := &BrowseEntry{
		Name:        pollerName,
		Path:        "/targets/" + targetName + "/" + pollerName,
		Type:        "poller",
		Description: info.Poller.Description,
		Poller:      info.Poller,
		AdminState:  info.AdminState,
		OperState:   info.OperState,
		HealthState: info.HealthState,
	}

	return &BrowseResponse{
		Path:    "/targets/" + targetName + "/" + pollerName,
		Entries: []*BrowseEntry{entry},
	}, nil
}

// browseTree returns tree entries.
func (h *BrowseHandler) browseTree(ctx *RequestContext, namespace, treePath string) (*BrowseResponse, error) {
	results, err := h.mgr.Tree.Browse(namespace, treePath, true, h.mgr.Targets, h.mgr.Pollers)
	if err != nil {
		return nil, Errorf(ErrInternal, "browse tree: %v", err)
	}

	var entries []*BrowseEntry
	for _, r := range results {
		entry := &BrowseEntry{
			Name:        lastName(r.Path),
			Path:        "/tree" + r.Path,
			Description: r.Description,
			LinkRef:     r.LinkRef,
		}

		switch r.NodeType {
		case "directory":
			entry.Type = "directory"

		case "link_target":
			entry.Type = "target"
			if r.LinkedTarget != nil {
				entry.Target = r.LinkedTarget
				entry.Stats, _ = h.mgr.Targets.GetStats(namespace, r.LinkedTarget.Name)
			}

		case "link_poller":
			entry.Type = "poller"
			if r.LinkedPoller != nil {
				entry.Poller = r.LinkedPoller
				state := h.mgr.States.Get(namespace, r.LinkedPoller.Target, r.LinkedPoller.Name)
				entry.AdminState, entry.OperState, entry.HealthState = state.GetState()
			}
		}

		entries = append(entries, entry)
	}

	return &BrowseResponse{
		Path:    "/tree" + treePath,
		Entries: entries,
	}, nil
}

// ============================================================================
// Tree Operations
// ============================================================================

// CreateDirectoryRequest holds mkdir request data.
type CreateDirectoryRequest struct {
	Path        string
	Description string
}

// CreateDirectoryResponse holds mkdir response data.
type CreateDirectoryResponse struct {
	Path string
}

// CreateDirectory creates a directory in the tree.
func (h *BrowseHandler) CreateDirectory(ctx *RequestContext, req *CreateDirectoryRequest) (*CreateDirectoryResponse, error) {
	if err := RequireNamespace(ctx); err != nil {
		return nil, err
	}

	namespace := ctx.MustNamespace()
	path := normalizePath(req.Path)

	if err := h.mgr.Tree.CreateDirectory(namespace, path, req.Description); err != nil {
		return nil, Errorf(ErrInvalidRequest, err.Error())
	}

	return &CreateDirectoryResponse{Path: path}, nil
}

// CreateLinkRequest holds link request data.
type CreateLinkRequest struct {
	Path     string // Directory path
	LinkName string
	LinkType string // "target" or "poller"
	LinkRef  string // "targetName" or "targetName/pollerName"
}

// CreateLinkResponse holds link response data.
type CreateLinkResponse struct {
	Path string
}

// CreateLink creates a symlink in the tree.
func (h *BrowseHandler) CreateLink(ctx *RequestContext, req *CreateLinkRequest) (*CreateLinkResponse, error) {
	if err := RequireNamespace(ctx); err != nil {
		return nil, err
	}

	namespace := ctx.MustNamespace()
	path := normalizePath(req.Path)

	if err := h.mgr.Tree.CreateLink(namespace, path, req.LinkName, req.LinkType, req.LinkRef); err != nil {
		return nil, Errorf(ErrInvalidRequest, err.Error())
	}

	fullPath := path + "/" + req.LinkName
	return &CreateLinkResponse{Path: fullPath}, nil
}

// DeleteTreeNodeRequest holds rmdir/unlink request data.
type DeleteTreeNodeRequest struct {
	Path      string
	Recursive bool
	Force     bool
}

// DeleteTreeNodeResponse holds delete response data.
type DeleteTreeNodeResponse struct {
	Deleted int
}

// DeleteTreeNode deletes a tree node.
func (h *BrowseHandler) DeleteTreeNode(ctx *RequestContext, req *DeleteTreeNodeRequest) (*DeleteTreeNodeResponse, error) {
	if err := RequireNamespace(ctx); err != nil {
		return nil, err
	}

	namespace := ctx.MustNamespace()
	path := normalizePath(req.Path)

	deleted, err := h.mgr.Tree.Delete(namespace, path, req.Recursive, req.Force)
	if err != nil {
		return nil, Errorf(ErrInvalidRequest, err.Error())
	}

	return &DeleteTreeNodeResponse{Deleted: deleted}, nil
}

// ============================================================================
// Helpers
// ============================================================================

func normalizePath(p string) string {
	if p == "" {
		return "/"
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	// Remove trailing slash
	if len(p) > 1 && strings.HasSuffix(p, "/") {
		p = p[:len(p)-1]
	}
	return p
}

func lastName(path string) string {
	if path == "/" {
		return "/"
	}
	parts := strings.Split(path, "/")
	return parts[len(parts)-1]
}

// ============================================================================
// Resolve Path
// ============================================================================

// ResolvePathRequest holds resolve request data.
type ResolvePathRequest struct {
	Path string
}

// ResolvePathResponse holds resolve response data.
type ResolvePathResponse struct {
	Type      string // "directory", "target", "poller"
	Namespace string
	Target    string
	Poller    string
}

// ResolvePath resolves a path to its target/poller.
func (h *BrowseHandler) ResolvePath(ctx *RequestContext, req *ResolvePathRequest) (*ResolvePathResponse, error) {
	if err := RequireNamespace(ctx); err != nil {
		return nil, err
	}

	namespace := ctx.MustNamespace()
	path := normalizePath(req.Path)

	// Handle /targets paths
	if strings.HasPrefix(path, "/targets/") {
		parts := strings.Split(strings.TrimPrefix(path, "/targets/"), "/")
		switch len(parts) {
		case 1:
			return &ResolvePathResponse{
				Type:      "target",
				Namespace: namespace,
				Target:    parts[0],
			}, nil
		case 2:
			return &ResolvePathResponse{
				Type:      "poller",
				Namespace: namespace,
				Target:    parts[0],
				Poller:    parts[1],
			}, nil
		}
	}

	// Handle /tree paths
	if strings.HasPrefix(path, "/tree/") {
		treePath := strings.TrimPrefix(path, "/tree")
		nodeType, linkRef, err := h.mgr.Tree.ResolvePath(namespace, treePath)
		if err != nil {
			return nil, Errorf(ErrNotFound, err.Error())
		}

		switch nodeType {
		case "directory":
			return &ResolvePathResponse{Type: "directory", Namespace: namespace}, nil

		case "link_target":
			linkType, targetName, _, _ := manager.ParseLinkRef(linkRef)
			if linkType == "target" {
				return &ResolvePathResponse{
					Type:      "target",
					Namespace: namespace,
					Target:    targetName,
				}, nil
			}

		case "link_poller":
			linkType, targetName, pollerName, _ := manager.ParseLinkRef(linkRef)
			if linkType == "poller" {
				return &ResolvePathResponse{
					Type:      "poller",
					Namespace: namespace,
					Target:    targetName,
					Poller:    pollerName,
				}, nil
			}
		}
	}

	return nil, Errorf(ErrNotFound, "cannot resolve path: %s", path)
}
