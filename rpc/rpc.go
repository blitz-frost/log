package rpc

import (
	"github.com/blitz-frost/log"
	"github.com/blitz-frost/log/logger"
	"github.com/blitz-frost/rpc"
)

type Logger struct {
	logger.T[logger.Data]
}

// BindTo binds a logging procedure to an rpc.Client, and returns a Logger that wraps this procedure.
//
// The underlying rpc system must be capable of handling interface types in general, as well as recognizing at least logger.Entries when used as interface values in particular.
//
// An empty name will default to "Log". onClose may be nil.
func BindTo(cli rpc.Client, name string, onClose func()) (Logger, error) {
	if name == "" {
		name = "Log"
	}

	var f func(logger.Data) error
	if err := cli.Bind(name, &f); err != nil {
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
// An empty name will default to "Log".
func RegisterWith(lib rpc.Library, dst log.Logger, name string) error {
	if name == "" {
		name = "Log"
	}

	f := func(data logger.Data) error {
		var e logger.Entries
		for _, s := range data.Entries {
			e = append(e, s...)
		}
		// being able to pass whatever you want individually, but not as part of a slice of elements that satisfy the required interface, is such a nice language feature innit?
		dst.Log(data.Level, data.Message, e)
		return nil
	}
	return lib.Register(name, f)
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

func (x core) Format(data logger.Data) logger.Data {
	return data
}

func (x core) Write(data logger.Data) {
	if err := x.f(data); err != nil {
		panic(err)
	}
}
