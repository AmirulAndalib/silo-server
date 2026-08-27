package nodepool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

const degradedCapabilityPayload = `{"resolved":"none","render_devices":[],` +
	`"detected_backends":[{"backend":"nvenc","verified":false}],"capability_hash":"sha256:degraded"}`

// The drift log line only reaches an operator who is reading logs. Persisting
// the same finding is what puts it on the node list, so the pool copy has to
// carry it the moment the report is applied.
func TestHealthCheckerCarriesCapabilityDriftIntoThePool(t *testing.T) {
	url := newHealthNode(t, "sha256:degraded")
	fetcher := &fakeCapabilityFetcher{payload: []byte(degradedCapabilityPayload), hash: "sha256:degraded"}
	fixture := newCapabilityCheckerFixture(t, &Node{
		ID: 1, Name: "gpu-1", Type: NodeTypeTranscode, URL: url, Enabled: true,
		Capabilities:     json.RawMessage(testCapabilityPayload),
		CapabilitiesHash: stringPtr("sha256:old"),
	}, fetcher)

	fixture.sweep()

	stored := fixture.storedNode(t)
	if stored.CapabilityDrift == nil {
		t.Fatal("pool copy carries no capability_drift after a regression")
	}
	note := *stored.CapabilityDrift
	for _, want := range []string{"nvenc", "/dev/dri/renderD128", "none"} {
		if !strings.Contains(note, want) {
			t.Fatalf("drift note %q does not name %q", note, want)
		}
	}
}

// A repaired node must stop being flagged. The note describes the last
// comparison, not a latched incident, so a clean report clears it — otherwise a
// one-off driver hiccup would mark a node broken forever.
func TestHealthCheckerClearsCapabilityDriftOnRecovery(t *testing.T) {
	url := newHealthNode(t, "sha256:new")
	fetcher := &fakeCapabilityFetcher{payload: []byte(testCapabilityPayload), hash: "sha256:new"}
	previousNote := "verified hardware backends lost: nvenc"
	fixture := newCapabilityCheckerFixture(t, &Node{
		ID: 1, Name: "gpu-1", Type: NodeTypeTranscode, URL: url, Enabled: true,
		Capabilities:     json.RawMessage(degradedCapabilityPayload),
		CapabilitiesHash: stringPtr("sha256:degraded"),
		CapabilityDrift:  &previousNote,
	}, fetcher)

	fixture.sweep()

	if stored := fixture.storedNode(t); stored.CapabilityDrift != nil {
		t.Fatalf("capability_drift = %q after recovery, want it cleared", *stored.CapabilityDrift)
	}
}

// A still-degraded report is not a recovery. Drift is a delta, so the refetch
// after a regression finds nothing *newly* lost — and clearing the note there
// would tell an operator the node is fine while its backend is still failing its
// probe. Anything that moves the hash reaches this path: a reboot moves boot_id,
// a reworded FFmpeg failure moves the probe reason.
func TestHealthCheckerKeepsCapabilityDriftWhileTheReportIsStillDegraded(t *testing.T) {
	const rebootedPayload = `{"resolved":"none","render_devices":[],` +
		`"detected_backends":[{"backend":"nvenc","verified":false,"reason":"no such device"}],` +
		`"boot_id":"after-reboot","capability_hash":"sha256:rebooted"}`
	url := newHealthNode(t, "sha256:rebooted")
	fetcher := &fakeCapabilityFetcher{payload: []byte(rebootedPayload), hash: "sha256:rebooted"}
	previousNote := "verified hardware backends lost: nvenc"
	fixture := newCapabilityCheckerFixture(t, &Node{
		ID: 1, Name: "gpu-1", Type: NodeTypeTranscode, URL: url, Enabled: true,
		Capabilities:     json.RawMessage(degradedCapabilityPayload),
		CapabilitiesHash: stringPtr("sha256:degraded"),
		CapabilityDrift:  &previousNote,
	}, fetcher)

	fixture.sweep()

	stored := fixture.storedNode(t)
	if stored.CapabilityDrift == nil {
		t.Fatal("capability_drift cleared by a refetch that found the node still degraded")
	}
	if *stored.CapabilityDrift != previousNote {
		t.Fatalf("capability_drift = %q, want the standing note %q", *stored.CapabilityDrift, previousNote)
	}
}

