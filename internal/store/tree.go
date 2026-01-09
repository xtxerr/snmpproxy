package store

import (
	"database/sql"
	"fmt"
	"path"
	"regexp"
	"strings"
	"time"
)

// TreeNode represents a node in the tree (directory or symlink).
type TreeNode struct {
	Namespace   string
	Path        string
	NodeType    string // "directory", "link_target", "link_poller"
	LinkRef     string // For links: "target:name" or "poller:target/name"
	Description string
	CreatedAt   time.Time
}

// CreateTreeDirectory creates a directory node and all parent directories.
func (s *Store) CreateTreeDirectory(namespace, nodePath, description string) error {
	nodePath = normalizePath(nodePath)

	return s.Transaction(func(tx *sql.Tx) error {
		// Create parent directories if needed
		parts := strings.Split(strings.Trim(nodePath, "/"), "/")
		currentPath := ""

		for i, part := range parts {
			if part == "" {
				continue
			}
			currentPath = currentPath + "/" + part
			desc := ""
			if i == len(parts)-1 {
				desc = description
			}

			// Try to insert (ignore if exists)
			_, err := tx.Exec(`
				INSERT OR IGNORE INTO tree_nodes (namespace, path, node_type, description, created_at)
				VALUES (?, ?, 'directory', ?, ?)
			`, namespace, currentPath, desc, time.Now())
			if err != nil {
				return fmt.Errorf("create directory %s: %w", currentPath, err)
			}
		}
		return nil
	})
}

// CreateTreeLink creates a symlink to a target or poller.
func (s *Store) CreateTreeLink(namespace, treePath, linkName, linkType, linkRef string) error {
	treePath = normalizePath(treePath)
	fullPath := path.Join(treePath, linkName)

	// Validate link type
	var nodeType string
	switch linkType {
	case "target":
		nodeType = "link_target"
	case "poller":
		nodeType = "link_poller"
	default:
		return fmt.Errorf("invalid link type: %s", linkType)
	}

	// Validate that target/poller exists
	exists, err := s.validateLinkRef(namespace, linkType, linkRef)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("%s not found: %s", linkType, linkRef)
	}

	// Format linkRef
	formattedRef := fmt.Sprintf("%s:%s", linkType, linkRef)

	_, err = s.db.Exec(`
		INSERT INTO tree_nodes (namespace, path, node_type, link_ref, created_at)
		VALUES (?, ?, ?, ?, ?)
	`, namespace, fullPath, nodeType, formattedRef, time.Now())

	return err
}

func (s *Store) validateLinkRef(namespace, linkType, linkRef string) (bool, error) {
	switch linkType {
	case "target":
		return s.TargetExists(namespace, linkRef)
	case "poller":
		parts := strings.SplitN(linkRef, "/", 2)
		if len(parts) != 2 {
			return false, fmt.Errorf("invalid poller reference: %s (expected target/poller)", linkRef)
		}
		return s.PollerExists(namespace, parts[0], parts[1])
	default:
		return false, fmt.Errorf("invalid link type: %s", linkType)
	}
}

// GetTreeNode retrieves a single tree node.
func (s *Store) GetTreeNode(namespace, nodePath string) (*TreeNode, error) {
	nodePath = normalizePath(nodePath)

	var node TreeNode
	var linkRef, description sql.NullString

	err := s.db.QueryRow(`
		SELECT namespace, path, node_type, link_ref, description, created_at
		FROM tree_nodes WHERE namespace = ? AND path = ?
	`, namespace, nodePath).Scan(&node.Namespace, &node.Path, &node.NodeType,
		&linkRef, &description, &node.CreatedAt)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query tree node: %w", err)
	}

	if linkRef.Valid {
		node.LinkRef = linkRef.String
	}
	if description.Valid {
		node.Description = description.String
	}

	return &node, nil
}

// ListTreeChildren returns direct children of a path.
func (s *Store) ListTreeChildren(namespace, parentPath string) ([]*TreeNode, error) {
	parentPath = normalizePath(parentPath)

	// Build pattern for direct children
	var pattern string
	if parentPath == "/" {
		pattern = "/[^/]+$" // Direct children of root
	} else {
		pattern = "^" + regexp.QuoteMeta(parentPath) + "/[^/]+$"
	}

	// DuckDB uses regexp_matches
	query := `
		SELECT namespace, path, node_type, link_ref, description, created_at
		FROM tree_nodes 
		WHERE namespace = ? AND regexp_matches(path, ?)
		ORDER BY node_type, path
	`

	// Simplified approach: list all and filter
	rows, err := s.db.Query(`
		SELECT namespace, path, node_type, link_ref, description, created_at
		FROM tree_nodes WHERE namespace = ?
		ORDER BY path
	`, namespace)
	if err != nil {
		return nil, fmt.Errorf("query tree nodes: %w", err)
	}
	defer rows.Close()

	var nodes []*TreeNode
	for rows.Next() {
		var node TreeNode
		var linkRef, description sql.NullString

		if err := rows.Scan(&node.Namespace, &node.Path, &node.NodeType,
			&linkRef, &description, &node.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan tree node: %w", err)
		}

		if linkRef.Valid {
			node.LinkRef = linkRef.String
		}
		if description.Valid {
			node.Description = description.String
		}

		// Filter for direct children
		if isDirectChild(parentPath, node.Path) {
			nodes = append(nodes, &node)
		}
	}

	return nodes, rows.Err()
}

