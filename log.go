// Package log provides a foundation for convenient, structured logging, centered around key-value blocks.
//
// The preferred usage pattern, for ultimate logging comfortableness, is to explicitly import this package using the "." notation, and replacing the DefaultLogger if needed.
// In case of identifier conflicts or special setups, you will likely want to at least alias the Log and Err methods of a Logger, as well as the Entry and Entries types.
//
// If you have static values that are reused throughout your code, consider preformatting them using a Formatter.
//
// Use the Node type to create log pipelines that can still be invoked through a single method call.
//
// Consider adding an unnamed Node pointer member to your central struct types. Don't forget to initialize it with NewNode.
//
//	type someType struct {
//		...
//		*Node
//	}
//
//	func (x someType) someMethod() {
//		...
//		x.Err(Warning, "oh no", someError)
//		...
//	}
//
// Need your types to self log dynamic data? Have them implement EntriesGiver and pass them to the Node construction.
//
//	x := someType{}
//	x.Node = NewNode(DefaultLogger, staticStuff, x)
//	...
//	x.Log(Emergency, "there's a handsome person in front of the screen")
package log

import (
	"fmt"
	"io"
	"os"
	"sync"
)

// predefined log levels
const (
	Default   = iota // no assigned level
	Debug            // debug or trace information
	Info             // routine information
	Notice           // normal but significant events
	Warning          // might cause problems
	Error            // likely to cause problems
	Critical         // severe problems or brief outage
	Alert            // needs immediate action
	Emergency        // one or more systems are down
)

var DefaultLogger Logger = &LineLogger{Dst: os.Stdout}

type Entries []Entry

func (x Entries) Entries() Entries {
	return x
}

type EntriesGiver interface {
	Entries() Entries
}

type Entry struct {
	Key   string
	Value any
}

func (x Entry) Entries() Entries {
	return Entries{x}
}

// ErrorLogger is a Logger extension that adds error logging convenience.
type ErrorLogger struct {
	Logger
}

func (x ErrorLogger) Err(lvl int, msg string, err error, e ...EntriesGiver) {
	LogError(x, lvl, msg, err, e...)
}

// A Formatter preprocesses EntriesGivers to a type optimized for a particular Logger.
type Formatter interface {
	Format(EntriesGiver) EntriesGiver
}

// A Logger handles data formatting and transfer to a log destination (stdout, file, remote service, etc.).
// A log consists of multiple key-value pairs (a block). Implementations should support recursive block formatting (the value of a key may be a subblock).
//
// Logging is an ubiquitous action, and often the only way errors are handled, therefore a Logger should be error resilient itself and provide best effort functionality.
// Implementations should panic in case of fatal internal errors. For less severe errors, an "OnError" method could be provided.
//
// This interface is meant to be concise and generalistic. A fair degree of optimization is achievable through the use of prefered EntriesGiver implementations.
//
// Since data is not guaranteed to be immutable, implementations should treat collection and formatting synchronously, while the backend communication can/should be performed asynchronously.
type Logger interface {
	Log(int, string, ...EntriesGiver) // should not modify mutable return values, such as Entry slices or mutable Entry values
}

// A Node facilitates logging flow by inserting predefined Entries as well as collecting from predefined EntriesGivers.
type Node struct {
	dst Logger

	src []EntriesGiver
}

// NewNode creates a new usable Node using dst as the actual Logger implementation.
//
// static should return a set of immutable Entries, or at least guaranteed to not change throughout the lifespan of the Node.
// If dst is a Formatter, static will be passed through it on creation.
// May be nil, in which case it is ignored.
//
// src is an optional list of EntriesGivers that will be drawn from for each log.
//
// New logs will contain: (possible preformated) static + src element Entries + individual log data.
func NewNode(dst Logger, static EntriesGiver, src ...EntriesGiver) *Node {
	var givers []EntriesGiver

	if static != nil {
		if f, ok := dst.(Formatter); ok {
			static = f.Format(static)
		}
		givers = append(givers, static)
	}

	givers = append(givers, src...)

	return &Node{
		dst: dst,
		src: givers,
	}
}

func (x *Node) Err(lvl int, msg string, err error, e ...EntriesGiver) {
	LogError(x, lvl, msg, err, e...)
}

func (x *Node) Log(lvl int, msg string, e ...EntriesGiver) {
	x.src = append(x.src, e...)

	x.dst.Log(lvl, msg, x.src...)

	x.src = x.src[:len(x.src)-len(e)]
}

// A LineLogger writes logs to an io.Writer using the following format:
//
//	LEVEL  msg
//	key0 - value0
//	key1 - value1
//	key2
//	  subkey0 - subvalue0
//	  subkey1 - subvalue1
//
// Its purpose is to provide human readable logs to stdout or local files.
//
// A LineLogger is concurrent safe, but is not designed for high volumes.
type LineLogger struct {
	Dst io.Writer // log destination; write errors will panic

	buf lineBuf

	mux sync.Mutex
}

func (x *LineLogger) Format(e EntriesGiver) EntriesGiver {
	return newLineEntries(e)
}