// The operator-triggered re-probe refetches unconditionally, with no hash gate.
// It is the action the docs and the UI tooltip prescribe for checking whether a
// drift note is still true, so it must be able to answer "yes" — clearing the
// badge on the first click regardless of what the probe found would make the
// only tool for confirming the note the tool that destroys it.
func TestRefreshNodeCapabilitiesKeepsDriftWhenNothingRecovered(t *testing.T) {
	url := newHealthNode(t, "sha256:degraded")
	fetcher := &fakeCapabilityFetcher{payload: []byte(degradedCapabilityPayload), hash: "sha256:degraded"}
	previousNote := "verified hardware backends lost: nvenc"
	node := &Node{
		ID: 1, Name: "gpu-1", Type: NodeTypeTranscode, URL: url, Enabled: true,
		Capabilities:     json.RawMessage(degradedCapabilityPayload),
		CapabilitiesHash: stringPtr("sha256:degraded"),
		CapabilityDrift:  &previousNote,
	}
	fixture := newCapabilityCheckerFixture(t, node, fetcher)

	if err := fixture.checker.RefreshNodeCapabilities(context.Background(), node); err != nil {
		t.Fatalf("RefreshNodeCapabilities: %v", err)
	}

	if stored := fixture.storedNode(t); stored.CapabilityDrift == nil {
		t.Fatal("a re-probe that found the same failing probe cleared the drift note")
	}
}

