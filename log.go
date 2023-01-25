// Package log provides a foundation for convenient, structured logging, centered around key-value blocks.
//
// The preferred usage pattern, for ultimate logging comfortableness, is to explicitly import this package using the "." notation, and replacing the DefaultLogger if needed.
// In case of identifier conflicts or special setups, you will likely want to at least alias the Log and Err methods of a Logger, as well as the Entry and Entries types.
//
// If you have static values that are reused throughout your code, consider preformatting them using a Formatter.
//
// Use the Node type to create log pipelines that can still be invoked through a single method call.
//
// Consider adding an unnamed Node pointer member to your central struct types. Don't forget to initialize it with MakeNode.
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

var DefaultLogger Logger = MakeLineLogger(os.Stdout)

// Loggers should generally have some degree of asynchronous operation, so they should also provide dedicated Close methods to ensure all work is finished before exiting.
type Closer = io.Closer

// Data can be used to transfer Log calls between goroutines. Exported for potential use by Logger implementations.
type Data struct {
	Level   int
	Message string
	Entries []Entries
}

type Entries []Entry

func (x Entries) Entries() Entries {
	return x
}

// An EntriesGiver hands over Key-Value pairs in significant order for logging.
// In order to avoid race conditions with asynchronous logging processes, implementations should ensure that returned Entry.Values are immutable or at least stable.
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
// Implementations should treat Entry collection synchronously, while formatting and backend transmission can/should be performed asynchronously.
// Conversely, EntriesGivers should return values that are immutable or stable.
type Logger interface {
	Log(int, string, ...EntriesGiver) // should not modify mutable return values, such as Entry slices or mutable Entry values
}

// A Node facilitates logging flow by inserting predefined Entries as well as collecting from predefined EntriesGivers.
type Node struct {
	dst Logger

	src []EntriesGiver
}

// MakeNode creates a new usable Node using dst as the actual Logger implementation.
//
// static should return a set of immutable Entries, or at least guaranteed to not change throughout the lifespan of the Node.
// If dst is a Formatter, static will be passed through it on creation.
// May be nil, in which case it is ignored.
//
// src is an optional list of EntriesGivers that will be drawn from for each log.
//
// New logs will contain: (possible preformated) static + src element Entries + particular log data.
func MakeNode(dst Logger, static EntriesGiver, src ...EntriesGiver) Node {
	var givers []EntriesGiver

	if static != nil {
		if f, ok := dst.(Formatter); ok {
			static = f.Format(static)
		}
		givers = append(givers, static)
	}

	givers = append(givers, src...)

	return Node{
		dst: dst,
		src: givers,
	}
}

func (x Node) Err(lvl int, msg string, err error, e ...EntriesGiver) {
	LogError(x, lvl, msg, err, e...)
}

