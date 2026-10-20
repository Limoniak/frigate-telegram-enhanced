package atomicfile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "state.json")
	for _, body := range []string{"first", "second"} {
		if err := Write(path, []byte(body)); err != nil {
			t.Fatal(err)
		}
		if b, _ := os.ReadFile(path); string(b) != body {
			t.Errorf("content = %q, want %q", b, body)
		}
	}
	if entries, _ := os.ReadDir(filepath.Dir(path)); len(entries) != 1 {
		t.Errorf("%d files left in the directory, want only the written one", len(entries))
	}
}

// A failed write leaves the previous file and no temporary file.
func TestWriteFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if err := Write(path, []byte("kept")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path+".dir", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Write(path+".dir", []byte("x")); err == nil {
		t.Fatal("writing over a directory succeeded")
	}
	if b, _ := os.ReadFile(path); string(b) != "kept" {
		t.Errorf("content = %q, want the previous one", b)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 2 {
		t.Errorf("%d entries in the directory, want the file and the directory only", len(entries))
	}
}
