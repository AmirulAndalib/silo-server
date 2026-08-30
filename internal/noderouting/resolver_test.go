package noderouting

import (
	"testing"

	dto "github.com/prometheus/client_model/go"

	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/nodepool"
)

func TestResolveFallsBackAcrossSoftPreference(t *testing.T) {
	decision, err := Resolve(nil, ResolveRequest{
		Request: Request{
			Workload: WorkloadVideoTranscode, Delivery: DeliveryHLSVideo,
			Policy: config.DefaultPlaybackRoutingPolicy(), ProxyAllowed: true,
		},
		SessionID: "session-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !decision.Selected() || decision.Shape.ID != "hls_video_api" {
		t.Fatalf("decision = %#v, want local soft fallback", decision)
	}
}

func TestResolveDoesNotCrossHardExecutionConstraint(t *testing.T) {
	policy := config.DefaultPlaybackRoutingPolicy()
	policy.VideoTranscodeExecution = config.PlaybackExecutionWorkerOnly
	decision, err := Resolve(nil, ResolveRequest{
		Request: Request{
			Workload: WorkloadVideoTranscode, Delivery: DeliveryHLSVideo,
			Policy: policy, ProxyAllowed: true,
		},
		SessionID: "session-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Outcome != OutcomeCapacityUnavailable {
		t.Fatalf("decision = %#v, want capacity unavailable without a worker", decision)
	}
}

func TestResolveUsesExactWorkerAPIRoute(t *testing.T) {
	transcodes := nodepool.NewTranscodePool()
	transcodes.SetNodes([]*nodepool.Node{{ID: 1, URL: "http://worker", Enabled: true, Healthy: true}})
	planner := nodepool.NewPlanner(nodepool.NewProxyPool(), transcodes)
	policy := config.DefaultPlaybackRoutingPolicy()
	policy.VideoTranscodeEgress = config.PlaybackEgressAPIOnly
	decision, err := Resolve(planner, ResolveRequest{
		Request: Request{
			Workload: WorkloadVideoTranscode, Delivery: DeliveryHLSVideo,
			Policy: policy, ProxyAllowed: true,
		},
		SessionID: "session-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Shape.ID != "hls_video_transcode_api" || decision.Plan.TranscodeNode == nil || decision.Plan.ProxyNode != nil {
		t.Fatalf("decision = %#v, want worker execution with API egress", decision)
	}
}

func TestResolveCountsLowCardinalityDecisionMetric(t *testing.T) {
	counter := routingDecisions.WithLabelValues(
		string(WorkloadDirectPlay), string(ExecutionNone), string(EgressAPI),
		string(OutcomeSelected), "selected",
	)
	before := routingCounterValue(t, counter)

	decision, err := Resolve(nil, ResolveRequest{Request: Request{
		Workload: WorkloadDirectPlay, Delivery: DeliveryDirect,
		Policy: config.PlaybackRoutingPolicy{
			DirectPlayEgress: config.PlaybackEgressAPIOnly,
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !decision.Selected() {
		t.Fatalf("decision = %#v, want selected", decision)
	}
	if got := routingCounterValue(t, counter); got != before+1 {
		t.Fatalf("decision counter = %v, want %v", got, before+1)
	}
}

func TestDecisionReasonUsesPolicyOutcomeWhenRejectionsAreMixed(t *testing.T) {
	decision := Decision{
		Outcome: OutcomePolicyUnsatisfied,
		Rejected: []Rejection{
			{ShapeID: "direct_proxy", Reason: RejectionClientUnsupported},
			{ShapeID: "direct_api", Reason: RejectionPolicyUnsatisfied},
		},
	}
	if got := decisionReason(decision); got != string(OutcomePolicyUnsatisfied) {
		t.Fatalf("decisionReason() = %q, want %q", got, OutcomePolicyUnsatisfied)
	}
}

func routingCounterValue(t *testing.T, counter interface{ Write(*dto.Metric) error }) float64 {
	t.Helper()
	metric := &dto.Metric{}
	if err := counter.Write(metric); err != nil {
		t.Fatal(err)
	}
	return metric.GetCounter().GetValue()
}
