// Package testfs implements a Connector that copies sealed segments to a
// configured directory on the local filesystem. Its only consumer is the
// spool's own test suite (internal/spool/spool_test.go), which can inspect
// captured records without standing up cloud emulators; the e2e suites use
// SeaweedFS / Azurite testcontainers and httptest webhook receivers instead.
//
// testfs is NOT a public connector type. It is not selectable via YAML.
// Production deployments wanting on-prem object storage configure the
// S3 connector against a SeaweedFS / MinIO / Garage / Ceph RGW endpoint
// instead.
package testfs
