// Package logger provides utilities for Logger implementations.
package logger

type Closer interface {
	Close()
}

// Core bundles the functionality needed by the default Logger implementation.
// In accordance with the general log module philosophy, errors must either be handled internally or result in a panic.
type Core[Raw any] interface {
	Format(Data) Raw // process log data into the raw format used by underlying recording service
	Write(Raw)       // commit a raw log to the backend
	Closer           // ensure all commited logs are fulfilled + cleanup
}

// Data can be used to transfer Log calls between goroutines.
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

// T is the default Logger implementation.
//
// It is concurrent safe, and should be capable of handling pretty high volumes.
// It must be closed when no longer needed, particularly to ensure that all logs have been written before program exit.
type T[Raw any] struct {
	c Core[Raw]

	dataChan  chan Data     // transfer Data from callers to dedicated goroutine
	writeChan chan chan Raw // queue raw formatted data to dedicated write goroutine

	done chan struct{} // closed when the write loop exits
}

// Make creates a Logger using the provided Core.
//
// The Format method of the provided Core must be concurrent safe.
func Make[Raw any](c Core[Raw]) T[Raw] {
	x := T[Raw]{
		c:         c,
		dataChan:  make(chan Data, 8),
		writeChan: make(chan chan Raw, 8),
		done:      make(chan struct{}),
	}

	go x.run()
	go x.write()

	return x
}

// Close waits for all scheduled logs to be written, then closes the underlying Core.
// As soon as Close is called, any further Log calls will panic.
func (x T[Raw]) Close() {
	close(x.dataChan)
	<-x.done

	x.c.Close()
}

func (x T[Raw]) Log(lvl int, msg string, e ...EntriesGiver) {
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

func (x T[Raw]) format(data Data, ch chan Raw) {
	ch <- x.c.Format(data)
}

// run pulls data from Log calls to process it asynchronously and unblock callers ASAP
func (x T[Raw]) run() {
	for data := range x.dataChan {
		ch := make(chan Raw) // transfer formatted log to the write goroutine

		go x.format(data, ch) // perform formatting asynchronously in order to pull data from callers ASAP

		x.writeChan <- ch // inform write goroutine of the next log in line
	}
	close(x.writeChan)
}

// write loop that ensures logs are written in the order they arrive
// also protects the underlying writer from concurrent calls
func (x T[Raw]) write() {
	for ch := range x.writeChan {
		x.c.Write(<-ch)
	}
	close(x.done)
}
