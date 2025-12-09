package internal

import (
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/zap"
)

var (
	// llmActiveRequests tracks the number of currently processing requests
	llmActiveRequests = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "llm_active_requests",
		Help: "Number of currently processing LLM requests",
	})

	// llmRequestTotal tracks the total number of requests with labels
	llmRequestTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "llm_request_total",
		Help: "Total number of LLM requests",
	}, []string{"status", "stream_type"})

	// llmFlushErrors tracks the number of flush errors
	llmFlushErrors = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "llm_flush_errors",
		Help: "Number of flush errors when sending data to clients",
	})

	// activeRequests tracks active requests with their start time for graceful shutdown
	activeRequests = &requestTracker{
		requests: make(map[string]time.Time),
	}
)

// requestTracker tracks active requests for graceful shutdown
type requestTracker struct {
	mu        sync.RWMutex
	requests  map[string]time.Time // requestID -> start time
	shuttingDown bool
}

// InitMetrics initializes and registers Prometheus metrics
func InitMetrics() {
	prometheus.MustRegister(llmActiveRequests)
	prometheus.MustRegister(llmRequestTotal)
	prometheus.MustRegister(llmFlushErrors)
}

// IncrementActiveRequests increments the active requests gauge
func IncrementActiveRequests() {
	llmActiveRequests.Inc()
}

// DecrementActiveRequests decrements the active requests gauge
func DecrementActiveRequests() {
	llmActiveRequests.Dec()
}

// IncrementRequestTotal increments the total requests counter
func IncrementRequestTotal(status, streamType string) {
	llmRequestTotal.WithLabelValues(status, streamType).Inc()
}

// IncrementFlushErrors increments the flush errors counter
func IncrementFlushErrors() {
	llmFlushErrors.Inc()
}

// AddRequest adds a request to the tracker
func AddRequest(requestID string) {
	activeRequests.mu.Lock()
	defer activeRequests.mu.Unlock()
	activeRequests.requests[requestID] = time.Now()
}

// RemoveRequest removes a request from the tracker
func RemoveRequest(requestID string) {
	activeRequests.mu.Lock()
	defer activeRequests.mu.Unlock()
	delete(activeRequests.requests, requestID)
}

// SetShuttingDown marks that the server is shutting down
func SetShuttingDown() {
	activeRequests.mu.Lock()
	defer activeRequests.mu.Unlock()
	activeRequests.shuttingDown = true
}

// IsShuttingDown returns true if the server is shutting down
func IsShuttingDown() bool {
	activeRequests.mu.RLock()
	defer activeRequests.mu.RUnlock()
	return activeRequests.shuttingDown
}

// GetActiveRequestsCount returns the number of active requests
func GetActiveRequestsCount() int {
	activeRequests.mu.RLock()
	defer activeRequests.mu.RUnlock()
	return len(activeRequests.requests)
}

// WaitForAllRequests waits for all active requests to complete or timeout
func WaitForAllRequests(timeout time.Duration) {
	start := time.Now()
	for {
		count := GetActiveRequestsCount()
		if count == 0 {
			Logger.Info("All requests completed")
			break
		}

		if time.Since(start) > timeout {
			Logger.Warn("Timeout waiting for requests to complete",
				zap.Int("remaining", count))
			break
		}

		Logger.Info("Waiting for requests to complete",
			zap.Int("active", count),
			zap.Duration("elapsed", time.Since(start)))

		time.Sleep(100 * time.Millisecond)
	}
}