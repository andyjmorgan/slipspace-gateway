package config

// ConnectorsConfig is the merged `connectors:` block — a flat slice of
// connector definitions in authoring order. Lookups use the index built
// at load time (ResolvedConfig.ConnectorIndex); this slice exists for
// enumeration (admin listing, audit).
type ConnectorsConfig []Connector

// Recognised connector types. Validation rejects anything else.
const (
	ConnectorTypeS3        = "s3"
	ConnectorTypeAzureBlob = "azure_blob"
	ConnectorTypeWebhook   = "webhook"
)

// DefaultMaxBodyBytes is the body cap applied to a binding whose
// MaxBodyBytes is nil (unset). Zero means no cap, for every connector type:
// an unset binding captures the full body. The inbound bodycapture buffer
// ([internal/middleware/bodycapture].MaxBodyBytes) already bounds what any
// connector can ever see, so there is no per-type default truncation — a
// previous 1 MiB webhook default silently dropped large request bodies
// (e.g. long-context model calls) from webhook deliveries, which is exactly
// what an operator wiring a "full payloads" webhook does not want. Operators
// who need a tighter per-binding cap set max_body_bytes explicitly.
//
// The connector type is retained in the signature as the single authority
// for any future per-type default — the runtime ([cmd/gateway/binding.go])
// reads it, never hardcodes the value.
func DefaultMaxBodyBytes(_ string) int {
	return 0
}

// Auth modes for cloud-bucket connectors (s3, azure_blob). The set of
// valid modes is type-specific; validation enforces.
const (
	AuthModeWorkloadIdentity = "workload_identity"
	AuthModeStatic           = "static"
	AuthModeAssumeRole       = "assume_role"
	AuthModeSASToken         = "sas_token"
	AuthModeAccountKey       = "account_key"
)

// Sampling key options on ConnectorBinding.
const (
	SamplingKeyCorrelationID = "correlation_id"
	SamplingKeyRandom        = "random"
)

// Oversize behaviour on ConnectorBinding.
const (
	OversizeMetadataOnly = "metadata_only"
	OversizeDropRecord   = "drop_record"
)

// DefaultUploadTimeoutSeconds is the per-attempt Connector.Upload deadline
// applied to a spool-backed connector whose upload_timeout_seconds is
// unset. Generous enough for a 64 MiB segment on a slow link, short
// enough that a destination holding the socket open surfaces as a
// retryable failure the circuit breaker can act on instead of parking
// the track's single uploader forever.
const DefaultUploadTimeoutSeconds = 60

// EffectiveUploadTimeoutSeconds returns upload_timeout_seconds with the
// default applied when unset.
func (c *Connector) EffectiveUploadTimeoutSeconds() int {
	if c == nil || c.UploadTimeoutSeconds <= 0 {
		return DefaultUploadTimeoutSeconds
	}
	return c.UploadTimeoutSeconds
}

