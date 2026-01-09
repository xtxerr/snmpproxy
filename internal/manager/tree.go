package manager

import (
	"fmt"
	"path"
	"strings"
	"sync"

	"github.com/xtxerr/snmpproxy/internal/store"
)

// TreeManager handles tree operations.
type TreeManager struct {
	store *store.Store
	mu    sync.RWMutex

	// Cache: namespace/path -> node
	nodes map[string]*store.TreeNode
}

// NewTreeManager creates a new tree manager.
func NewTreeManager(s *store.Store) *TreeManager {
	return &TreeManager{
		store: s,
		nodes: make(map[string]*store.TreeNode),
	}
}

// treeKey returns the cache key for a tree node.
func treeKey(namespace, nodePath string) string {
	return namespace + ":" + normalizePath(nodePath)
}

// normalizePath normalizes a path.
func normalizePath(p string) string {
	p = path.Clean(p)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	if p != "/" && strings.HasSuffix(p, "/") {
		p = strings.TrimSuffix(p, "/")
	}
	return p
}

// Load loads all tree nodes from store.
func (m *TreeManager) Load(namespace string) error {
	// We load on-demand, but could pre-load for specific namespaces
	return nil
}

// CreateDirectory creates a directory and all parent directories.
func (m *TreeManager) CreateDirectory(namespace, nodePath, description string) error {
	nodePath = normalizePath(nodePath)

	if err := m.store.CreateTreeDirectory(namespace, nodePath, description); err != nil {
		return err
	}

	// Invalidate cache for this path and parents
	m.invalidatePath(namespace, nodePath)
	return nil
}

// CreateLink creates a symlink to a target or poller.
func (m *TreeManager) CreateLink(namespace, treePath, linkName, linkType, linkRef string) error {
	treePath = normalizePath(treePath)

	// Ensure parent directory exists
	if treePath != "/" {
		if err := m.store.CreateTreeDirectory(namespace, treePath, ""); err != nil {
			return err
		}
	}

	if err := m.store.CreateTreeLink(namespace, treePath, linkName, linkType, linkRef); err != nil {
		return err
	}

	// Invalidate cache
	fullPath := path.Join(treePath, linkName)
	m.invalidatePath(namespace, fullPath)
	return nil
}

// Get returns a tree node.
func (m *TreeManager) Get(namespace, nodePath string) (*store.TreeNode, error) {
	nodePath = normalizePath(nodePath)
	key := treeKey(namespace, nodePath)

	m.mu.RLock()
	node, ok := m.nodes[key]
	m.mu.RUnlock()

	if ok {
		return node, nil
	}

	// Load from store
	node, err := m.store.GetTreeNode(namespace, nodePath)
	if err != nil {
		return nil, err
	}
	if node == nil {
		return nil, nil
	}

	// Update cache
	m.mu.Lock()
	m.nodes[key] = node
	m.mu.Unlock()

	return node, nil
}

// ListChildren returns direct children of a path.
func (m *TreeManager) ListChildren(namespace, parentPath string) ([]*store.TreeNode, error) {
	parentPath = normalizePath(parentPath)
	return m.store.ListTreeChildren(namespace, parentPath)
}

// Delete deletes a tree node.
func (m *TreeManager) Delete(namespace, nodePath string, recursive, force bool) (int, error) {
	nodePath = normalizePath(nodePath)

	deleted, err := m.store.DeleteTreeNode(namespace, nodePath, recursive, force)
	if err != nil {
		return 0, err
	}

	// Invalidate cache
	m.invalidatePath(namespace, nodePath)
	if recursive {
		// Invalidate all cached children
		m.mu.Lock()
		prefix := treeKey(namespace, nodePath) + "/"
		for k := range m.nodes {
			if strings.HasPrefix(k, prefix) {
				delete(m.nodes, k)
			}
		}
		m.mu.Unlock()
	}

	return deleted, nil
}

// ResolvePath resolves a tree path, following symlinks.
// Returns the final entity type and reference.
func (m *TreeManager) ResolvePath(namespace, nodePath string) (nodeType, linkRef string, err error) {
	nodePath = normalizePath(nodePath)

	node, err := m.Get(namespace, nodePath)
	if err != nil {
		return "", "", err
	}
	if node == nil {
		return "", "", fmt.Errorf("path not found: %s", nodePath)
	}

	return node.NodeType, node.LinkRef, nil
}

