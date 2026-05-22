// Package connector defines the Connector interface every destination
// implementation satisfies, and ships the test-only filesystem connector
// under testfs/.
//
// Concrete public connectors (s3, azure_blob, webhook) live in their own
// subpackages and land in subsequent PRs. This package intentionally
// contains only the interface + the testfs impl so the spool can land
// independently with a real exerciser.
package connector
