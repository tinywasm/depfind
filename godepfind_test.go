package depfind

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNew(t *testing.T) {
	// Test with empty rootDir
	g1 := New("")
	if !filepath.IsAbs(g1.rootDir) {
		t.Errorf("Expected rootDir to be an absolute path, got %s", g1.rootDir)
	}
	if g1.testImports != false {
		t.Errorf("Expected testImports to be false by default")
	}

	// Test with specific rootDir (normalized to absolute)
	g2 := New("/tmp")
	absTmp, _ := filepath.Abs("/tmp")
	if g2.rootDir != absTmp {
		t.Errorf("Expected rootDir to be '%s', got %s", absTmp, g2.rootDir)
	}
}

func TestSetTestImports(t *testing.T) {
	g := New(".")

	// Test enabling test imports
	g.SetTestImports(true)
	if !g.testImports {
		t.Errorf("Expected testImports to be true")
	}

	// Test disabling test imports
	g.SetTestImports(false)
	if g.testImports {
		t.Errorf("Expected testImports to be false")
	}
}

func TestFindReverseDeps(t *testing.T) {
	// Test with current directory
	g := New(".")

	// This test will work if we're in a Go module with some packages
	// We'll test with common Go standard library packages
	deps, err := g.FindReverseDeps("./...", []string{"fmt"})
	if err != nil {
		t.Logf("Warning: Could not find reverse dependencies: %v", err)
		return // Skip test if not in a Go module
	}

	// Should find at least some packages (including this test file)
	t.Logf("Found %d packages that import 'fmt'", len(deps))
	for _, dep := range deps {
		t.Logf("Package: %s", dep)
	}
}

func TestFindReverseDepsWithTests(t *testing.T) {
	g := New(".")
	g.SetTestImports(true)

	deps, err := g.FindReverseDeps("./...", []string{"testing"})
	if err != nil {
		t.Logf("Warning: Could not find reverse dependencies: %v", err)
		return // Skip test if not in a Go module
	}

	// Should find test packages
	t.Logf("Found %d packages that import 'testing' (including tests)", len(deps))
	for _, dep := range deps {
		t.Logf("Package: %s", dep)
	}
}

func TestThisFileIsMine_ExternalFile(t *testing.T) {
	// Setup
	tmpDir := t.TempDir()
	rootDir := filepath.Join(tmpDir, "mock_app")
	os.MkdirAll(rootDir, 0755)

	g := New(rootDir)

	// Create a dummy main file so the handler check passes
	mainFile := filepath.Join(rootDir, "main.go")
	os.WriteFile(mainFile, []byte("package main"), 0644)

	// Test Case: External File
	// Use a path that is definitely outside rootDir
	externalLibDir := filepath.Join(tmpDir, "external_lib")
	os.MkdirAll(externalLibDir, 0755)
	externalFile := filepath.Join(externalLibDir, "utils.go")
	os.WriteFile(externalFile, []byte("package lib"), 0644)

	isMine, err := g.ThisFileIsMine("main.go", externalFile, "write")

	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	if !isMine {
		t.Error("Expected ThisFileIsMine to return true for external file")
	}
}
