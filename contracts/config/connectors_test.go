package config

import (
	"errors"
	"strings"
	"testing"
)

func TestConnector_Validate_S3HappyPath(t *testing.T) {
	c := &Connector{
		Name:   "refinement-s3",
		Type:   ConnectorTypeS3,
		Bucket: "sluice-refinement",
		Region: "eu-west-2",
	}
	if err := c.Validate(); err != nil {
		t.Errorf("happy s3: %v", err)
	}
}

func TestConnector_Validate_S3OnPremCompatible(t *testing.T) {
	c := &Connector{
		Name:         "minio",
		Type:         ConnectorTypeS3,
		Bucket:       "sluice",
		Region:       "us-east-1",
		EndpointURL:  "https://minio.internal:9000",
		UsePathStyle: true,
		Auth: &ConnectorAuth{ //nolint:gosec // G101: fixture credentials in test
			Mode:               AuthModeStatic,
			AccessKeyIDRef:     "env:MINIO_ACCESS_KEY",
			SecretAccessKeyRef: "env:MINIO_SECRET_KEY",
		},
	}
	if err := c.Validate(); err != nil {
		t.Errorf("on-prem s3: %v", err)
	}
}

func TestConnector_Validate_S3AssumeRole(t *testing.T) {
	c := &Connector{
		Name:   "cross-account",
		Type:   ConnectorTypeS3,
		Bucket: "b",
		Region: "us-west-2",
		Auth: &ConnectorAuth{ //nolint:gosec // G101: fixture credentials in test
			Mode:          AuthModeAssumeRole,
			RoleARN:       "arn:aws:iam::123:role/x",
			ExternalIDRef: "env:S3_EXTERNAL_ID",
		},
	}
	if err := c.Validate(); err != nil {
		t.Errorf("assume_role s3: %v", err)
	}
}

func TestConnector_Validate_AzureBlobHappy(t *testing.T) {
	c := &Connector{
		Name:      "azure",
		Type:      ConnectorTypeAzureBlob,
		Account:   "myaccount",
		Container: "sluice",
		Auth: &ConnectorAuth{ //nolint:gosec // G101: fixture credentials in test
			Mode:        AuthModeSASToken,
			SASTokenRef: "env:AZURE_SAS",
		},
	}
	if err := c.Validate(); err != nil {
		t.Errorf("azure happy: %v", err)
	}
}

func TestConnector_Validate_WebhookHappy(t *testing.T) {
	c := &Connector{ //nolint:gosec // G101: SecretRef is an env: indirection, not a literal credential
		Name:      "hook",
		Type:      ConnectorTypeWebhook,
		URL:       "https://hooks.example/sluice",
		SecretRef: "env:HOOK_SECRET",
		TimeoutMS: 3000,
	}
	if err := c.Validate(); err != nil {
		t.Errorf("webhook happy: %v", err)
	}
}

