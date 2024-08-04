package observability

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.17.0"
	"go.opentelemetry.io/otel/trace"
)

var (
	tracer     trace.Tracer
	meter      metric.Meter
	counters   = make(map[string]metric.Int64Counter)
	histograms = make(map[string]metric.Float64Histogram)
)

type ObservabilityShutdownFunc func()

func InitTelemetry(serviceName string, tracingEndpoint string, metricEndpoint string, isInsecure bool, ratioTrace float64) (ObservabilityShutdownFunc, error) {
	// Create a resource with service name and other attributes
	res, err := resource.New(context.Background(),
		resource.WithAttributes(
			semconv.ServiceNameKey.String(serviceName),
			semconv.ServiceVersionKey.String("1.0.0"),
			semconv.DeploymentEnvironmentKey.String("production"),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create resource: %w", err)
	}

	// Initialize trace exporter
	var traceExporterOpts []otlptracehttp.Option
	traceExporterOpts = append(traceExporterOpts, otlptracehttp.WithEndpoint(tracingEndpoint))
	if isInsecure {
		traceExporterOpts = append(traceExporterOpts, otlptracehttp.WithInsecure())
	}
	traceExporter, err := otlptracehttp.New(context.Background(), traceExporterOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create trace exporter: %w", err)
	}

	// Create trace provider
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(traceExporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.TraceIDRatioBased(ratioTrace)),
	)
	otel.SetTracerProvider(tp)
	tracer = tp.Tracer("application-tracer")

	// Initialize metric exporter
	var metricExporterOpts []otlpmetrichttp.Option
	metricExporterOpts = append(metricExporterOpts, otlpmetrichttp.WithEndpoint(metricEndpoint))
	if isInsecure {
		metricExporterOpts = append(metricExporterOpts, otlpmetrichttp.WithInsecure())
	}
	metricExporter, err := otlpmetrichttp.New(context.Background(), metricExporterOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create metric exporter: %w", err)
	}

	// Create meter provider
	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExporter, sdkmetric.WithInterval(10*time.Second))),
		sdkmetric.WithResource(res),
	)
	otel.SetMeterProvider(mp)
	meter = mp.Meter("application-metrics")

	// Create common metrics
	if _, err := CreateCounter("http_requests_total", "Total number of HTTP requests", "{count}"); err != nil {
		return nil, fmt.Errorf("failed to create http_requests_total counter: %w", err)
	}
	if _, err := CreateHistogram("http_request_duration_seconds", "HTTP request latencies in seconds", "s"); err != nil {
		return nil, fmt.Errorf("failed to create http_request_duration_seconds histogram: %w", err)
	}

	return ObservabilityShutdownFunc(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := tp.Shutdown(ctx); err != nil {
			log.Printf("Error shutting down tracer provider: %v", err)
		}
		if err := mp.Shutdown(ctx); err != nil {
			log.Printf("Error shutting down meter provider: %v", err)
		}
	}), nil
}

// Tracing helper functions

func StartSpan(ctx context.Context, name string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	return tracer.Start(ctx, name, opts...)
}

func AddEvent(ctx context.Context, name string, attrs ...attribute.KeyValue) {
	span := trace.SpanFromContext(ctx)
	span.AddEvent(name, trace.WithAttributes(attrs...))
}

func SetAttributes(ctx context.Context, attrs ...attribute.KeyValue) {
	span := trace.SpanFromContext(ctx)
	span.SetAttributes(attrs...)
}

// Metrics helper functions

func CreateCounter(name, description, unit string) (metric.Int64Counter, error) {
	if counter, exists := counters[name]; exists {
		return counter, nil
	}
	counter, err := meter.Int64Counter(
		name,
		metric.WithDescription(description),
		metric.WithUnit(unit),
	)
	if err != nil {
		return nil, err
	}
	counters[name] = counter
	return counter, nil
}

func CreateHistogram(name, description, unit string) (metric.Float64Histogram, error) {
	if histogram, exists := histograms[name]; exists {
		return histogram, nil
	}
	histogram, err := meter.Float64Histogram(
		name,
		metric.WithDescription(description),
		metric.WithUnit(unit),
	)
	if err != nil {
		return nil, err
	}
	histograms[name] = histogram
	return histogram, nil
}

func IncrementCounter(ctx context.Context, name string, value int64, attrs ...attribute.KeyValue) {
	if counter, exists := counters[name]; exists {
		counter.Add(ctx, value, metric.WithAttributes(attrs...))
	}
}

func RecordHistogram(ctx context.Context, name string, value float64, attrs ...attribute.KeyValue) {
	if histogram, exists := histograms[name]; exists {
		histogram.Record(ctx, value, metric.WithAttributes(attrs...))
	}
}

// Middleware for HTTP servers

// TODO: Need to understand why it send two trace.
func TelemetryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		start := time.Now()

		// Start the main span for the request
		ctx, span := tracer.Start(ctx, "http_request",
			trace.WithAttributes(
				attribute.String("http.method", r.Method),
				attribute.String("http.url", r.URL.String()),
				attribute.String("http.host", r.Host),
				attribute.String("http.user_agent", r.UserAgent()),
				attribute.String("path", r.URL.Path),
			),
		)
		defer span.End()

		// Call the next handler with the updated context
		next.ServeHTTP(w, r.WithContext(ctx))

		duration := time.Since(start).Seconds()

		// Record metrics
		IncrementCounter(ctx, "http_requests_total", 1,
			attribute.String("method", r.Method),
			attribute.String("path", r.URL.Path),
		)
		RecordHistogram(ctx, "http_request_duration_seconds", duration,
			attribute.String("method", r.Method),
			attribute.String("path", r.URL.Path),
		)

		// You could add some response information here if needed
		// For example:
		// span.SetAttributes(attribute.Int("http.status_code", getStatusCode(w)))
	})
}
