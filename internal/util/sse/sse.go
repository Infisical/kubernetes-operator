package sse

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Event represents a Server-Sent Event
type Event struct {
	ID    string
	Event string
	Data  string
	Retry int
}

// SubscriptionParams holds parameters for SSE subscription to detect changes
type SubscriptionParams struct {
	ProjectID   string
	EnvSlug     string
	SecretsPath string
}

// ErrPlanNotSupported indicates SSE is not available on the current plan
var ErrPlanNotSupported = fmt.Errorf("event subscriptions are not available on your current plan")

// isPermanentError checks if an error should not be retried
func isPermanentError(err error) bool {
	if err == nil {
		return false
	}
	errMsg := strings.ToLower(err.Error())
	// Plan-related errors should not be retried
	if strings.Contains(errMsg, "not available on your current plan") ||
		strings.Contains(errMsg, "plan") && strings.Contains(errMsg, "not available") {
		return true
	}
	return false
}

// Equals compares two SubscriptionParams for equality
func (p SubscriptionParams) Equals(other SubscriptionParams) bool {
	return p.ProjectID == other.ProjectID &&
		p.EnvSlug == other.EnvSlug &&
		p.SecretsPath == other.SecretsPath
}

// ConnectionMeta holds metadata about an SSE connection
type ConnectionMeta struct {
	params     SubscriptionParams
	ctx        context.Context    // Connection-specific context
	cancel     context.CancelFunc // Cancels this connection only
	body       io.ReadCloser      // HTTP response body - must close to unblock scanner
	lastPingAt atomic.Value       // stores time.Time
}

// LastPing returns the last ping time
func (c *ConnectionMeta) LastPing() time.Time {
	if t, ok := c.lastPingAt.Load().(time.Time); ok {
		return t
	}
	return time.Time{}
}

// UpdateLastPing atomically updates the last ping time
func (c *ConnectionMeta) UpdateLastPing() {
	c.lastPingAt.Store(time.Now())
}

// Cancel terminates the connection
func (c *ConnectionMeta) Cancel() {
	if c.cancel != nil {
		c.cancel()
	}
}

// ReconnectConfig holds configuration for reconnection with backoff
type ReconnectConfig struct {
	MaxRetries     int
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
	BackoffFactor  float64
}

// DefaultReconnectConfig returns the default reconnection configuration
func DefaultReconnectConfig() ReconnectConfig {
	return ReconnectConfig{
		MaxRetries:     5,
		InitialBackoff: time.Second,
		MaxBackoff:     30 * time.Second,
		BackoffFactor:  2.0,
	}
}

// ConnectionRegistry manages SSE connections with callbacks
type ConnectionRegistry struct {
	mu   sync.RWMutex
	conn *ConnectionMeta

	// Lifecycle management
	ctx    context.Context    // Registry-level context for all goroutines
	cancel context.CancelFunc // Cancels all goroutines on Close()
	wg     sync.WaitGroup     // Tracks all goroutines (monitor, processStream, reconnect)

	// Reconnection with backoff
	reconnectConfig ReconnectConfig
	requestFn       func() (*http.Response, error) // Stored for reconnection

	// Callbacks instead of channels
	onEvent     func(Event)
	onError     func(error)
	onReconnect func() // Called after max retries exhausted
}

// NewConnectionRegistry creates a new connection registry with callbacks
func NewConnectionRegistry(onEvent func(Event), onError func(error), onReconnect func()) *ConnectionRegistry {
	ctx, cancel := context.WithCancel(context.Background())
	r := &ConnectionRegistry{
		ctx:             ctx,
		cancel:          cancel,
		reconnectConfig: DefaultReconnectConfig(),
		onEvent:         onEvent,
		onError:         onError,
		onReconnect:     onReconnect,
	}

	// Start health monitor - runs for lifetime of registry
	r.wg.Add(1)
	go r.monitorHealth()

	return r
}

// SubscribeWithParams subscribes to SSE events with parameter tracking
// If params differ from existing connection, the old connection is closed first
func (r *ConnectionRegistry) SubscribeWithParams(params SubscriptionParams, requestFn func() (*http.Response, error)) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Check if we already have a connection with the same params
	if r.conn != nil {
		if r.conn.params.Equals(params) {
			// Same params, check if connection is still valid
			select {
			case <-r.conn.ctx.Done():
				// Connection is dead, fall through to create new one
			default:
				// Connection is alive with same params, nothing to do
				return nil
			}
		}
		// Different params or dead connection, close it
		r.closeConnectionLocked()
	}

	// Store requestFn for reconnection
	r.requestFn = requestFn

	// Create the new connection
	return r.createConnectionLocked(params, requestFn)
}

