package cerbosclient_test

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	effectv1 "github.com/cerbos/cerbos/api/genpb/cerbos/effect/v1"
	enginev1 "github.com/cerbos/cerbos/api/genpb/cerbos/engine/v1"
	requestv1 "github.com/cerbos/cerbos/api/genpb/cerbos/request/v1"
	responsev1 "github.com/cerbos/cerbos/api/genpb/cerbos/response/v1"
	svcv1 "github.com/cerbos/cerbos/api/genpb/cerbos/svc/v1"
	"google.golang.org/grpc"

	"github.com/tishan-harischandra/cerbos-poc/libs/cerbosclient"
)

func TestCheckReportsADecisionForEveryRequestedLeaf(t *testing.T) {
	pdp := startFakePDP(t, func(*requestv1.CheckResourcesRequest) *responsev1.CheckResourcesResponse {
		return &responsev1.CheckResourcesResponse{
			CerbosCallId: "call-1",
			Results: []*responsev1.CheckResourcesResponse_ResultEntry{
				resultFor("patient-456", map[string]effectv1.Effect{
					"read":   effectv1.Effect_EFFECT_ALLOW,
					"update": effectv1.Effect_EFFECT_DENY,
				}),
			},
		}
	})

	client := dial(t, pdp.address)

	result, err := client.Check(context.Background(), cerbosclient.Request{
		Principal: cerbosclient.Principal{ID: "user-123", Attr: map[string]any{"tenantId": "tenant-a"}},
		Resources: []cerbosclient.ResourceCheck{{
			Resource: cerbosclient.ResourceRef{Kind: "patient_record", ID: "patient-456"},
			Attr:     map[string]any{"tenantId": "tenant-a"},
			Actions:  []string{"read", "update"},
		}},
	})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}

	readLeaf := cerbosclient.Leaf{
		Resource: cerbosclient.ResourceRef{Kind: "patient_record", ID: "patient-456"},
		Action:   "read",
	}
	updateLeaf := cerbosclient.Leaf{
		Resource: cerbosclient.ResourceRef{Kind: "patient_record", ID: "patient-456"},
		Action:   "update",
	}

	if got, ok := result.Decisions[readLeaf]; !ok || !got.Allowed {
		t.Errorf("read decision = %+v (present %t), want allowed", got, ok)
	}
	if got, ok := result.Decisions[updateLeaf]; !ok || got.Allowed {
		t.Errorf("update decision = %+v (present %t), want denied", got, ok)
	}
	if len(result.Decisions) != 2 {
		t.Errorf("decisions = %d, want 2", len(result.Decisions))
	}
}

// §11.3 requires the Cerbos call ID to be logged alongside the application
// correlation ID, so the client has to surface it rather than discard it.
func TestCheckCapturesTheCerbosCallID(t *testing.T) {
	pdp := startFakePDP(t, func(*requestv1.CheckResourcesRequest) *responsev1.CheckResourcesResponse {
		return &responsev1.CheckResourcesResponse{
			CerbosCallId: "01HQ8WZ5F3",
			Results: []*responsev1.CheckResourcesResponse_ResultEntry{
				resultFor("patient-456", map[string]effectv1.Effect{"read": effectv1.Effect_EFFECT_ALLOW}),
			},
		}
	})

	result, err := dial(t, pdp.address).Check(context.Background(), readRequest())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}

	if result.CallID != "01HQ8WZ5F3" {
		t.Errorf("CallID = %q, want %q", result.CallID, "01HQ8WZ5F3")
	}
}

