package main

import (
	"encoding/base64"
	"os"
	"strings"
	"testing"
)

func TestSaveChatAttachmentsAndNote(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "sentcode_attach_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create a dummy PNG base64 string
	sampleBytes := []byte("fake png binary content for test")
	b64 := base64.StdEncoding.EncodeToString(sampleBytes)
	dataURI := "data:image/png;base64," + b64

	paths := saveChatAttachments(tempDir, []string{dataURI})
	if len(paths) != 1 {
		t.Fatalf("expected 1 saved path, got %d", len(paths))
	}

	savedPath := paths[0]
	if !strings.HasPrefix(savedPath, tempDir) {
		t.Errorf("saved path %s should be inside tempDir %s", savedPath, tempDir)
	}
	if !strings.HasSuffix(savedPath, ".png") {
		t.Errorf("saved path %s should end with .png", savedPath)
	}

	// Verify file content on disk
	readBytes, err := os.ReadFile(savedPath)
	if err != nil {
		t.Fatalf("failed to read saved file: %v", err)
	}
	if string(readBytes) != string(sampleBytes) {
		t.Errorf("file content mismatch: got %s, want %s", string(readBytes), string(sampleBytes))
	}

	// Verify formatAttachmentNote
	note := formatAttachmentNote(tempDir, paths)
	if !strings.Contains(note, "[Attached Image(s)]:") {
		t.Errorf("expected attachment note to contain header, got: %s", note)
	}
	if !strings.Contains(note, ".sentcode/attachments") {
		t.Errorf("expected attachment note to contain relative path with .sentcode/attachments, got: %s", note)
	}
	if !strings.Contains(note, savedPath) {
		t.Errorf("expected attachment note to contain full path, got: %s", note)
	}
}