// createConnectionLocked creates a new SSE connection (must be called with lock held)
func (r *ConnectionRegistry) createConnectionLocked(params SubscriptionParams, requestFn func() (*http.Response, error)) error {
	// Check if registry is closing
	select {
	case <-r.ctx.Done():
		return fmt.Errorf("registry is closed")
	default:
	}

	res, err := requestFn()
	if err != nil {
		return fmt.Errorf("failed to establish SSE connection: %w", err)
	}

	// Create connection-specific context that's also cancelled when registry closes
	connCtx, connCancel := context.WithCancel(r.ctx)

	meta := &ConnectionMeta{
		params: params,
		ctx:    connCtx,
		cancel: connCancel,
		body:   res.Body, // Store body so we can close it to unblock scanner
	}
	meta.UpdateLastPing()

	r.conn = meta

	// Start the stream processor goroutine - tracked by WaitGroup
	r.wg.Add(1)
	go r.processStream(connCtx, meta)

	return nil
}

// closeConnectionLocked closes the current connection (must be called with lock held)
func (r *ConnectionRegistry) closeConnectionLocked() {
	if r.conn != nil {
		// Close body first to unblock any pending scanner.Scan()
		if r.conn.body != nil {
			r.conn.body.Close()
		}
		// Then cancel context
		r.conn.Cancel()
		r.conn = nil
	}
}

// Get retrieves the current connection
func (r *ConnectionRegistry) Get() (*ConnectionMeta, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.conn, r.conn != nil
}

// IsConnected checks if there's an active connection
func (r *ConnectionRegistry) IsConnected() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if r.conn == nil {
		return false
	}

	// Check if connection context is still valid
	select {
	case <-r.conn.ctx.Done():
		return false
	default:
		return true
	}
}

// GetParams returns the current subscription params if connected
func (r *ConnectionRegistry) GetParams() (SubscriptionParams, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if r.conn == nil {
		return SubscriptionParams{}, false
	}
	return r.conn.params, true
}

// Close gracefully shuts down the registry
func (r *ConnectionRegistry) Close() {
	// Close connection first (this closes body and unblocks processStream)
	r.mu.Lock()
	r.closeConnectionLocked()
	r.mu.Unlock()

	// Cancel registry context - signals all goroutines to exit
	r.cancel()

	// Wait for all goroutines with timeout
	done := make(chan struct{})
	go func() {
		r.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// All goroutines finished cleanly
	case <-time.After(5 * time.Second):
		// Timeout, force continue
	}
}

// processStream reads and parses SSE events from the response body
// This goroutine is tied to the connection context, not the reconciler context
func (r *ConnectionRegistry) processStream(ctx context.Context, meta *ConnectionMeta) {
	defer r.wg.Done()
	defer func() {
		if rec := recover(); rec != nil {
			r.safeCallOnError(fmt.Errorf("panic in processStream: %v", rec))
		}
	}()

	scanner := bufio.NewScanner(meta.body)

	var currentEvent Event
	var dataBuilder strings.Builder

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return
		default:
		}

		line := scanner.Text()

		// Empty line indicates end of event
		if len(line) == 0 {
			if currentEvent.Data != "" || currentEvent.Event != "" {
				// Finalize data
				if dataBuilder.Len() > 0 {
					currentEvent.Data = dataBuilder.String()
					dataBuilder.Reset()
				}

				// Handle ping events
				if r.isPingEvent(currentEvent) {
					meta.UpdateLastPing()
				} else {
					// Send non-ping events via callback
					r.safeCallOnEvent(currentEvent)
				}

				// Reset for next event
				currentEvent = Event{}
			}
			continue
		}

		// Parse line
		r.parseLine(line, &currentEvent, &dataBuilder)
	}

	if err := scanner.Err(); err != nil {
		select {
		case <-ctx.Done():
			// Context cancelled, expected shutdown
			return
		default:
			// Ignore "body closed" errors - expected during intentional shutdown
			errMsg := strings.ToLower(err.Error())
			if strings.Contains(errMsg, "closed") || strings.Contains(errMsg, "eof") {
				return
			}
			r.safeCallOnError(err)
			// Only reconnect for transient errors
			if !isPermanentError(err) {
				r.triggerReconnect()
			}
		}
	}
}

// triggerReconnect initiates the reconnection process
func (r *ConnectionRegistry) triggerReconnect() {
	// Check if registry is closing
	select {
	case <-r.ctx.Done():
		return
	default:
	}

	r.mu.RLock()
	params := SubscriptionParams{}
	requestFn := r.requestFn
	if r.conn != nil {
		params = r.conn.params
	}
	r.mu.RUnlock()

	if requestFn == nil {
		return
	}

	r.wg.Add(1)
	go r.reconnectLoop(params, requestFn)
}

