package controlplane_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	contractsconfig "github.com/andyjmorgan/sluice-gateway/contracts/config"
	"github.com/andyjmorgan/sluice-gateway/internal/config"
	"github.com/andyjmorgan/sluice-gateway/internal/controlplane"
	"github.com/andyjmorgan/sluice-gateway/internal/controlplane/configdb"
)

func TestEntityRoundTripFromConfigDev(t *testing.T) {
	resolved, err := config.LoadV2(context.Background(), "../../config-dev")
	if err != nil {
		t.Skipf("config-dev not loadable: %v", err)
	}

	entities, err := controlplane.EntityFromConfig(resolved)
	if err != nil {
		t.Fatalf("EntityFromConfig: %v", err)
	}

	// api-key entities are keyed by a uuid.
	for _, e := range entities {
		if e.Kind == controlplane.KindAPIKey {
			if _, perr := uuid.Parse(e.Name); perr != nil {
				t.Fatalf("api_key entity name is not a uuid: %q", e.Name)
			}
		}
	}

	rc, err := controlplane.EntitiesToConfig(entities)
	if err != nil {
		t.Fatalf("EntitiesToConfig: %v", err)
	}
	if len(rc.Backends) != len(resolved.Backends) {
		t.Fatalf("backends: %d, want %d", len(rc.Backends), len(resolved.Backends))
	}
	if len(rc.Configurations) != len(resolved.Configurations) {
		t.Fatalf("configurations: %d, want %d", len(rc.Configurations), len(resolved.Configurations))
	}
	if len(rc.APIKeys) != len(resolved.APIKeys) {
		t.Fatalf("api_keys: %d, want %d", len(rc.APIKeys), len(resolved.APIKeys))
	}
	if len(rc.Rules) != len(resolved.Rules) {
		t.Fatalf("rules: %d, want %d", len(rc.Rules), len(resolved.Rules))
	}

	// The recomposed config must still validate.
	body, err := config.MarshalConfig(rc)
	if err != nil {
		t.Fatalf("MarshalConfig(recomposed): %v", err)
	}
	if _, err := config.ResolveClosure(body); err != nil {
		t.Fatalf("recomposed config does not validate: %v", err)
	}
}

func TestEntityFromConfig_APIKeyIDHandling(t *testing.T) {
	id := uuid.New()
	resolved := &config.ResolvedConfigV2{
		APIKeys: contractsconfig.APIKeysConfig{
			{Secret: "sk_explicit", Name: "withid", Configuration: "c", ID: &id}, //nolint:gosec // test fixture, not a credential
			{Secret: "sk_minted", Name: "noid", Configuration: "c"},              //nolint:gosec // test fixture, not a credential
		},
	}
	entities, err := controlplane.EntityFromConfig(resolved)
	if err != nil {
		t.Fatal(err)
	}
	if len(entities) != 2 {
		t.Fatalf("entities = %d, want 2", len(entities))
	}

	var sawExplicit bool
	for _, e := range entities {
		if e.Kind != controlplane.KindAPIKey {
			t.Fatalf("unexpected kind %q", e.Kind)
		}
		if _, perr := uuid.Parse(e.Name); perr != nil {
			t.Fatalf("entity name not a uuid: %q", e.Name)
		}
		if e.Name == id.String() {
			sawExplicit = true
		}
	}
	if !sawExplicit {
		t.Fatal("explicit api-key ID was not preserved as the entity key")
	}
}

func TestEntityFromConfig_NilResolved(t *testing.T) {
	if _, err := controlplane.EntityFromConfig(nil); err == nil {
		t.Fatal("want error for nil resolved config")
	}
}

func TestEntitiesToConfig_UnknownKind(t *testing.T) {
	if _, err := controlplane.EntitiesToConfig([]configdb.Entity{{Kind: "bogus", Name: "x", Body: []byte("{}")}}); err == nil {
		t.Fatal("want error for unknown kind")
	}
}

func TestEntitiesToConfig_DecodeErrorPerKind(t *testing.T) {
	kinds := []string{
		controlplane.KindBackend,
		controlplane.KindGroup,
		controlplane.KindConfiguration,
		controlplane.KindAPIKey,
		controlplane.KindRule,
		controlplane.KindConnector,
	}
	for _, kind := range kinds {
		t.Run(kind, func(t *testing.T) {
			_, err := controlplane.EntitiesToConfig([]configdb.Entity{{Kind: kind, Name: "x", Body: []byte("{{{")}})
			if err == nil {
				t.Fatalf("kind %q: want decode error for malformed body", kind)
			}
		})
	}
}