// isDirectChild checks if childPath is a direct child of parentPath.
func isDirectChild(parentPath, childPath string) bool {
	parentPath = normalizePath(parentPath)
	childPath = normalizePath(childPath)

	if parentPath == "/" {
		// Direct child of root: /name (no more slashes after)
		trimmed := strings.TrimPrefix(childPath, "/")
		return trimmed != "" && !strings.Contains(trimmed, "/")
	}

	// Must start with parent path
	if !strings.HasPrefix(childPath, parentPath+"/") {
		return false
	}

	// Rest must not contain slashes
	rest := strings.TrimPrefix(childPath, parentPath+"/")
	return rest != "" && !strings.Contains(rest, "/")
}

// DeleteTreeNode deletes a tree node.
func (s *Store) DeleteTreeNode(namespace, nodePath string, recursive, force bool) (int, error) {
	nodePath = normalizePath(nodePath)

	// Check if has children
	children, err := s.ListTreeChildren(namespace, nodePath)
	if err != nil {
		return 0, err
	}

	if len(children) > 0 && !recursive && !force {
		return 0, fmt.Errorf("directory not empty: %d children", len(children))
	}

	var deleted int
	err = s.Transaction(func(tx *sql.Tx) error {
		if recursive {
			// Delete all descendants
			result, err := tx.Exec(`
				DELETE FROM tree_nodes 
				WHERE namespace = ? AND (path = ? OR path LIKE ?)
			`, namespace, nodePath, nodePath+"/%")
			if err != nil {
				return err
			}
			rows, _ := result.RowsAffected()
			deleted = int(rows)
		} else {
			// Delete only this node
			result, err := tx.Exec(`
				DELETE FROM tree_nodes WHERE namespace = ? AND path = ?
			`, namespace, nodePath)
			if err != nil {
				return err
			}
			rows, _ := result.RowsAffected()
			deleted = int(rows)
		}
		return nil
	})

	return deleted, err
}

// DeleteLinksToTarget deletes all links pointing to a target.
func (s *Store) DeleteLinksToTarget(namespace, targetName string) (int, error) {
	result, err := s.db.Exec(`
		DELETE FROM tree_nodes 
		WHERE namespace = ? AND link_ref = ?
	`, namespace, "target:"+targetName)
	if err != nil {
		return 0, err
	}
	rows, _ := result.RowsAffected()
	return int(rows), nil
}

// DeleteLinksToPoller deletes all links pointing to a poller.
func (s *Store) DeleteLinksToPoller(namespace, targetName, pollerName string) (int, error) {
	result, err := s.db.Exec(`
		DELETE FROM tree_nodes 
		WHERE namespace = ? AND link_ref = ?
	`, namespace, fmt.Sprintf("poller:%s/%s", targetName, pollerName))
	if err != nil {
		return 0, err
	}
	rows, _ := result.RowsAffected()
	return int(rows), nil
}

// DeleteLinksToTargetAndPollers deletes all links to a target and all its pollers.
func (s *Store) DeleteLinksToTargetAndPollers(namespace, targetName string) (int, error) {
	result, err := s.db.Exec(`
		DELETE FROM tree_nodes 
		WHERE namespace = ? AND (link_ref = ? OR link_ref LIKE ?)
	`, namespace, "target:"+targetName, "poller:"+targetName+"/%")
	if err != nil {
		return 0, err
	}
	rows, _ := result.RowsAffected()
	return int(rows), nil
}

// GetLinksToTarget returns all tree paths that link to a target.
func (s *Store) GetLinksToTarget(namespace, targetName string) ([]string, error) {
	rows, err := s.db.Query(`
		SELECT path FROM tree_nodes 
		WHERE namespace = ? AND link_ref = ?
	`, namespace, "target:"+targetName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var paths []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		paths = append(paths, p)
	}
	return paths, rows.Err()
}

// GetLinksToPoller returns all tree paths that link to a poller.
func (s *Store) GetLinksToPoller(namespace, targetName, pollerName string) ([]string, error) {
	rows, err := s.db.Query(`
		SELECT path FROM tree_nodes 
		WHERE namespace = ? AND link_ref = ?
	`, namespace, fmt.Sprintf("poller:%s/%s", targetName, pollerName))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var paths []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		paths = append(paths, p)
	}
	return paths, rows.Err()
}

// normalizePath normalizes a path to have a leading slash and no trailing slash.
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

// Compile regex pattern for path matching
var _ = regexp.MustCompile // Ensure import is used
