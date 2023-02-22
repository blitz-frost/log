package rpc

import (
	"github.com/blitz-frost/log"
	"github.com/blitz-frost/log/logger"
	"github.com/blitz-frost/rpc"
)

var ProcedureName = "Log"

type Logger struct {
	logger.T[logger.Data]
}

// BindTo binds a logging procedure to an rpc.Client, and returns a Logger that wraps this procedure.
//
// The underlying rpc system must be capable of handling interface types in general, as well as recognizing at least logger.Entries when used as interface values in particular.
//
// onClose may be nil.
//
// The used name can be controlled through the ProcedureName global variable.
func BindTo(cli rpc.Client, onClose func()) (Logger, error) {
	var f func(logger.Data) error
	if err := cli.Bind(ProcedureName, &f); err != nil {
		return Logger{}, err
	}

	return Logger{logger.Make[logger.Data](core{
		f:       f,
		onClose: onClose,
	})}, nil
}

// RegisterWith registers a logging procedure to an rpc.Library. The procesure will use dst as the actual server-side Logger implementation.
//
// The underlying rpc system must be capable of handling interface types in general, as well as recognizing at least logger.Entries when used as interface values in particular.
//
// The used name can be controlled through the ProcedureName global variable.
func RegisterWith(lib rpc.Library, dst log.Logger) error {
	f := func(data logger.Data) error {
		var e logger.Entries
		for _, s := range data.Entries {
			e = append(e, s...)
		}
		// being able to pass whatever you want individually, but not as part of a slice of elements that satisfy the required interface, is such a nice language feature innit?
		dst.Log(data.Level, data.Message, e)
		return nil
	}
	return lib.Register(ProcedureName, f)
}

type core struct {
	f       func(logger.Data) error
	onClose func()
}

func (x core) Close() {
	if x.onClose != nil {
		x.onClose()
	}
}

// Format replaces error Entry.Values with their error string, otherwise it might not really mean much to the receiver if concrete type information is lost.
// Also ensures there are only Entries instead of EntriesGivers.
func (x core) Format(data logger.Data) logger.Data {
	for _, e := range data.Entries {
		format(e)
	}
	return data
}

func (x core) Write(data logger.Data) {
	if err := x.f(data); err != nil {
		panic(err)
	}
}

func format(e log.Entries) {
	for i := range e {
		v := e[i].Value
		switch val := v.(type) {
		case log.Entries:
			// recursive format

			format(val)
		case log.EntriesGiver:
			// replace interface with slice + recursive format

			entries := val.Entries()
			format(entries)
			e[i].Value = entries

		case error:
			// replace interface with string
			// note that log.errorBlock will satisfy the EntriesGiver branch

			e[i].Value = val.Error()
		}
	}
}
