// Package azureblob implements a Connector that ships sealed segments
// to Azure Blob Storage.
//
// Blob layout mirrors the design note (and the S3 connector's keys):
//
//	[<prefix>/]records/instance=<id>/date=YYYY-MM-DD/hour=HH/<unix_ns>-<seq>-<delivery_id>.ndjson.zst
//
// Auth modes (per the validated contracts/config.ConnectorAuth):
//   - workload_identity (default): azidentity.NewDefaultAzureCredential
//     walks AKS Workload Identity / Managed Identity / env vars / Azure
//     CLI in order.
//   - sas_token: shared-access signature URL fragment read via the
//     secret_ref env:/file: indirection.
//   - account_key: storage account key + the configured account name.
//
// Azurite (mcr.microsoft.com/azure-storage/azurite) is the official
// emulator the e2e suite drives.
package azureblob
