package controlplane

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/andyjmorgan/sluice-gateway/internal/controlplane/configdb"
)

var _ Registry = (*DBRegistry)(nil)

type fakeFleetStore struct {
	registerCalls  []registerCall
	heartbeatCalls []heartbeatCall
	ret            configdb.FleetGateway
	list           []configdb.FleetGateway
	err            error
}

type registerCall struct {
	id      string
	version string
	labels  map[string]string
}

type heartbeatCall struct {
	id      string
	version string
	labels  map[string]string
	hashes  []string
}

func (f *fakeFleetStore) RegisterGateway(_ context.Context, id, version string, labels map[string]string) (configdb.FleetGateway, error) {
	f.registerCalls = append(f.registerCalls, registerCall{id, version, labels})
	return f.ret, f.err
}

func (f *fakeFleetStore) HeartbeatGateway(_ context.Context, id, version string, labels map[string]string, hashes []string) (configdb.FleetGateway, error) {
	f.heartbeatCalls = append(f.heartbeatCalls, heartbeatCall{id, version, labels, hashes})
	return f.ret, f.err
}

func (f *fakeFleetStore) ListGateways(_ context.Context) ([]configdb.FleetGateway, error) {
	return f.list, f.err
}

func TestDBRegistry_MissingGatewayID(t *testing.T) {
	store := &fakeFleetStore{}
	r := NewDBRegistry(store)

	if _, err := r.Register(context.Background(), RegisterInput{}); !errors.Is(err, ErrMissingGatewayID) {
		t.Errorf("Register: want ErrMissingGatewayID, got %v", err)
	}
	if _, err := r.Heartbeat(context.Background(), HeartbeatInput{}); !errors.Is(err, ErrMissingGatewayID) {
		t.Errorf("Heartbeat: want ErrMissingGatewayID, got %v", err)
	}
	if len(store.registerCalls)+len(store.heartbeatCalls) != 0 {
		t.Error("store was called despite missing id")
	}
}

func TestDBRegistry_RegisterAndHeartbeatMap(t *testing.T) {
	now := time.Now().UTC()
	store := &fakeFleetStore{
		ret: configdb.FleetGateway{
			ID:                 "gw-1",
			Version:            "v9",
			Labels:             map[string]string{"role": "edge"},
			CachedConfigHashes: []string{"h1"},
			RegisteredAt:       now,
			LastSeen:           now,
		},
	}
	r := NewDBRegistry(store)

	g, err := r.Register(context.Background(), RegisterInput{ID: "gw-1", Version: "v9", Labels: map[string]string{"role": "edge"}})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if g.ID != "gw-1" || g.Version != "v9" || g.Labels["role"] != "edge" || g.CachedConfigHashes[0] != "h1" {
		t.Errorf("Register map = %+v", g)
	}
	if store.registerCalls[0].id != "gw-1" {
		t.Errorf("store register call = %+v", store.registerCalls[0])
	}

	hb, err := r.Heartbeat(context.Background(), HeartbeatInput{ID: "gw-1", Version: "v9", CachedConfigHashes: []string{"h1"}})
	if err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	if hb.ID != "gw-1" || hb.LastSeen != now {
		t.Errorf("Heartbeat map = %+v", hb)
	}
	if store.heartbeatCalls[0].hashes[0] != "h1" {
		t.Errorf("store heartbeat call = %+v", store.heartbeatCalls[0])
	}
}

func TestDBRegistry_ListNormalisesEmptyCollections(t *testing.T) {
	store := &fakeFleetStore{
		list: []configdb.FleetGateway{
			{ID: "gw-a", Labels: map[string]string{}, CachedConfigHashes: []string{}},
			{ID: "gw-b", Labels: map[string]string{"k": "v"}},
		},
	}
	r := NewDBRegistry(store)

	got, err := r.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("List len = %d", len(got))
	}
	// Empty map/slice normalise to nil so the DB registry renders identically
	// to MemoryRegistry through the read API.
	if got[0].Labels != nil || got[0].CachedConfigHashes != nil {
		t.Errorf("empty collections not normalised: %+v", got[0])
	}
	if got[1].Labels["k"] != "v" {
		t.Errorf("populated labels lost: %+v", got[1])
	}
}

func TestDBRegistry_PropagatesStoreErrors(t *testing.T) {
	sentinel := errors.New("db down")
	store := &fakeFleetStore{err: sentinel}
	r := NewDBRegistry(store)

	if _, err := r.Register(context.Background(), RegisterInput{ID: "gw-1"}); !errors.Is(err, sentinel) {
		t.Errorf("Register err = %v", err)
	}
	if _, err := r.Heartbeat(context.Background(), HeartbeatInput{ID: "gw-1"}); !errors.Is(err, sentinel) {
		t.Errorf("Heartbeat err = %v", err)
	}
	if _, err := r.List(context.Background()); !errors.Is(err, sentinel) {
		t.Errorf("List err = %v", err)
	}
}