// Clearing the note requires evidence that hardware came back, and a report in
// which nothing was probed carries none.
//
// The two shapes that produce no passing probe are the two ways hardware goes
// away: a device the node can no longer open reports the backend `skipped`, and
// a card that is gone entirely leaves no candidate backend to report at all.
// Both used to read as clean — the first because a skipped backend is not a
// failure, the second because a loop over an empty list finds none — so a
// standing regression was erased by the next unrelated hash change (a reboot
// moving boot_id is enough), telling an operator a still-broken node had
// recovered.
//
// A proxy pointed at a cluster-wide hw_device does not get stuck behind this:
// it never verified those backends in the first place, so computeCapabilityDrift
// never gives it a note to hold open.
func TestResolveDriftNoteKeepsNoteWhenNoProbePassed(t *testing.T) {
	tests := []struct {
		name    string
		payload string
	}{
		{
			name: "every candidate device is inaccessible",
			payload: `{"resolved":"none","render_devices":[],` +
				`"detected_backends":[{"backend":"vaapi","verified":false,"skipped":true}]}`,
		},
		{
			name:    "the gpu is gone, so nothing was a candidate",
			payload: `{"resolved":"none","render_devices":[],"detected_backends":[]}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			standing := "verified hardware backends lost: vaapi"
			payload := []byte(test.payload)
			// Both sides degraded: the delta finds nothing newly lost, which is
			// exactly the state in which clearing has to be refused.
			drift, parsed := computeCapabilityDrift(payload, payload)
			got := resolveDriftNote(&standing, drift, parsed, payload)
			if got == nil {
				t.Fatal("capability_drift was cleared by a report in which no probe passed")
			}
			if *got != standing {
				t.Fatalf("capability_drift = %q, want the standing note %q", *got, standing)
			}
		})
	}
}

// The complement: a report with a backend that actually passed its probe is the
// evidence recovery needs, and clears the note.
func TestResolveDriftNoteClearsOnAPassingProbe(t *testing.T) {
	const recoveredPayload = `{"resolved":"vaapi","render_devices":["/dev/dri/renderD128"],` +
		`"detected_backends":[{"backend":"vaapi","verified":true},` +
		`{"backend":"qsv","verified":false,"skipped":true}]}`
	standing := "verified hardware backends lost: vaapi"
	payload := []byte(recoveredPayload)
	drift, parsed := computeCapabilityDrift(payload, payload)
	if got := resolveDriftNote(&standing, drift, parsed, payload); got != nil {
		t.Fatalf("capability_drift = %q, want a verified backend to clear it", *got)
	}
}

// A node's very first report has nothing to compare against, so it must not be
// flagged.
func TestHealthCheckerStoresNoDriftOnFirstReport(t *testing.T) {
	url := newHealthNode(t, "sha256:new")
	fetcher := &fakeCapabilityFetcher{payload: []byte(testCapabilityPayload), hash: "sha256:new"}
	fixture := newCapabilityCheckerFixture(t, &Node{
		ID: 1, Name: "gpu-1", Type: NodeTypeTranscode, URL: url, Enabled: true,
	}, fetcher)

	fixture.sweep()

	if stored := fixture.storedNode(t); stored.CapabilityDrift != nil {
		t.Fatalf("capability_drift = %q on a first report, want none", *stored.CapabilityDrift)
	}
}

// An unreadable payload means the drift is unknown, not that the node recovered.
func TestComputeCapabilityDriftReportsUnparseablePayloads(t *testing.T) {
	if _, parsed := computeCapabilityDrift([]byte(testCapabilityPayload), []byte(`not json`)); parsed {
		t.Fatal("an unreadable new report parsed")
	}
	if _, parsed := computeCapabilityDrift([]byte(`not json`), []byte(testCapabilityPayload)); parsed {
		t.Fatal("an unreadable stored report parsed")
	}
	drift, parsed := computeCapabilityDrift(nil, []byte(testCapabilityPayload))
	if !parsed || !drift.first || drift.regressed() {
		t.Fatalf("first report drift = %+v, parsed = %v", drift, parsed)
	}
}

// The note is echoed to every admin listing nodes, and its inputs come from a
// worker that may run on remote hardware.
func TestCapabilityDriftNoteIsBounded(t *testing.T) {
	drift := capabilityDrift{}
	for range 400 {
		drift.lostDevices = append(drift.lostDevices, "/dev/dri/renderD128")
	}
	note := drift.persistedNote()
	if note == nil {
		t.Fatal("a regression produced no note")
	}
	if len(*note) > maxCapabilityDriftNoteBytes+3 {
		t.Fatalf("note is %d bytes, want it bounded at %d", len(*note), maxCapabilityDriftNoteBytes)
	}
}

// capability_drift is a Postgres text column, which rejects invalid UTF-8
// outright — and the rejected UPDATE takes capabilities and capabilities_hash
// with it, so the stored hash never advances and every later sweep refetches and
// fails again. Device names come from a worker that may run elsewhere, so the
// bound has to cut on a rune boundary at every alignment, not just the lucky
// ones.
func TestCapabilityDriftNoteStaysValidUTF8AtEveryTruncationOffset(t *testing.T) {
	for pad := range 8 {
		drift := capabilityDrift{}
		for range 40 {
			drift.lostDevices = append(drift.lostDevices,
				"/dev/dri/"+strings.Repeat("x", pad)+strings.Repeat("é", 12))
		}
		note := drift.persistedNote()
		if note == nil {
			t.Fatalf("pad %d: a regression produced no note", pad)
		}
		if len(*note) <= maxCapabilityDriftNoteBytes {
			t.Fatalf("pad %d: note is %d bytes, the fixture must exceed the bound", pad, len(*note))
		}
		if !utf8.ValidString(*note) {
			t.Fatalf("pad %d: truncated note is not valid UTF-8: %q", pad, *note)
		}
	}
}

// The operator-triggered re-probe stores the node's new report immediately, and
// must go through the same fetch, drift, and publish path the sweep uses rather
// than a second implementation.
func TestRefreshNodeCapabilitiesStoresImmediately(t *testing.T) {
	url := newHealthNode(t, "sha256:degraded")
	fetcher := &fakeCapabilityFetcher{payload: []byte(degradedCapabilityPayload), hash: "sha256:degraded"}
	node := &Node{
		ID: 1, Name: "gpu-1", Type: NodeTypeTranscode, URL: url, Enabled: true,
		Capabilities:     json.RawMessage(testCapabilityPayload),
		CapabilitiesHash: stringPtr("sha256:old"),
	}
	fixture := newCapabilityCheckerFixture(t, node, fetcher)

	if err := fixture.checker.RefreshNodeCapabilities(context.Background(), node); err != nil {
		t.Fatalf("RefreshNodeCapabilities: %v", err)
	}

	stored := fixture.storedNode(t)
	if stored.CapabilitiesHash == nil || *stored.CapabilitiesHash != "sha256:degraded" {
		t.Fatalf("stored hash = %v, want the refetched report", stored.CapabilitiesHash)
	}
	if stored.CapabilityDrift == nil {
		t.Fatal("an immediate refresh did not persist the drift the sweep would have")
	}
	// The capability cache must be told, exactly as on a sweep refresh.
	if notifications := fixture.notifications(); len(notifications) != 1 || notifications[0] != url {
		t.Fatalf("notifications = %v, want one for %s", notifications, url)
	}
	if got := fetcher.callCount(); got != 1 {
		t.Fatalf("fetch calls = %d, want exactly one", got)
	}
}

// A refresh already running is at least as fresh as the one being asked for, so
// the second caller is told rather than starting a duplicate fetch.
func TestRefreshNodeCapabilitiesRefusesWhenAlreadyInFlight(t *testing.T) {
	url := newHealthNode(t, "sha256:new")
	fetcher := &fakeCapabilityFetcher{payload: []byte(testCapabilityPayload), hash: "sha256:new"}
	node := &Node{ID: 1, Name: "gpu-1", Type: NodeTypeTranscode, URL: url, Enabled: true}
	fixture := newCapabilityCheckerFixture(t, node, fetcher)

	fixture.checker.capabilityRefreshInFlight.Store(node.ID, struct{}{})
	defer fixture.checker.capabilityRefreshInFlight.Delete(node.ID)

	if err := fixture.checker.RefreshNodeCapabilities(context.Background(), node); err == nil {
		t.Fatal("a duplicate refresh was allowed to start")
	}
	if got := fetcher.callCount(); got != 0 {
		t.Fatalf("fetch calls = %d, want none while a refresh is in flight", got)
	}
}

// DRM is free to hand the same card a different renderD number across a reboot.
// Comparing enumeration paths alone then reports a GPU as gone, and because the
// reboot moves boot_id it also triggers the refetch that persists the note — so
// an operator sees a hardware regression for a card that never moved.
func TestComputeCapabilityDriftMatchesRenumberedRenderDevices(t *testing.T) {
	const before = `{"resolved":"qsv","render_devices":["/dev/dri/renderD128"],` +
		`"render_device_details":[{"path":"/dev/dri/renderD128","pci_address":"0000:03:00.0"}],` +
		`"detected_backends":[{"backend":"qsv","verified":true}]}`
	const after = `{"resolved":"qsv","render_devices":["/dev/dri/renderD129"],` +
		`"render_device_details":[{"path":"/dev/dri/renderD129","pci_address":"0000:03:00.0"}],` +
		`"detected_backends":[{"backend":"qsv","verified":true}]}`

	drift, parsed := computeCapabilityDrift([]byte(before), []byte(after))
	if !parsed {
		t.Fatal("both reports should parse")
	}
	if drift.regressed() {
		t.Fatalf("drift = %+v, want a renumbered path at the same PCI slot to be no regression", drift)
	}
}

// A card that genuinely goes away has neither its path nor its slot in the new
// report, and must still be caught.
func TestComputeCapabilityDriftStillCatchesARemovedDevice(t *testing.T) {
	const before = `{"resolved":"qsv","render_devices":["/dev/dri/renderD128","/dev/dri/renderD129"],` +
		`"render_device_details":[{"path":"/dev/dri/renderD128","pci_address":"0000:03:00.0"},` +
		`{"path":"/dev/dri/renderD129","pci_address":"0000:04:00.0"}],` +
		`"detected_backends":[{"backend":"qsv","verified":true}]}`
	const after = `{"resolved":"qsv","render_devices":["/dev/dri/renderD128"],` +
		`"render_device_details":[{"path":"/dev/dri/renderD128","pci_address":"0000:03:00.0"}],` +
		`"detected_backends":[{"backend":"qsv","verified":true}]}`

	drift, parsed := computeCapabilityDrift([]byte(before), []byte(after))
	if !parsed {
		t.Fatal("both reports should parse")
	}
	if len(drift.lostDevices) != 1 || drift.lostDevices[0] != "/dev/dri/renderD129" {
		t.Fatalf("lostDevices = %v, want the card at 0000:04:00.0 reported gone", drift.lostDevices)
	}
}

// An NVIDIA uuid outranks the slot, so a card moved between slots is still the
// same card.
func TestComputeCapabilityDriftMatchesAMovedCardByUUID(t *testing.T) {
	const before = `{"resolved":"nvenc","render_devices":["/dev/dri/renderD128"],` +
		`"render_device_details":[{"path":"/dev/dri/renderD128","pci_address":"0000:03:00.0","gpu_uuid":"GPU-abc"}],` +
		`"detected_backends":[{"backend":"nvenc","verified":true}]}`
	const after = `{"resolved":"nvenc","render_devices":["/dev/dri/renderD130"],` +
		`"render_device_details":[{"path":"/dev/dri/renderD130","pci_address":"0000:07:00.0","gpu_uuid":"GPU-abc"}],` +
		`"detected_backends":[{"backend":"nvenc","verified":true}]}`

	drift, parsed := computeCapabilityDrift([]byte(before), []byte(after))
	if !parsed || drift.regressed() {
		t.Fatalf("drift = %+v (parsed=%v), want the same uuid to be the same card", drift, parsed)
	}
}

// A node that predates render_device_details reports paths only, and must still
// be comparable.
func TestComputeCapabilityDriftFallsBackToPathsWithoutDetails(t *testing.T) {
	const before = `{"resolved":"qsv","render_devices":["/dev/dri/renderD128"],` +
		`"detected_backends":[{"backend":"qsv","verified":true}]}`
	const after = `{"resolved":"none","render_devices":[],` +
		`"detected_backends":[{"backend":"qsv","verified":false}]}`

	drift, parsed := computeCapabilityDrift([]byte(before), []byte(after))
	if !parsed {
		t.Fatal("both reports should parse")
	}
	if len(drift.lostDevices) != 1 || drift.lostDevices[0] != "/dev/dri/renderD128" {
		t.Fatalf("lostDevices = %v, want the path-only device reported gone", drift.lostDevices)
	}
}

// nvidia-smi is queried behind a circuit breaker, so the same NVIDIA card
// publishes a uuid on one pass and only its PCI address on another. Keeping
// just the strongest identity made those two reports describe different
// devices, persisting a "render device gone" note for a card that never moved.
func TestComputeCapabilityDriftMatchesAcrossIdentityStrength(t *testing.T) {
	const withUUID = `{"resolved":"nvenc","render_devices":["/dev/dri/renderD128"],` +
		`"render_device_details":[{"path":"/dev/dri/renderD128","pci_address":"0000:03:00.0","gpu_uuid":"GPU-abc"}],` +
		`"detected_backends":[{"backend":"nvenc","verified":true}]}`
	const withoutUUID = `{"resolved":"nvenc","render_devices":["/dev/dri/renderD128"],` +
		`"render_device_details":[{"path":"/dev/dri/renderD128","pci_address":"0000:03:00.0"}],` +
		`"detected_backends":[{"backend":"nvenc","verified":true}]}`

	for _, test := range []struct{ name, before, after string }{
		{"uuid disappears", withUUID, withoutUUID},
		{"uuid appears", withoutUUID, withUUID},
	} {
		t.Run(test.name, func(t *testing.T) {
			drift, parsed := computeCapabilityDrift([]byte(test.before), []byte(test.after))
			if !parsed {
				t.Fatal("both reports should parse")
			}
			if drift.regressed() {
				t.Fatalf("drift = %+v, want a shared PCI alias to identify the same card", drift)
			}
		})
	}
}
