package jellycompat

import (
	"testing"

	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/noderouting"
)

func TestCompatLocalHLSRouteAllowedHonorsHardPolicyBoundaries(t *testing.T) {
	tests := []struct {
		name     string
		workload noderouting.Workload
		mutate   func(*config.PlaybackRoutingPolicy)
		want     bool
	}{
		{name: "remux default", workload: noderouting.WorkloadRemux, want: true},
		{name: "remux worker only", workload: noderouting.WorkloadRemux, mutate: func(policy *config.PlaybackRoutingPolicy) {
			policy.RemuxExecution = config.PlaybackExecutionWorkerOnly
		}},
		{name: "remux proxy only", workload: noderouting.WorkloadRemux, mutate: func(policy *config.PlaybackRoutingPolicy) {
			policy.RemuxEgress = config.PlaybackEgressProxyOnly
		}},
		{name: "video default", workload: noderouting.WorkloadVideoTranscode, want: true},
		{name: "video worker only", workload: noderouting.WorkloadVideoTranscode, mutate: func(policy *config.PlaybackRoutingPolicy) {
			policy.VideoTranscodeExecution = config.PlaybackExecutionWorkerOnly
		}},
		{name: "video proxy only", workload: noderouting.WorkloadVideoTranscode, mutate: func(policy *config.PlaybackRoutingPolicy) {
			policy.VideoTranscodeEgress = config.PlaybackEgressProxyOnly
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			policy := config.DefaultPlaybackRoutingPolicy()
			if test.mutate != nil {
				test.mutate(&policy)
			}
			if got := compatLocalHLSRouteAllowed(test.workload, policy); got != test.want {
				t.Fatalf("compatLocalHLSRouteAllowed() = %t, want %t", got, test.want)
			}
		})
	}
}

func TestCompatWorkerHLSRouteAllowedHonorsHardExecutionBoundaries(t *testing.T) {
	tests := []struct {
		name     string
		workload noderouting.Workload
		mutate   func(*config.PlaybackRoutingPolicy)
		want     bool
	}{
		{name: "remux default", workload: noderouting.WorkloadRemux, want: true},
		{name: "remux API only", workload: noderouting.WorkloadRemux, mutate: func(policy *config.PlaybackRoutingPolicy) {
			policy.RemuxExecution = config.PlaybackExecutionAPIOnly
		}},
		{name: "video default", workload: noderouting.WorkloadVideoTranscode, want: true},
		{name: "video API only", workload: noderouting.WorkloadVideoTranscode, mutate: func(policy *config.PlaybackRoutingPolicy) {
			policy.VideoTranscodeExecution = config.PlaybackExecutionAPIOnly
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			policy := config.DefaultPlaybackRoutingPolicy()
			if test.mutate != nil {
				test.mutate(&policy)
			}
			if got := compatWorkerHLSRouteAllowed(test.workload, policy); got != test.want {
				t.Fatalf("compatWorkerHLSRouteAllowed() = %t, want %t", got, test.want)
			}
		})
	}
}
