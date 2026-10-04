// Package telemetry wires a process into the OTel Collector for both
// traces and metrics. Both the gateway and control-api call Init at
// startup and Shutdown on the way out, so every span and every metric
// from either service lands in the same backends (Tempo for traces,
// Prometheus for metrics, both viewed through Grafana) under its own
// service name.
//
// Metrics instruments live in the specific package that produces them
// (internal/policy, internal/approval, etc.), each via its own
// otel.Meter("warden/<package>") call — this file only sets up the one
// global MeterProvider they all register against, plus the automatic
// HTTP server metrics (request rate, added latency) that otelhttp emits
// for free once a MeterProvider exists, with no extra instrumentation
// needed at the handler level.
package telemetry

import (
	"context"
	"fmt"
	"os"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// Init starts trace and metric pipelines for serviceName, exporting to the
// OTLP endpoint named by OTEL_EXPORTER_OTLP_ENDPOINT (default
// localhost:4317, matching the local docker-compose collector). It returns
// a shutdown func that flushes and closes both exporters; callers must
// invoke it before exit.
func Init(ctx context.Context, serviceName string) (shutdown func(context.Context) error, err error) {
	endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	if endpoint == "" {
		endpoint = "localhost:4317"
	}

	conn, err := grpc.NewClient(endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("telemetry: dial collector at %s: %w", endpoint, err)
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(semconv.ServiceName(serviceName)),
	)
	if err != nil {
		return nil, fmt.Errorf("telemetry: build resource: %w", err)
	}

	traceExporter, err := otlptracegrpc.New(ctx, otlptracegrpc.WithGRPCConn(conn))
	if err != nil {
		return nil, fmt.Errorf("telemetry: create OTLP trace exporter: %w", err)
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(traceExporter),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)

	metricExporter, err := otlpmetricgrpc.New(ctx, otlpmetricgrpc.WithGRPCConn(conn))
	if err != nil {
		return nil, fmt.Errorf("telemetry: create OTLP metric exporter: %w", err)
	}
	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		// 15s, not the SDK's 60s default — short enough that a Grafana
		// panel watched live during a demo (e.g. the approval queue depth
		// while running cmd/killtest) visibly updates, not just
		// eventually-correct numbers nobody sees move.
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExporter, sdkmetric.WithInterval(15*time.Second))),
	)
	otel.SetMeterProvider(mp)

	return func(shutdownCtx context.Context) error {
		if err := tp.Shutdown(shutdownCtx); err != nil {
			return err
		}
		if err := mp.Shutdown(shutdownCtx); err != nil {
			return err
		}
		return conn.Close()
	}, nil
}