func TestConnector_Validate_Rejections(t *testing.T) {
	// The webhook SSRF-rejection cases below rely on the
	// allow-private escape hatch being off. If a developer set it
	// in their shell for the e2e harness it would leak here, so
	// pin it for the scope of this test.
	t.Setenv(envWebhookAllowPrivate, "")
	cases := []struct {
		name   string
		c      Connector
		substr string
	}{
		{"nil-ish", Connector{}, "name is required"},
		{"unknown type", Connector{Name: "x", Type: "blob"}, "unknown type"},
		{"missing type", Connector{Name: "x"}, "type is required"},
		{"s3 missing bucket", Connector{Name: "x", Type: "s3", Region: "r"}, "bucket is required"},
		{"s3 missing region", Connector{Name: "x", Type: "s3", Bucket: "b"}, "region is required"},
		{"s3 has azure fields", Connector{Name: "x", Type: "s3", Bucket: "b", Region: "r", Account: "a"}, "azure_blob fields"},
		{"s3 has webhook fields", Connector{Name: "x", Type: "s3", Bucket: "b", Region: "r", URL: "https://x"}, "webhook fields"},
		{"s3 bad endpoint_url", Connector{Name: "x", Type: "s3", Bucket: "b", Region: "r", EndpointURL: "::%"}, "endpoint_url"},
		{"s3 static missing key", Connector{Name: "x", Type: "s3", Bucket: "b", Region: "r",
			Auth: &ConnectorAuth{Mode: AuthModeStatic, SecretAccessKeyRef: "env:s"}}, "access_key_id_ref"},
		{"s3 bad secret ref", Connector{Name: "x", Type: "s3", Bucket: "b", Region: "r",
			Auth: &ConnectorAuth{Mode: AuthModeStatic, AccessKeyIDRef: "plain", SecretAccessKeyRef: "env:s"}}, `must start with "env:" or "file:"`},
		{"s3 wi with refs", Connector{Name: "x", Type: "s3", Bucket: "b", Region: "r",
			Auth: &ConnectorAuth{Mode: AuthModeWorkloadIdentity, AccessKeyIDRef: "env:foo"}}, "workload_identity must leave"},
		{"s3 assume_role missing arn", Connector{Name: "x", Type: "s3", Bucket: "b", Region: "r",
			Auth: &ConnectorAuth{Mode: AuthModeAssumeRole}}, "role_arn"},
		{"s3 azure auth mode", Connector{Name: "x", Type: "s3", Bucket: "b", Region: "r",
			Auth: &ConnectorAuth{Mode: AuthModeSASToken, SASTokenRef: "env:s"}}, "invalid for s3"},
		{"s3 static with sas", Connector{Name: "x", Type: "s3", Bucket: "b", Region: "r",
			Auth: &ConnectorAuth{Mode: AuthModeStatic, AccessKeyIDRef: "env:a", SecretAccessKeyRef: "env:s", SASTokenRef: "env:wrong"}}, "azure_blob fields"},

		{"azure missing account", Connector{Name: "x", Type: ConnectorTypeAzureBlob, Container: "c"}, "account is required"},
		{"azure missing container", Connector{Name: "x", Type: ConnectorTypeAzureBlob, Account: "a"}, "container is required"},
		{"azure has s3 field", Connector{Name: "x", Type: ConnectorTypeAzureBlob, Account: "a", Container: "c", Bucket: "b"}, "s3 fields"},
		{"azure has webhook url", Connector{Name: "x", Type: ConnectorTypeAzureBlob, Account: "a", Container: "c", URL: "https://x"}, "webhook fields"},
		{"azure sas missing ref", Connector{Name: "x", Type: ConnectorTypeAzureBlob, Account: "a", Container: "c",
			Auth: &ConnectorAuth{Mode: AuthModeSASToken}}, "sas_token_ref"},
		{"azure account_key missing ref", Connector{Name: "x", Type: ConnectorTypeAzureBlob, Account: "a", Container: "c",
			Auth: &ConnectorAuth{Mode: AuthModeAccountKey}}, "account_key_ref"},
		{"azure s3 auth mode", Connector{Name: "x", Type: ConnectorTypeAzureBlob, Account: "a", Container: "c",
			Auth: &ConnectorAuth{Mode: AuthModeStatic, AccessKeyIDRef: "env:a", SecretAccessKeyRef: "env:b"}}, "invalid for azure_blob"},
		{"azure wi with refs", Connector{Name: "x", Type: ConnectorTypeAzureBlob, Account: "a", Container: "c",
			Auth: &ConnectorAuth{Mode: AuthModeWorkloadIdentity, SASTokenRef: "env:s"}}, "workload_identity must leave"},

		{"webhook missing url", Connector{Name: "x", Type: ConnectorTypeWebhook, SecretRef: "env:s", TimeoutMS: 1000}, "url is required"},
		{"webhook bad url", Connector{Name: "x", Type: ConnectorTypeWebhook, URL: "::%", SecretRef: "env:s", TimeoutMS: 1000}, "valid URL"},
		{"webhook unsupported scheme", Connector{Name: "x", Type: ConnectorTypeWebhook, URL: "ftp://x", SecretRef: "env:s", TimeoutMS: 1000}, "url scheme"},
		{"webhook missing secret", Connector{Name: "x", Type: ConnectorTypeWebhook, URL: "https://x", TimeoutMS: 1000}, "secret_ref is required"},
		{"webhook bad secret ref", Connector{Name: "x", Type: ConnectorTypeWebhook, URL: "https://x", SecretRef: "plain", TimeoutMS: 1000}, `must start with "env:"`},
		{"webhook zero timeout", Connector{Name: "x", Type: ConnectorTypeWebhook, URL: "https://x", SecretRef: "env:s"}, "timeout_ms"},
		{"webhook huge timeout", Connector{Name: "x", Type: ConnectorTypeWebhook, URL: "https://x", SecretRef: "env:s", TimeoutMS: 70_000}, "timeout_ms"},
		{"webhook has bucket", Connector{Name: "x", Type: ConnectorTypeWebhook, URL: "https://x", SecretRef: "env:s", TimeoutMS: 1000, Bucket: "b"}, "cloud-storage fields"},
		{"webhook with auth block", Connector{Name: "x", Type: ConnectorTypeWebhook, URL: "https://x", SecretRef: "env:s", TimeoutMS: 1000,
			Auth: &ConnectorAuth{Mode: AuthModeWorkloadIdentity}}, "auth block"},

		// SSRF rejection at config-load. The runtime guard is the
		// second line of defence; these stop typo'd / explicit
		// localhost / RFC1918 / link-local URLs before the binary
		// boots.
		{"webhook localhost url", Connector{Name: "x", Type: ConnectorTypeWebhook, URL: "http://localhost:9000/hook", SecretRef: "env:s", TimeoutMS: 1000}, "localhost"},
		{"webhook 127.0.0.1 url", Connector{Name: "x", Type: ConnectorTypeWebhook, URL: "http://127.0.0.1:9000/hook", SecretRef: "env:s", TimeoutMS: 1000}, "denied address"},
		{"webhook RFC1918 10.x url", Connector{Name: "x", Type: ConnectorTypeWebhook, URL: "https://10.0.0.1/hook", SecretRef: "env:s", TimeoutMS: 1000}, "denied address"},
		{"webhook RFC1918 192.168.x url", Connector{Name: "x", Type: ConnectorTypeWebhook, URL: "https://192.168.1.1/hook", SecretRef: "env:s", TimeoutMS: 1000}, "denied address"},
		{"webhook link-local 169.254 url", Connector{Name: "x", Type: ConnectorTypeWebhook, URL: "https://169.254.169.254/hook", SecretRef: "env:s", TimeoutMS: 1000}, "denied address"},
		{"webhook 0.0.0.0 url", Connector{Name: "x", Type: ConnectorTypeWebhook, URL: "https://0.0.0.0/hook", SecretRef: "env:s", TimeoutMS: 1000}, "denied address"},
		{"webhook IPv6 loopback url", Connector{Name: "x", Type: ConnectorTypeWebhook, URL: "https://[::1]/hook", SecretRef: "env:s", TimeoutMS: 1000}, "denied address"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.c.Validate()
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.substr)
			}
			if !errors.Is(err, ErrConnectorValidation) {
				t.Errorf("error not wrapped with ErrConnectorValidation: %v", err)
			}
			if !strings.Contains(err.Error(), tc.substr) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.substr)
			}
		})
	}
}

