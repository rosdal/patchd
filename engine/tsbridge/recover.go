package tsbridge

import (
	"fmt"
	"log/slog"
	"runtime/debug"
)

// PanicError is a panic inside the compiler, recovered at the tsbridge
// boundary (TSB007). Stack holds the goroutine's stack at the panic.
type PanicError struct {
	Op    string
	Value any
	Stack []byte
}

func (e *PanicError) Error() string {
	return fmt.Sprintf("tsbridge: %s: compiler panic: %v", e.Op, e.Value)
}

// recoverPanic converts a panic into a *PanicError in *err. Every exported
// function that calls into the compiler defers it (or recoverQuery).
func recoverPanic(err *error, op string) {
	if r := recover(); r != nil {
		pe := &PanicError{Op: op, Value: r, Stack: debug.Stack()}
		slog.Debug("tsbridge: recovered compiler panic", "op", op, "panic", r, "stack", string(pe.Stack))
		*err = pe
	}
}

// recoverQuery recovers a panic in a query method, records it on the program
// and leaves the method's zero result in place.
func (p *Program) recoverQuery(op string) {
	if r := recover(); r != nil {
		p.recordPanic(op, r)
	}
}

// recordPanic logs a recovered panic and records the first few as TSB007.
// Callers hold p.mu.
func (p *Program) recordPanic(op string, r any) {
	slog.Debug("tsbridge: recovered compiler panic", "op", op, "panic", r, "stack", string(debug.Stack()))
	p.panics++
	if p.panics <= 10 {
		p.warnings = append(p.warnings, Warning{Code: WarnCompilerPanic, Message: fmt.Sprintf("%s: compiler panic: %v", op, r)})
	}
}
