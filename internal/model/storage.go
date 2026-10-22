package model

import "errors"

var (
	ErrPrivateObjectNotFound = errors.New("private object not found")
	ErrPrivateObjectInvalid  = errors.New("private object is invalid")
)
