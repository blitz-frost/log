// Package log provides a foundation for convenient, structured logging, centered around key-value blocks.
//
// TODO update comments
// The preferred usage pattern, for ultimate logging comfortableness, is to explicitly import this package using the "." notation, and replacing the DefaultLogger if needed.
// In case of identifier conflicts or special setups, you will likely want to at least alias the Log and Err methods of a Logger, as well as the Entry and Entries types.
//
// If you have static values that are reused throughout your code, consider preformatting them.
//
// Use the Node type to create log pipelines that can be invoked through a single call.
//
// Need your types to self log dynamic data? Have them implement EntriesGiver.
//
//	x := someType{}
//	...
//	Log(recorder, Emergency, "there's a handsome person in front of the screen", x)
package log

import (
	"github.com/blitz-frost/errors"
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

type Async chan Data

func AsyncMake(capacity int) Async {
	return make(Async, capacity)
}

func (x Async) Close() {
	close(x)
}

func (x Async) Record(data Data) error {
	x <- data
	return nil
}

func (x Async) Run(dst Recorder) error {
	var err error
	for data := range x {
		if err = dst(data); err != nil {
			break
		}
	}
	return err
}

// Data is the general form of a log, that can be passed around between functions.
type Data struct {
	Level   int
	Message string
	Entries []Entries
}

func DataOf(lvl int, msg string, e ...EntriesGiver) Data {
	s := make([]Entries, len(e))
	for i := range e {
		s[i] = e[i].Entries()
	}

	return Data{
		Level:   lvl,
		Message: msg,
		Entries: s,
	}
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

func Err(err error) EntriesGiver {
	return errorEntries{err}
}

type Entry struct {
	Key   string
	Value any
}

func (x Entry) Entries() Entries {
	return Entries{x}
}

// Recorder exists simply to make signatures and documentation a bit more intuitive to read.
type Recorder func(Data) error

// Fallback wraps a recorder that can fail.
//
// On error, invokes the given handler. If this returns a non-nil function, it will be used to retry the failed recording, as well as continue to be used for future calls. Otherwise returns the encountered error.
func Fallback(dst Recorder, handler func(error) Recorder) Recorder {
	return func(data Data) error {
	do:
		err := dst(data)
		if err != nil {
			if replace := handler(err); replace != nil {
				dst = replace
				goto do
			}
		}

		return err
	}
}

// FallbackHandleOnce returns a handler usable by [Fallback].
//
// The first time a produced handler is called, it will attempt to log the received error using the specified fallback Recorder, as well as returning it. This error log will have the specified level and message.
// If the error log fails, the handler panics.
// Any subsequent calls will noop and return nil.
func FallbackHandleOnce(errLvl int, errMsg string, fallback Recorder) func(error) Recorder {
	var triggered bool
	return func(err error) Recorder {
		if triggered {
			return nil
		}

		data := DataOf(errLvl, errMsg, Err(err))
		if err2 := fallback(data); err2 != nil {
			e := errors.Message("log fallback", err)
			e.Link(err2)
			panic(e)
		}

		return fallback
	}
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

// Print is the central logging function, that helps create Data to be handed over to a Recorder.
// It is meant to be wrapped by project-wide or local variants, that use a specific default Recorder, or automatically discard certain log levels.
// Errors will automatically panic.
//
// NOTE This is a function that takes a Recorder as first parameter, instead of being a Recorder method, because it would be annoying to always cast regular static functions to a specific type.
// Also, this highlights the intended usage as a global entrypoint.
// Of course, in practice nothing is preventing anyone from completely ignoring this function and using custom variants, or just using Recorders directly.
func Print(recorder Recorder, lvl int, msg string, e ...EntriesGiver) {
	data := DataOf(lvl, msg, e...)
	if err := recorder(data); err != nil {
		panic(err)
	}
}

// Node creates a Recorder that facilitates logging flow by appending Entries collected from predefined EntriesGivers.
func Node(dst Recorder, e ...EntriesGiver) Recorder {
	return func(data Data) error {
		for _, giver := range e {
			data.Entries = append(data.Entries, giver.Entries())
		}

		return dst(data)
	}
}
