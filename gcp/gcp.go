package gcp

import (
	"context"
	"encoding/json"

	"cloud.google.com/go/logging"
	"github.com/blitz-frost/errors"
	"github.com/blitz-frost/log"
	"google.golang.org/api/option"
)

// Client is a shorthand for logging.NewClient -> Client.Logger -> Recorder.
func Client(config Config) (log.Recorder, *logging.Client, error) {
	if config.Ctx == nil {
		config.Ctx = context.Background()
	}

	cli, err := logging.NewClient(config.Ctx, config.Parent, config.ClientOptions...)
	if err != nil {
		return nil, nil, errors.Message("new GCP client", err)
	}

	dst := cli.Logger(config.LogID, config.LoggerOptions...)
	return Recorder(dst), cli, nil
}

// Recorder wraps a logging.Logger. Useful for custom setups.
//
// Handles JSON marshaling and inserts encountered errors in the produced log (values starting in "LOG ERROR").
func Recorder(dst *logging.Logger) log.Recorder {
	return func(data log.Data) error {
		e := format(data)
		dst.Log(e)
		return nil
	}
}

// Used by MakeLogger. Only Parent and LogID are mandatory.
// See https://pkg.go.dev/cloud.google.com/go/logging (NewClient and Client.NewLogger) for more details.
type Config struct {
	Ctx           context.Context
	Parent        string
	LogID         string
	ClientOptions []option.ClientOption
	LoggerOptions []logging.LoggerOption
}

type buffer []byte

func bufferMake() *buffer {
	x := make(buffer, 0, 1024)
	return &x
}

func (x *buffer) append(e log.EntriesGiver) {
	// check for preformatted entries
	if fmt, ok := e.(Entries); ok {
		*x = append(*x, fmt.buf...)
		return
	}

	for _, entry := range e.Entries() {
		x.appendEntry(entry)
	}
}

func (x *buffer) appendEntry(e log.Entry) {
	m, _ := json.Marshal(e.Key) // might need escaping; marshalling a string never fails
	*x = append(*x, m...)
	*x = append(*x, ':')

	switch sub := e.Value.(type) {
	case log.EntriesGiver:
		x.start()
		x.append(sub)
		x.end()
	case error:
		// json marshal might produce nonsense
		m, _ = json.Marshal(sub.Error())
		*x = append(*x, m...)
	default:
		m, err := json.Marshal(sub)
		if err != nil {
			// not worth panicking over
			// replace the value with the json marshalling error
			s := "LOG ERROR: " + err.Error()
			m, _ = json.Marshal(s)
		}
		*x = append(*x, m...)
	}
	*x = append(*x, ',')
}

// end an object
func (x *buffer) end() {
	n := len(*x) - 1
	if (*x)[n] != '{' {
		// appended objects should end in an unnecessary comma
		(*x)[n] = '}'
	} else {
		// otherwise we are in an empty object; close it properly
		*x = append(*x, '}')
	}
}

// clear buffer, but keep memory
func (x *buffer) reset() {
	*x = (*x)[:0]
}

// start a new object
func (x *buffer) start() {
	*x = append(*x, '{')
}

func format(data log.Data) logging.Entry {
	// map level to gcp severity; seems to be 100 * lvl, but a switch is safer
	var sev logging.Severity
	switch data.Level {
	case log.Default:
		sev = logging.Default
	case log.Debug:
		sev = logging.Debug
	case log.Info:
		sev = logging.Info
	case log.Notice:
		sev = logging.Notice
	case log.Warning:
		sev = logging.Warning
	case log.Error:
		sev = logging.Error
	case log.Critical:
		sev = logging.Critical
	case log.Alert:
		sev = logging.Alert
	case log.Emergency:
		sev = logging.Emergency
	}

	buf := bufferMake()

	buf.start()
	buf.append(log.Entry{"msg", data.Message})
	for _, e := range data.Entries {
		buf.append(e)
	}
	buf.end()

	return logging.Entry{
		Severity: sev,
		Payload:  json.RawMessage(*buf),
	}
}

// Entries is the optimized preformatted log.Entries for this package.
type Entries struct {
	src log.Entries

	buf buffer // holds comma separated json object members; ends in a comma
}

func EntriesMake(src log.EntriesGiver) Entries {
	if same, ok := src.(Entries); ok {
		return same
	}

	buf := bufferMake()
	e := src.Entries()
	for _, entry := range e {
		buf.appendEntry(entry)
	}

	return Entries{
		src: e,
		buf: *buf,
	}
}

func (x Entries) Entries() log.Entries {
	return x.src
}
