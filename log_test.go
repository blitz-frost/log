package log

import (
	"errors"
	"testing"
)

func TestDefault(t *testing.T) {
	e1 := Entry{"one", 1}
	e2 := Entry{"two", 2}
	e3 := Entry{"twelve", Entries{e1, e2}}
	e4 := Entry{"1212", Entries{e1, e2, e3}}
	Log(Default, "something happened", e1, e2, e3, e4)
	Log(Info, "another thing happened", Entry{"hmm", "Yoshaaa!"})

	err := errors.New("some error")
	Err(Error, "oh", err)

	err2 := MakeError("better error", err, e3)
	Err(Error, "oh no", err2, e1)
}