// Connector is one reusable destination. Many configurations may bind
// the same connector with different sampling / filter overrides; the
// connector itself owns rotation policy + auth + transport details.
//
// All sub-blocks are optional in the struct sense — validation per
// connector Type enforces which are required.
type Connector struct {
	// Name is the operator-visible identifier ConnectorBinding.Connector
	// references. Required; unique across the connectors slice.
	Name string `yaml:"name" json:"name"`

	// Type is one of "s3", "azure_blob", "webhook". Required. The s3 and
	// azure_blob types are durable spool-backed archival destinations; the
	// webhook type is a real-time, non-spooled pusher (per-record HMAC POST,
	// drop-on-full) — it never touches the disk spool.
	Type string `yaml:"type" json:"type"`

	// --- s3 + azure_blob common ---

	// Auth is the credential resolution policy. Required for cloud
	// connectors; ignored for webhook.
	Auth *ConnectorAuth `yaml:"auth,omitempty" json:"auth,omitempty"`

	// Rotation controls when this connector's active segment seals.
	// Optional — zero values fall back to defaults documented in
	// internal/spool's RotationOpts.
	Rotation *ConnectorRotation `yaml:"rotation,omitempty" json:"rotation,omitempty"`

	// UploadTimeoutSeconds bounds one Connector.Upload attempt for a
	// spool-backed connector. Zero (unset) applies
	// DefaultUploadTimeoutSeconds; negative is a config error. Each
	// retry attempt gets its own deadline, so this caps a single
	// wedged PUT, not the whole retry schedule. Not applicable to
	// webhook (use timeout_ms there).
	UploadTimeoutSeconds int `yaml:"upload_timeout_seconds,omitempty" json:"upload_timeout_seconds,omitempty"`

	// --- s3 specifics ---

	// Bucket is the S3 bucket name. Required when Type == s3.
	Bucket string `yaml:"bucket,omitempty" json:"bucket,omitempty"`

	// Prefix is the key prefix beneath the bucket. Optional.
	Prefix string `yaml:"prefix,omitempty" json:"prefix,omitempty"`

	// Region is the AWS region. Required when Type == s3; the SDK
	// still wants a region for S3-compatible providers even when
	// EndpointURL is set.
	Region string `yaml:"region,omitempty" json:"region,omitempty"`

	// EndpointURL points at an S3-compatible provider (MinIO,
	// SeaweedFS, Garage, Ceph RGW). Empty = use real AWS S3.
	EndpointURL string `yaml:"endpoint_url,omitempty" json:"endpoint_url,omitempty"`

	// UsePathStyle selects path-style addressing
	// (bucket in the URL path) instead of virtual-hosted (bucket as
	// the host subdomain). MinIO defaults to path-style; AWS prefers
	// virtual-hosted. Defaults to false (virtual-hosted).
	UsePathStyle bool `yaml:"use_path_style,omitempty" json:"use_path_style,omitempty"`

	// --- azure_blob specifics ---

	// Account is the Azure Storage account name. Required when
	// Type == azure_blob.
	Account string `yaml:"account,omitempty" json:"account,omitempty"`

	// Container is the blob container name. Required when
	// Type == azure_blob.
	Container string `yaml:"container,omitempty" json:"container,omitempty"`

	// --- webhook specifics ---

	// URL is the receiver endpoint each record is POSTed to (e.g. the
	// Arbiter's /api/v1/ingest/record, or any customer endpoint).
	// Required when Type == webhook.
	URL string `yaml:"url,omitempty" json:"url,omitempty"`

	// SecretRef is the secret_ref indirection (env:NAME or file:/path)
	// pointing at the HMAC signing key. Required when Type == webhook.
	SecretRef string `yaml:"secret_ref,omitempty" json:"secret_ref,omitempty"`

	// GatewayID is sent as the X-Slipspace-Gateway-Id header so a receiver that
	// keys HMAC secrets by gateway (the Arbiter registry) can look
	// the secret up. Optional for a generic receiver that verifies the
	// signature alone; required when pushing to the Arbiter.
	GatewayID string `yaml:"gateway_id,omitempty" json:"gateway_id,omitempty"`

	// TimeoutMS is the per-call HTTP timeout in milliseconds. Required
	// when Type == webhook; 0 < TimeoutMS <= 60000.
	TimeoutMS int `yaml:"timeout_ms,omitempty" json:"timeout_ms,omitempty"`
}

// ConnectorAuth carries the per-mode credential refs for cloud
// connectors. The set of populated fields must match Mode; validation
// rejects mismatches.
type ConnectorAuth struct {
	// Mode is one of "workload_identity", "static", "assume_role"
	// (s3 only), "sas_token" / "account_key" (azure only).
	Mode string `yaml:"mode" json:"mode"`

	// --- static (s3) ---

	// AccessKeyIDRef is the secret_ref for the AWS access key ID.
	// Required when Mode == static.
	AccessKeyIDRef string `yaml:"access_key_id_ref,omitempty" json:"access_key_id_ref,omitempty"`

	// SecretAccessKeyRef is the secret_ref for the AWS secret. Required
	// when Mode == static.
	SecretAccessKeyRef string `yaml:"secret_access_key_ref,omitempty" json:"secret_access_key_ref,omitempty"`

	// --- assume_role (s3) ---

	// RoleARN is the IAM role to assume. Required when Mode == assume_role.
	RoleARN string `yaml:"role_arn,omitempty" json:"role_arn,omitempty"`

	// ExternalIDRef is the secret_ref for the assume-role external ID.
	// Optional but recommended for cross-account trust.
	ExternalIDRef string `yaml:"external_id_ref,omitempty" json:"external_id_ref,omitempty"`

	// --- azure_blob ---

	// SASTokenRef is the secret_ref for the SAS token. Required when
	// Mode == sas_token.
	SASTokenRef string `yaml:"sas_token_ref,omitempty" json:"sas_token_ref,omitempty"`

	// AccountKeyRef is the secret_ref for the storage account key.
	// Required when Mode == account_key.
	AccountKeyRef string `yaml:"account_key_ref,omitempty" json:"account_key_ref,omitempty"`
}

// ConnectorRotation overrides the spool's default rotation policy
// per-connector.
type ConnectorRotation struct {
	// MaxBytes is the uncompressed byte cap on the active segment. Zero
	// = use the spool default (64 MiB).
	MaxBytes int64 `yaml:"max_bytes,omitempty" json:"max_bytes,omitempty"`

	// MaxAgeSeconds caps the time the active segment stays open. Zero
	// = use the spool default (60s).
	MaxAgeSeconds int `yaml:"max_age_seconds,omitempty" json:"max_age_seconds,omitempty"`
}