func (x *LineLogger) Log(lvl int, msg string, e ...EntriesGiver) {
	x.mux.Lock()

	x.buf.data = append(x.buf.data, LevelString(lvl)...)
	x.buf.data = append(x.buf.data, "  "...)
	x.buf.data = append(x.buf.data, msg...)
	x.buf.data = append(x.buf.data, '\n')

	for _, elem := range e {
		entries := elem.Entries()
		for _, entry := range entries {
			x.buf.print(entry)
		}
	}
	x.buf.data = append(x.buf.data, '\n')

	// formatting is done, no need to hold up the caller anymore
	go func(x *LineLogger) {
		if _, err := x.Dst.Write(x.buf.data); err != nil {
			x.mux.Unlock()
			panic(err)
		}
		x.buf.reset()

		x.mux.Unlock()
	}(x)
}

// errorBlock is an error type that may contain optional entries for logging.
// For calling efficiency, is a single Entry slice that starts with {"msg", [string]}.
// It may wrap another error, which will be appended as a final {"err", [error]} element.
type errorBlock []Entry

func (x errorBlock) Entries() []Entry {
	return x
}

func (x errorBlock) Error() string {
	return x[0].Value.(string)
}

func (x errorBlock) Unwrap() error {
	v := x[len(x)-1].Value
	if err, ok := v.(error); ok {
		return err
	}
	return nil
}

// lineBuf is the prefered formated block used by LineLogger.
type lineBuf struct {
	data []byte // preformated lines
	ends []int  // line end indices; needed when working with preformated subblocks to insert additional spacing

	space []byte // used to insert line spacing
}

func (x *lineBuf) endLine() {
	x.data = append(x.data, '\n')
	x.ends = append(x.ends, len(x.data))
}

func (x *lineBuf) print(e Entry) {
	x.data = append(x.data, x.space...)
	x.data = append(x.data, e.Key...)

	switch sub := e.Value.(type) {
	case *lineEntries:
		x.endLine()
		x.space = append(x.space, "  "...)

		// copy preformated string, inserting appropriate spacing
		start := 0
		for _, end := range sub.buf.ends {
			x.data = append(x.data, x.space...)
			x.data = append(x.data, sub.buf.data[start:end]...)
			x.ends = append(x.ends, len(x.data))
			start = end
		}

		x.space = x.space[:len(x.space)-2]

	case EntriesGiver:
		x.endLine()
		x.space = append(x.space, "  "...)

		// print each subblock entry recursively
		for _, entry := range sub.Entries() {
			x.print(entry)
		}

		x.space = x.space[:len(x.space)-2]

	default:
		// use default value formatting
		x.data = append(x.data, " - "...)
		x.data = fmt.Append(x.data, e.Value)
		x.endLine()
	}
}

// reset clears all data while retaining allocated memory, making reuse more efficient than creating a new value
func (x *lineBuf) reset() {
	x.data = x.data[:0]
	x.ends = x.ends[:0]
	x.space = x.space[:0]
}

// lineEntries is the optimized preformated Entries for LinePrinter.
type lineEntries struct {
	src []Entry

	buf lineBuf
}

func newLineEntries(src EntriesGiver) lineEntries {
	if same, ok := src.(lineEntries); ok {
		return same
	}

	buf := lineBuf{}
	entries := src.Entries()
	for _, entry := range entries {
		buf.print(entry)
	}

	return lineEntries{
		src: entries,
		buf: buf,
	}
}

func (x lineEntries) Entries() Entries {
	return x.src
}

// Err logs an error value using the DefaultLogger.
func Err(lvl int, msg string, err error, e ...EntriesGiver) {
	LogError(DefaultLogger, lvl, msg, err, e...)
}

// Predefined level string forms (the constant identifier in all uppercase)
func LevelString(lvl int) string {
	switch lvl {
	case Default:
		return "DEFAULT"
	case Debug:
		return "DEBUG"
	case Info:
		return "INFO"
	case Notice:
		return "NOTICE"
	case Warning:
		return "WARNING"
	case Error:
		return "ERROR"
	case Critical:
		return "CRITICAL"
	case Alert:
		return "ALERT"
	case Emergency:
		return "EMERGENCY"
	}
	return ""
}

// Log calls the DefaultLogger.
func Log(lvl int, msg string, e ...EntriesGiver) {
	DefaultLogger.Log(lvl, msg, e...)
}

// LogError is a convenience function to handle errors of arbitrary type.
// Typically used to create "Err" methods.
func LogError(x Logger, lvl int, msg string, err error, e ...EntriesGiver) {
	e = append(e, Entry{"err", err})
	x.Log(lvl, msg, e...)
}

// MakeError creates a new error value that implements Entries and may contain additional logging information.
// If err is non-nil, the new error will wrap it.
func MakeError(msg string, err error, e ...EntriesGiver) error {
	o := errorBlock{Entry{"msg", msg}}

	for _, elem := range e {
		o = append(o, elem.Entries()...)
	}

	if err != nil {
		o = append(o, Entry{"err", err})
	}

	return o
}