// §11.3/§21: labelling a decision's source has to read a fact the PDP already
// computed, not rank grants against revokes in Go. The rule names in the
// PDP's own output reporting are that fact, so the client must surface them
// exactly as received rather than interpret them.
func TestCheckReportsTheFiredRuleNamesPerResource(t *testing.T) {
	pdp := startFakePDP(t, func(*requestv1.CheckResourcesRequest) *responsev1.CheckResourcesResponse {
		entry := resultFor("patient-456", map[string]effectv1.Effect{"read": effectv1.Effect_EFFECT_ALLOW})
		entry.Outputs = []*enginev1.OutputEntry{
			{Src: "resource.patient_record.vdefault/default#grant_read_to_role"},
			{Src: "resource.patient_record.vdefault/default#tenant_and_hospital_isolation"},
		}
		return &responsev1.CheckResourcesResponse{
			CerbosCallId: "call-1",
			Results:      []*responsev1.CheckResourcesResponse_ResultEntry{entry},
		}
	})

	result, err := dial(t, pdp.address).Check(context.Background(), readRequest())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}

	ref := cerbosclient.ResourceRef{Kind: "patient_record", ID: "patient-456"}
	got := result.FiredRules[ref]
	want := []string{"grant_read_to_role", "tenant_and_hospital_isolation"}
	if len(got) != len(want) {
		t.Fatalf("FiredRules[%v] = %v, want %v", ref, got, want)
	}
	for i, name := range want {
		if got[i] != name {
			t.Errorf("FiredRules[%v][%d] = %q, want %q", ref, i, got[i], name)
		}
	}
}

func TestCheckReportsNoFiredRulesWhenThePDPSendsNoOutputs(t *testing.T) {
	pdp := startFakePDP(t, func(*requestv1.CheckResourcesRequest) *responsev1.CheckResourcesResponse {
		return &responsev1.CheckResourcesResponse{
			CerbosCallId: "call-1",
			Results: []*responsev1.CheckResourcesResponse_ResultEntry{
				resultFor("patient-456", map[string]effectv1.Effect{"read": effectv1.Effect_EFFECT_ALLOW}),
			},
		}
	})

	result, err := dial(t, pdp.address).Check(context.Background(), readRequest())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}

	ref := cerbosclient.ResourceRef{Kind: "patient_record", ID: "patient-456"}
	if len(result.FiredRules[ref]) != 0 {
		t.Errorf("FiredRules[%v] = %v, want none", ref, result.FiredRules[ref])
	}
}

// ADR: the gRPC channel is long-lived. Dialling per request would add a TCP and
// TLS handshake to every authorization decision, which is the hot path.
func TestTheGRPCChannelIsCreatedOncePerClientNotPerRequest(t *testing.T) {
	pdp := startFakePDP(t, func(*requestv1.CheckResourcesRequest) *responsev1.CheckResourcesResponse {
		return &responsev1.CheckResourcesResponse{
			CerbosCallId: "call-1",
			Results: []*responsev1.CheckResourcesResponse_ResultEntry{
				resultFor("patient-456", map[string]effectv1.Effect{"read": effectv1.Effect_EFFECT_ALLOW}),
			},
		}
	})

	client := dial(t, pdp.address)

	for i := range 5 {
		if _, err := client.Check(context.Background(), readRequest()); err != nil {
			t.Fatalf("Check %d: %v", i, err)
		}
	}

	if got := pdp.requests(); got != 5 {
		t.Fatalf("the fake PDP served %d requests, want 5", got)
	}
	if got := pdp.connections(); got != 1 {
		t.Errorf("the client opened %d connections for 5 requests, want 1", got)
	}
}

func TestTheSyntheticRoleIsTheOnlyRoleSentToThePDP(t *testing.T) {
	var seen []string
	pdp := startFakePDP(t, func(req *requestv1.CheckResourcesRequest) *responsev1.CheckResourcesResponse {
		seen = req.GetPrincipal().GetRoles()
		return &responsev1.CheckResourcesResponse{
			CerbosCallId: "call-1",
			Results: []*responsev1.CheckResourcesResponse_ResultEntry{
				resultFor("patient-456", map[string]effectv1.Effect{"read": effectv1.Effect_EFFECT_ALLOW}),
			},
		}
	})

	if _, err := dial(t, pdp.address).Check(context.Background(), readRequest()); err != nil {
		t.Fatalf("Check: %v", err)
	}

	if len(seen) != 1 || seen[0] != cerbosclient.EvaluationRole {
		t.Errorf("roles sent to the PDP = %v, want [%s]", seen, cerbosclient.EvaluationRole)
	}
}

