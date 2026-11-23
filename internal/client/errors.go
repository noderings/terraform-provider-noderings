package client

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// gRPC status codes used by the API's error bodies.
const (
	CodeInvalidArgument    = 3
	CodeNotFound           = 5
	CodeAlreadyExists      = 6
	CodePermissionDenied   = 7
	CodeResourceExhausted  = 8
	CodeFailedPrecondition = 9
	CodeUnauthenticated    = 16
)

// Error is an API error. The API returns {"code": <grpc code>, "message": "...", "details": [...]}.
type Error struct {
	HTTPStatus int
	Code       int
	Message    string
	Details    []ErrorDetail
}

// ErrorDetail is one entry of an error's details. Only ErrorInfo fields are decoded.
type ErrorDetail struct {
	Type     string            `json:"@type"`
	Reason   string            `json:"reason"`
	Domain   string            `json:"domain"`
	Metadata map[string]string `json:"metadata"`
}

func (e *Error) Error() string {
	msg := strings.TrimSpace(e.Message)
	if msg == "" {
		msg = http.StatusText(e.HTTPStatus)
	}
	if q := e.quotaInfo(); q != nil {
		if limit, ok := q.Metadata["limit"]; ok {
			return fmt.Sprintf("%s (quota %s: %s of %s used)", msg, q.Metadata["limit_key"], q.Metadata["current"], limit)
		}
	}
	return fmt.Sprintf("%s (HTTP %d)", msg, e.HTTPStatus)
}

func (e *Error) quotaInfo() *ErrorDetail {
	for i := range e.Details {
		d := &e.Details[i]
		if strings.HasSuffix(d.Type, "google.rpc.ErrorInfo") && strings.HasSuffix(d.Reason, "LIMIT_REACHED") {
			return d
		}
	}
	return nil
}

// IsQuota reports whether the error is an exhausted plan quota rather than a rate limit.
func (e *Error) IsQuota() bool { return e.quotaInfo() != nil }

func decodeError(status int, body []byte) *Error {
	e := &Error{HTTPStatus: status}
	var raw struct {
		Code    int               `json:"code"`
		Message string            `json:"message"`
		Details []json.RawMessage `json:"details"`
	}
	if err := json.Unmarshal(body, &raw); err == nil {
		e.Code = raw.Code
		e.Message = raw.Message
		for _, d := range raw.Details {
			var detail ErrorDetail
			if json.Unmarshal(d, &detail) == nil {
				e.Details = append(e.Details, detail)
			}
		}
	} else {
		e.Message = strings.TrimSpace(string(body))
		if len(e.Message) > 300 {
			e.Message = e.Message[:300]
		}
	}
	if e.Code == 0 {
		switch status {
		case http.StatusNotFound:
			e.Code = CodeNotFound
		case http.StatusConflict:
			e.Code = CodeAlreadyExists
		case http.StatusForbidden:
			e.Code = CodePermissionDenied
		case http.StatusUnauthorized:
			e.Code = CodeUnauthenticated
		}
	}
	return e
}

func hasCode(err error, code int) bool {
	var e *Error
	return errors.As(err, &e) && e.Code == code
}

// notFoundErr builds a NotFound error for responses that omit the expected object.
func notFoundErr(msg string) error {
	return &Error{HTTPStatus: http.StatusNotFound, Code: CodeNotFound, Message: msg}
}

// IsNotFound reports whether err is a NotFound API error.
func IsNotFound(err error) bool { return hasCode(err, CodeNotFound) }

// IsAlreadyExists reports whether err is an AlreadyExists API error.
func IsAlreadyExists(err error) bool { return hasCode(err, CodeAlreadyExists) }

// IsFailedPrecondition reports whether err is a FailedPrecondition API error.
func IsFailedPrecondition(err error) bool { return hasCode(err, CodeFailedPrecondition) }
