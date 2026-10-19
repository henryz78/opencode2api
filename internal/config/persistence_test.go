package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestSaveAtomicKeepsPreviousConfigWithReadOnlyBackup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	for i, key := range []string{"first-key", "second-key", "third-key"} {
		if err := SaveAtomic(path, Config{ServerKeys: []string{key}}); err != nil {
			t.Fatalf("save %d: %v", i, err)
		}
		if i == 0 {
			continue
		}
		for file, want := range map[string]string{path: key, path + ".bak": []string{"first-key", "second-key"}[i-1]} {
			data, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			var stored Config
			if err := json.Unmarshal(data, &stored); err != nil {
				t.Fatal(err)
			}
			if len(stored.ServerKeys) != 1 || stored.ServerKeys[0] != want {
				t.Fatalf("%s contains keys %v, want %s", file, stored.ServerKeys, want)
			}
		}
		// Unix deployments can replace a read-only file when its directory is
		// writable. Windows uses the upstream's separate staged-backup path.
		if i == 1 && runtime.GOOS != "windows" {
			if err := os.Chmod(path+".bak", 0400); err != nil {
				t.Fatal(err)
			}
		}
	}
}