func (x Node) Log(lvl int, msg string, e ...EntriesGiver) {
	givers := make([]EntriesGiver, len(x.src)+len(e))
	copy(givers, x.src)
	copy(givers[len(x.src):], e)

	x.dst.Log(lvl, msg, givers...)
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
// A LineLogger is concurrent safe, and should be capable of handling pretty high volumes.
//
// Values must be created using MakeLineLogger and Closed when no longer needed.
type LineLogger struct {
	dst io.Writer // log destination; write errors will panic

	dataChan  chan Data        // transfer Data from callers to dedicated goroutine
	writeChan chan chan []byte // queue raw formatted data to dedicated write goroutine

	done chan struct{} // closed when the write loop exits
}

func MakeLineLogger(dst io.Writer) LineLogger {
	x := LineLogger{
		dst:       dst,
		dataChan:  make(chan Data, 8),
		writeChan: make(chan chan []byte, 8),
		done:      make(chan struct{}),
	}

	go x.run()
	go x.write()

	return x
}

// Close waits for all scheduled logs to be written, then closes the underlying Writer if it is also a Closer.
// As soon as Close is called, any further Log calls will panic.
func (x LineLogger) Close() error {
	close(x.dataChan)
	<-x.done

	var err error
	if c, ok := x.dst.(Closer); ok {
		err = c.Close()
	}

	return err
}

func (x LineLogger) Format(e EntriesGiver) EntriesGiver {
	return makeLineEntries(e)
}

func (x LineLogger) Log(lvl int, msg string, e ...EntriesGiver) {
	// gather entries synchronously
	s := make([]Entries, len(e))
	for i := range e {
		s[i] = e[i].Entries()
	}

	x.dataChan <- Data{
		Level:   lvl,
		Message: msg,
		Entries: s,
	}
}

// asynchronous part
func (x LineLogger) log(data Data, ch chan []byte) {
	buf := newLineBuffer()

	buf.data = append(buf.data, LevelString(data.Level)...)
	buf.data = append(buf.data, "  "...)
	buf.data = append(buf.data, data.Message...)
	buf.data = append(buf.data, '\n')

	for _, elem := range data.Entries {
		buf.append(elem)
	}
	buf.data = append(buf.data, '\n')

	ch <- buf.data // pass over to writing
}

// run pulls data from Log calls to process it asynchronously and unblock callers ASAP
func (x LineLogger) run() {
	for data := range x.dataChan {
		ch := make(chan []byte) // transfer formatted log to the write goroutine
		go x.log(data, ch)      // perform formatting asynchronously in order to pull data from callers ASAP
		x.writeChan <- ch       // inform write goroutine of the next log in line
	}
	close(x.writeChan)
}

// write loop that ensures logs are written in the order they arrive
// also protects the underlying writer from concurrent calls
func (x LineLogger) write() {
	for ch := range x.writeChan {
		if _, err := x.dst.Write(<-ch); err != nil {
			panic(err)
		}
	}
	close(x.done)
}

// errorBlock is an error type that may contain optional entries for logging.
// For calling efficiency, is a single Entry slice that starts with {"msg", [string]}.
// It may wrap another error, which will be appended as a final {"err", [error]} element.
type errorBlock []Entry

func (x errorBlock) Entries() Entries {
	return Entries(x)
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

// lineBuffer is the prefered formated block used by LineLogger.
type lineBuffer struct {
	data []byte // preftormated lines
	ends []int  // line end indices; needed when working with preformated subblocks to insert additional spacing

	space []byte // used to insert line spacing
}

// newLineBuffer allocates a lineBuffer with sufficient space for most uses
func newLineBuffer() *lineBuffer {
	return &lineBuffer{
		data:  make([]byte, 0, 1024),
		ends:  make([]int, 0, 16),
		space: []byte("        ")[:0],
	}
}

func (x *lineBuffer) append(e EntriesGiver) {
	if pre, ok := e.(lineEntries); ok {
		// copy preformatted string, inserting appropriate spacing
		start := 0
		for _, end := range pre.buf.ends {
			x.data = append(x.data, x.space...)
			x.data = append(x.data, pre.buf.data[start:end]...)
			x.ends = append(x.ends, len(x.data))
			start = end
		}
		return
	}

	for _, entry := range e.Entries() {
		x.appendEntry(entry)
	}
}

func (x *lineBuffer) appendEntry(e Entry) {
	x.data = append(x.data, x.space...)
	x.data = append(x.data, e.Key...)

	switch sub := e.Value.(type) {
	case EntriesGiver:
		x.endLine()
		x.space = append(x.space, "  "...)
		x.append(sub)
		x.space = x.space[:len(x.space)-2]

	default:
		// use default value formatting
		x.data = append(x.data, " - "...)
		x.data = fmt.Append(x.data, e.Value)
		x.endLine()
	}
}

func (x *lineBuffer) endLine() {
	x.data = append(x.data, '\n')
	x.ends = append(x.ends, len(x.data))
}

// reset clears all data while retaining allocated memory, making reuse more efficient than creating a new value
func (x *lineBuffer) reset() {
	x.data = x.data[:0]
	x.ends = x.ends[:0]
	x.space = x.space[:0]
}

// lineEntries is the optimized preformated Entries for LinePrinter.
type lineEntries struct {
	src []Entry

	buf lineBuffer
}

func makeLineEntries(src EntriesGiver) lineEntries {
	if same, ok := src.(lineEntries); ok {
		return same
	}

	buf := lineBuffer{}
	entries := src.Entries()
	for _, entry := range entries {
		buf.appendEntry(entry)
	}

	return lineEntries{
		src: entries,
		buf: buf,
	}
}

func (x lineEntries) Entries() Entries {
	return x.src
}

// Close closes the DefaultLogger if it is a Closer.
func Close() error {
	var err error
	if c, ok := DefaultLogger.(Closer); ok {
		err = c.Close()
	}
	return err
}

// Err logs an error value using the DefaultLogger.
func Err(lvl int, msg string, err error, e ...EntriesGiver) {
	LogError(DefaultLogger, lvl, msg, err, e...)
}

// Format preformats the input if the DefaultLogger is a Formatter.
// Otherwise returns the input unchanged.
func Format(e EntriesGiver) EntriesGiver {
	if f, ok := DefaultLogger.(Formatter); ok {
		return f.Format(e)
	}
	return e
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
