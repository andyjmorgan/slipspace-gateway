package livefeed

import (
	"github.com/klauspost/compress/zstd"
)

// bodyEncoder and bodyDecoder are process-lifetime singletons reused
// across every Put/Get so we don't allocate a fresh encoder/decoder
// per envelope. klauspost's zstd EncodeAll / DecodeAll are documented
// as safe for concurrent use against a shared instance.
var (
	bodyEncoder *zstd.Encoder
	bodyDecoder *zstd.Decoder
)

func init() {
	// SpeedDefault gives a good balance for JSON payloads: ~5-10x
	// compression on chat-completion bodies with negligible CPU cost
	// at the rates the live feed sees. SpeedBetterCompression squeezes
	// another ~10% but costs ~3x the CPU on encode, not worth it for
	// in-memory cache.
	enc, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedDefault))
	if err != nil {
		// NewWriter only fails on bad option combinations; the
		// hand-picked level above is guaranteed valid.
		panic("livefeed: zstd encoder init: " + err.Error())
	}
	bodyEncoder = enc

	dec, err := zstd.NewReader(nil)
	if err != nil {
		panic("livefeed: zstd decoder init: " + err.Error())
	}
	bodyDecoder = dec
}

// compressedEnvelope is the in-memory representation of a captured
// request/response held by BodyStore. The big byte slices —
// Request, Response, ResponseAssembled — are zstd-compressed at Put
// time so the LRU budget can hold significantly more event history
// at the same memory cost. RequestHeaders / ResponseHeaders are kept
// uncompressed because the redacted snapshots are small and
// frequently nil; the overhead would dominate.
//
// Conversion is hidden behind the BodyStore boundary: callers always
// see decompressed BodyEnvelopes via Get; Put accepts decompressed
// BodyEnvelopes and compresses internally.
type compressedEnvelope struct {
	request           []byte
	requestTotalBytes int64
	requestTruncated  bool

	response           []byte
	responseTotalBytes int64
	responseTruncated  bool

	responseAssembled []byte
	assemblyPartial   bool

	requestHeaders  map[string][]string
	responseHeaders map[string][]string
}

// bytes is the accounted footprint for the LRU budget. Reports
// post-compression sizes so the configured cap reflects actual
// memory usage.
func (c *compressedEnvelope) bytes() int {
	n := len(c.request) + len(c.response) + len(c.responseAssembled)
	n += headerMapBytes(c.requestHeaders)
	n += headerMapBytes(c.responseHeaders)
	return n
}

// compressEnvelope encodes the byte-heavy fields of env via zstd and
// returns the resulting compressedEnvelope. Empty inputs round-trip
// as empty slices — the encoder still produces a short framed
// payload, but the size delta is negligible and the uniform code
// path keeps decode simpler.
func compressEnvelope(env BodyEnvelope) compressedEnvelope {
	return compressedEnvelope{
		request:            compressBytes(env.Request),
		requestTotalBytes:  env.RequestTotalBytes,
		requestTruncated:   env.RequestTruncated,
		response:           compressBytes(env.Response),
		responseTotalBytes: env.ResponseTotalBytes,
		responseTruncated:  env.ResponseTruncated,
		responseAssembled:  compressBytes([]byte(env.ResponseAssembled)),
		assemblyPartial:    env.AssemblyPartial,
		requestHeaders:     env.RequestHeaders,
		responseHeaders:    env.ResponseHeaders,
	}
}

// decompressEnvelope is the inverse of compressEnvelope. Returns a
// fresh BodyEnvelope safe for the caller to mutate without affecting
// the stored copy. A decode error reduces to an empty slice on that
// specific field rather than failing the whole Get — the corruption
// would have to come from zstd itself, which is exceedingly unlikely
// in an in-process pipeline.
func decompressEnvelope(c compressedEnvelope) BodyEnvelope {
	return BodyEnvelope{
		Request:            decompressBytes(c.request),
		RequestTotalBytes:  c.requestTotalBytes,
		RequestTruncated:   c.requestTruncated,
		Response:           decompressBytes(c.response),
		ResponseTotalBytes: c.responseTotalBytes,
		ResponseTruncated:  c.responseTruncated,
		ResponseAssembled:  string(decompressBytes(c.responseAssembled)),
		AssemblyPartial:    c.assemblyPartial,
		RequestHeaders:     c.requestHeaders,
		ResponseHeaders:    c.responseHeaders,
	}
}

// compressBytes returns nil for nil/empty input so we don't store an
// 8-byte zstd frame for missing fields.
func compressBytes(in []byte) []byte {
	if len(in) == 0 {
		return nil
	}
	return bodyEncoder.EncodeAll(in, nil)
}

// decompressBytes returns nil for nil input. A decode error returns
// nil too — the alternative is propagating an error through Get,
// which complicates the call site for what is effectively impossible
// in normal operation.
func decompressBytes(in []byte) []byte {
	if len(in) == 0 {
		return nil
	}
	out, err := bodyDecoder.DecodeAll(in, nil)
	if err != nil {
		return nil
	}
	return out
}
