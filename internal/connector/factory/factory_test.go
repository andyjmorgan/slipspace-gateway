package factory

import (
	"context"
	"strings"
	"testing"

	contractsconfig "github.com/andyjmorgan/sluice-gateway/contracts/config"
)

func TestBuild_S3(t *testing.T) {
	c, err := Build(context.Background(), contractsconfig.Connector{
		Type: "s3", Name: "x", Bucket: "b", Region: "us-east-1",
	}, Options{InstanceID: "i"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if c.Type() != "s3" {
		t.Errorf("Type = %q", c.Type())
	}
}

func TestBuild_AzureBlob(t *testing.T) {
	c, err := Build(context.Background(), contractsconfig.Connector{
		Type: "azure_blob", Name: "x", Account: "a", Container: "c",
	}, Options{InstanceID: "i"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if c.Type() != "azure_blob" {
		t.Errorf("Type = %q", c.Type())
	}
}

func TestBuild_Webhook(t *testing.T) {
	t.Setenv("FAC_HOOK_SECRET", "k")
	c, err := Build(context.Background(), contractsconfig.Connector{ //nolint:gosec // G101: fixture
		Type: "webhook", Name: "x", URL: "https://example.com",
		SecretRef: "env:FAC_HOOK_SECRET", TimeoutMS: 1000,
	}, Options{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if c.Type() != "webhook" {
		t.Errorf("Type = %q", c.Type())
	}
}

func TestBuild_UnknownType(t *testing.T) {
	_, err := Build(context.Background(), contractsconfig.Connector{
		Type: "carrier-pigeon", Name: "x",
	}, Options{})
	if err == nil || !strings.Contains(err.Error(), "unknown connector type") {
		t.Errorf("got %v", err)
	}
}

func TestBuildAll_PreservesOrder(t *testing.T) {
	t.Setenv("HOOK_S", "k")
	cs := []contractsconfig.Connector{
		{Type: "s3", Name: "first", Bucket: "b", Region: "r"},
		{Type: "azure_blob", Name: "second", Account: "a", Container: "c"},
		{Type: "webhook", Name: "third", URL: "https://x", SecretRef: "env:HOOK_S", TimeoutMS: 1000}, //nolint:gosec // G101: fixture
	}
	out, err := BuildAll(context.Background(), cs, Options{})
	if err != nil {
		t.Fatalf("BuildAll: %v", err)
	}
	if len(out) != 3 {
		t.Fatalf("len = %d, want 3", len(out))
	}
	wantNames := []string{"first", "second", "third"}
	for i, want := range wantNames {
		if out[i].Name() != want {
			t.Errorf("[%d] name = %q, want %q", i, out[i].Name(), want)
		}
	}
}

func TestBuildAll_PropagatesError(t *testing.T) {
	cs := []contractsconfig.Connector{
		{Type: "s3", Name: "ok", Bucket: "b", Region: "r"},
		{Type: "bad-type", Name: "broken"},
	}
	_, err := BuildAll(context.Background(), cs, Options{})
	if err == nil || !strings.Contains(err.Error(), "broken") {
		t.Errorf("expected error citing broken connector, got %v", err)
	}
}

func TestBuildAll_EmptyOK(t *testing.T) {
	out, err := BuildAll(context.Background(), nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 0 {
		t.Errorf("empty input → len %d, want 0", len(out))
	}
}
