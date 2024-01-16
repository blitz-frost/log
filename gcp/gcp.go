package gcp

import (
	"context"
	"encoding/json"

	"cloud.google.com/go/logging"
	"github.com/blitz-frost/log"
	"github.com/blitz-frost/log/logger"
	"google.golang.org/api/option"
)

// Logger wraps a GCP logging.Logger. Values must be created using LoggerMake or LoggerOf.
//
// Handles JSON marshaling and inserts encountered errors in the produced log (values starting in "LOG ERROR").
//
// When closing, the underlying Logger will always be flushed before executing any custom Close function.
type Logger struct {
	logger.T[logging.Entry]
}

// LoggerMake creates a Logger value. It is a shorthand for logging.NewClient -> Client.Logger -> MakeLoggerOf.
//
// If the provided OnClose method is nil, it will default to closing the created Client.
func LoggerMake(setup LoggerSetup) (Logger, error) {
	if setup.Ctx == nil {
		setup.Ctx = context.Background()
	}

	cli, err := logging.NewClient(setup.Ctx, setup.Parent, setup.ClientOptions...)
	if err != nil {
		return Logger{}, log.ErrorMake("new GCP client", err)
	}

	dst := cli.Logger(setup.LogID, setup.LoggerOptions...)

	if setup.OnClose == nil {
		setup.OnClose = func() {
			if err := cli.Close(); err != nil {
				panic(err)
			}
		}
	}

	return LoggerOf(dst, setup.OnClose), nil
}

// LoggerOf wraps a logging.Logger. Useful for custom setups.
// onClose may be nil, in which case it will simply NoOp (the source logging.Client is unknown).
func LoggerOf(dst *logging.Logger, onClose func()) Logger {
	return Logger{logger.Make[logging.Entry](core{
		dst:     dst,
		onClose: onClose,
	})}
}

func (x Logger) Preformat(e log.EntriesGiver) log.EntriesGiver {
	return entriesMake(e)
}

// Used by MakeLogger. Only Parent and LogID are mandatory.
// See https://pkg.go.dev/cloud.google.com/go/logging (NewClient and Client.NewLogger) for more details.
type LoggerSetup struct {
	Ctx           context.Context
	Parent        string
	LogID         string
	ClientOptions []option.ClientOption
	LoggerOptions []logging.LoggerOption
	OnClose       func()
}

type buffer []byte

func bufferNew() *buffer {
	x := make(buffer, 0, 1024)
	return &x
}

func (x *buffer) append(e log.EntriesGiver) {
	// check for preformatted entries
	if fmt, ok := e.(entries); ok {
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

type core struct {
	dst     *logging.Logger
	onClose func()
}

func (x core) Close() {
	if err := x.dst.Flush(); err != nil {
		panic(err)
	}

	if x.onClose != nil {
		x.onClose()
	}
}

func (x core) Format(data logger.Data) logging.Entry {
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

	buf := bufferNew()

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

func (x core) Write(e logging.Entry) {
	x.dst.Log(e)
}

// entries is the optimized preformatted entries for Logger.
type entries struct {
	src log.Entries

	buf buffer // holds comma separated json object members; ends in a comma
}

func entriesMake(src log.EntriesGiver) entries {
	if same, ok := src.(entries); ok {
		return same
	}

	buf := bufferNew()
	e := src.Entries()
	for _, entry := range e {
		buf.appendEntry(entry)
	}

	return entries{
		src: e,
		buf: *buf,
	}
}

func (x entries) Entries() log.Entries {
	return x.src
}
