package depfind

import (
	"os"
	"path/filepath"
	"testing"
)

func TestThisFileIsMine_GoListFails(t *testing.T) {
	// Create minimal project without external dependencies (go list can run)
	// But simulate go list failure by using a directory without go.mod for rebuildCache
	tmp := t.TempDir()

	// Create structure: main.go + pkg/dep/dep.go WITHOUT go.mod
	os.MkdirAll(filepath.Join(tmp, "pkg/dep"), 0755)
	os.WriteFile(filepath.Join(tmp, "main.go"), []byte("package main\nfunc main(){}"), 0644)
	os.WriteFile(filepath.Join(tmp, "pkg/dep/dep.go"), []byte("package dep"), 0644)

	// go list will fail because there is no go.mod
	finder := New(tmp)

	isMine, err := finder.ThisFileIsMine("main.go", filepath.Join(tmp, "pkg/dep/dep.go"), "write")

	// Should not return fatal error
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	// Fallback: file is under rootDir and main.go exists -> should be true
	if !isMine {
		t.Error("Expected isMine=true via fallback when go list fails")
	}
}
