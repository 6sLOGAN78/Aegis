package control

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadIdentityMappings(t *testing.T) {
	dir := t.TempDir()
	write := func(content string) string {
		p := filepath.Join(dir, "m.json")
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	tests := []struct {
		name    string
		path    func() string
		wantErr bool
	}{
		{"valid", func() string { return write(`{"b":["x","y"],"a":["z"]}`) }, false},
		{"empty path", func() string { return "" }, true},
		{"unreadable", func() string { return filepath.Join(dir, "missing.json") }, true},
		{"invalid json", func() string { return write(`{nope`) }, true},
		{"empty role", func() string { return write(`{"":["x"]}`) }, true},
		{"empty perms", func() string { return write(`{"a":[]}`) }, true},
		{"empty string perm", func() string { return write(`{"a":[""]}`) }, true},
		{"empty object", func() string { return write(`{}`) }, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := LoadIdentityMappings(tc.path())
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tc.wantErr)
			}
		})
	}
	got, _ := LoadIdentityMappings(write(`{"b":["x","y"],"a":["z"]}`))
	if len(got) != 2 || got[0].Role != "a" || got[1].Permissions[1] != "y" {
		t.Fatalf("bad sort/order: %v", got)
	}
}

func TestSetIdentityMappings(t *testing.T) {
	p := filepath.Join(t.TempDir(), "m.json")
	if err := os.WriteFile(p, []byte(`{"a":["x"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := LoadIdentityMappings(p)
	if err != nil {
		t.Fatal(err)
	}
	s := &APIServer{}
	s.SetIdentityMappings(m)
	if len(s.identityMappings) != 1 {
		t.Fatal("setter did not apply")
	}
}