// GetLinksToTarget returns all tree paths that link to a target.
func (m *TreeManager) GetLinksToTarget(namespace, targetName string) ([]string, error) {
	return m.store.GetLinksToTarget(namespace, targetName)
}

// GetLinksToPoller returns all tree paths that link to a poller.
func (m *TreeManager) GetLinksToPoller(namespace, targetName, pollerName string) ([]string, error) {
	return m.store.GetLinksToPoller(namespace, targetName, pollerName)
}

// DeleteLinksToTarget deletes all links pointing to a target.
func (m *TreeManager) DeleteLinksToTarget(namespace, targetName string) (int, error) {
	deleted, err := m.store.DeleteLinksToTarget(namespace, targetName)
	if err != nil {
		return 0, err
	}

	// Invalidate cache
	m.invalidateAllForNamespace(namespace)
	return deleted, nil
}

// DeleteLinksToPoller deletes all links pointing to a poller.
func (m *TreeManager) DeleteLinksToPoller(namespace, targetName, pollerName string) (int, error) {
	deleted, err := m.store.DeleteLinksToPoller(namespace, targetName, pollerName)
	if err != nil {
		return 0, err
	}

	// Invalidate cache
	m.invalidateAllForNamespace(namespace)
	return deleted, nil
}

// DeleteLinksToTargetAndPollers deletes all links to a target and its pollers.
func (m *TreeManager) DeleteLinksToTargetAndPollers(namespace, targetName string) (int, error) {
	deleted, err := m.store.DeleteLinksToTargetAndPollers(namespace, targetName)
	if err != nil {
		return 0, err
	}

	// Invalidate cache
	m.invalidateAllForNamespace(namespace)
	return deleted, nil
}

// ParseLinkRef parses a link reference.
// Returns linkType ("target" or "poller") and the reference parts.
func ParseLinkRef(ref string) (linkType string, target string, poller string, err error) {
	if strings.HasPrefix(ref, "target:") {
		return "target", ref[7:], "", nil
	}
	if strings.HasPrefix(ref, "poller:") {
		parts := strings.SplitN(ref[7:], "/", 2)
		if len(parts) != 2 {
			return "", "", "", fmt.Errorf("invalid poller reference: %s", ref)
		}
		return "poller", parts[0], parts[1], nil
	}
	return "", "", "", fmt.Errorf("unknown link type: %s", ref)
}

// invalidatePath removes a path from the cache.
func (m *TreeManager) invalidatePath(namespace, nodePath string) {
	key := treeKey(namespace, nodePath)

	m.mu.Lock()
	delete(m.nodes, key)
	m.mu.Unlock()
}

// invalidateAllForNamespace removes all cached nodes for a namespace.
func (m *TreeManager) invalidateAllForNamespace(namespace string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	prefix := namespace + ":"
	for k := range m.nodes {
		if strings.HasPrefix(k, prefix) {
			delete(m.nodes, k)
		}
	}
}

// TreeBrowseResult represents a result when browsing the tree.
type TreeBrowseResult struct {
	Path        string
	NodeType    string
	Description string
	LinkRef     string

	// If this is a link, these are populated
	LinkedTarget *store.Target
	LinkedPoller *store.Poller
}

// Browse returns contents of a tree path with resolved link information.
func (m *TreeManager) Browse(namespace, parentPath string, resolveLinks bool, targetMgr *TargetManager, pollerMgr *PollerManager) ([]*TreeBrowseResult, error) {
	children, err := m.ListChildren(namespace, parentPath)
	if err != nil {
		return nil, err
	}

	var results []*TreeBrowseResult
	for _, child := range children {
		result := &TreeBrowseResult{
			Path:        child.Path,
			NodeType:    child.NodeType,
			Description: child.Description,
			LinkRef:     child.LinkRef,
		}

		// Resolve links if requested
		if resolveLinks && child.LinkRef != "" {
			linkType, targetName, pollerName, err := ParseLinkRef(child.LinkRef)
			if err == nil {
				switch linkType {
				case "target":
					if target, err := targetMgr.Get(namespace, targetName); err == nil && target != nil {
						result.LinkedTarget = target
					}
				case "poller":
					if poller, err := pollerMgr.Get(namespace, targetName, pollerName); err == nil && poller != nil {
						result.LinkedPoller = poller
					}
				}
			}
		}

		results = append(results, result)
	}

	return results, nil
}