func TestConnector_NilReceiverIsRejected(t *testing.T) {
	var c *Connector
	if err := c.Validate(); err == nil {
		t.Error("nil receiver should error")
	}
}

func TestConnectorAuth_FileSecretRef(t *testing.T) {
	c := &Connector{
		Name:   "x",
		Type:   ConnectorTypeS3,
		Bucket: "b",
		Region: "r",
		Auth: &ConnectorAuth{ //nolint:gosec // G101: fixture credentials in test
			Mode:               AuthModeStatic,
			AccessKeyIDRef:     "file:/var/secrets/aws-id",
			SecretAccessKeyRef: "file:/var/secrets/aws-key",
		},
	}
	if err := c.Validate(); err != nil {
		t.Errorf("file: secret_ref should validate: %v", err)
	}
}

func TestValidateSecretRef(t *testing.T) {
	cases := []struct {
		in   string
		want string // empty = OK
	}{
		{"env:NAME", ""},
		{"file:/etc/x", ""},
		{"env:", "env: prefix"},
		{"file:", "file: prefix"},
		{"plain", `must start with "env:"`},
		{"", `must start with "env:"`},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			err := validateSecretRef(tc.in)
			if tc.want == "" {
				if err != nil {
					t.Errorf("expected nil, got %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("expected error containing %q, got %v", tc.want, err)
			}
		})
	}
}

