package gcp

import (
	"context"
	"encoding/json"

	"cloud.google.com/go/logging"
	"github.com/blitz-frost/log"
	"google.golang.org/api/option"
)

// Logger wraps a GCP logging.Logger. Values may be created either manually, or using NewLogger.
//
// Handles JSON marshaling and inserts encountered errors in the produced log (values starting in "LOG ERROR").
//
// Concurrent safe and should be able to handle fairly large volumes.
type Logger struct {
	// Since a logging.Client can have multiple Loggers, a particular setup may not want to automatically close the Client when closing the Logger.
	// Therefore, the desired behaviour is defered to the user.
	// Must be set before using the Logger.
	// May be nil.
	OnClose func() error

	// this implementation mirrors log.LineLogger

	dst *logging.Logger

	dataChan  chan log.Data
	writeChan chan chan logging.Entry

	done chan struct{}
}

// MakeLogger creates a new Logger value. It is a shorthand for logging.NewClient -> Client.Logger -> MakeLoggerOf.
// Sets the resulting Logger's OnClose to the underlying client's Close method.
func MakeLogger(setup LoggerSetup) (Logger, error) {
	if setup.Ctx == nil {
		setup.Ctx = context.Background()
	}

	cli, err := logging.NewClient(setup.Ctx, setup.Parent, setup.ClientOptions...)
	if err != nil {
		return Logger{}, log.MakeError("new GCP client", err)
	}

	dst := cli.Logger(setup.LogID, setup.LoggerOptions...)

	x := MakeLoggerOf(dst)
	x.OnClose = cli.Close

	return x, nil
}

// MakeLoggerOf wraps a logging.Logger. Useful for custom setups.
func MakeLoggerOf(dst *logging.Logger) Logger {
	x := Logger{
		dst:       dst,
		dataChan:  make(chan log.Data, 8),
		writeChan: make(chan chan logging.Entry, 8),
		done:      make(chan struct{}),
	}

	go x.run()
	go x.write()

	return x
}

// Close waits for all log calls to finish, flushes the underlying logging.Logger, then executes OnClose.
func (x Logger) Close() error {
	close(x.dataChan)
	<-x.done

	errors := make([]error, 0, 2) // potential double error situation

	if err := x.dst.Flush(); err != nil {
		errors = append(errors, err)
	}

	if x.OnClose != nil {
		if err := x.OnClose(); err != nil {
			errors = append(errors, err)
		}
	}

	if len(errors) == 0 {
		return nil
	}

	if len(errors) == 1 {
		return errors[0]
	}

	return log.MakeError("logger close", errors[1], log.Entry{"flush error", errors[0]})
}

func (x Logger) Format(e log.EntriesGiver) log.EntriesGiver {
	return makeEntries(e)
}

func (x Logger) Log(lvl int, msg string, e ...log.EntriesGiver) {
	s := make([]log.Entries, len(e))
	for i := range e {
		s[i] = e[i].Entries()
	}

	x.dataChan <- log.Data{
		Level:   lvl,
		Message: msg,
		Entries: s,
	}
}

func (x Logger) log(data log.Data, ch chan logging.Entry) {
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

	buf := newBuffer()

	buf.start()
	buf.append(log.Entry{"msg", data.Message})
	for _, e := range data.Entries {
		buf.append(e)
	}
	buf.end()

	ch <- logging.Entry{
		Severity: sev,
		Payload:  json.RawMessage(*buf),
	}
}

func (x Logger) run() {
	for data := range x.dataChan {
		ch := make(chan logging.Entry)
		go x.log(data, ch)
		x.writeChan <- ch
	}
	close(x.writeChan)
}

func (x Logger) write() {
	for ch := range x.writeChan {
		x.dst.Log(<-ch)
		/*
			a := ((<-ch).Payload.(json.RawMessage))
			log.Log(log.Debug, string(a))
		*/
	}
	close(x.done)
}

// Used by NewLogger. Only Parent and LogID are mandatory.
// See https://pkg.go.dev/cloud.google.com/go/logging (NewClient and Client.NewLogger) for more details.
type LoggerSetup struct {
	Ctx           context.Context
	Parent        string
	LogID         string
	ClientOptions []option.ClientOption
	LoggerOptions []logging.LoggerOption
}

type buffer []byte

func newBuffer() *buffer {
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

// entries is the optimized preformatted entries for Logger.
type entries struct {
	src log.Entries

	buf buffer // holds comma separated json object members; ends in a comma
}

func makeEntries(src log.EntriesGiver) entries {
	if same, ok := src.(entries); ok {
		return same
	}

	buf := newBuffer()
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
