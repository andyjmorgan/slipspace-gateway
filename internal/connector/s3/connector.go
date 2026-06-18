package s3

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	s3sdk "github.com/aws/aws-sdk-go-v2/service/s3"
	stssdk "github.com/aws/aws-sdk-go-v2/service/sts"
	smithyhttp "github.com/aws/smithy-go/transport/http"

	contractsconfig "github.com/andyjmorgan/slipspace-gateway/contracts/config"
	cc "github.com/andyjmorgan/slipspace-gateway/contracts/connector"
	"github.com/andyjmorgan/slipspace-gateway/internal/connector"
)

// S3 putter narrowed to what we need; lets tests inject a fake without
// pulling the whole SDK surface.
type s3Putter interface {
	PutObject(ctx context.Context, in *s3sdk.PutObjectInput, opts ...func(*s3sdk.Options)) (*s3sdk.PutObjectOutput, error)
}

// SecretLoader reads a secret_ref string ("env:NAME" or "file:/path")
// and returns the resolved secret. Pulled out so tests can inject
// without setting env vars.
type SecretLoader func(ref string) (string, error)

// DefaultSecretLoader resolves the env:/file: indirection at runtime.
func DefaultSecretLoader(ref string) (string, error) {
	switch {
	case strings.HasPrefix(ref, "env:"):
		name := strings.TrimPrefix(ref, "env:")
		val, ok := os.LookupEnv(name)
		if !ok {
			return "", fmt.Errorf("env var %q not set", name)
		}
		return val, nil
	case strings.HasPrefix(ref, "file:"):
		p := strings.TrimPrefix(ref, "file:")
		b, err := os.ReadFile(p) //nolint:gosec // operator-supplied path; same trust level as YAML
		if err != nil {
			return "", fmt.Errorf("read %q: %w", p, err)
		}
		return strings.TrimRight(string(b), "\r\n"), nil
	default:
		return "", fmt.Errorf("secret_ref must start with env: or file:, got %q", ref)
	}
}

// Options configures a Connector. The Config field is the validated
// contracts/config.Connector entry — all per-type fields (bucket,
// region, endpoint_url, ...) flow through there.
type Options struct {
	// Config is the validated YAML view. Required, must have
	// Type == "s3".
	Config contractsconfig.Connector

	// InstanceID is the gateway pod's stable identifier; goes into the
	// object key partition. Defaults to "local" when empty.
	InstanceID string

	// Clock supplies wall-clock for partition fallback when a segment
	// arrives with TsMinNs == 0. Defaults to time.Now.
	Clock func() time.Time

	// SecretLoader resolves env:/file: references. Defaults to
	// DefaultSecretLoader. Tests substitute.
	SecretLoader SecretLoader

	// Client is an injected S3 client. When nil, New builds one from
	// the auth + endpoint settings. Tests inject a fake; production code
	// leaves nil.
	Client s3Putter
}

// Connector implements connector.Connector against an S3-compatible
// provider.
type Connector struct {
	name         string
	bucket       string
	prefix       string
	usePathStyle bool

	client     s3Putter
	instanceID string
	clock      func() time.Time
}

// Compile-time assertion the impl satisfies the interface.
var _ connector.Connector = (*Connector)(nil)

// New constructs a Connector. Required fields on Options.Config:
// Type == "s3", Bucket, Region. Returns wrapped errors that callers can
// surface to the operator at gateway startup.
func New(ctx context.Context, opts Options) (*Connector, error) {
	if opts.Config.Type != contractsconfig.ConnectorTypeS3 {
		return nil, fmt.Errorf("s3: Options.Config.Type = %q, want s3", opts.Config.Type)
	}
	if opts.Config.Name == "" {
		return nil, errors.New("s3: Options.Config.Name is required")
	}
	if opts.Config.Bucket == "" {
		return nil, errors.New("s3: Options.Config.Bucket is required")
	}
	if opts.Config.Region == "" {
		return nil, errors.New("s3: Options.Config.Region is required")
	}

	loader := opts.SecretLoader
	if loader == nil {
		loader = DefaultSecretLoader
	}
	clock := opts.Clock
	if clock == nil {
		clock = time.Now
	}
	instanceID := opts.InstanceID
	if instanceID == "" {
		instanceID = "local"
	}

	client := opts.Client
	if client == nil {
		built, err := buildClient(ctx, opts.Config, loader)
		if err != nil {
			return nil, err
		}
		client = built
	}

	return &Connector{
		name:         opts.Config.Name,
		bucket:       opts.Config.Bucket,
		prefix:       opts.Config.Prefix,
		usePathStyle: opts.Config.UsePathStyle,
		client:       client,
		instanceID:   instanceID,
		clock:        clock,
	}, nil
}

// Name implements connector.Connector.
func (c *Connector) Name() string { return c.name }

// Type implements connector.Connector. Always "s3".
func (c *Connector) Type() string { return contractsconfig.ConnectorTypeS3 }

// Upload ships one sealed segment to S3. Classification:
//
//   - 2xx                 → nil (success).
//   - 4xx (except 429)    → *cc.Permanent (auth, bucket missing,
//     signature) — spool moves to deadletter.
//   - 5xx + 429 + I/O     → *cc.Retryable — spool backoff + retry.
//   - context cancelled   → *cc.Retryable wrapping ctx.Err.
func (c *Connector) Upload(ctx context.Context, seg cc.SealedSegment) error {
	if seg.Path == "" {
		return &cc.Permanent{Err: errors.New("s3: SealedSegment.Path is empty")}
	}

	f, err := os.Open(seg.Path) //nolint:gosec // path produced by Manager
	if err != nil {
		// Missing file is a real-but-not-retry problem if the path
		// is gone (operator deleted, FS corruption); retry on most
		// other I/O failures.
		if errors.Is(err, os.ErrNotExist) {
			return &cc.Permanent{Err: fmt.Errorf("s3: open segment: %w", err)}
		}
		return &cc.Retryable{Err: fmt.Errorf("s3: open segment: %w", err)}
	}
	defer func() { _ = f.Close() }()

	key := c.objectKey(seg)

	_, putErr := c.client.PutObject(ctx, &s3sdk.PutObjectInput{
		Bucket:      awssdk.String(c.bucket),
		Key:         awssdk.String(key),
		Body:        f,
		ContentType: awssdk.String("application/zstd"),
	})
	if putErr != nil {
		return classifyError(putErr)
	}
	return nil
}

