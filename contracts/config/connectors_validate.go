package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
)

// envWebhookAllowPrivate is the test-only escape hatch shared with
// the runtime SSRF guard in internal/connector/webhook. When set
// ("1"/"true"), validateWebhook accepts loopback / RFC1918 /
// link-local URLs so the e2e harness's httptest.Server (bound to
// 127.0.0.1) can be wired as a webhook receiver. Production never
// sets it.
const envWebhookAllowPrivate = "SLUICE_WEBHOOK_ALLOW_PRIVATE" //nolint:gosec // env var name, not a credential

// ErrConnectorValidation is the sentinel returned (via fmt.Errorf wrap) by
// Connector.Validate / ConnectorBinding.Validate. Callers can use
// errors.Is to bucket connector-config errors away from other config
// parse failures.
var ErrConnectorValidation = errors.New("connector validation")

// Validate enforces per-type required-field and shape rules. It does
// NOT check uniqueness (the loader does that across the whole slice).
//
// The validator is permissive on optional / overridable fields — most
// per-type defaults live in internal/spool. The goal here is to catch
// shape errors at config-load time so the operator sees them on boot
// rather than at first request.
func (c *Connector) Validate() error {
	if c == nil {
		return fmt.Errorf("%w: nil Connector", ErrConnectorValidation)
	}
	if c.Name == "" {
		return fmt.Errorf("%w: name is required", ErrConnectorValidation)
	}
	switch c.Type {
	case ConnectorTypeS3:
		return c.validateS3()
	case ConnectorTypeAzureBlob:
		return c.validateAzureBlob()
	case ConnectorTypeWebhook:
		return c.validateWebhook()
	case ConnectorTypeControlPlane:
		return c.validateControlPlane()
	case "":
		return fmt.Errorf("%w: %q: type is required", ErrConnectorValidation, c.Name)
	default:
		return fmt.Errorf("%w: %q: unknown type %q (want s3 | azure_blob | webhook | controlplane)",
			ErrConnectorValidation, c.Name, c.Type)
	}
}

// validateControlPlane enforces the control-plane connector's shape. It reuses
// the webhook transport fields (url, secret_ref, timeout_ms) but, unlike
// webhook, it does NOT run the SSRF guard: it targets the operator's own
// control-plane service, which is exactly the internal/private address an SSRF
// guard would reject.
func (c *Connector) validateControlPlane() error {
	if c.URL == "" {
		return c.errf("url is required (the control plane's /api/v1/ingest/segment endpoint)")
	}
	parsed, err := url.Parse(c.URL)
	if err != nil {
		return c.errf("url is not a valid URL: %v", err)
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return c.errf("url scheme must be http or https, got %q", parsed.Scheme)
	}
	if c.SecretRef == "" {
		return c.errf("secret_ref is required (the control-plane bootstrap token, e.g. env:SLUICE_CP_TOKEN)")
	}
	if err := validateSecretRef(c.SecretRef); err != nil {
		return c.errf("secret_ref: %v", err)
	}
	if c.TimeoutMS <= 0 || c.TimeoutMS > 60_000 {
		return c.errf("timeout_ms must be in (0, 60000], got %d", c.TimeoutMS)
	}
	if c.Bucket != "" || c.Region != "" || c.EndpointURL != "" || c.UsePathStyle ||
		c.Account != "" || c.Container != "" {
		return c.errf("cloud-storage fields not allowed on controlplane")
	}
	if c.Auth != nil {
		return c.errf("auth block does not apply to controlplane (use secret_ref for the token)")
	}
	return nil
}

func (c *Connector) validateS3() error {
	if c.Bucket == "" {
		return c.errf("bucket is required")
	}
	if c.Region == "" {
		return c.errf("region is required")
	}
	if c.Account != "" || c.Container != "" {
		return c.errf("account/container are azure_blob fields, not s3")
	}
	if c.URL != "" || c.SecretRef != "" || c.TimeoutMS != 0 {
		return c.errf("url/secret_ref/timeout_ms are webhook fields, not s3")
	}
	if c.EndpointURL != "" {
		if _, err := url.Parse(c.EndpointURL); err != nil {
			return c.errf("endpoint_url is not a valid URL: %v", err)
		}
	}
	return c.validateS3Auth()
}

func (c *Connector) validateAzureBlob() error {
	if c.Account == "" {
		return c.errf("account is required")
	}
	if c.Container == "" {
		return c.errf("container is required")
	}
	if c.Bucket != "" || c.Region != "" || c.EndpointURL != "" || c.UsePathStyle {
		return c.errf("bucket/region/endpoint_url/use_path_style are s3 fields, not azure_blob")
	}
	if c.URL != "" || c.SecretRef != "" || c.TimeoutMS != 0 {
		return c.errf("url/secret_ref/timeout_ms are webhook fields, not azure_blob")
	}
	return c.validateAzureAuth()
}

