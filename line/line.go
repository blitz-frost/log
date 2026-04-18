// Package line records log data through io.Writers, using the following format:
//
//	LEVEL  msg
//	key0 - value0
//	key1 - value1
//	key2
//	  subkey0 - subvalue0
//	  subkey1 - subvalue1
//
// Its purpose is to provide human readable logs to stdout or local files.
package line

import (
	"fmt"
	"io"

	"github.com/blitz-frost/log"
)

// The produced recorder will not be concurrent safe.
func Recorder(dst io.Writer) log.Recorder {
	buf := lineBufferMake()
	return func(data log.Data) error {
		buf.data = append(buf.data, log.LevelString(data.Level)...)
		buf.data = append(buf.data, "  "...)
		buf.data = append(buf.data, data.Message...)
		buf.data = append(buf.data, '\n')

		for _, elem := range data.Entries {
			buf.append(elem)
		}
		buf.data = append(buf.data, '\n')

		_, err := dst.Write(buf.data)
		buf.reset()
		return err
	}
}

// lineBuffer is the prefered formated block.
type lineBuffer struct {
	data []byte // preftormated lines
	ends []int  // line end indices; needed when working with preformated subblocks to insert additional spacing

	space []byte // used to insert line spacing
}

// lineBufferMake allocates a lineBuffer with sufficient space for most uses
func lineBufferMake() *lineBuffer {
	return &lineBuffer{
		data:  make([]byte, 0, 1024),
		ends:  make([]int, 0, 16),
		space: []byte("        ")[:0],
	}
}

func (x *lineBuffer) append(e log.Reporter) {
	if pre, ok := e.(Entries); ok {
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

	for _, entry := range e.Report() {
		x.appendEntry(entry)
	}
}

func (x *lineBuffer) appendEntry(e log.Entry) {
	x.data = append(x.data, x.space...)
	x.data = append(x.data, e.Key...)

	switch sub := e.Value.(type) {
	case log.Reporter:
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

// Entries is the optimized preformated EntriesGiver for this package.
type Entries struct {
	src []log.Entry

	buf lineBuffer
}

func EntriesMake(src log.Reporter) Entries {
	if same, ok := src.(Entries); ok {
		return same
	}

	buf := lineBuffer{}
	entries := src.Report()
	for _, entry := range entries {
		buf.appendEntry(entry)
	}

	return Entries{
		src: entries,
		buf: buf,
	}
}

func (x Entries) Report() log.Entries {
	return x.src
}