func TestConnectorBinding_Validate_Happy(t *testing.T) {
	b := &ConnectorBinding{
		Connector:         "x",
		Sampling:          0.5,
		SamplingKey:       SamplingKeyCorrelationID,
		MaxBodyBytes:      ptrInt(1024),
		OversizeBehaviour: OversizeMetadataOnly,
		Filter: &ConnectorFilter{
			Providers: []string{"openai"},
			Models:    []string{"gpt-4o-*"},
			StatusMin: 200,
			StatusMax: 599,
		},
	}
	if err := b.Validate(); err != nil {
		t.Errorf("happy binding: %v", err)
	}
}

func TestConnectorBinding_Validate_Rejections(t *testing.T) {
	cases := []struct {
		name   string
		b      ConnectorBinding
		substr string
	}{
		{"missing connector", ConnectorBinding{}, "connector is required"},
		{"sampling below 0", ConnectorBinding{Connector: "x", Sampling: -0.1}, "sampling"},
		{"sampling above 1", ConnectorBinding{Connector: "x", Sampling: 1.5}, "sampling"},
		{"bad sampling key", ConnectorBinding{Connector: "x", SamplingKey: "uuid"}, "sampling_key"},
		{"bad oversize", ConnectorBinding{Connector: "x", OversizeBehaviour: "truncate"}, "oversize_behaviour"},
		{"negative max body", ConnectorBinding{Connector: "x", MaxBodyBytes: ptrInt(-1)}, "max_body_bytes"},
		{"filter bad status min", ConnectorBinding{Connector: "x", Filter: &ConnectorFilter{StatusMin: 50, StatusMax: 200}}, "status_min"},
		{"filter status reversed", ConnectorBinding{Connector: "x", Filter: &ConnectorFilter{StatusMin: 500, StatusMax: 300}}, "status_min"},
		{"filter wildcard mid-string", ConnectorBinding{Connector: "x", Filter: &ConnectorFilter{Models: []string{"gpt-*-mini"}}}, "wildcard"},
		{"filter multiple wildcards", ConnectorBinding{Connector: "x", Filter: &ConnectorFilter{Models: []string{"a*b*"}}}, "wildcards"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.b.Validate()
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.substr)
			}
			if !strings.Contains(err.Error(), tc.substr) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.substr)
			}
		})
	}
}

func TestConnectorFilter_NilValidatesOK(t *testing.T) {
	var f *ConnectorFilter
	if err := f.Validate(); err != nil {
		t.Errorf("nil filter should be OK, got %v", err)
	}
}

func TestConnectorBinding_NilIsRejected(t *testing.T) {
	var b *ConnectorBinding
	if err := b.Validate(); err == nil {
		t.Error("nil binding should error")
	}
}

// --- additional coverage for auth branches ---

func TestConnectorAuth_AzureAccountKeyHappy(t *testing.T) {
	c := &Connector{
		Name:      "x",
		Type:      ConnectorTypeAzureBlob,
		Account:   "a",
		Container: "c",
		Auth: &ConnectorAuth{ //nolint:gosec // G101: fixture credentials in test
			Mode:          AuthModeAccountKey,
			AccountKeyRef: "env:AZURE_KEY",
		},
	}
	if err := c.Validate(); err != nil {
		t.Errorf("happy account_key: %v", err)
	}
}

func TestConnectorAuth_AzureBadAccountKeyRef(t *testing.T) {
	c := &Connector{
		Name:      "x",
		Type:      ConnectorTypeAzureBlob,
		Account:   "a",
		Container: "c",
		Auth: &ConnectorAuth{ //nolint:gosec // G101: fixture credentials in test
			Mode:          AuthModeAccountKey,
			AccountKeyRef: "plain-key",
		},
	}
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "account_key_ref") {
		t.Errorf("expected account_key_ref error, got %v", err)
	}
}

func TestConnectorAuth_AzureBadSASRef(t *testing.T) {
	c := &Connector{
		Name:      "x",
		Type:      ConnectorTypeAzureBlob,
		Account:   "a",
		Container: "c",
		Auth: &ConnectorAuth{ //nolint:gosec // G101: fixture credentials in test
			Mode:        AuthModeSASToken,
			SASTokenRef: "plain",
		},
	}
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "sas_token_ref") {
		t.Errorf("expected sas_token_ref error, got %v", err)
	}
}

