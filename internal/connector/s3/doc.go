// Package s3 implements a Connector backed by AWS S3 or any S3-compatible
// provider (MinIO, SeaweedFS, Garage, Ceph RGW).
//
// Object layout mirrors the design note (the optional configured prefix is
// prepended when set):
//
//	[<prefix>/]records/instance=<id>/date=YYYY-MM-DD/hour=HH/<unix_ns>-<seq>-<delivery_id>.ndjson.zst
//
// The spool sets delivery_id to the segment filename stem, so the stem
// appears twice in production keys.
//
// Auth modes (per the validated contracts/config.ConnectorAuth):
//   - workload_identity (default): the SDK's default credential chain
//     resolves IRSA / EKS pod identity / instance role / env vars in order.
//   - static: explicit access_key_id + secret_access_key read via the
//     secret_ref env:/file: indirection.
//   - assume_role: cross-account role assumption with optional external_id.
//
// S3-compatible providers are addressed via Connector.EndpointURL +
// UsePathStyle. The SDK still requires a Region; arbitrary values are
// accepted by SeaweedFS / MinIO.
package s3
