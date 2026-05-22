package azureblob

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"

	contractsconfig "github.com/andyjmorgan/sluice-gateway/contracts/config"
	cc "github.com/andyjmorgan/sluice-gateway/contracts/connector"
	"github.com/andyjmorgan/sluice-gateway/internal/connector"
)

// uploader is the narrow surface tests inject a fake into. The real
// *azblob.Client satisfies this via the UploadStream method.
type uploader interface {
	UploadStream(ctx context.Context, container, blob string, body io.Reader, opts *azblob.UploadStreamOptions) (azblob.UploadStreamResponse, error)
}

// SecretLoader resolves the env:/file: indirection at runtime. Same
// shape as the S3 connector for consistency; tests substitute.
type SecretLoader func(ref string) (string, error)

// DefaultSecretLoader walks env: and file: refs.
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
		b, err := os.ReadFile(p) //nolint:gosec // operator-supplied path
		if err != nil {
			return "", fmt.Errorf("read %q: %w", p, err)
		}
		return strings.TrimRight(string(b), "\r\n"), nil
	default:
		return "", fmt.Errorf("secret_ref must start with env: or file:, got %q", ref)
	}
}

// Options configures a Connector.
type Options struct {
	// Config is the validated YAML view. Required, Type == "azure_blob".
	Config contractsconfig.Connector

	// InstanceID is the gateway pod's stable identifier; goes into the
	// blob name partition. Defaults to "local" when empty.
	InstanceID string

	// Clock supplies wall-clock for partition fallback when TsMinNs==0.
	// Defaults to time.Now.
	Clock func() time.Time

	// SecretLoader resolves env:/file: refs. Defaults to
	// DefaultSecretLoader.
	SecretLoader SecretLoader

	// Client is an injected uploader. When nil, New builds an
	// *azblob.Client from the auth + endpoint settings. Tests inject a
	// fake; production code leaves nil.
	Client uploader

	// ServiceURLOverride lets the e2e harness point at Azurite. When
	// empty, the URL is derived from the account name as
	// https://<account>.blob.core.windows.net.
	ServiceURLOverride string
}

// Connector implements connector.Connector against Azure Blob Storage.
type Connector struct {
	name       string
	container  string
	prefix     string
	client     uploader
	instanceID string
	clock      func() time.Time
}

var _ connector.Connector = (*Connector)(nil)

// New constructs a Connector. Required: Options.Config.Type ==
// "azure_blob", Account, Container.
func New(_ context.Context, opts Options) (*Connector, error) {
	if opts.Config.Type != contractsconfig.ConnectorTypeAzureBlob {
		return nil, fmt.Errorf("azureblob: Options.Config.Type = %q, want azure_blob", opts.Config.Type)
	}
	if opts.Config.Name == "" {
		return nil, errors.New("azureblob: Options.Config.Name is required")
	}
	if opts.Config.Account == "" {
		return nil, errors.New("azureblob: Options.Config.Account is required")
	}
	if opts.Config.Container == "" {
		return nil, errors.New("azureblob: Options.Config.Container is required")
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
		built, err := buildClient(opts.Config, loader, opts.ServiceURLOverride)
		if err != nil {
			return nil, err
		}
		client = built
	}

	return &Connector{
		name:       opts.Config.Name,
		container:  opts.Config.Container,
		prefix:     opts.Config.Prefix,
		client:     client,
		instanceID: instanceID,
		clock:      clock,
	}, nil
}

// Name implements connector.Connector.
func (c *Connector) Name() string { return c.name }

// Type implements connector.Connector. Always "azure_blob".
func (c *Connector) Type() string { return contractsconfig.ConnectorTypeAzureBlob }

// Upload ships one sealed segment to Azure. Status-based error
// classification mirrors the S3 connector: 4xx (not 429) → Permanent;
// 5xx / 429 / context cancellation / unknown → Retryable.
func (c *Connector) Upload(ctx context.Context, seg cc.SealedSegment) error {
	if seg.Path == "" {
		return &cc.Permanent{Err: errors.New("azureblob: SealedSegment.Path is empty")}
	}
	f, err := os.Open(seg.Path) //nolint:gosec // path produced by Manager
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &cc.Permanent{Err: fmt.Errorf("azureblob: open segment: %w", err)}
		}
		return &cc.Retryable{Err: fmt.Errorf("azureblob: open segment: %w", err)}
	}
	defer func() { _ = f.Close() }()

	blob := c.blobName(seg)
	_, uerr := c.client.UploadStream(ctx, c.container, blob, f, &azblob.UploadStreamOptions{})
	if uerr != nil {
		return classifyError(uerr)
	}
	return nil
}

// blobName produces the design-note partition path under the optional
// configured Prefix.
func (c *Connector) blobName(seg cc.SealedSegment) string {
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

// classifyError maps Azure / context / I/O errors into Retryable vs
// Permanent.
func classifyError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return &cc.Retryable{Err: err}
	}
	var respErr *azcore.ResponseError
	if errors.As(err, &respErr) {
		status := respErr.StatusCode
		switch {
		case status == 429:
			return &cc.Retryable{Err: err}
		case status >= 500:
			return &cc.Retryable{Err: err}
		case status >= 400:
			return &cc.Permanent{Err: err}
		}
	}
	// Default to Retryable; MaxAttempts caps blast radius.
	return &cc.Retryable{Err: err}
}

// buildClient wires *azblob.Client per the declared auth mode.
//
//   - workload_identity → DefaultAzureCredential (the full chain)
//   - sas_token → URL-with-SAS, no credential
//   - account_key → SharedKeyCredential built from account + key
func buildClient(cfg contractsconfig.Connector, load SecretLoader, serviceURLOverride string) (*azblob.Client, error) {
	serviceURL := serviceURLOverride
	if serviceURL == "" {
		serviceURL = fmt.Sprintf("https://%s.blob.core.windows.net", cfg.Account)
	}

	mode := ""
	if cfg.Auth != nil {
		mode = cfg.Auth.Mode
	}

	switch mode {
	case "", contractsconfig.AuthModeWorkloadIdentity:
		cred, err := azidentity.NewDefaultAzureCredential(nil)
		if err != nil {
			return nil, fmt.Errorf("azureblob: workload_identity credential: %w", err)
		}
		c, err := azblob.NewClient(serviceURL, cred, nil)
		if err != nil {
			return nil, fmt.Errorf("azureblob: client: %w", err)
		}
		return c, nil

	case contractsconfig.AuthModeSASToken:
		token, err := load(cfg.Auth.SASTokenRef)
		if err != nil {
			return nil, fmt.Errorf("azureblob: load sas_token_ref: %w", err)
		}
		full := serviceURL + "?" + strings.TrimPrefix(token, "?")
		c, err := azblob.NewClientWithNoCredential(full, nil)
		if err != nil {
			return nil, fmt.Errorf("azureblob: sas client: %w", err)
		}
		return c, nil

	case contractsconfig.AuthModeAccountKey:
		key, err := load(cfg.Auth.AccountKeyRef)
		if err != nil {
			return nil, fmt.Errorf("azureblob: load account_key_ref: %w", err)
		}
		cred, cerr := azblob.NewSharedKeyCredential(cfg.Account, key)
		if cerr != nil {
			return nil, fmt.Errorf("azureblob: shared key credential: %w", cerr)
		}
		c, err := azblob.NewClientWithSharedKeyCredential(serviceURL, cred, nil)
		if err != nil {
			return nil, fmt.Errorf("azureblob: shared key client: %w", err)
		}
		return c, nil

	default:
		return nil, fmt.Errorf("azureblob: unsupported auth mode %q", mode)
	}
}
