package transcode

import "testing"

func TestJobCostCenti(t *testing.T) {
	tests := []struct {
		name     string
		w, h     int
		decision string
		want     int
	}{
		{"1080p transcode", 1920, 1080, "transcode", 100},
		{"4K transcode ~4x", 3840, 2160, "transcode", 400},
		{"720p transcode", 1280, 720, "transcode", 44},
		{"480p floored to 25", 854, 480, "transcode", 25},
		{"unknown dims default 100", 0, 0, "transcode", 100},
		{"remux cheap regardless of dims", 3840, 2160, "remux", 25},
		{"remux with zero dims still cheap", 0, 0, "remux", 25},
		{"directPlay fixed cheap", 3840, 2160, "directPlay", 25},
		{"directStream fixed cheap", 3840, 2160, "directStream", 25},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := JobCostCenti(tt.w, tt.h, tt.decision); got != tt.want {
				t.Errorf("JobCostCenti(%d,%d,%q) = %d, want %d", tt.w, tt.h, tt.decision, got, tt.want)
			}
		})
	}
}

// A worker grinding on a single 4K stream should be deprioritized below an idle
// worker even though both report ActiveSessions==... close — the whole point of
// cost weighting. Under the old session-count scoring the 16-slot worker always
// won; here the 4K cost (400) sinks it below the idle 12-slot worker.
func TestSelectWorker_PrefersLowerCostNotLowerCount(t *testing.T) {
	gpu := []string{"h264_nvenc"}
	busy4K := WorkerRegistration{
		Addr: "busy", Capabilities: gpu,
		MaxSessions: 16, ActiveSessions: 1, ActiveCostCenti: 400, // one 4K stream
	}
	idle := WorkerRegistration{
		Addr: "idle", Capabilities: gpu,
		MaxSessions: 12, ActiveSessions: 0, ActiveCostCenti: 0,
	}
	got := selectWorker([]WorkerRegistration{busy4K, idle}, JobNeeds{})
	if got.Addr != "idle" {
		t.Fatalf("expected idle worker, got %q (busy score=%d idle score=%d)",
			got.Addr, workerScore(busy4K, JobNeeds{}), workerScore(idle, JobNeeds{}))
	}
}

// GPU workers must still win over CPU-only workers regardless of cost budget.
func TestWorkerScore_GPUBeatsCPU(t *testing.T) {
	cpu := WorkerRegistration{Addr: "cpu", Capabilities: []string{"libx264"}, MaxSessions: 64}
	gpuLoaded := WorkerRegistration{
		Addr: "gpu", Capabilities: []string{"hevc_nvenc"},
		MaxSessions: 4, ActiveSessions: 3, ActiveCostCenti: 1100, // over budget
	}
	if workerScore(gpuLoaded, JobNeeds{}) <= workerScore(cpu, JobNeeds{}) {
		t.Fatalf("GPU worker (%d) should outrank idle CPU worker (%d)",
			workerScore(gpuLoaded, JobNeeds{}), workerScore(cpu, JobNeeds{}))
	}
}

// The hard count cap takes precedence over any remaining cost budget: a worker
// at MaxSessions concurrent jobs scores 0 even if each job is cheap.
func TestWorkerScore_CountCapZeroesScore(t *testing.T) {
	full := WorkerRegistration{
		Addr: "full", Capabilities: []string{"h264_nvenc"},
		MaxSessions: 4, ActiveSessions: 4, ActiveCostCenti: 100, // budget left, but slots full
	}
	if got := workerScore(full, JobNeeds{}); got != 0 {
		t.Fatalf("count-capped worker should score 0, got %d", got)
	}
}

// An HDR (tonemap) job must prefer a GPU-tonemap node over a non-tonemap GPU
// node even when the tonemap node is busier — software zscale can't sustain 4K,
// so capability fit outranks load. Without ToneMap in needs, the idle node wins.
func TestSelectWorker_HDRPrefersTonemapNode(t *testing.T) {
	gpu := []string{"h264_nvenc", "hevc_nvenc"}
	tonemapBusy := WorkerRegistration{
		Addr: "tonemap", Capabilities: gpu, HasGPUTonemap: true,
		MaxSessions: 12, ActiveSessions: 2, ActiveCostCenti: 600, // 50% loaded
	}
	plainIdle := WorkerRegistration{
		Addr: "plain", Capabilities: gpu, HasGPUTonemap: false,
		MaxSessions: 12, ActiveSessions: 0, ActiveCostCenti: 0, // idle
	}
	workers := []WorkerRegistration{tonemapBusy, plainIdle}

	if got := selectWorker(workers, JobNeeds{ToneMap: true}); got.Addr != "tonemap" {
		t.Fatalf("HDR job: want tonemap node, got %q", got.Addr)
	}
	// A non-HDR job has no tonemap need, so the idle node wins on load.
	if got := selectWorker(workers, JobNeeds{}); got.Addr != "plain" {
		t.Fatalf("non-HDR job: want idle plain node, got %q", got.Addr)
	}
}

// An AV1-output job must prefer a node with an AV1 encoder over one without,
// so we don't silently fall back to HEVC when an AV1-capable node is available.
func TestSelectWorker_AV1PrefersAV1Encoder(t *testing.T) {
	av1Node := WorkerRegistration{
		Addr: "av1", Capabilities: []string{"h264_nvenc", "hevc_nvenc", "av1_nvenc"},
		MaxSessions: 12, ActiveSessions: 1, ActiveCostCenti: 100, // slightly loaded
	}
	noAV1 := WorkerRegistration{
		Addr: "noav1", Capabilities: []string{"h264_amf", "hevc_amf"},
		MaxSessions: 12, ActiveSessions: 0, ActiveCostCenti: 0, // idle
	}
	workers := []WorkerRegistration{av1Node, noAV1}

	if got := selectWorker(workers, JobNeeds{PreferAV1: true}); got.Addr != "av1" {
		t.Fatalf("AV1 job: want av1-capable node, got %q", got.Addr)
	}
}

// Capability fit must not override the GPU tier: a CPU-only node that nominally
// "matches" must never beat a GPU node. (CPU nodes never advertise GPU tonemap
// or hardware AV1, but guard the ordering explicitly.)
func TestWorkerScore_GPUTierBeatsCapabilityTier(t *testing.T) {
	gpuNoMatch := WorkerRegistration{
		Addr: "gpu", Capabilities: []string{"h264_nvenc"}, HasGPUTonemap: false,
		MaxSessions: 4, ActiveSessions: 3, ActiveCostCenti: 1100,
	}
	cpuPretendMatch := WorkerRegistration{
		Addr: "cpu", Capabilities: []string{"libx264"}, HasGPUTonemap: true,
		MaxSessions: 64, ActiveSessions: 0,
	}
	needs := JobNeeds{ToneMap: true}
	if workerScore(gpuNoMatch, needs) <= workerScore(cpuPretendMatch, needs) {
		t.Fatalf("GPU node (%d) must outrank a capability-matching CPU node (%d)",
			workerScore(gpuNoMatch, needs), workerScore(cpuPretendMatch, needs))
	}
}
