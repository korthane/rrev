package executor

import (
	"context"
	"fmt"
	"slices"
	"sync"
)

// Response is one scripted Mock reply. Its signal is detected from Output the
// same way a real executor's is, so a phase test exercises real detection.
type Response struct {
	Output string
	Err    error
}

// Mock is a scripted executor for phase tests: it records every request it
// receives and replays a prepared response per call. It is part of the package
// rather than a test file so tests in other packages can drive a pipeline
// without any tool installed.
type Mock struct {
	// Tool is the name the mock reports; empty means "mock".
	Tool string
	// Responses are replayed in order and the last one repeats once they are
	// exhausted, so a test that only cares about convergence scripts one.
	Responses []Response
	// Handler overrides Responses when set, for a test whose reply depends on
	// the request.
	Handler func(ctx context.Context, req Request) (Result, error)

	mu    sync.Mutex
	calls []Request
}

// Name identifies the tool.
func (m *Mock) Name() string {
	if m.Tool != "" {
		return m.Tool
	}
	return "mock"
}

// Bin reports nothing to check: a mock invokes no executable.
func (m *Mock) Bin() string { return "" }

// Run records the request and replays the scripted response.
func (m *Mock) Run(ctx context.Context, req Request) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}

	m.mu.Lock()
	m.calls = append(m.calls, req)
	call := len(m.calls)
	responses := m.Responses
	m.mu.Unlock()

	if m.Handler != nil {
		return m.Handler(ctx, req)
	}
	if len(responses) == 0 {
		return Result{}, fmt.Errorf("%s: no scripted response for call %d", m.Name(), call)
	}
	response := responses[min(call, len(responses))-1]
	col := &collector{stream: req.Stream}
	col.say(response.Output)
	return col.result(), response.Err
}

// Calls returns the requests the mock received, in order.
func (m *Mock) Calls() []Request {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.calls)
}

// CallCount is how many times the mock was run.
func (m *Mock) CallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.calls)
}
