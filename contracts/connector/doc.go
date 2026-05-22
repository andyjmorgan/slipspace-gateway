// Package connector defines the public wire format every connector emits
// and the typed-error contract every connector returns from Upload.
//
// The shape of [Record] is the v1 wire format. Per the connector schema
// rule (see DonkeyWork design note "Connector + Spool Architecture"),
// fields can be added but never removed, renamed, type-narrowed, or
// repurposed. Breaking changes ship as a new connector type, never as a
// schema bump on an existing one.
//
// Consumers of records (auditors, refinement-data pipelines) MUST tolerate
// unknown fields. Do not call json.Decoder.DisallowUnknownFields on
// inbound batches — that contradicts the additive-only contract.
package connector
