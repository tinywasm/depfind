package depfind_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tinywasm/depfind"
)

// TestNestedDependencyOwnership verifies that ThisFileIsMine correctly identifies
// ownership of files in deeply nested dependencies (level 3).
//
// Scenario:
// main.go -> imports level1
// level1  -> imports level2
// level2  -> imports level3
// level3  -> contains target.go
//
// Expected: handler for main.go should claim level3/target.go
func TestNestedDependencyOwnership(t *testing.T) {
	tmp := t.TempDir()

	// 1. Setup directories
	// cmd
	// level1
	// level2
	// level3
	// level4
	dirs := []string{"cmd", "level1", "level2", "level3", "level4"}
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(tmp, d), 0755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}

	// 2. Write go.mod
	goModContent := `module testproject

go 1.21
`
	if err := os.WriteFile(filepath.Join(tmp, "go.mod"), []byte(goModContent), 0644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}

	// 3. Create level4/target.go
	l4Path := filepath.Join(tmp, "level4", "target.go")
	l4Content := `package level4

func DoSomething() {
	println("Level 4")
}
`
	if err := os.WriteFile(l4Path, []byte(l4Content), 0644); err != nil {
		t.Fatalf("write level4: %v", err)
	}

	// 3b. Create level3/lib.go -> imports level4
	l3Path := filepath.Join(tmp, "level3", "lib.go")
	l3Content := `package level3

import "testproject/level4"

func DoLevel3() {
	level4.DoSomething()
}
`
	if err := os.WriteFile(l3Path, []byte(l3Content), 0644); err != nil {
		t.Fatalf("write level3: %v", err)
	}

	// 4. Create level2/lib.go -> imports level3
	l2Path := filepath.Join(tmp, "level2", "lib.go")
	l2Content := `package level2

import "testproject/level3"

func DoLevel2() {
	level3.DoLevel3()
}
`
	if err := os.WriteFile(l2Path, []byte(l2Content), 0644); err != nil {
		t.Fatalf("write level2: %v", err)
	}

	// 5. Create level1/lib.go -> imports level2
	l1Path := filepath.Join(tmp, "level1", "lib.go")
	l1Content := `package level1

import "testproject/level2"

func DoLevel1() {
	level2.DoLevel2()
}
`
	if err := os.WriteFile(l1Path, []byte(l1Content), 0644); err != nil {
		t.Fatalf("write level1: %v", err)
	}

	// 6. Create cmd/main.go -> imports level1
	mainPath := filepath.Join(tmp, "cmd", "main.go")
	mainContent := `package main

import "testproject/level1"

func main() {
	level1.DoLevel1()
}
`
	if err := os.WriteFile(mainPath, []byte(mainContent), 0644); err != nil {
		t.Fatalf("write main: %v", err)
	}

	// 7. Initialize finder
	finder := depfind.New(tmp)

	// 8. Check ownership
	handlerMainRelative := "cmd/main.go"

	// We want to know if this handler claims level4/target.go
	t.Logf("Checking if handler %s claims %s", handlerMainRelative, l4Path)

	isMine, err := finder.ThisFileIsMine(handlerMainRelative, l4Path, "write")
	if err != nil {
		t.Fatalf("ThisFileIsMine failed: %v", err)
	}

	if !isMine {
		t.Errorf("FAILED: Handler for %s DID NOT claim nested dependency %s", handlerMainRelative, l4Path)
	} else {
		t.Logf("SUCCESS: Handler for %s correctly claimed nested dependency %s (4 levels deep)", handlerMainRelative, l4Path)
	}
}