func (c *Connector) validateWebhook() error {
	if c.URL == "" {
		return c.errf("url is required")
	}
	parsed, err := url.Parse(c.URL)
	if err != nil {
		return c.errf("url is not a valid URL: %v", err)
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return c.errf("url scheme must be http or https, got %q", parsed.Scheme)
	}
	if !webhookAllowPrivateNetworks() {
		if err := rejectLocalOrPrivateHost(parsed.Hostname()); err != nil {
			return c.errf("%v", err)
		}
	}
	if c.SecretRef == "" {
		return c.errf("secret_ref is required (HMAC signing key)")
	}
	if err := validateSecretRef(c.SecretRef); err != nil {
		return c.errf("secret_ref: %v", err)
	}
	if c.TimeoutMS <= 0 || c.TimeoutMS > 60_000 {
		return c.errf("timeout_ms must be in (0, 60000], got %d", c.TimeoutMS)
	}
	if c.Bucket != "" || c.Region != "" || c.EndpointURL != "" || c.UsePathStyle ||
		c.Account != "" || c.Container != "" {
		return c.errf("cloud-storage fields not allowed on webhook")
	}
	if c.Auth != nil {
		return c.errf("auth block does not apply to webhook (use secret_ref)")
	}
	return nil
}

// webhookAllowPrivateNetworks reports whether the test-only env
// var is set to flip both the config-load and runtime SSRF checks
// off. Centralised here so validateWebhook and the runtime guard
// agree on the spelling.
func webhookAllowPrivateNetworks() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(envWebhookAllowPrivate)))
	return v == "1" || v == "true"
}

// rejectLocalOrPrivateHost catches the common SSRF footguns at
// config-load: literal localhost / 127.x / RFC1918 / link-local / 0/8 /
// multicast hosts that no production webhook receiver should use. This
// is the static check the runtime SSRF guard's docstring claims; it
// cannot defend against a DNS name that resolves to a private IP —
// the runtime dial-time check covers that.
func rejectLocalOrPrivateHost(host string) error {
	if host == "" {
		return errors.New("url has no host")
	}
	if strings.EqualFold(host, "localhost") {
		return errors.New("url host must not be localhost")
	}
	// Trim IPv6 brackets that url.Hostname() already strips, but defend
	// in depth if upstream changes.
	host = strings.TrimPrefix(strings.TrimSuffix(host, "]"), "[")
	if ip := net.ParseIP(host); ip != nil {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
			ip.IsUnspecified() || ip.IsMulticast() || ip.IsLinkLocalMulticast() {
			return fmt.Errorf("url host %s is a denied address (loopback/RFC1918/link-local/unspecified/multicast)", host)
		}
	}
	return nil
}

func (c *Connector) validateS3Auth() error {
	if c.Auth == nil {
		// workload_identity is the default; explicitly absent is fine.
		return nil
	}
	switch c.Auth.Mode {
	case AuthModeWorkloadIdentity, "":
		// SDK default credential chain — nothing to fill in.
		if hasAnyRef(c.Auth.AccessKeyIDRef, c.Auth.SecretAccessKeyRef,
			c.Auth.RoleARN, c.Auth.ExternalIDRef,
			c.Auth.SASTokenRef, c.Auth.AccountKeyRef) {
			return c.errf("auth.mode=workload_identity must leave credential refs unset")
		}
		return nil
	case AuthModeStatic:
		if c.Auth.AccessKeyIDRef == "" || c.Auth.SecretAccessKeyRef == "" {
			return c.errf("auth.mode=static requires access_key_id_ref and secret_access_key_ref")
		}
		if err := validateSecretRef(c.Auth.AccessKeyIDRef); err != nil {
			return c.errf("auth.access_key_id_ref: %v", err)
		}
		if err := validateSecretRef(c.Auth.SecretAccessKeyRef); err != nil {
			return c.errf("auth.secret_access_key_ref: %v", err)
		}
		if c.Auth.SASTokenRef != "" || c.Auth.AccountKeyRef != "" {
			return c.errf("auth.sas_token_ref/account_key_ref are azure_blob fields")
		}
		return nil
	case AuthModeAssumeRole:
		if c.Auth.RoleARN == "" {
			return c.errf("auth.mode=assume_role requires role_arn")
		}
		if c.Auth.ExternalIDRef != "" {
			if err := validateSecretRef(c.Auth.ExternalIDRef); err != nil {
				return c.errf("auth.external_id_ref: %v", err)
			}
		}
		return nil
	default:
		return c.errf("auth.mode %q invalid for s3 (want workload_identity | static | assume_role)", c.Auth.Mode)
	}
}

