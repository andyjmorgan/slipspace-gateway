package config

import (
	contractsconfig "github.com/andyjmorgan/sluice-gateway/contracts/config"
)

// Clone returns a deep copy of the resolved v2 config suitable for the admin
// write path: clone → mutate clone → RevalidateAndIndex → persist →
// store.Replace (invariant #9). The indexes are NOT copied — the caller
// rebuilds them via RevalidateAndIndex after mutating, so a clone always passes
// through validation before it is published. The Admin/Telemetry blocks are
// shared by pointer/value as read-only.
func (r *ResolvedConfig) Clone() *ResolvedConfig {
	if r == nil {
		return nil
	}
	out := &ResolvedConfig{
		Admin:       r.Admin,
		Telemetry:   r.Telemetry,
		SourceFiles: cloneStringMap(r.SourceFiles),
	}

	if r.Backends != nil {
		out.Backends = make(contractsconfig.BackendsConfig, len(r.Backends))
		for name, be := range r.Backends {
			out.Backends[name] = cloneBackend(be)
		}
	}
	if r.Groups != nil {
		out.Groups = make(contractsconfig.GroupsConfig, len(r.Groups))
		for name, g := range r.Groups {
			out.Groups[name] = cloneGroup(g)
		}
	}
	if r.Configurations != nil {
		out.Configurations = make(map[string]contractsconfig.Configuration, len(r.Configurations))
		for name, cfg := range r.Configurations {
			out.Configurations[name] = cloneConfiguration(cfg)
		}
	}
	out.APIKeys = cloneAPIKeys(r.APIKeys)
	out.Rules = cloneRules(r.Rules)
	if r.Connectors != nil {
		out.Connectors = make(contractsconfig.ConnectorsConfig, len(r.Connectors))
		copy(out.Connectors, r.Connectors)
	}
	return out
}

// RevalidateAndIndex re-runs validation against the (possibly mutated) clone
// and rebuilds the lookup indexes. The admin write path calls this after
// mutating a clone and before store.Replace.
func (r *ResolvedConfig) RevalidateAndIndex() error {
	if err := r.Validate(); err != nil {
		return err
	}
	r.buildIndexes()
	return nil
}

func cloneBackend(in contractsconfig.Backend) contractsconfig.Backend {
	out := in
	out.RequiredHeaders = cloneStringMap(in.RequiredHeaders)
	out.Query = cloneStringMap(in.Query)
	if in.Protocols != nil {
		out.Protocols = make(map[string]contractsconfig.BackendProtocol, len(in.Protocols))
		for k, p := range in.Protocols {
			cp := p
			if p.Auth != nil {
				a := *p.Auth
				cp.Auth = &a
			}
			out.Protocols[k] = cp
		}
	}
	if in.Passthrough != nil {
		out.Passthrough = make(map[string]contractsconfig.PassthroughFamily, len(in.Passthrough))
		for k, f := range in.Passthrough {
			cf := f
			if f.Auth != nil {
				a := *f.Auth
				cf.Auth = &a
			}
			if f.Paths != nil {
				cf.Paths = make([]contractsconfig.PassthroughPath, len(f.Paths))
				for i, pp := range f.Paths {
					cpp := pp
					cpp.Methods = cloneStringSlice(pp.Methods)
					cf.Paths[i] = cpp
				}
			}
			out.Passthrough[k] = cf
		}
	}
	return out
}

func cloneGroup(in contractsconfig.Group) contractsconfig.Group {
	out := in
	out.FailureStatusCodes = append([]int(nil), in.FailureStatusCodes...)
	if in.CircuitBreaker != nil {
		cb := *in.CircuitBreaker
		out.CircuitBreaker = &cb
	}
	if in.Targets != nil {
		out.Targets = make([]contractsconfig.Target, len(in.Targets))
		for i, t := range in.Targets {
			ct := t
			ct.Query = cloneStringMap(t.Query)
			out.Targets[i] = ct
		}
	}
	return out
}

func cloneConfiguration(in contractsconfig.Configuration) contractsconfig.Configuration {
	out := in
	out.Credentials = cloneStringMap(in.Credentials)
	out.RuleNames = cloneStringSlice(in.RuleNames)
	out.Tags = cloneStringMap(in.Tags)
	if in.Bindings != nil {
		out.Bindings = make([]contractsconfig.Binding, len(in.Bindings))
		for i, b := range in.Bindings {
			cb := b
			cb.Models = cloneStringSlice(b.Models)
			cb.Query = cloneStringMap(b.Query)
			cb.Tags = cloneStringSlice(b.Tags)
			out.Bindings[i] = cb
		}
	}
	if in.PassthroughBindings != nil {
		out.PassthroughBindings = make([]contractsconfig.PassthroughBinding, len(in.PassthroughBindings))
		for i, pb := range in.PassthroughBindings {
			cpb := pb
			cpb.Tags = cloneStringSlice(pb.Tags)
			out.PassthroughBindings[i] = cpb
		}
	}
	out.ConnectorBindings = cloneConnectorBindings(in.ConnectorBindings)
	return out
}
