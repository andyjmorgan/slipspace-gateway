// Package connector defines the Connector interface every spool
// destination satisfies, and ships the test-only filesystem connector
// under testfs/.
//
// The durable destinations live in their own subpackages — s3/ and
// azureblob/ — and are constructed through factory/. This package holds
// only the interface plus testfs so the spool can be exercised without a
// cloud dependency.
//
// The `webhook` connector type is deliberately NOT one of these. It is a
// real-time, non-spooled pusher realized by internal/arbiter/pusher and
// wired directly in cmd/gateway; factory.Build rejects it.
package connector