func TestCheckSurfacesATransportFailure(t *testing.T) {
	client, err := cerbosclient.New(cerbosclient.Config{
		Address:      "127.0.0.1:1",
		PlaintextTLS: true,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if _, err := client.Check(ctx, readRequest()); err == nil {
		t.Fatal("Check against a closed port returned no error")
	}
}

// The PDP is restarted routinely, by a policy rollout or a container recreate.
// gRPC's default reconnect backoff climbs to two minutes, which on the decision
// path means the ADS keeps failing closed long after the PDP is answering again.
// Recovery has to be prompt.
func TestTheClientRecoversPromptlyAfterThePDPRestarts(t *testing.T) {
	respond := func(*requestv1.CheckResourcesRequest) *responsev1.CheckResourcesResponse {
		return &responsev1.CheckResourcesResponse{
			CerbosCallId: "call-1",
			Results: []*responsev1.CheckResourcesResponse_ResultEntry{
				resultFor("patient-456", map[string]effectv1.Effect{
					"read": effectv1.Effect_EFFECT_ALLOW,
				}),
			},
		}
	}

	pdp := startFakePDP(t, respond)
	client := dial(t, pdp.address)

	if _, err := client.Check(context.Background(), readRequest()); err != nil {
		t.Fatalf("first Check: %v", err)
	}

	// Drop the PDP and let the channel see the failure, so it enters backoff.
	pdp.stop()
	if _, err := client.Check(context.Background(), readRequest()); err == nil {
		t.Fatal("Check succeeded while the PDP was down")
	}

	restartFakePDP(t, pdp, respond)

	deadline := time.Now().Add(20 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		if _, lastErr = client.Check(context.Background(), readRequest()); lastErr == nil {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("the client never recovered after the PDP came back: %v", lastErr)
}

// A decision request must not sit waiting on a dead address. gRPC's default
// connect timeout is 20 seconds, so when the PDP moves to a new IP - a container
// recreate, a rollout - the ADS holds each caller for 20s before failing closed,
// and the channel only re-resolves DNS once the attempt gives up. Bounding the
// attempt is what turns a half-minute outage into a few seconds.
func TestAConnectionToAnUnreachableAddressFailsFast(t *testing.T) {
	// TEST-NET-1 (RFC 5737) is reserved and routed nowhere, so a connection
	// attempt hangs rather than being refused.
	client, err := cerbosclient.New(cerbosclient.Config{
		Address:      "192.0.2.1:3593",
		PlaintextTLS: true,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	// Comfortably below gRPC's 20s default, comfortably above our own bound.
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()

	start := time.Now()
	_, err = client.Check(ctx, readRequest())
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("Check against an unroutable address succeeded")
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatalf("Check was still waiting after %s; the connect attempt is unbounded", elapsed)
	}
}

func TestNewRejectsAnEmptyAddress(t *testing.T) {
	_, err := cerbosclient.New(cerbosclient.Config{})
	if err == nil {
		t.Fatal("New with no address returned no error")
	}
}

// The ADS sends idpRoles as a []string. Nothing in the policy requires it, so if
// the SDK dropped that shape the way it drops structs, the roles would vanish
// from every audit record and no test would notice. Assert against the encoded
// protobuf the PDP actually receives.
func TestStringSliceAttributesSurviveEncoding(t *testing.T) {
	var received *requestv1.CheckResourcesRequest
	pdp := startFakePDP(t, func(req *requestv1.CheckResourcesRequest) *responsev1.CheckResourcesResponse {
		received = req
		return &responsev1.CheckResourcesResponse{}
	})

	_, err := dial(t, pdp.address).Check(context.Background(), cerbosclient.Request{
		Principal: cerbosclient.Principal{
			ID: "user-123",
			Attr: map[string]any{
				"idpRoles": []string{"kc:tenant-a:realm:doctor", "kc:tenant-a:realm:nurse"},
			},
		},
		Resources: []cerbosclient.ResourceCheck{{
			Resource: cerbosclient.ResourceRef{Kind: "patient_record", ID: "patient-456"},
			Attr:     map[string]any{"tenantId": "tenant-a"},
			Actions:  []string{"read"},
		}},
	})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}

	attr := received.GetPrincipal().GetAttr()
	roles, present := attr["idpRoles"]
	if !present {
		t.Fatalf("idpRoles never reached the PDP; principal attributes were %v", attr)
	}

	values := roles.GetListValue().GetValues()
	if len(values) != 2 {
		t.Fatalf("idpRoles arrived as %v, want a two-element list", roles)
	}
	if got := values[0].GetStringValue(); got != "kc:tenant-a:realm:doctor" {
		t.Errorf("idpRoles[0] = %q, want the role the caller presented", got)
	}
}

// An attribute the wire format cannot carry must fail loudly. The Cerbos SDK
// drops such values silently, which produced a PDP that answered "denied" for
// everything because permissionContext never arrived.
func TestAnAttributeThatCannotBeSentIsReportedRatherThanDropped(t *testing.T) {
	pdp := startFakePDP(t, func(req *requestv1.CheckResourcesRequest) *responsev1.CheckResourcesResponse {
		t.Error("the PDP was called with an attribute that could not be encoded")
		return &responsev1.CheckResourcesResponse{}
	})

	type unencodable struct{ Actions []string }

	_, err := dial(t, pdp.address).Check(context.Background(), cerbosclient.Request{
		Principal: cerbosclient.Principal{ID: "user-123"},
		Resources: []cerbosclient.ResourceCheck{{
			Resource: cerbosclient.ResourceRef{Kind: "patient_record", ID: "patient-456"},
			Attr:     map[string]any{"permissionContext": unencodable{Actions: []string{"read"}}},
			Actions:  []string{"read"},
		}},
	})
	if err == nil {
		t.Fatal("Check accepted an attribute that cannot be encoded")
	}
}

func TestAnUnencodablePrincipalAttributeIsReported(t *testing.T) {
	pdp := startFakePDP(t, func(*requestv1.CheckResourcesRequest) *responsev1.CheckResourcesResponse {
		t.Error("the PDP was called with an attribute that could not be encoded")
		return &responsev1.CheckResourcesResponse{}
	})

	_, err := dial(t, pdp.address).Check(context.Background(), cerbosclient.Request{
		Principal: cerbosclient.Principal{
			ID:   "user-123",
			Attr: map[string]any{"idpRoles": make(chan int)},
		},
		Resources: []cerbosclient.ResourceCheck{{
			Resource: cerbosclient.ResourceRef{Kind: "patient_record", ID: "patient-456"},
			Actions:  []string{"read"},
		}},
	})
	if err == nil {
		t.Fatal("Check accepted a principal attribute that cannot be encoded")
	}
}

func TestCheckRejectsARequestWithNoResources(t *testing.T) {
	pdp := startFakePDP(t, func(*requestv1.CheckResourcesRequest) *responsev1.CheckResourcesResponse {
		t.Error("the PDP was called for a request with no resources")
		return &responsev1.CheckResourcesResponse{}
	})

	_, err := dial(t, pdp.address).Check(context.Background(), cerbosclient.Request{
		Principal: cerbosclient.Principal{ID: "user-123"},
	})
	if err == nil {
		t.Fatal("Check with no resources returned no error")
	}
}

func readRequest() cerbosclient.Request {
	return cerbosclient.Request{
		Principal: cerbosclient.Principal{ID: "user-123", Attr: map[string]any{"tenantId": "tenant-a"}},
		Resources: []cerbosclient.ResourceCheck{{
			Resource: cerbosclient.ResourceRef{Kind: "patient_record", ID: "patient-456"},
			Attr:     map[string]any{"tenantId": "tenant-a"},
			Actions:  []string{"read"},
		}},
	}
}

func dial(t *testing.T, address string) *cerbosclient.Client {
	t.Helper()
	client, err := cerbosclient.New(cerbosclient.Config{
		Address:      address,
		PlaintextTLS: true,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func resultFor(id string, actions map[string]effectv1.Effect) *responsev1.CheckResourcesResponse_ResultEntry {
	return &responsev1.CheckResourcesResponse_ResultEntry{
		Resource: &responsev1.CheckResourcesResponse_ResultEntry_Resource{
			Kind: "patient_record",
			Id:   id,
		},
		Actions: actions,
	}
}

// fakePDP is a real gRPC server speaking the Cerbos protocol. It exists to
// observe how the client uses its transport - how many connections it opens and
// what it sends - not to make authorization decisions. Decisions are proven
// against a real PDP by the Cerbos policy suite and the integration test.
type fakePDP struct {
	svcv1.UnimplementedCerbosServiceServer

	respond func(*requestv1.CheckResourcesRequest) *responsev1.CheckResourcesResponse
	mu      sync.Mutex
	served  int
}

func (f *fakePDP) CheckResources(_ context.Context, req *requestv1.CheckResourcesRequest) (*responsev1.CheckResourcesResponse, error) {
	f.mu.Lock()
	f.served++
	f.mu.Unlock()

	if len(req.GetResources()) == 0 {
		return nil, errors.New("no resources in request")
	}
	return f.respond(req), nil
}

type countingListener struct {
	net.Listener
	mu       sync.Mutex
	accepted int
}

func (l *countingListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err == nil {
		l.mu.Lock()
		l.accepted++
		l.mu.Unlock()
	}
	return conn, err
}

type fakePDPHandle struct {
	address  string
	pdp      *fakePDP
	listener *countingListener
	server   *grpc.Server
}

// stop shuts the server down while leaving the address free to be listened on
// again, so a restart can be simulated.
func (h *fakePDPHandle) stop() {
	h.server.Stop()
}

func (h *fakePDPHandle) requests() int {
	h.pdp.mu.Lock()
	defer h.pdp.mu.Unlock()
	return h.pdp.served
}

func (h *fakePDPHandle) connections() int {
	h.listener.mu.Lock()
	defer h.listener.mu.Unlock()
	return h.listener.accepted
}

func startFakePDP(t *testing.T, respond func(*requestv1.CheckResourcesRequest) *responsev1.CheckResourcesResponse) *fakePDPHandle {
	t.Helper()

	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening: %v", err)
	}
	listener := &countingListener{Listener: raw}

	pdp := &fakePDP{respond: respond}
	server := grpc.NewServer()
	svcv1.RegisterCerbosServiceServer(server, pdp)

	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)

	return &fakePDPHandle{
		address:  raw.Addr().String(),
		pdp:      pdp,
		listener: listener,
		server:   server,
	}
}

// restartFakePDP brings a stopped PDP back on the same address, the way a
// container restart or a policy rollout does.
func restartFakePDP(t *testing.T, handle *fakePDPHandle, respond func(*requestv1.CheckResourcesRequest) *responsev1.CheckResourcesResponse) {
	t.Helper()

	raw, err := net.Listen("tcp", handle.address)
	if err != nil {
		t.Fatalf("re-listening on %s: %v", handle.address, err)
	}
	listener := &countingListener{Listener: raw}

	pdp := &fakePDP{respond: respond}
	server := grpc.NewServer()
	svcv1.RegisterCerbosServiceServer(server, pdp)

	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)

	handle.pdp = pdp
	handle.listener = listener
	handle.server = server
}
