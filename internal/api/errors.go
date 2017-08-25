package api

import (
	"github.com/pkg/errors"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"

	"github.com/joriwind/loraserver/internal/downlink"
	"github.com/joriwind/loraserver/internal/gateway"
	"github.com/joriwind/loraserver/internal/session"
)

var errToCode = map[error]codes.Code{
	downlink.ErrFPortMustNotBeZero:     codes.InvalidArgument,
	downlink.ErrFPortMustBeZero:        codes.InvalidArgument,
	downlink.ErrNoLastRXInfoSet:        codes.FailedPrecondition,
	downlink.ErrInvalidDataRate:        codes.Internal,
	downlink.ErrMaxPayloadSizeExceeded: codes.InvalidArgument,

	gateway.ErrDoesNotExist:               codes.NotFound,
	gateway.ErrAlreadyExists:              codes.AlreadyExists,
	gateway.ErrInvalidAggregationInterval: codes.InvalidArgument,
	gateway.ErrInvalidName:                codes.InvalidArgument,

	session.ErrDoesNotExistOrFCntOrMICInvalid: codes.NotFound,
	session.ErrDoesNotExist:                   codes.NotFound,
}

func errToRPCError(err error) error {
	cause := errors.Cause(err)
	code, ok := errToCode[cause]
	if !ok {
		code = codes.Unknown
	}
	return grpc.Errorf(code, cause.Error())
}
