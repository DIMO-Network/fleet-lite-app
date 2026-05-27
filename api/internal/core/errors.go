package core

import "fmt"

var (
	ErrBadRequest         = fmt.Errorf("bad request")
	ErrUnsupportedCommand = fmt.Errorf("unsupported command")
)
