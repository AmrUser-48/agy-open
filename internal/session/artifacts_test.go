package session

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func testArtifactStore(t *testing.T) *Store {
	t.Helper()
	return &Store{path: filepath.Join(t.TempDir(), "conversation.jsonl.zst")}
}

func assertNoArtifactFiles(t *testing.T, store *Store) {
	t.Helper()
	entries, err := filepath.Glob(filepath.Dir(store.path) + "/*")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("tool output created filesystem entries: %v", entries)
	}
}


func TestInlineOutputSharesMemoryBudgetWithArtifacts(t *testing.T) {
	store := testArtifactStore(t)
	if err := store.ReserveInlineOutput(OutputArtifactSessionMemoryLimit - 1); err != nil {
		t.Fatal(err)
	}
	capture, err := store.BeginArtifactCapture("shell")
	if err != nil { t.Fatal(err) }
	output := strings.Repeat("x", OutputArtifactInlineLimit+1)
	if _, err := capture.Write([]byte(output)); err != nil { t.Fatal(err) }
	if _, err := capture.Finalize(); !errors.Is(err, ErrArtifactMemoryLimit) {
		t.Fatalf("Finalize error = %v, want combined session memory limit", err)
	}
	assertNoArtifactFiles(t, store)
}

func TestInlineOutputEnforcesPerOutputLimit(t *testing.T) {
	store := testArtifactStore(t)
	if err := store.ReserveInlineOutput(OutputArtifactMemoryLimit + 1); !errors.Is(err, ErrArtifactMemoryLimit) {
		t.Fatalf("ReserveInlineOutput error = %v, want ErrArtifactMemoryLimit", err)
	}
	if err := store.ReserveInlineOutput(OutputArtifactMemoryLimit); err != nil {
		t.Fatalf("maximum permitted inline output reservation failed: %v", err)
	}
}

func TestClearArtifactsReleasesAllSessionMemoryReservations(t *testing.T) {
	store := testArtifactStore(t)
	if err := store.ReserveInlineOutput(1024); err != nil { t.Fatal(err) }
	artifact, err := store.SaveArtifact("shell", strings.Repeat("z", OutputArtifactInlineLimit+1))
	if err != nil || artifact == nil { t.Fatalf("SaveArtifact = %#v, err=%v", artifact, err) }
	store.ClearArtifacts()
	if store.HasArtifacts() { t.Fatal("ClearArtifacts retained complete artifact bytes") }
	if err := store.ReserveInlineOutput(OutputArtifactSessionMemoryLimit); err != nil {
		t.Fatalf("ClearArtifacts did not release the full session budget: %v", err)
	}
	assertNoArtifactFiles(t, store)
}

func TestArtifactCaptureRetainsCompleteOutputInMemory(t *testing.T) {
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
		t.Fatal("large output was not retained in session memory")
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
	if !store.HasArtifacts() {
		t.Fatal("session did not report its in-memory artifact")
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
	assertNoArtifactFiles(t, store)
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
	if store.HasArtifacts() {
		t.Fatal("small output was retained as a separate artifact")
	}
	assertNoArtifactFiles(t, store)
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
		t.Fatal("large MCP output was not retained in memory")
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
	assertNoArtifactFiles(t, store)
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
		t.Fatal("session did not report its in-memory artifact")
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
	assertNoArtifactFiles(t, first)
	assertNoArtifactFiles(t, second)
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
		t.Fatal("expected in-memory artifact")
	}
	for _, offset := range []int64{-1, artifact.Size + 1} {
		if _, err := store.ReadArtifact(artifact.ID, offset, 10); err == nil {
			t.Errorf("offset %d was accepted", offset)
		}
	}
}

func TestCloseClearsInMemoryArtifacts(t *testing.T) {
	store := testArtifactStore(t)
	artifact, err := store.SaveArtifact("test", strings.Repeat("x", OutputArtifactInlineLimit+1))
	if err != nil || artifact == nil {
		t.Fatalf("SaveArtifact = %#v, err=%v", artifact, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if store.HasArtifacts() {
		t.Fatal("session close retained output bytes in memory")
	}
	if _, err := store.ReadArtifact(artifact.ID, 0, 128); err == nil {
		t.Fatal("closed session still exposed its output")
	}
	assertNoArtifactFiles(t, store)
}

func TestBeginAndLargeFinalizeNeverCreateDiskFiles(t *testing.T) {
	store := testArtifactStore(t)
	capture, err := store.BeginArtifactCapture("shell")
	if err != nil {
		t.Fatal(err)
	}
	assertNoArtifactFiles(t, store)
	output := strings.Repeat("large output ", 2000)
	if _, err := capture.Write([]byte(output)); err != nil {
		t.Fatal(err)
	}
	if _, err := capture.Finalize(); err != nil {
		t.Fatal(err)
	}
	assertNoArtifactFiles(t, store)
}

func TestArtifactCaptureEnforcesMemoryLimitWithoutSpooling(t *testing.T) {
	store := testArtifactStore(t)
	capture, err := store.BeginArtifactCapture("shell")
	if err != nil {
		t.Fatal(err)
	}
	output := bytes.Repeat([]byte("x"), OutputArtifactMemoryLimit+1)
	if n, err := capture.Write(output); err != nil || n != len(output) {
		t.Fatalf("Write = %d, %v; want full input consumed", n, err)
	}
	if _, err := capture.Finalize(); !errors.Is(err, ErrArtifactMemoryLimit) {
		t.Fatalf("Finalize error = %v, want ErrArtifactMemoryLimit", err)
	}
	if store.HasArtifacts() {
		t.Fatal("oversized output was retained as a complete artifact")
	}
	assertNoArtifactFiles(t, store)
}
