package engineclient

import (
	"io"
	"strings"
	"testing"
)

func streamFrom(body string) *AnalyzeStream {
	return &AnalyzeStream{Body: io.NopCloser(strings.NewReader(body))}
}

func TestEventParsesFrames(t *testing.T) {
	stream := streamFrom("event: stage_start\ndata: {\"stage\":\"planning\"}\n\nevent: run_completed\ndata: {\"ok\":true}\n\n")
	defer stream.Close()

	name, data, err := stream.Event()
	if err != nil || name != "stage_start" || string(data) != `{"stage":"planning"}` {
		t.Fatalf("first frame = %q %q %v", name, data, err)
	}
	name, data, err = stream.Event()
	if err != nil || name != "run_completed" || string(data) != `{"ok":true}` {
		t.Fatalf("second frame = %q %q %v", name, data, err)
	}
	if _, _, err := stream.Event(); err != io.EOF {
		t.Fatalf("third read = %v, want io.EOF", err)
	}
}

func TestEventKeepsBufferedFramesAcrossCalls(t *testing.T) {
	// Two frames arrive in one read: the reader must not lose the second.
	stream := streamFrom("event: a\ndata: 1\n\nevent: b\ndata: 2\n\n")
	defer stream.Close()
	if name, _, _ := stream.Event(); name != "a" {
		t.Fatalf("first = %q", name)
	}
	if name, _, _ := stream.Event(); name != "b" {
		t.Fatalf("second = %q, buffered frame was lost", name)
	}
}

func TestEventHandlesCRLF(t *testing.T) {
	stream := streamFrom("event: x\r\ndata: {\"v\":1}\r\n\r\n")
	defer stream.Close()
	name, data, err := stream.Event()
	if err != nil || name != "x" || string(data) != `{"v":1}` {
		t.Fatalf("frame = %q %q %v", name, data, err)
	}
}
