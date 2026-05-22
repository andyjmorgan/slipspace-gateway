// Package testfs implements a Connector that copies sealed segments to a
// configured directory on the local filesystem. It is the destination
// used by the e2e harness and by `make dev` so a developer can `ls` and
// inspect captured records without standing up cloud emulators.
//
// testfs is NOT a public connector type. It is not selectable via YAML.
// Production deployments wanting on-prem object storage configure the
// S3 connector against a SeaweedFS / MinIO / Garage / Ceph RGW endpoint
// instead.
package testfs
