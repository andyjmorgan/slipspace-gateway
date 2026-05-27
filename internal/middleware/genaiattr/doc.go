// Package genaiattr extracts OpenTelemetry GenAI semantic-convention
// attributes from provider request and response bodies.
//
// It is the attribute-side companion to the tokens package: tokens pulls
// usage counts, genaiattr pulls the request parameters (temperature, top_p,
// max_tokens, ...) and response descriptors (id, model, finish_reasons)
// that the GenAI spans convention defines as Conditionally Required or
// Recommended span attributes. Both follow the same endpoint-keyed registry
// so adding a provider extends them in tandem.
//
// Extraction is best-effort and lossless-on-failure: an unrecognised
// endpoint or an unparseable body yields a zero-value result whose pointer
// fields are nil, so the caller simply omits the attributes rather than
// emitting wrong ones. Only fields actually present in the body are
// populated — pointers distinguish "absent" from "zero".
package genaiattr
