package audit

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// verifyFailures and lastVerifyOK back the spec's "alerts on ... audit-chain
// verification failures" requirement. A counter alone can't express "the
// chain is CURRENTLY broken" (it only ever goes up, including for a
// failure that's since been investigated and explained), so this also
// keeps a gauge of the most recent result specifically for alerting on
// current state, separate from the counter's job of showing failure
// volume over time.
var (
	verifyFailures metric.Int64Counter
	lastVerifyOK   metric.Int64Gauge
)

func init() {
	meter := otel.Meter("warden/audit")
	var err error
	verifyFailures, err = meter.Int64Counter(
		"warden.audit.verify_failures",
		metric.WithDescription("Audit chain verification runs that found tampering"),
	)
	if err != nil {
		panic(err)
	}
	lastVerifyOK, err = meter.Int64Gauge(
		"warden.audit.last_verify_ok",
		metric.WithDescription("1 if the most recent audit chain verification passed, 0 if it found tampering"),
	)
	if err != nil {
		panic(err)
	}
}

// RecordVerifyResult reports a completed verification (console-triggered
// or periodic — see cmd/control-api) to the metrics backend. Call this
// everywhere a VerifyResult is produced, not just in one caller, so the
// gauge/counter reflect every check regardless of what triggered it.
func RecordVerifyResult(ctx context.Context, source string, result VerifyResult) {
	ok := int64(0)
	if result.OK {
		ok = 1
	} else {
		verifyFailures.Add(ctx, 1, metric.WithAttributes(attribute.String("source", source)))
	}
	lastVerifyOK.Record(ctx, ok, metric.WithAttributes(attribute.String("source", source)))
}