func TestConnectorAuth_S3BadSecretRef(t *testing.T) {
	c := &Connector{
		Name:   "x",
		Type:   ConnectorTypeS3,
		Bucket: "b",
		Region: "r",
		Auth: &ConnectorAuth{ //nolint:gosec // G101: fixture credentials in test
			Mode:               AuthModeStatic,
			AccessKeyIDRef:     "env:OK",
			SecretAccessKeyRef: "plain",
		},
	}
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "secret_access_key_ref") {
		t.Errorf("expected secret_access_key_ref error, got %v", err)
	}
}

func TestConnectorAuth_S3AssumeRoleBadExternalIDRef(t *testing.T) {
	c := &Connector{
		Name:   "x",
		Type:   ConnectorTypeS3,
		Bucket: "b",
		Region: "r",
		Auth: &ConnectorAuth{ //nolint:gosec // G101: fixture credentials in test
			Mode:          AuthModeAssumeRole,
			RoleARN:       "arn:aws:iam::1:role/r",
			ExternalIDRef: "plain",
		},
	}
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "external_id_ref") {
		t.Errorf("expected external_id_ref error, got %v", err)
	}
}

func TestConnectorAuth_AzureUnknownMode(t *testing.T) {
	c := &Connector{
		Name:      "x",
		Type:      ConnectorTypeAzureBlob,
		Account:   "a",
		Container: "c",
		Auth:      &ConnectorAuth{Mode: "carrier-pigeon"},
	}
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "invalid for azure_blob") {
		t.Errorf("expected invalid mode, got %v", err)
	}
}

func TestWebhookAllowPrivateNetworks_EnvFlip(t *testing.T) {
	// Negative case: empty env → reject.
	t.Setenv(envWebhookAllowPrivate, "")
	if webhookAllowPrivateNetworks() {
		t.Error("empty env should not enable allow-private")
	}
	c := Connector{
		Name: "x", Type: ConnectorTypeWebhook, URL: "http://127.0.0.1:9000/hook",
		SecretRef: "env:s", TimeoutMS: 1000,
	}
	if err := c.Validate(); err == nil {
		t.Error("loopback URL should reject by default")
	}

	// Positive case: env set → accept.
	t.Setenv(envWebhookAllowPrivate, "1")
	if !webhookAllowPrivateNetworks() {
		t.Error("env=1 should enable allow-private")
	}
	if err := c.Validate(); err != nil {
		t.Errorf("loopback URL should pass when env set, got %v", err)
	}
}

func TestRejectLocalOrPrivateHost_NonIPHostPasses(t *testing.T) {
	// Public DNS names should pass — only IP literals get parsed
	// and inspected; DNS names rely on the runtime guard.
	for _, h := range []string{
		"hooks.example.com",
		"webhook.acme.io",
		"example.test", // reserved TLD, not parsed as IP
	} {
		if err := rejectLocalOrPrivateHost(h); err != nil {
			t.Errorf("rejectLocalOrPrivateHost(%q) = %v, want nil (DNS names pass at config-time)", h, err)
		}
	}
}

func TestConnectorBinding_Validate_ExplicitZeroMaxBodyOK(t *testing.T) {
	b := &ConnectorBinding{Connector: "x", MaxBodyBytes: ptrInt(0)}
	if err := b.Validate(); err != nil {
		t.Errorf("explicit max_body_bytes: 0 is the unlimited override, should validate: %v", err)
	}
}

func TestDefaultMaxBodyBytes(t *testing.T) {
	// No connector type caps by default — unset means no cap everywhere
	// (the inbound bodycapture buffer is the real bound). Webhook used to
	// default to 1 MiB and silently truncate large bodies; it no longer does.
	for _, ct := range []string{ConnectorTypeWebhook, ConnectorTypeS3, ConnectorTypeAzureBlob, "", "unknown"} {
		if got := DefaultMaxBodyBytes(ct); got != 0 {
			t.Errorf("DefaultMaxBodyBytes(%q) = %d, want 0 (no cap)", ct, got)
		}
	}
}

func ptrInt(i int) *int { return &i }

func TestHasAnyRef(t *testing.T) {
	if hasAnyRef("", "", "") {
		t.Error("all-empty should be false")
	}
	if !hasAnyRef("", "env:NAME", "") {
		t.Error("one non-empty should be true")
	}
	if !hasAnyRef("file:/x") {
		t.Error("single non-empty should be true")
	}
	if hasAnyRef() {
		t.Error("no args should be false")
	}
}
