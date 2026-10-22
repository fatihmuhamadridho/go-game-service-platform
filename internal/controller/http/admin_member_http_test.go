package controllers

import (
	"net/url"
	"testing"
)

func TestValidMemberListQuery(t *testing.T) {
	for _, test := range []struct {
		name  string
		query string
		want  bool
	}{
		{name: "empty", query: "", want: true},
		{name: "source supported parameters", query: "page=2&pageSize=20&search=alice&status=ACTIVE", want: true},
		{name: "unknown parameter", query: "unexpected=1", want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := validMemberListQuery(mustParseQuery(test.query)); got != test.want {
				t.Fatalf("validMemberListQuery() = %v, want %v", got, test.want)
			}
		})
	}
}

func mustParseQuery(raw string) map[string][]string {
	query, err := url.ParseQuery(raw)
	if err != nil {
		panic(err)
	}
	return query
}
