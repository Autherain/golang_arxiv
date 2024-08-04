package observability

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"runtime"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.17.0"
	"go.opentelemetry.io/otel/trace"
	nooptrace "go.opentelemetry.io/otel/trace/noop"
)

var (
	tracer          trace.Tracer
	meter           metric.Meter
	counters        = make(map[string]metric.Int64Counter)
	histograms      = make(map[string]metric.Float64Histogram)
	gauges          = make(map[string]metric.Float64UpDownCounter)
	lastKnownValues = make(map[string]float64)
)

type ObservabilityShutdownFunc func()

func InitTelemetry(serviceName string, tracingEndpoint string, metricEndpoint string, isInsecure bool, ratioTrace float64, enableTelemetry bool) (ObservabilityShutdownFunc, error) {
	if !enableTelemetry {
		otel.SetTracerProvider(nooptrace.NewTracerProvider())
		otel.SetMeterProvider(noop.NewMeterProvider())
		return func() {}, nil
	}

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

	var traceExporterOpts []otlptracehttp.Option
	traceExporterOpts = append(traceExporterOpts, otlptracehttp.WithEndpoint(tracingEndpoint))
	if isInsecure {
		traceExporterOpts = append(traceExporterOpts, otlptracehttp.WithInsecure())
	}
	traceExporter, err := otlptracehttp.New(context.Background(), traceExporterOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create trace exporter: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(traceExporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.TraceIDRatioBased(ratioTrace)),
	)
	otel.SetTracerProvider(tp)
	tracer = tp.Tracer("application-tracer")

	var metricExporterOpts []otlpmetrichttp.Option
	metricExporterOpts = append(metricExporterOpts, otlpmetrichttp.WithEndpoint(metricEndpoint))
	if isInsecure {
		metricExporterOpts = append(metricExporterOpts, otlpmetrichttp.WithInsecure())
	}
	metricExporter, err := otlpmetrichttp.New(context.Background(), metricExporterOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create metric exporter: %w", err)
	}

	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExporter, sdkmetric.WithInterval(10*time.Second))),
		sdkmetric.WithResource(res),
	)
	otel.SetMeterProvider(mp)
	meter = mp.Meter("application-metrics")

	if err := createMetrics(); err != nil {
		return nil, fmt.Errorf("failed to create metrics: %w", err)
	}

	go updateSystemMetrics(context.Background())

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

func createMetrics() error {
	metricsToCreate := []struct {
		name        string
		description string
		unit        string
		metricType  string
	}{
		{"memory_alloc_bytes", "Current memory allocation in bytes", "bytes", "gauge"},
		{"memory_total_alloc_bytes", "Total memory allocation in bytes", "bytes", "gauge"},
		{"memory_sys_bytes", "System memory obtained in bytes", "bytes", "gauge"},
		{"num_goroutines", "Number of goroutines", "", "gauge"},
		{"num_cpu", "Number of CPUs", "", "gauge"},
		{"gc_runs_total", "Total number of completed GC cycles", "", "counter"},
		{"http_request_duration_seconds", "HTTP request duration in seconds", "s", "histogram"},
		{"http_requests_total", "Total number of HTTP requests", "", "counter"},
	}

	for _, m := range metricsToCreate {
		var err error
		switch m.metricType {
		case "counter":
			_, err = CreateCounter(m.name, m.description, m.unit)
		case "gauge":
			_, err = CreateGauge(m.name, m.description, m.unit)
		case "histogram":
			_, err = CreateHistogram(m.name, m.description, m.unit)
		default:
			return fmt.Errorf("unknown metric type: %s", m.metricType)
		}
		if err != nil {
			return fmt.Errorf("failed to create %s: %w", m.name, err)
		}
	}

	return nil
}

// CreateDynamicCounter creates a counter metric if it doesn't already exist
func CreateDynamicCounter(name, description, unit string) (metric.Int64Counter, error) {
	if counter, exists := counters[name]; exists {
		return counter, nil
	}
	return CreateCounter(name, description, unit)
}

func StartSpan(ctx context.Context, name string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	if tracer == nil {
		return ctx, nooptrace.Span{}
	}
	return tracer.Start(ctx, name, opts...)
}

func AddEvent(ctx context.Context, name string, attrs ...attribute.KeyValue) {
	if span := trace.SpanFromContext(ctx); span != nil {
		span.AddEvent(name, trace.WithAttributes(attrs...))
	}
}