// ConnectorBinding attaches one connector to a configuration with
// per-binding overrides (sampling, filter, body cap). Different
// configurations can bind the same connector with different overrides;
// the spool routes records into one logical track per connector.
type ConnectorBinding struct {
	// Connector references a Connector.Name from the top-level
	// `connectors:` slice. Required.
	Connector string `yaml:"connector" json:"connector"`

	// Sampling is the fraction [0, 1] of records routed to this
	// binding. A pointer so the unset case is distinguishable from an
	// explicit zero:
	//
	//   - nil (unset) → 1.0, ship everything.
	//   - 0 → ship nothing. The binding stays declared (and its
	//     connector keeps its spool track / pusher) but no record
	//     reaches it — the way to mute a destination without deleting
	//     the binding.
	//   - (0, 1) → ship that deterministic (or random, per SamplingKey)
	//     fraction.
	//   - 1 → ship everything.
	//
	// Values outside [0, 1] are a config error. Use SamplingRate to
	// read the effective value.
	Sampling *float64 `yaml:"sampling,omitempty" json:"sampling,omitempty"`

	// SamplingKey is "correlation_id" (default — deterministic, all
	// records for one request in or out together) or "random".
	SamplingKey string `yaml:"sampling_key,omitempty" json:"sampling_key,omitempty"`

	// MaxBodyBytes caps the larger of the per-record request/response
	// body that ships to this destination. A pointer so the unset case
	// is distinguishable from an explicit zero:
	//
	//   - nil (unset) → apply the connector-type default, which is no cap
	//     for every type (DefaultMaxBodyBytes). The bodycapture middleware
	//     already bounds inbound bodies, so the per-type webhook default
	//     was pure footgun and was dropped.
	//   - 0 → no cap (same as unset; kept distinguishable for clarity).
	//   - >0 → cap at that many bytes.
	//
	// Operators who need a tighter per-binding cap (e.g. a webhook receiver
	// that processes deliveries synchronously) set max_body_bytes
	// explicitly. Bodies are bounded upstream regardless by the bodycapture
	// middleware's 10 MiB inbound read limit. Negative is a config error.
	MaxBodyBytes *int `yaml:"max_body_bytes,omitempty" json:"max_body_bytes,omitempty"`

	// OversizeBehaviour is "metadata_only" (default) or "drop_record".
	OversizeBehaviour string `yaml:"oversize_behaviour,omitempty" json:"oversize_behaviour,omitempty"`

	// Filter narrows which records this binding receives. Empty filter
	// (all-empty lists) includes everything.
	Filter *ConnectorFilter `yaml:"filter,omitempty" json:"filter,omitempty"`
}

// SamplingRate returns the effective sampling fraction: 1.0 when
// Sampling is unset, otherwise the configured value (including an
// explicit 0, which means ship nothing).
func (b *ConnectorBinding) SamplingRate() float64 {
	if b == nil || b.Sampling == nil {
		return 1.0
	}
	return *b.Sampling
}

// ConnectorFilter narrows the records a ConnectorBinding receives.
// Within a single field (e.g. Providers), values combine with OR. Across
// fields, the predicates combine with AND. Empty lists = no constraint.
type ConnectorFilter struct {
	// Providers limits to the named providers (e.g. ["anthropic"]).
	Providers []string `yaml:"providers,omitempty" json:"providers,omitempty"`

	// Protocols limits to the named protocols or passthrough families
	// (e.g. ["chat", "messages", "messages_batches"]).
	Protocols []string `yaml:"protocols,omitempty" json:"protocols,omitempty"`

	// Models is a list of model-name patterns. A trailing "*" is the
	// only wildcard form supported (e.g. "claude-*"). Other matches
	// are exact-equal.
	Models []string `yaml:"models,omitempty" json:"models,omitempty"`

	// StatusMin / StatusMax bound the HTTP status code range. Zero
	// values default to 200 / 599 — i.e. include everything.
	StatusMin int `yaml:"status_min,omitempty" json:"status_min,omitempty"`

	StatusMax int `yaml:"status_max,omitempty" json:"status_max,omitempty"`

	// TagsAny matches if the record carries any of these tags.
	TagsAny []string `yaml:"tags_any,omitempty" json:"tags_any,omitempty"`

	// TagsAll matches only if the record carries every one of these
	// tags.
	TagsAll []string `yaml:"tags_all,omitempty" json:"tags_all,omitempty"`
}
