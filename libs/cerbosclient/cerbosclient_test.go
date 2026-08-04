package cerbosclient_test

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	effectv1 "github.com/cerbos/cerbos/api/genpb/cerbos/effect/v1"
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

func TestNewRejectsAnEmptyAddress(t *testing.T) {
	_, err := cerbosclient.New(cerbosclient.Config{})
	if err == nil {
		t.Fatal("New with no address returned no error")
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
	}
}
