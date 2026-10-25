package publicgame

import (
	"context"
	"strings"
	"time"
)

const (
	successCode   = "GAMS00000"
	invalidCode   = "GAMB02001"
	technicalCode = "GAMT03001"
	rateCode      = "GAMB02005"
)

type Service struct {
	Data Data
}

type ServiceError struct {
	Code       string
	HTTPStatus int
	RetryAfter int
	Cause      error
}

func (e *ServiceError) Error() string {
	if e.Cause != nil {
		return e.Cause.Error()
	}
	return e.Code
}

func (e *ServiceError) Unwrap() error { return e.Cause }

func (s *Service) List(ctx context.Context, filter Filter) (Page, error) {
	filter.Search = strings.TrimSpace(filter.Search)
	if filter.Page == 0 {
		filter.Page = 1
	}
	if filter.Limit == 0 {
		if filter.Popular {
			filter.Limit = 6
		} else {
			filter.Limit = 20
		}
	}
	if len([]rune(filter.Search)) > 100 || filter.Page < 1 || filter.Page > 999999999 {
		return Page{}, invalidError()
	}
	if filter.Popular {
		if filter.Limit < 1 || filter.Limit > 50 {
			return Page{}, invalidError()
		}
	} else if filter.Limit < 1 || filter.Limit > 100 {
		return Page{}, invalidError()
	}
	if filter.Platform != "" && filter.Platform != "MOBILE" && filter.Platform != "PC" && filter.Platform != "CONSOLE" {
		return Page{}, invalidError()
	}
	if !filter.Popular && filter.Sort != "SORT_ORDER_ASC" && filter.Sort != "POPULAR" && filter.Sort != "NAME_ASC" {
		return Page{}, invalidError()
	}
	page, err := s.Data.List(ctx, filter)
	if err != nil {
		return Page{}, &ServiceError{Code: technicalCode, HTTPStatus: 500, Cause: err}
	}
	return page, nil
}

func (s *Service) Allow(ctx context.Context, ip string) error {
	if s.Data == nil {
		return &ServiceError{Code: technicalCode, HTTPStatus: 500}
	}
	retry, allowed, err := s.Data.Allow(ctx, ip, nowUTC())
	if err != nil {
		return &ServiceError{Code: technicalCode, HTTPStatus: 500, Cause: err}
	}
	if !allowed {
		seconds := int(retry.Seconds())
		if retry%1000000000 != 0 {
			seconds++
		}
		if seconds < 1 {
			seconds = 1
		}
		return &ServiceError{Code: rateCode, HTTPStatus: 429, RetryAfter: seconds}
	}
	return nil
}

func invalidError() error { return &ServiceError{Code: invalidCode, HTTPStatus: 400} }

func nowUTC() time.Time { return time.Now().UTC() }