// objectKey produces the design-note partition path:
//
//	[<prefix>/]records/instance=<id>/date=YYYY-MM-DD/hour=HH/<unix_ns>-<seq>-<delivery_id>.ndjson.zst
func (c *Connector) objectKey(seg cc.SealedSegment) string {
	var t time.Time
	if seg.TsMinNs > 0 {
		t = time.Unix(0, seg.TsMinNs).UTC()
	} else {
		t = c.clock().UTC()
	}
	base := path.Base(seg.Path)
	const ext = ".ndjson.zst"
	stem := base
	if len(stem) > len(ext) && stem[len(stem)-len(ext):] == ext {
		stem = stem[:len(stem)-len(ext)]
	}
	name := stem + ext
	if seg.DeliveryID != "" {
		name = stem + "-" + seg.DeliveryID + ext
	}
	parts := []string{}
	if c.prefix != "" {
		parts = append(parts, strings.Trim(c.prefix, "/"))
	}
	parts = append(parts,
		"records",
		"instance="+c.instanceID,
		"date="+t.Format("2006-01-02"),
		fmt.Sprintf("hour=%02d", t.Hour()),
		name,
	)
	return strings.Join(parts, "/")
}

// classifyError maps an AWS / smithy / I/O error into the typed pair the
// spool's uploader understands. Anything 4xx-except-429 is permanent;
// anything 5xx/429/network is retryable.
func classifyError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return &cc.Retryable{Err: err}
	}
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return &cc.Retryable{Err: err}
	}
	var httpErr *smithyhttp.ResponseError
	if errors.As(err, &httpErr) {
		status := httpErr.HTTPStatusCode()
		switch {
		case status == 429:
			return &cc.Retryable{Err: err}
		case status >= 500:
			return &cc.Retryable{Err: err}
		case status >= 400:
			return &cc.Permanent{Err: err}
		}
	}
	// Default: retry. Conservative — the spool's MaxAttempts caps
	// the blast radius if we're wrong.
	return &cc.Retryable{Err: err}
}

// buildClient wires aws-sdk-go-v2 with the auth mode declared in the
// config. workload_identity → SDK default chain; static → explicit
// credentials; assume_role → STS-backed credential provider.
func buildClient(ctx context.Context, cfg contractsconfig.Connector, load SecretLoader) (*s3sdk.Client, error) {
	loadOpts := []func(*awsconfig.LoadOptions) error{
		awsconfig.WithRegion(cfg.Region),
	}

	switch {
	case cfg.Auth == nil, cfg.Auth.Mode == "", cfg.Auth.Mode == contractsconfig.AuthModeWorkloadIdentity:
		// Default credential chain.

	case cfg.Auth.Mode == contractsconfig.AuthModeStatic:
		accessKey, err := load(cfg.Auth.AccessKeyIDRef)
		if err != nil {
			return nil, fmt.Errorf("s3: load access_key_id_ref: %w", err)
		}
		secretKey, err := load(cfg.Auth.SecretAccessKeyRef)
		if err != nil {
			return nil, fmt.Errorf("s3: load secret_access_key_ref: %w", err)
		}
		loadOpts = append(loadOpts, awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(accessKey, secretKey, ""),
		))

	case cfg.Auth.Mode == contractsconfig.AuthModeAssumeRole:
		// AssumeRole sits on top of the default chain — we need a base
		// config to mint the STS client from.
		baseCfg, berr := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(cfg.Region))
		if berr != nil {
			return nil, fmt.Errorf("s3: base config for assume_role: %w", berr)
		}
		stsClient := stssdk.NewFromConfig(baseCfg)

		assumeOpts := []func(*stscreds.AssumeRoleOptions){}
		if cfg.Auth.ExternalIDRef != "" {
			extID, lerr := load(cfg.Auth.ExternalIDRef)
			if lerr != nil {
				return nil, fmt.Errorf("s3: load external_id_ref: %w", lerr)
			}
			assumeOpts = append(assumeOpts, func(o *stscreds.AssumeRoleOptions) {
				o.ExternalID = awssdk.String(extID)
			})
		}
		provider := stscreds.NewAssumeRoleProvider(stsClient, cfg.Auth.RoleARN, assumeOpts...)
		loadOpts = append(loadOpts, awsconfig.WithCredentialsProvider(provider))

	default:
		return nil, fmt.Errorf("s3: unsupported auth mode %q", cfg.Auth.Mode)
	}

	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, loadOpts...)
	if err != nil {
		return nil, fmt.Errorf("s3: load aws config: %w", err)
	}

	clientOpts := []func(*s3sdk.Options){}
	if cfg.EndpointURL != "" {
		ep := cfg.EndpointURL
		clientOpts = append(clientOpts, func(o *s3sdk.Options) {
			o.BaseEndpoint = awssdk.String(ep)
		})
	}
	if cfg.UsePathStyle {
		clientOpts = append(clientOpts, func(o *s3sdk.Options) {
			o.UsePathStyle = true
		})
	}
	return s3sdk.NewFromConfig(awsCfg, clientOpts...), nil
}