func SetAttributes(ctx context.Context, attrs ...attribute.KeyValue) {
	if span := trace.SpanFromContext(ctx); span != nil {
		span.SetAttributes(attrs...)
	}
}

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

// sanitizePath replaces non-alphanumeric characters with underscores to create valid metric names
func sanitizePath(path string) string {
	return strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			return r
		}
		return '_'
	}, path)
}

func IncrementCounter(ctx context.Context, name string, value int64, attrs ...attribute.KeyValue) {
	counter, err := CreateDynamicCounter(name, fmt.Sprintf("Dynamic counter for %s", name), "")
	if err != nil {
		log.Printf("Warning: Failed to create counter '%s': %v", name, err)
		return
	}
	counter.Add(ctx, value, metric.WithAttributes(attrs...))
}

func RecordHistogram(ctx context.Context, name string, value float64, attrs ...attribute.KeyValue) {
	if histogram, exists := histograms[name]; exists {
		histogram.Record(ctx, value, metric.WithAttributes(attrs...))
	} else {
		log.Printf("Warning: Histogram '%s' not found", name)
	}
}

func CreateGauge(name, description, unit string) (metric.Float64UpDownCounter, error) {
	if gauge, exists := gauges[name]; exists {
		return gauge, nil
	}
	gauge, err := meter.Float64UpDownCounter(
		name,
		metric.WithDescription(description),
		metric.WithUnit(unit),
	)
	if err != nil {
		return nil, err
	}
	gauges[name] = gauge
	return gauge, nil
}

func SetGauge(ctx context.Context, name string, value float64, attrs ...attribute.KeyValue) {
	if gauge, exists := gauges[name]; exists {
		current := getGaugeValue(ctx, name)
		diff := value - current
		gauge.Add(ctx, diff, metric.WithAttributes(attrs...))
		lastKnownValues[name] = value
	}
}

func getGaugeValue(ctx context.Context, name string) float64 {
	return lastKnownValues[name]
}

func updateSystemMetrics(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	var m runtime.MemStats
	var lastNumGC uint32

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runtime.ReadMemStats(&m)

			SetGauge(ctx, "memory_alloc_bytes", float64(m.Alloc))
			SetGauge(ctx, "memory_total_alloc_bytes", float64(m.TotalAlloc))
			SetGauge(ctx, "memory_sys_bytes", float64(m.Sys))
			SetGauge(ctx, "num_goroutines", float64(runtime.NumGoroutine()))
			SetGauge(ctx, "num_cpu", float64(runtime.NumCPU()))

			gcRuns := m.NumGC - lastNumGC
			if gcRuns > 0 {
				IncrementCounter(ctx, "gc_runs_total", int64(gcRuns))
				lastNumGC = m.NumGC
			}
		}
	}
}

func TraceMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, span := StartSpan(r.Context(), "http_request")
		defer span.End()

		method := r.Method
		path := r.URL.Path

		span.SetAttributes(
			attribute.String("http.method", method),
			attribute.String("http.url", r.URL.String()),
			attribute.String("http.path", path),
			attribute.String("http.host", r.Host),
			attribute.String("http.user_agent", r.UserAgent()),
		)

		crw := &customResponseWriter{ResponseWriter: w, statusCode: http.StatusOK}

		startTime := time.Now()

		next.ServeHTTP(crw, r.WithContext(ctx))

		duration := time.Since(startTime)

		span.SetAttributes(attribute.Int("http.status_code", crw.statusCode))

		labels := []attribute.KeyValue{
			attribute.String("method", method),
			attribute.String("path", path),
			attribute.Int("status", crw.statusCode),
		}

		RecordHistogram(ctx, "http_request_duration_seconds", duration.Seconds(), labels...)

		IncrementCounter(ctx, "http_requests_total", 1, labels...)

		// Add separate counters for each method and path combination
		methodPathCounter := fmt.Sprintf("http_requests_%s_%s", strings.ToLower(method), sanitizePath(path))
		IncrementCounter(ctx, methodPathCounter, 1, attribute.Int("status", crw.statusCode))
	})
}

type customResponseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (crw *customResponseWriter) WriteHeader(statusCode int) {
	crw.statusCode = statusCode
	crw.ResponseWriter.WriteHeader(statusCode)
}
