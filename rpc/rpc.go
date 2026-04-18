package rpc

import (
	"github.com/blitz-frost/log"
	"github.com/blitz-frost/rpc"
)

var ProcedureName = "Log"

// BindTo binds a logging procedure to an rpc.Client, and returns a log.Recorder that wraps this procedure.
//
// The underlying rpc system must be capable of handling interface types in general, as well as recognizing at least log.Entries when used as interface values in particular.
//
// The used name can be controlled through the ProcedureName global variable.
func BindTo(cli rpc.Client) (log.Recorder, error) {
	var f func(log.Data) error
	if err := cli.Bind(ProcedureName, &f); err != nil {
		return nil, err
	}

	return func(data log.Data) error {
		for _, e := range data.Entries {
			format(e)
		}
		return f(data)
	}, nil
}

// RegisterWith registers a logging procedure to an rpc.Library. The procedure will use dst as the actual server-side recorder implementation.
//
// The underlying rpc system must be capable of handling interface types in general, as well as recognizing at least log.Entries when used as interface values in particular.
//
// The used name can be controlled through the ProcedureName global variable.
func RegisterWith(lib rpc.Library, dst log.Recorder) error {
	return lib.Register(ProcedureName, dst)
}

// format replaces error Entry.Values with their error string, otherwise it might not really mean much to the receiver if concrete type information is lost.
// Also ensures there are only Entries instead of EntriesGivers.
func format(e log.Entries) {
	for i := range e {
		v := e[i].Value
		switch val := v.(type) {
		case log.Entries:
			// recursive format

			format(val)
		case log.Reporter:
			// replace interface with slice + recursive format

			entries := val.Report()
			format(entries)
			e[i].Value = entries

		case error:
			// replace interface with string

			e[i].Value = val.Error()
		}
	}
}
