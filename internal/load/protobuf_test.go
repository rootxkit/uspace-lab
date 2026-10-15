package load

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A track frame's JSON cost is timed and set against each hot-path
// process's CPU per message; a process with no messages or no CPU gets
// no share, and without a frame (or with one that is not a track) there
// is nothing to time and the estimate says so.
func TestProtobufEstimate(t *testing.T) {
	files, _ := filepath.Glob(repo("schemas/common/track/telemetry/v1/examples/*.json"))
	if len(files) == 0 {
		t.Fatal("no track example")
	}
	frame, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	hot := map[string]string{"ingest": `ussp_accepted{component="telemetry"}`, "idle": "frames"}
	rise := map[string]map[string]float64{
		"ingest": {metricCPU: 10, `ussp_accepted{component="telemetry"}`: 1e6},
		"idle":   {metricCPU: 1},
	}
	pb := protobufEstimate(frame, hot, rise)
	if pb.JSONUS <= 0 || len(pb.Processes) != 2 {
		t.Fatalf("estimate %+v", pb)
	}
	in := pb.Processes[1]
	if in.Process != "ingest" || in.CPUPerMsgUS != 10 || in.Share <= 0 || in.Share != round3(pb.JSONUS/10) {
		t.Fatalf("ingest %+v", in)
	}
	if idle := pb.Processes[0]; idle.Share != 0 || idle.CPUPerMsgUS != 0 {
		t.Fatalf("a process with no messages %+v", idle)
	}
	if pb := protobufEstimate(nil, hot, rise); pb.JSONUS != 0 || !strings.Contains(pb.Note, "no track frame") {
		t.Fatalf("no frame %+v", pb)
	}
	if pb := protobufEstimate([]byte(`{"body": 3}`), hot, rise); pb.JSONUS != 0 || !strings.Contains(pb.Note, "does not decode") {
		t.Fatalf("not a track %+v", pb)
	}
}
