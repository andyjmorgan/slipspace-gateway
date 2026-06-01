package controlplane

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/google/uuid"

	contractsconfig "github.com/andyjmorgan/sluice-gateway/contracts/config"
	rulescontract "github.com/andyjmorgan/sluice-gateway/contracts/rules"
	"github.com/andyjmorgan/sluice-gateway/internal/config"
	"github.com/andyjmorgan/sluice-gateway/internal/controlplane/configdb"
)

// Entity kinds stored in configdb. Name-keyed kinds use the config map key (or
// the unique label); api-keys are keyed by their uuid id, since a key's Name is
// a non-unique label and its Secret is the credential.
const (
	KindBackend       = "backend"
	KindGroup         = "group"
	KindConfiguration = "configuration"
	KindAPIKey        = "api_key"
	KindRule          = "rule"
	KindConnector     = "connector"
)

// EntityFromConfig decomposes a resolved v2 config into the editable entity
// rows the control plane stores. Bodies are the contracts/config types as JSON.
// api-keys without an id are assigned one (and it is carried in the body, so it
// is stable thereafter). Output order is deterministic.
func EntityFromConfig(resolved *config.ResolvedConfigV2) ([]configdb.Entity, error) {
	if resolved == nil {
		return nil, fmt.Errorf("controlplane: entity from config: nil resolved config")
	}

	var out []configdb.Entity
	// json.Marshal cannot fail for the concrete contracts/config types (same
	// reasoning as config.appendBlock's ignored Encode error), so the error is
	// dropped — there is no failing input to test against.
	add := func(kind, name string, v any) {
		body, _ := json.Marshal(v)
		out = append(out, configdb.Entity{Kind: kind, Name: name, Body: body})
	}

	for _, name := range sortedKeys(resolved.Backends) {
		add(KindBackend, name, resolved.Backends[name])
	}
	for _, name := range sortedKeys(resolved.Groups) {
		add(KindGroup, name, resolved.Groups[name])
	}
	for _, name := range sortedKeys(resolved.Configurations) {
		add(KindConfiguration, name, resolved.Configurations[name])
	}
	for i := range resolved.APIKeys {
		k := resolved.APIKeys[i]
		if k.ID == nil {
			id := uuid.New()
			k.ID = &id
		}
		add(KindAPIKey, k.ID.String(), k)
	}
	for i := range resolved.Rules {
		add(KindRule, resolved.Rules[i].Name, resolved.Rules[i])
	}
	for i := range resolved.Connectors {
		add(KindConnector, resolved.Connectors[i].Name, resolved.Connectors[i])
	}
	return out, nil
}

// EntitiesToConfig recomposes a resolved v2 config from entity rows — the
// inverse of EntityFromConfig. It does not validate; callers serialize with
// config.MarshalConfig and round-trip through config.ResolveClosure to validate
// before publishing.
func EntitiesToConfig(entities []configdb.Entity) (*config.ResolvedConfigV2, error) {
	r := &config.ResolvedConfigV2{
		Backends:       contractsconfig.BackendsConfig{},
		Groups:         contractsconfig.GroupsConfig{},
		Configurations: map[string]contractsconfig.ConfigurationV2{},
	}
	for _, e := range entities {
		switch e.Kind {
		case KindBackend:
			var b contractsconfig.Backend
			if err := json.Unmarshal(e.Body, &b); err != nil {
				return nil, entityErr(e, err)
			}
			r.Backends[e.Name] = b
		case KindGroup:
			var g contractsconfig.Group
			if err := json.Unmarshal(e.Body, &g); err != nil {
				return nil, entityErr(e, err)
			}
			r.Groups[e.Name] = g
		case KindConfiguration:
			var c contractsconfig.ConfigurationV2
			if err := json.Unmarshal(e.Body, &c); err != nil {
				return nil, entityErr(e, err)
			}
			r.Configurations[e.Name] = c
		case KindAPIKey:
			var k contractsconfig.APIKey
			if err := json.Unmarshal(e.Body, &k); err != nil {
				return nil, entityErr(e, err)
			}
			r.APIKeys = append(r.APIKeys, k)
		case KindRule:
			var rule rulescontract.RuleContract
			if err := json.Unmarshal(e.Body, &rule); err != nil {
				return nil, entityErr(e, err)
			}
			r.Rules = append(r.Rules, rule)
		case KindConnector:
			var c contractsconfig.Connector
			if err := json.Unmarshal(e.Body, &c); err != nil {
				return nil, entityErr(e, err)
			}
			r.Connectors = append(r.Connectors, c)
		default:
			return nil, fmt.Errorf("controlplane: unknown entity kind %q", e.Kind)
		}
	}
	return r, nil
}

func entityErr(e configdb.Entity, err error) error {
	return fmt.Errorf("controlplane: decode %s/%s: %w", e.Kind, e.Name, err)
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
