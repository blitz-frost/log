package log

import (
	"strconv"

	"github.com/blitz-frost/errors"
)

type errorEntries struct {
	err error
}

func (x errorEntries) Report() Entries {
	p := errorPrinterMake()
	errors.Traverse(p, x.err)

	return Entries{{"err", p.result}}
}

type errorPrinter struct {
	result any

	pending []*any
}

func errorPrinterMake() *errorPrinter {
	var o errorPrinter
	o.pending = append(o.pending, &o.result)
	return &o
}

func (x *errorPrinter) Group() {
	// turn current pending into a group
	p := x.get()
	*p = Entries{}
}

func (x *errorPrinter) GroupEnd() {
	x.end()
}

func (x *errorPrinter) Print(err *errors.T) {
	var e Entries

	if err.Message != "" {
		e = append(e, Entry{"msg", err.Message})
	}

	if err.Trace != nil {
		e = append(e, Entry{"trace", err.Trace})
	}

	if err.Info != nil {
		e = append(e, Entry{"info", err.Info})
	}

	x.print(e)
}

func (x *errorPrinter) PrintError(err error) {
	x.print(err)
}

func (x *errorPrinter) Sub() {
	x.sub(Entry{"err", nil})
}

func (x *errorPrinter) SubEnd() {
	x.end()
}

func (x *errorPrinter) Tail() {
	x.sub(Entry{"tail", Entries{}})
}

func (x *errorPrinter) TailEnd() {
	x.end()
}

func (x *errorPrinter) end() {
	x.pending = x.pending[:len(x.pending)-1]
}

func (x *errorPrinter) get() *any {
	return x.pending[len(x.pending)-1]
}

func (x *errorPrinter) print(v any) {
	p := x.get()

	if *p == nil {
		// this is an entry value
		*p = v
	} else {
		// this is a group
		g := (*p).(Entries)
		n := strconv.Itoa(len(g))

		g = append(g, Entry{"err" + n, v})
		*p = g
	}
}

// sub appends an Entry to the most recent error, and sets its value as the current pending.
func (x *errorPrinter) sub(entry Entry) {
	p := x.get()

	// this is either a T or group/tail
	e := (*p).(Entries)

	// a group will have at least one member to reach this point
	// a T might be empty so we have to check the length
	if len(e) > 0 && e[0].Key == "err0" {
		// this is a group, target its last member
		p = &e[len(e)-1].Value
		e = (*p).(Entries)
	}

	// add suberror entry
	e = append(e, entry)
	*p = e

	// point pending to suberror value
	x.pending = append(x.pending, &e[len(e)-1].Value)
}
