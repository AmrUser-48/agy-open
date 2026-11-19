package session

import (
	"bytes"
	"errors"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testArtifactStore(t *testing.T) *Store {
	t.Helper()
	return &Store{path: filepath.Join(t.TempDir(), "conversation.jsonl.zst")}
}

func TestArtifactCaptureRetainsCompleteOutputPrivately(t *testing.T) {
	store := testArtifactStore(t)
	capture, err := store.BeginArtifactCapture("shell")
	if err != nil {
		t.Fatal(err)
	}
	output := append(bytes.Repeat([]byte("first-line\n"), 1500), bytes.Repeat([]byte("last-line\n"), 500)...)
	for start := 0; start < len(output); {
		end := start + 997
		if end > len(output) {
			end = len(output)
		}
		if _, err := capture.Write(output[start:end]); err != nil {
			t.Fatal(err)
		}
		start = end
	}
	artifact, err := capture.Finalize()
	if err != nil {
		t.Fatal(err)
	}
	if artifact == nil {
		t.Fatal("large output was not saved as an artifact")
	}
	if artifact.Tool != "shell" || artifact.Size != int64(len(output)) {
		t.Fatalf("artifact metadata = %#v", artifact)
	}
	sum := sha256.Sum256(output)
	if artifact.SHA256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("SHA256 = %q, want %x", artifact.SHA256, sum)
	}
	if !strings.Contains(artifact.Preview, "bytes omitted") ||
		!strings.Contains(artifact.Preview, "last-line") {
		t.Fatalf("preview lacks omission marker or tail: %q", artifact.Preview)
	}

	file, info, err := store.artifactFile(artifact.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if info.Size() != int64(len(output)) {
		t.Fatalf("stored size = %d, want %d", info.Size(), len(output))
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("artifact permissions = %o, want 0600", info.Mode().Perm())
	}
	dirInfo, err := os.Stat(store.path + ".artifacts")
	if err != nil {
		t.Fatal(err)
	}
	if dirInfo.Mode().Perm() != 0700 {
		t.Fatalf("artifact directory permissions = %o, want 0700", dirInfo.Mode().Perm())
	}
	chunk, err := store.ReadArtifact(artifact.ID, 123, 4096)
	if err != nil {
		t.Fatal(err)
	}
	if chunk.Offset != 123 || chunk.NextOffset != 123+4096 ||
		chunk.Size != int64(len(output)) || chunk.Complete {
		t.Fatalf("artifact read metadata = %#v", chunk)
	}
	if chunk.Content != string(output[123:123+4096]) {
		t.Fatal("artifact range content differs from original output")
	}
}

func TestArtifactCaptureKeepsSmallOutputInline(t *testing.T) {
	store := testArtifactStore(t)
	capture, err := store.BeginArtifactCapture("read_file")
	if err != nil {
		t.Fatal(err)
	}
	const output = "short tool result"
	if _, err := capture.Write([]byte(output)); err != nil {
		t.Fatal(err)
	}
	artifact, err := capture.Finalize()
	if err != nil {
		t.Fatal(err)
	}
	if artifact != nil {
		t.Fatalf("small output unexpectedly became an artifact: %#v", artifact)
	}
	if _, err := os.Stat(store.path + ".artifacts"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("small output created an artifact directory: %v", err)
	}
}

func TestSaveArtifactAndSearchContinuation(t *testing.T) {
	store := testArtifactStore(t)
	prefix := bytes.Repeat([]byte("x"), OutputArtifactSearchWindow+200)
	content := append(prefix, []byte("unique-cross-window-marker")...)
	artifact, err := store.SaveArtifact("mcp__server__tool", string(content))
	if err != nil {
		t.Fatal(err)
	}
	if artifact == nil {
		t.Fatal("large MCP output was not offloaded")
	}
	first, err := store.SearchArtifact(artifact.ID, "unique-cross-window-marker", 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if first.Complete {
		t.Fatal("first search window unexpectedly reached end of artifact")
	}
	second, err := store.SearchArtifact(artifact.ID, "unique-cross-window-marker", first.NextOffset, 10)
	if err != nil {
		t.Fatal(err)
	}
	wantOffset := int64(len(prefix))
	if len(second.Matches) != 1 || second.Matches[0].Offset != wantOffset {
		t.Fatalf("search matches = %#v, want one match at byte %d", second.Matches, wantOffset)
	}
	if !second.Complete {
		t.Fatal("second search window should reach end of artifact")
	}
}

func TestArtifactSearchPreservesWhitespaceAndSessionIsolation(t *testing.T) {
	first := testArtifactStore(t)
	second := testArtifactStore(t)
	complete := strings.Repeat("prefix-", 2000) + "  exact marker  " + strings.Repeat("-suffix", 2000)
	artifact, err := first.SaveArtifact("search", complete)
	if err != nil || artifact == nil {
		t.Fatalf("SaveArtifact = %#v, err=%v", artifact, err)
	}
	if !first.HasArtifacts() {
		t.Fatal("session did not report its committed artifact")
	}
	if second.HasArtifacts() {
		t.Fatal("different session reported another session's artifact")
	}
	found, err := first.SearchArtifact(artifact.ID, "  exact marker  ", 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(found.Matches) != 1 || !strings.Contains(found.Matches[0].Snippet, "  exact marker  ") {
		t.Fatalf("whitespace-sensitive search = %#v", found)
	}
	if _, err := second.ReadArtifact(artifact.ID, 0, 128); err == nil {
		t.Fatal("one session accessed another session's artifact")
	}
}

func TestArtifactReadRejectsInvalidIDsAndOffsets(t *testing.T) {
	store := testArtifactStore(t)
	if _, err := store.ReadArtifact("../../outside", 0, 100); err == nil {
		t.Fatal("path-like artifact ID was accepted")
	}
	artifact, err := store.SaveArtifact("shell", strings.Repeat("z", OutputArtifactInlineLimit+1))
	if err != nil {
		t.Fatal(err)
	}
	if artifact == nil {
		t.Fatal("expected artifact")
	}
	for _, offset := range []int64{-1, artifact.Size + 1} {
		if _, err := store.ReadArtifact(artifact.ID, offset, 10); err == nil {
			t.Errorf("offset %d was accepted", offset)
		}
	}
}

func TestCloseRemovesArtifactsFromEmptySession(t *testing.T) {
	store := testArtifactStore(t)
	if _, err := store.SaveArtifact("test", strings.Repeat("x", OutputArtifactInlineLimit+1)); err != nil {
		t.Fatal(err)
	}
	dir := store.path + ".artifacts"
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("artifact directory was not created: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("empty session artifact directory still exists: %v", err)
	}
}

func TestHasArtifactsDoesNotCreateSidecarDirectory(t *testing.T) {
	store := testArtifactStore(t)
	if store.HasArtifacts() { t.Fatal("new session unexpectedly has artifacts") }
	if _, err := os.Stat(store.path + ".artifacts"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("HasArtifacts created a sidecar directory: %v", err)
	}
}

func TestMissingArtifactReadDoesNotCreateSidecarDirectory(t *testing.T) {
	store := testArtifactStore(t)
	if _, _, err := store.artifactFile("out-0123456789abcdef"); err == nil {
		t.Fatal("missing artifact unexpectedly opened")
	}
	if _, err := os.Stat(store.path + ".artifacts"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing artifact read created a sidecar directory: %v", err)
	}
}

func TestBeginAndSmallFinalizeDoNotCreateArtifactSidecar(t *testing.T) {
	store := testArtifactStore(t)
	capture, err := store.BeginArtifactCapture("shell")
	if err != nil { t.Fatal(err) }
	if _, err := os.Stat(store.path + ".artifacts"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("BeginArtifactCapture created a sidecar directory: %v", err)
	}
	if _, err := capture.Write([]byte("short output")); err != nil { t.Fatal(err) }
	if _, err := capture.Finalize(); err != nil { t.Fatal(err) }
	if _, err := os.Stat(store.path + ".artifacts"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("small capture created a sidecar directory: %v", err)
	}
}

func TestArtifactFileRejectsSymlink(t *testing.T) {
	store := testArtifactStore(t)
	dir := store.path + ".artifacts"
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	id := "out-0123456789abcdef"
	target := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(target, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, id+".out")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, _, err := store.artifactFile(id); err == nil {
		t.Fatal("symlink artifact was accepted")
	}
}
