package mq

import "errors"

var (
	errNilHandler = errors.New("mq: nil handler")
	errClosed     = errors.New("mq: bus closed")
)