func (c *Connector) validateAzureAuth() error {
	if c.Auth == nil {
		return nil
	}
	switch c.Auth.Mode {
	case AuthModeWorkloadIdentity, "":
		if hasAnyRef(c.Auth.SASTokenRef, c.Auth.AccountKeyRef,
			c.Auth.AccessKeyIDRef, c.Auth.SecretAccessKeyRef,
			c.Auth.RoleARN, c.Auth.ExternalIDRef) {
			return c.errf("auth.mode=workload_identity must leave credential refs unset")
		}
		return nil
	case AuthModeSASToken:
		if c.Auth.SASTokenRef == "" {
			return c.errf("auth.mode=sas_token requires sas_token_ref")
		}
		if err := validateSecretRef(c.Auth.SASTokenRef); err != nil {
			return c.errf("auth.sas_token_ref: %v", err)
		}
		return nil
	case AuthModeAccountKey:
		if c.Auth.AccountKeyRef == "" {
			return c.errf("auth.mode=account_key requires account_key_ref")
		}
		if err := validateSecretRef(c.Auth.AccountKeyRef); err != nil {
			return c.errf("auth.account_key_ref: %v", err)
		}
		return nil
	default:
		return c.errf("auth.mode %q invalid for azure_blob (want workload_identity | sas_token | account_key)", c.Auth.Mode)
	}
}

func (c *Connector) errf(format string, args ...any) error {
	return fmt.Errorf("%w: connector %q (%s): %s",
		ErrConnectorValidation, c.Name, c.Type, fmt.Sprintf(format, args...))
}

// validateSecretRef enforces the env:NAME / file:/path indirection
// mini-language. Anything else is rejected at config-load to keep
// credential material out of YAML.
func validateSecretRef(ref string) error {
	switch {
	case strings.HasPrefix(ref, "env:"):
		name := strings.TrimPrefix(ref, "env:")
		if name == "" {
			return errors.New("env: prefix requires a non-empty variable name")
		}
		return nil
	case strings.HasPrefix(ref, "file:"):
		path := strings.TrimPrefix(ref, "file:")
		if path == "" {
			return errors.New("file: prefix requires a non-empty path")
		}
		return nil
	default:
		return errors.New(`must start with "env:" or "file:"`)
	}
}

func hasAnyRef(refs ...string) bool {
	for _, r := range refs {
		if r != "" {
			return true
		}
	}
	return false
}

// Validate enforces shape on a ConnectorBinding. Cross-reference
// validation (that Connector references a defined connector) happens
// in the loader, not here — bindings don't have the connector
// catalog.
func (b *ConnectorBinding) Validate() error {
	if b == nil {
		return fmt.Errorf("%w: nil ConnectorBinding", ErrConnectorValidation)
	}
	if b.Connector == "" {
		return fmt.Errorf("%w: binding.connector is required", ErrConnectorValidation)
	}
	if b.Sampling < 0 || b.Sampling > 1 {
		return fmt.Errorf("%w: binding %q: sampling must be in [0, 1], got %v",
			ErrConnectorValidation, b.Connector, b.Sampling)
	}
	switch b.SamplingKey {
	case "", SamplingKeyCorrelationID, SamplingKeyRandom:
	default:
		return fmt.Errorf("%w: binding %q: sampling_key %q invalid (want correlation_id | random)",
			ErrConnectorValidation, b.Connector, b.SamplingKey)
	}
	switch b.OversizeBehaviour {
	case "", OversizeMetadataOnly, OversizeDropRecord:
	default:
		return fmt.Errorf("%w: binding %q: oversize_behaviour %q invalid (want metadata_only | drop_record)",
			ErrConnectorValidation, b.Connector, b.OversizeBehaviour)
	}
	if b.MaxBodyBytes != nil && *b.MaxBodyBytes < 0 {
		return fmt.Errorf("%w: binding %q: max_body_bytes cannot be negative",
			ErrConnectorValidation, b.Connector)
	}
	if b.Filter != nil {
		if err := b.Filter.Validate(); err != nil {
			return fmt.Errorf("%w: binding %q: %v", ErrConnectorValidation, b.Connector, err)
		}
	}
	return nil
}

// Validate enforces shape on a ConnectorFilter. Range checks only;
// the loader doesn't know provider/endpoint names here so name
// existence is checked one level up.
func (f *ConnectorFilter) Validate() error {
	if f == nil {
		return nil
	}
	statusMin := f.StatusMin
	statusMax := f.StatusMax
	if statusMin == 0 {
		statusMin = 200
	}
	if statusMax == 0 {
		statusMax = 599
	}
	if statusMin < 100 || statusMin > 599 {
		return fmt.Errorf("status_min %d out of range [100, 599]", statusMin)
	}
	if statusMax < 100 || statusMax > 599 {
		return fmt.Errorf("status_max %d out of range [100, 599]", statusMax)
	}
	if statusMin > statusMax {
		return fmt.Errorf("status_min %d > status_max %d", statusMin, statusMax)
	}
	for _, m := range f.Models {
		if strings.Count(m, "*") > 1 {
			return fmt.Errorf("model pattern %q has multiple wildcards (only trailing * is supported)", m)
		}
		if strings.Contains(m, "*") && !strings.HasSuffix(m, "*") {
			return fmt.Errorf("model pattern %q has a non-trailing wildcard", m)
		}
	}
	return nil
}