// reconnectLoop attempts to reconnect with exponential backoff
func (r *ConnectionRegistry) reconnectLoop(params SubscriptionParams, requestFn func() (*http.Response, error)) {
	defer r.wg.Done()

	backoff := r.reconnectConfig.InitialBackoff

	for attempt := range r.reconnectConfig.MaxRetries {
		// Wait with backoff, checking for cancellation
		select {
		case <-r.ctx.Done():
			return // Registry is closing
		case <-time.After(backoff):
		}

		// Attempt reconnection
		r.mu.Lock()
		err := r.createConnectionLocked(params, requestFn)
		r.mu.Unlock()

		if err == nil {
			return // Success
		}

		// Don't retry permanent errors (e.g., plan not supported)
		if isPermanentError(err) {
			r.safeCallOnError(fmt.Errorf("permanent error, stopping reconnection: %w", err))
			return
		}

		r.safeCallOnError(fmt.Errorf("reconnect attempt %d failed: %w", attempt+1, err))

		backoff = time.Duration(float64(backoff) * r.reconnectConfig.BackoffFactor)
		backoff = min(backoff, r.reconnectConfig.MaxBackoff)
	}

	// Max retries exceeded - trigger reconciliation as fallback
	r.safeCallOnReconnect()
}

// safeCallOnEvent calls onEvent with panic recovery
func (r *ConnectionRegistry) safeCallOnEvent(event Event) {
	if r.onEvent == nil {
		return
	}
	defer func() {
		if rec := recover(); rec != nil {
			// Log but don't crash
		}
	}()
	r.onEvent(event)
}

// safeCallOnError calls onError with panic recovery
func (r *ConnectionRegistry) safeCallOnError(err error) {
	if r.onError == nil {
		return
	}
	defer func() {
		if rec := recover(); rec != nil {
			// Log but don't crash
		}
	}()
	r.onError(err)
}

// safeCallOnReconnect calls onReconnect with panic recovery
func (r *ConnectionRegistry) safeCallOnReconnect() {
	if r.onReconnect == nil {
		return
	}
	defer func() {
		if rec := recover(); rec != nil {
			// Log but don't crash
		}
	}()
	r.onReconnect()
}

// parseLine efficiently parses SSE protocol lines
func (r *ConnectionRegistry) parseLine(line string, event *Event, dataBuilder *strings.Builder) {
	colonIndex := strings.IndexByte(line, ':')
	if colonIndex == -1 {
		return // Invalid line format
	}

	field := line[:colonIndex]
	value := line[colonIndex+1:]

	// Trim leading space from value (SSE spec)
	if len(value) > 0 && value[0] == ' ' {
		value = value[1:]
	}

	switch field {
	case "data":
		if dataBuilder.Len() > 0 {
			dataBuilder.WriteByte('\n')
		}
		dataBuilder.WriteString(value)
	case "event":
		event.Event = value
	case "id":
		event.ID = value
	case "retry":
		// Parse retry value if needed
		// This could be used to configure reconnection delay
	case "":
		// Comment line, ignore
	}
}

// isPingEvent checks if an event is a ping/keepalive
func (r *ConnectionRegistry) isPingEvent(event Event) bool {
	// Check for common ping patterns
	if event.Event == "ping" {
		return true
	}

	// Check for heartbeat data (common pattern is "1" or similar)
	if event.Event == "" && strings.TrimSpace(event.Data) == "1" {
		return true
	}

	return false
}

// monitorHealth checks connection health periodically
func (r *ConnectionRegistry) monitorHealth() {
	defer r.wg.Done()

	const (
		checkInterval = 30 * time.Second
		pingTimeout   = 2 * time.Minute
	)

	ticker := time.NewTicker(checkInterval)
	defer ticker.Stop()

	for {
		select {
		case <-r.ctx.Done():
			return
		case <-ticker.C:
			r.checkConnectionHealth(pingTimeout)
		}
	}
}

// checkConnectionHealth verifies connection is still alive
func (r *ConnectionRegistry) checkConnectionHealth(timeout time.Duration) {
	// Check if registry is closing
	select {
	case <-r.ctx.Done():
		return
	default:
	}

	r.mu.RLock()
	conn := r.conn
	requestFn := r.requestFn
	r.mu.RUnlock()

	if conn == nil {
		return
	}

	if time.Since(conn.LastPing()) > timeout {
		// Connection is stale
		r.mu.Lock()
		// Verify it's still the same connection
		if r.conn == conn {
			params := conn.params
			r.closeConnectionLocked()
			r.mu.Unlock()

			// Trigger reconnection (uses wg.Add internally)
			if requestFn != nil {
				r.wg.Add(1)
				go r.reconnectLoop(params, requestFn)
			}
		} else {
			r.mu.Unlock()
		}
	}
}
