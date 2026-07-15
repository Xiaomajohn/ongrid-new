package sandbox

import (
	"errors"
	"path/filepath"
	"strings"
)

var ErrPathEscape = errors.New("sandbox: path escapes root")

func EvalSymlinks(path string) (string, error) {
	return filepath.EvalSymlinks(path)
}

func PathHasPrefix(path, prefix string) bool {
	pathAbs, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return false
	}
	prefixAbs, err := filepath.Abs(filepath.Clean(prefix))
	if err != nil {
		return false
	}

	rel, err := filepath.Rel(prefixAbs, pathAbs)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func PathSafeUnderRoot(path, root string) bool {
	resolvedPath, err := EvalSymlinks(path)
	if err != nil {
		return false
	}
	resolvedRoot, err := EvalSymlinks(root)
	if err != nil {
		return false
	}
	return PathHasPrefix(resolvedPath, resolvedRoot)
}
